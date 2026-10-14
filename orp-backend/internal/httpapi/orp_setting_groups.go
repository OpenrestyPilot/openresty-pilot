package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

func (s *Server) settingGroupExists(r *http.Request, name string) (bool, error) {
	groups, err := s.orpLoad(r.Context(), "setting-groups")
	if err != nil {
		return false, err
	}
	for _, group := range groups {
		if toString(group["name"]) == name {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) orpSettingBatchMove(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:config:write")
	if !ok {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	ids, ok := body["ids"].([]any)
	if !ok {
		orpFailure(w, 400, "ids 必须是数组")
		return
	}
	target := toString(body["targetGroup"])
	exists, err := s.settingGroupExists(r, target)
	if err != nil {
		orpFailure(w, 500, "读取分组失败")
		return
	}
	if !exists {
		orpFailure(w, 400, "目标分组不存在")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建事务")
		return
	}
	defer tx.Rollback()
	for _, value := range ids {
		number, ok := value.(float64)
		if !ok || number < 1 || number != float64(int64(number)) {
			orpFailure(w, 400, "无效的配置项 ID")
			return
		}
		id := int64(number)
		var previous []byte
		if err := tx.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind='settings' AND id=? FOR UPDATE", id).Scan(&previous); err != nil {
			orpFailure(w, 404, fmt.Sprintf("配置项 %d 不存在", id))
			return
		}
		var item orpDocument
		if err := json.Unmarshal(previous, &item); err != nil {
			orpFailure(w, 500, "配置项数据损坏")
			return
		}
		item["group"] = target
		raw, _ := json.Marshal(item)
		if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=? WHERE kind='settings' AND id=?", raw, id); err != nil {
			orpFailure(w, 500, "移动配置项失败")
			return
		}
		if err := orpAudit(r.Context(), tx, actor, "update", "settings", id, previous, raw, orpClientIP(r)); err != nil {
			orpFailure(w, 500, "记录审计失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交配置项移动失败")
		return
	}
	orpReply(w, 200, map[string]any{"moved": len(ids), "targetGroup": target})
}

func (s *Server) orpSettingGroupDelete(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:config:write")
	if !ok {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	group, err := s.orpRawDocument(r, "setting-groups", id)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "分组不存在")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取分组失败")
		return
	}
	if group["isSystem"] == true {
		orpFailure(w, 409, "系统分组不可删除")
		return
	}
	mode := r.URL.Query().Get("mode")
	target := r.URL.Query().Get("targetGroup")
	settings, err := s.orpLoad(r.Context(), "settings")
	if err != nil {
		orpFailure(w, 500, "读取配置项失败")
		return
	}
	affected := []int64{}
	for _, setting := range settings {
		if toString(setting["group"]) == toString(group["name"]) {
			affected = append(affected, setting["id"].(int64))
		}
	}
	if len(affected) > 0 {
		if mode != "move" && mode != "cascade" {
			orpFailure(w, 409, "分组内有配置项，请指定转移或级联删除")
			return
		}
		if mode == "move" {
			exists, err := s.settingGroupExists(r, target)
			if err != nil || !exists || target == toString(group["name"]) {
				orpFailure(w, 400, "目标分组不存在或无效")
				return
			}
		}
		if mode == "cascade" && r.URL.Query().Get("riskConfirmed") != "true" {
			orpFailure(w, 409, "级联删除需要风险确认")
			return
		}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建事务")
		return
	}
	defer tx.Rollback()
	for _, settingID := range affected {
		var previous []byte
		if err := tx.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind='settings' AND id=? FOR UPDATE", settingID).Scan(&previous); err != nil {
			orpFailure(w, 500, "读取配置项失败")
			return
		}
		var after []byte
		if mode == "move" {
			var item orpDocument
			if err := json.Unmarshal(previous, &item); err != nil {
				orpFailure(w, 500, "配置项数据损坏")
				return
			}
			item["group"] = target
			after, _ = json.Marshal(item)
			if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=? WHERE kind='settings' AND id=?", after, settingID); err != nil {
				orpFailure(w, 500, "迁移配置项失败")
				return
			}
		} else {
			if _, err := tx.ExecContext(r.Context(), "DELETE FROM orp_resource WHERE kind='settings' AND id=?", settingID); err != nil {
				orpFailure(w, 500, "删除配置项失败")
				return
			}
		}
		action := "update"
		if mode == "cascade" {
			action = "delete"
		}
		if err := orpAudit(r.Context(), tx, actor, action, "settings", settingID, previous, after, orpClientIP(r)); err != nil {
			orpFailure(w, 500, "记录审计失败")
			return
		}
	}
	var previous []byte
	if err := tx.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind='setting-groups' AND id=? FOR UPDATE", id).Scan(&previous); err != nil {
		orpFailure(w, 404, "分组不存在")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM orp_resource WHERE kind='setting-groups' AND id=?", id); err != nil {
		orpFailure(w, 500, "删除分组失败")
		return
	}
	if err := orpAudit(r.Context(), tx, actor, "delete", "setting-groups", id, previous, nil, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交删除失败")
		return
	}
	orpReply(w, 200, nil)
}
