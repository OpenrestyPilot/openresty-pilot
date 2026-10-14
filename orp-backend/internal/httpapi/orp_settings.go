package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

var singletonNames = map[string]bool{
	"http-directives": true, "stream-directives": true,
	"http-log-format": true, "stream-log-format": true,
	"http-error-pages": true,
}

func (s *Server) orpSingletonGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	name := r.PathValue("name")
	if !singletonNames[name] {
		orpFailure(w, 404, "配置类型不存在")
		return
	}
	var raw []byte
	err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_singleton WHERE name=?", name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		if strings.HasSuffix(name, "-directives") {
			orpReply(w, 200, []any{})
		} else if name == "http-error-pages" {
			orpReply(w, 200, map[string]string{})
		} else {
			orpReply(w, 200, map[string]string{"name": "", "format": ""})
		}
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取配置失败")
		return
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		orpFailure(w, 500, "配置数据损坏")
		return
	}
	orpReply(w, 200, data)
}

func (s *Server) orpSingletonPut(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !singletonNames[name] {
		orpFailure(w, 404, "配置类型不存在")
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	var data any = body
	if strings.HasSuffix(name, "-directives") {
		list, ok := body["directives"].([]any)
		if !ok {
			orpFailure(w, 400, "directives 必须是数组")
			return
		}
		for _, entry := range list {
			item, ok := entry.(map[string]any)
			if !ok || strings.TrimSpace(toString(item["name"])) == "" {
				orpFailure(w, 400, "指令名称不能为空")
				return
			}
		}
		data = list
	} else if name == "http-error-pages" {
		pages, ok := body["errorPages"].(map[string]any)
		if !ok {
			orpFailure(w, 400, "errorPages 必须是对象")
			return
		}
		pageMap := make(map[string]string, len(pages))
		for key, value := range pages {
			content, ok := value.(string)
			if !ok {
				orpFailure(w, 400, "错误页面内容必须是文本")
				return
			}
			pageMap[key] = content
		}
		if !validErrorPages(pageMap) {
			orpFailure(w, 400, "错误页面配置无效")
			return
		}
		data = pageMap
	} else if !requiredString(body, "name") || !requiredString(body, "format") {
		orpFailure(w, 400, "日志格式名称和内容不能为空")
		return
	}
	raw, _ := json.Marshal(data)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建事务")
		return
	}
	defer tx.Rollback()
	var previous []byte
	err = tx.QueryRowContext(r.Context(), "SELECT document FROM orp_singleton WHERE name=? FOR UPDATE", name).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 500, "读取原配置失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO orp_singleton(name,document) VALUES(?,?) ON DUPLICATE KEY UPDATE document=VALUES(document)", name, raw); err != nil {
		orpFailure(w, 500, "保存配置失败")
		return
	}
	if err := orpAudit(r.Context(), tx, actor, "update", name, 0, previous, raw, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交配置失败")
		return
	}
	orpReply(w, 200, data)
}

func (s *Server) orpSettingGroupNames(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	groups, err := s.orpLoad(r.Context(), "setting-groups")
	if err != nil {
		orpFailure(w, 500, "读取分组失败")
		return
	}
	seen := map[string]bool{}
	names := []string{}
	for _, group := range groups {
		name := toString(group["name"])
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	orpReply(w, 200, names)
}

func (s *Server) orpSettingStatus(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:config:write")
	if !ok {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	status := toString(body["status"])
	if status != "enabled" && status != "disabled" {
		orpFailure(w, 400, "无效的状态")
		return
	}
	current, err := s.orpRawDocument(r, "settings", id)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "配置项不存在")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取配置项失败")
		return
	}
	previous, _ := json.Marshal(current)
	current["status"] = status
	delete(current, "id")
	raw, _ := json.Marshal(current)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建事务")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=? WHERE kind='settings' AND id=?", raw, id)
	if err != nil {
		orpFailure(w, 500, "更新状态失败")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		orpFailure(w, 404, "配置项不存在")
		return
	}
	if err := orpAudit(r.Context(), tx, actor, "update", "settings", id, previous, raw, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交状态失败")
		return
	}
	orpRedact("settings", current)
	current["id"] = id
	orpReply(w, 200, current)
}

func (s *Server) orpSettingGroupReorder(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:config:write"); !ok {
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
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建事务")
		return
	}
	defer tx.Rollback()
	for index, value := range ids {
		id := int64(0)
		if number, ok := value.(float64); ok {
			id = int64(number)
		}
		if id < 1 {
			orpFailure(w, 400, "无效的分组 ID")
			return
		}
		if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=JSON_SET(document,'$.sortOrder',?) WHERE kind='setting-groups' AND id=?", index+1, id); err != nil {
			orpFailure(w, 500, "保存排序失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交排序失败")
		return
	}
	groups, err := s.orpLoad(r.Context(), "setting-groups")
	if err != nil {
		orpFailure(w, 500, "读取分组失败")
		return
	}
	sort.Slice(groups, func(i, j int) bool {
		a, _ := strconv.Atoi(fmt.Sprint(groups[i]["sortOrder"]))
		b, _ := strconv.Atoi(fmt.Sprint(groups[j]["sortOrder"]))
		return a < b
	})
	orpReply(w, 200, groups)
}
