package httpapi

import (
	"database/sql"
	"net/http"
	"strconv"
)

const defaultValidationCenterMaxInflight = 4
const maxValidationCenterInflight = 32

func (s *Server) orpValidationPolicyGet(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:read")
	if !ok {
		return
	}
	centerID, err := strconv.ParseInt(r.PathValue("centerId"), 10, 64)
	if err != nil || centerID < 1 {
		orpFailure(w, 400, "中心 ID 无效")
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	if !accountHasCenterPermission(roles, "orp:read", strconv.FormatInt(centerID, 10)) {
		orpFailure(w, 403, "无权读取该中心校验策略")
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_resource WHERE kind='centers' AND id=?", centerID).Scan(&exists); err != nil {
		orpFailure(w, 500, "读取中心失败")
		return
	}
	if exists == 0 {
		orpFailure(w, 404, "中心不存在")
		return
	}
	maxInflight := defaultValidationCenterMaxInflight
	err = s.db.QueryRowContext(r.Context(), "SELECT max_inflight FROM orp_validation_center_policy WHERE center_id=?", centerID).Scan(&maxInflight)
	if err != nil && err != sql.ErrNoRows {
		orpFailure(w, 500, "读取中心校验策略失败")
		return
	}
	orpReply(w, 200, map[string]any{"centerId": centerID, "maxInflight": maxInflight, "default": err == sql.ErrNoRows})
}

func (s *Server) orpValidationPolicyPut(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:config:write")
	if !ok {
		return
	}
	centerID, err := strconv.ParseInt(r.PathValue("centerId"), 10, 64)
	if err != nil || centerID < 1 {
		orpFailure(w, 400, "中心 ID 无效")
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	maxInflight64, err := resourceNumber(body["maxInflight"])
	if err != nil || maxInflight64 < 1 || maxInflight64 > maxValidationCenterInflight {
		orpFailure(w, 400, "maxInflight 须为 1 到 32")
		return
	}
	maxInflight := int(maxInflight64)
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	if !accountHasCenterPermission(roles, "orp:config:write", strconv.FormatInt(centerID, 10)) {
		orpFailure(w, 403, "无权修改该中心校验策略")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建策略事务")
		return
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_resource WHERE kind='centers' AND id=?", centerID).Scan(&exists); err != nil {
		orpFailure(w, 500, "读取中心失败")
		return
	}
	if exists == 0 {
		orpFailure(w, 404, "中心不存在")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO orp_validation_center_policy(center_id,max_inflight) VALUES(?,?) ON DUPLICATE KEY UPDATE center_id=VALUES(center_id)", centerID, defaultValidationCenterMaxInflight); err != nil {
		orpFailure(w, 500, "初始化中心校验策略失败")
		return
	}
	var beforeLimit int
	if err := tx.QueryRowContext(r.Context(), "SELECT max_inflight FROM orp_validation_center_policy WHERE center_id=? FOR UPDATE", centerID).Scan(&beforeLimit); err != nil {
		orpFailure(w, 500, "读取当前中心校验策略失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_validation_center_policy SET max_inflight=? WHERE center_id=?", maxInflight, centerID); err != nil {
		orpFailure(w, 500, "保存中心校验策略失败")
		return
	}
	before := adminAuditDocument(map[string]any{"centerId": centerID, "maxInflight": beforeLimit})
	after := adminAuditDocument(map[string]any{"centerId": centerID, "maxInflight": maxInflight})
	if err := orpAudit(r.Context(), tx, actor, "update", "validation-center-policy", centerID, before, after, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录中心校验策略审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交中心校验策略失败")
		return
	}
	orpReply(w, 200, map[string]any{"centerId": centerID, "maxInflight": maxInflight})
}
