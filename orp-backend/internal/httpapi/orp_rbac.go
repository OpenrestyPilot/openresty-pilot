package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func accountHasPermissionForSystem(s *Server, w http.ResponseWriter, r *http.Request, actor string) bool {
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return false
	}
	for _, role := range roles {
		for _, permission := range role.Permissions {
			if permission == "*" {
				return true
			}
		}
	}
	orpFailure(w, 403, "仅超级管理员可操作")
	return false
}

func (s *Server) requireSuper(w http.ResponseWriter, r *http.Request) bool {
	username, ok := s.orpIdentity(w, r)
	if !ok {
		return false
	}
	roles, err := s.accountRoles(r, username)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return false
	}
	for _, role := range roles {
		for _, permission := range role.Permissions {
			if permission == "*" {
				return true
			}
		}
	}
	orpFailure(w, 403, "仅超级管理员可操作")
	return false
}

func orpPage(r *http.Request, items []map[string]any) map[string]any {
	pageNo, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if pageNo < 1 {
		pageNo = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 200 {
		pageSize = 200
	}
	start := (pageNo - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return map[string]any{"items": items[start:end], "total": len(items)}
}

func (s *Server) rbacUsersList(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,username,real_name,email,phone,status,role_ids,created_at,last_login_at FROM orp_account ORDER BY id DESC")
	if err != nil {
		orpFailure(w, 500, "读取用户失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	keyword := strings.ToLower(r.URL.Query().Get("keyword"))
	status := r.URL.Query().Get("status")
	roleFilter := r.URL.Query().Get("roleId")
	for rows.Next() {
		var id int64
		var username, realName, state string
		var email, phone sql.NullString
		var rolesJSON []byte
		var created time.Time
		var lastLogin sql.NullTime
		if err := rows.Scan(&id, &username, &realName, &email, &phone, &state, &rolesJSON, &created, &lastLogin); err != nil {
			orpFailure(w, 500, "读取用户失败")
			return
		}
		if status != "" && status != state {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(username+" "+realName+" "+email.String+" "+phone.String), keyword) {
			continue
		}
		var roleIDs []int64
		if err := json.Unmarshal(rolesJSON, &roleIDs); err != nil {
			orpFailure(w, 500, "用户角色数据损坏")
			return
		}
		if !matchesRoleFilter(roleIDs, roleFilter) {
			continue
		}
		roleNames := []string{}
		for _, roleID := range roleIDs {
			var name string
			if s.db.QueryRowContext(r.Context(), "SELECT name FROM orp_role WHERE id=?", roleID).Scan(&name) == nil {
				roleNames = append(roleNames, name)
			}
		}
		var last any
		if lastLogin.Valid {
			last = lastLogin.Time.Format("2006-01-02 15:04:05")
		}
		items = append(items, map[string]any{"id": id, "username": username, "realName": realName, "email": orpNullableString(email), "phone": orpNullableString(phone), "status": state, "roleIds": roleIDs, "roleNames": roleNames, "createdAt": created.Format("2006-01-02 15:04:05"), "lastLoginAt": last})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取用户失败")
		return
	}
	orpReply(w, 200, orpPage(r, items))
}

func matchesRoleFilter(roleIDs []int64, roleFilter string) bool {
	if roleFilter == "" || roleFilter == "all" {
		return true
	}
	for _, roleID := range roleIDs {
		if strconv.FormatInt(roleID, 10) == roleFilter {
			return true
		}
	}
	return false
}

func orpNullableString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func adminAuditDocument(values map[string]any) []byte {
	raw, _ := json.Marshal(values)
	return raw
}

func auditAdminChange(ctx context.Context, tx *sql.Tx, r *http.Request, actor, action, kind string, id int64, before, after map[string]any) error {
	return orpAudit(ctx, tx, actor, action, kind, id, adminAuditDocument(before), adminAuditDocument(after), orpClientIP(r))
}

func (s *Server) rbacUserCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok || !accountHasPermissionForSystem(s, w, r, actor) {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	username, password, realName := strings.TrimSpace(toString(body["username"])), toString(body["password"]), strings.TrimSpace(toString(body["realName"]))
	if username == "" || len(password) < 12 || realName == "" {
		orpFailure(w, 400, "用户名、姓名不能为空，密码至少 12 位")
		return
	}
	roleIDs, err := parseRoleIDs(body["roleIds"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if err := s.validateRoleIDs(r, roleIDs); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		orpFailure(w, 500, "加密密码失败")
		return
	}
	rawRoles, _ := json.Marshal(roleIDs)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建事务失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), "INSERT INTO orp_account(username,password_hash,real_name,email,phone,status,role_ids) VALUES(?,?,?,?,?,?,?)", username, string(hash), realName, nullableInput(body["email"]), nullableInput(body["phone"]), statusInput(body["status"]), rawRoles)
	if err != nil {
		orpFailure(w, 409, "用户名已存在或保存失败")
		return
	}
	id, _ := result.LastInsertId()
	if err := auditAdminChange(r.Context(), tx, r, actor, "create", "rbac-user", id, nil, map[string]any{"username": username, "realName": realName, "email": nullableInput(body["email"]), "phone": nullableInput(body["phone"]), "status": statusInput(body["status"]), "roleIds": roleIDs}); err != nil {
		orpFailure(w, 500, "记录用户审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交用户失败")
		return
	}
	orpReply(w, 201, map[string]any{"id": id, "username": username, "realName": realName, "roleIds": roleIDs, "status": statusInput(body["status"])})
}

func parseRoleIDs(value any) ([]int64, error) {
	if value == nil {
		return []int64{}, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, errors.New("角色 ID 必须为数组")
	}
	ids := []int64{}
	for _, raw := range list {
		number, ok := raw.(float64)
		if !ok || number < 1 || number != float64(int64(number)) {
			return nil, errors.New("角色 ID 无效")
		}
		id := int64(number)
		for _, existing := range ids {
			if existing == id {
				return nil, errors.New("角色 ID 不能重复")
			}
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Server) validateRoleIDs(r *http.Request, ids []int64) error {
	for _, id := range ids {
		var enabled bool
		if err := s.db.QueryRowContext(r.Context(), "SELECT status='enabled' FROM orp_role WHERE id=?", id).Scan(&enabled); err != nil || !enabled {
			return fmt.Errorf("角色 %d 不存在或已禁用", id)
		}
	}
	return nil
}
func nullableInput(value any) any {
	if text := strings.TrimSpace(toString(value)); text != "" {
		return text
	}
	return nil
}
func statusInput(value any) string {
	if value == "disabled" {
		return "disabled"
	}
	return "enabled"
}

func (s *Server) rbacUserUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
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
	roleIDs, err := parseRoleIDs(body["roleIds"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if err := s.validateRoleIDs(r, roleIDs); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	var currentUsername string
	if err := s.db.QueryRowContext(r.Context(), "SELECT username FROM orp_account WHERE id=?", id).Scan(&currentUsername); err != nil {
		orpFailure(w, 404, "用户不存在")
		return
	}
	var superID int64
	if err := s.db.QueryRowContext(r.Context(), "SELECT id FROM orp_role WHERE code='super'").Scan(&superID); err != nil {
		orpFailure(w, 500, "读取超级管理员角色失败")
		return
	}
	hasSuper := false
	for _, roleID := range roleIDs {
		if roleID == superID {
			hasSuper = true
			break
		}
	}
	if currentUsername == usernameFromToken(r.Header.Get("Authorization")) {
		if statusInput(body["status"]) != "enabled" {
			orpFailure(w, 409, "不能禁用当前登录账号")
			return
		}
		if !hasSuper {
			orpFailure(w, 409, "不能移除当前登录账号的超级管理员角色")
			return
		}
	}
	if !hasSuper {
		var superCount int
		if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM orp_account a JOIN JSON_TABLE(a.role_ids, '$[*]' COLUMNS(role_id BIGINT PATH '$')) r WHERE a.status='enabled' AND r.role_id=?`, superID).Scan(&superCount); err != nil {
			orpFailure(w, 500, "检查超级管理员账号失败")
			return
		}
		if (statusInput(body["status"]) != "enabled" || currentUsername != usernameFromToken(r.Header.Get("Authorization"))) && superCount <= 1 {
			orpFailure(w, 409, "至少需要保留一个启用的超级管理员账号")
			return
		}
	}
	rolesJSON, _ := json.Marshal(roleIDs)
	name := strings.TrimSpace(toString(body["realName"]))
	if name == "" {
		orpFailure(w, 400, "姓名不能为空")
		return
	}
	password := toString(body["password"])
	if password != "" && len(password) < 12 {
		orpFailure(w, 400, "密码至少 12 位")
		return
	}
	var hash []byte
	if password != "" {
		hash, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			orpFailure(w, 500, "加密密码失败")
			return
		}
	}
	var oldName, oldStatus string
	var oldEmail, oldPhone sql.NullString
	var oldRoles []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT real_name,email,phone,status,role_ids FROM orp_account WHERE id=?", id).Scan(&oldName, &oldEmail, &oldPhone, &oldStatus, &oldRoles); err != nil {
		orpFailure(w, 404, "用户不存在")
		return
	}
	var oldRoleIDs []int64
	_ = json.Unmarshal(oldRoles, &oldRoleIDs)
	before := map[string]any{"username": currentUsername, "realName": oldName, "email": orpNullableString(oldEmail), "phone": orpNullableString(oldPhone), "status": oldStatus, "roleIds": oldRoleIDs}
	after := map[string]any{"username": currentUsername, "realName": name, "email": nullableInput(body["email"]), "phone": nullableInput(body["phone"]), "status": statusInput(body["status"]), "roleIds": roleIDs, "passwordReset": password != ""}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建事务失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), "UPDATE orp_account SET real_name=?,email=?,phone=?,status=?,role_ids=? WHERE id=?", name, nullableInput(body["email"]), nullableInput(body["phone"]), statusInput(body["status"]), rolesJSON, id)
	if err != nil {
		orpFailure(w, 500, "更新用户失败")
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		orpFailure(w, 404, "用户不存在")
		return
	}
	if password != "" {
		if _, err := tx.ExecContext(r.Context(), "UPDATE orp_account SET password_hash=? WHERE id=?", string(hash), id); err != nil {
			orpFailure(w, 500, "更新密码失败")
			return
		}
	}
	if err := auditAdminChange(r.Context(), tx, r, usernameFromToken(r.Header.Get("Authorization")), "update", "rbac-user", id, before, after); err != nil {
		orpFailure(w, 500, "记录用户审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交更新失败")
		return
	}
	orpReply(w, 200, map[string]any{"id": id, "realName": name, "roleIds": roleIDs, "status": statusInput(body["status"])})
}

func (s *Server) rbacUserDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	var username string
	if err := s.db.QueryRowContext(r.Context(), "SELECT username FROM orp_account WHERE id=?", id).Scan(&username); errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "用户不存在")
		return
	} else if err != nil {
		orpFailure(w, 500, "读取用户失败")
		return
	}
	if username == usernameFromToken(r.Header.Get("Authorization")) {
		orpFailure(w, 409, "不能删除当前登录账号")
		return
	}
	var name, status string
	var email, phone sql.NullString
	var rolesRaw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT real_name,email,phone,status,role_ids FROM orp_account WHERE id=?", id).Scan(&name, &email, &phone, &status, &rolesRaw); err != nil {
		orpFailure(w, 404, "用户不存在")
		return
	}
	var roleIDs []int64
	_ = json.Unmarshal(rolesRaw, &roleIDs)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建事务失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM orp_account WHERE id=?", id); err != nil {
		orpFailure(w, 500, "删除用户失败")
		return
	}
	if err := auditAdminChange(r.Context(), tx, r, usernameFromToken(r.Header.Get("Authorization")), "delete", "rbac-user", id, map[string]any{"username": username, "realName": name, "email": orpNullableString(email), "phone": orpNullableString(phone), "status": status, "roleIds": roleIDs}, nil); err != nil {
		orpFailure(w, 500, "记录用户审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交用户删除失败")
		return
	}
	orpReply(w, 200, nil)
}

func (s *Server) rbacUserResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	var username string
	if err := s.db.QueryRowContext(r.Context(), "SELECT username FROM orp_account WHERE id=?", id).Scan(&username); err != nil {
		orpFailure(w, 404, "用户不存在")
		return
	}
	password, err := randomPassword()
	if err != nil {
		orpFailure(w, 500, "生成密码失败")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		orpFailure(w, 500, "加密密码失败")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建事务失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_account SET password_hash=? WHERE id=?", string(hash), id); err != nil {
		orpFailure(w, 500, "重置密码失败")
		return
	}
	if err := auditAdminChange(r.Context(), tx, r, usernameFromToken(r.Header.Get("Authorization")), "update", "rbac-user", id, map[string]any{"username": username}, map[string]any{"username": username, "passwordReset": true}); err != nil {
		orpFailure(w, 500, "记录密码重置审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交密码重置失败")
		return
	}
	orpReply(w, 200, map[string]any{"username": username, "resetTo": password})
}

func (s *Server) rbacRolesList(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,code,name,description,status,builtin,permission_keys,center_ids,created_at FROM orp_role ORDER BY id")
	if err != nil {
		orpFailure(w, 500, "读取角色失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
	statusFilter := r.URL.Query().Get("status")
	for rows.Next() {
		var id int64
		var code, name, status string
		var desc sql.NullString
		var builtin bool
		var raw, centerIDsRaw []byte
		var created time.Time
		if err := rows.Scan(&id, &code, &name, &desc, &status, &builtin, &raw, &centerIDsRaw, &created); err != nil {
			orpFailure(w, 500, "读取角色失败")
			return
		}
		if statusFilter != "" && statusFilter != status {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(code+" "+name+" "+desc.String), keyword) {
			continue
		}
		var keys []string
		if err := json.Unmarshal(raw, &keys); err != nil {
			orpFailure(w, 500, "角色权限数据损坏")
			return
		}
		keys = normalizePermissionKeys(keys)
		var centerIDs []string
		if err := json.Unmarshal(centerIDsRaw, &centerIDs); err != nil {
			orpFailure(w, 500, "角色中心范围数据损坏")
			return
		}
		var userCount int
		if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM orp_account a WHERE JSON_CONTAINS(a.role_ids, CAST(? AS JSON))`, id).Scan(&userCount); err != nil {
			orpFailure(w, 500, "读取角色用户数失败")
			return
		}
		items = append(items, map[string]any{"id": id, "code": code, "name": name, "description": orpNullableString(desc), "status": status, "builtin": builtin, "userCount": userCount, "permissionKeys": keys, "centerIds": centerIDs, "createdAt": created.Format("2006-01-02 15:04:05")})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取角色失败")
		return
	}
	orpReply(w, 200, orpPage(r, items))
}

func (s *Server) rbacRoleCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(toString(body["name"]))
	if name == "" {
		orpFailure(w, 400, "角色名称不能为空")
		return
	}
	code := strings.TrimSpace(toString(body["code"]))
	if code == "" {
		code = "role-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if len(code) < 2 || len(code) > 32 || code[0] < 'a' || code[0] > 'z' {
		orpFailure(w, 400, "角色编码必须为 2-32 位小写字母、数字或下划线")
		return
	}
	for _, ch := range code {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			orpFailure(w, 400, "角色编码必须为 2-32 位小写字母、数字或下划线")
			return
		}
	}
	permissions, err := validatedRolePermissions(body["permissionKeys"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	keys, _ := json.Marshal(permissions)
	centerIDs, err := validatedCenterIDs(body["centerIds"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if err := s.validateCenterIDs(r, centerIDs); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	scopes, _ := json.Marshal(centerIDs)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建事务失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), "INSERT INTO orp_role(code,name,description,status,builtin,permission_keys,center_ids) VALUES(?,?,?,?,false,?,?)", code, name, nullableInput(body["description"]), statusInput(body["status"]), keys, scopes)
	if err != nil {
		orpFailure(w, 409, "角色代码已存在或保存失败")
		return
	}
	id, _ := result.LastInsertId()
	if err := auditAdminChange(r.Context(), tx, r, usernameFromToken(r.Header.Get("Authorization")), "create", "rbac-role", id, nil, map[string]any{"code": code, "name": name, "description": nullableInput(body["description"]), "status": statusInput(body["status"]), "permissionKeys": permissions, "centerIds": centerIDs}); err != nil {
		orpFailure(w, 500, "记录角色审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交角色失败")
		return
	}
	orpReply(w, 201, map[string]any{"id": id, "code": code, "name": name, "builtin": false, "status": statusInput(body["status"]), "permissionKeys": permissions, "centerIds": centerIDs})
}

func (s *Server) rbacRoleUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
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
	name := strings.TrimSpace(toString(body["name"]))
	if name == "" {
		orpFailure(w, 400, "角色名称不能为空")
		return
	}
	var builtin bool
	var code, oldName, oldStatus string
	var oldDescription sql.NullString
	var oldKeysRaw, oldCenterIDsRaw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT builtin,code,name,description,status,permission_keys,center_ids FROM orp_role WHERE id=?", id).Scan(&builtin, &code, &oldName, &oldDescription, &oldStatus, &oldKeysRaw, &oldCenterIDsRaw); err != nil {
		orpFailure(w, 404, "角色不存在")
		return
	}
	if builtin {
		orpFailure(w, 409, "内置角色不可修改")
		return
	}
	permissions, err := validatedRolePermissions(body["permissionKeys"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	keys, _ := json.Marshal(permissions)
	centerIDs, err := validatedCenterIDs(body["centerIds"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if err := s.validateCenterIDs(r, centerIDs); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	scopes, _ := json.Marshal(centerIDs)
	var oldKeys, oldCenterIDs []string
	_ = json.Unmarshal(oldKeysRaw, &oldKeys)
	_ = json.Unmarshal(oldCenterIDsRaw, &oldCenterIDs)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建事务失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_role SET name=?,description=?,status=?,permission_keys=?,center_ids=? WHERE id=?", name, nullableInput(body["description"]), statusInput(body["status"]), keys, scopes, id); err != nil {
		orpFailure(w, 500, "更新角色失败")
		return
	}
	if err := auditAdminChange(r.Context(), tx, r, usernameFromToken(r.Header.Get("Authorization")), "update", "rbac-role", id, map[string]any{"code": code, "name": oldName, "description": orpNullableString(oldDescription), "status": oldStatus, "permissionKeys": oldKeys, "centerIds": oldCenterIDs}, map[string]any{"code": code, "name": name, "description": nullableInput(body["description"]), "status": statusInput(body["status"]), "permissionKeys": permissions, "centerIds": centerIDs}); err != nil {
		orpFailure(w, 500, "记录角色审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交角色失败")
		return
	}
	orpReply(w, 200, map[string]any{"id": id, "name": name, "status": statusInput(body["status"]), "permissionKeys": permissions, "centerIds": centerIDs})
}

func validatedCenterIDs(value any) ([]string, error) {
	if value == nil {
		return []string{}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("中心范围必须为数组")
	}
	result := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		id, ok := item.(string)
		if !ok {
			if number, numeric := item.(float64); numeric && number > 0 && number == float64(int64(number)) {
				id = strconv.FormatInt(int64(number), 10)
				ok = true
			}
		}
		if !ok || id == "" || id == "*" && len(items) > 1 {
			return nil, errors.New("中心范围标识无效，全部中心不能与单独中心混用")
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, nil
}

func (s *Server) validateCenterIDs(r *http.Request, ids []string) error {
	for _, id := range ids {
		if id == "*" {
			continue
		}
		numericID, err := strconv.ParseInt(id, 10, 64)
		if err != nil || numericID < 1 {
			return fmt.Errorf("中心范围标识无效：%s", id)
		}
		var exists bool
		if err := s.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM orp_resource WHERE kind='centers' AND id=?)", numericID).Scan(&exists); err != nil || !exists {
			return fmt.Errorf("中心 %s 不存在", id)
		}
	}
	return nil
}

func (s *Server) rbacRoleDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	var builtin bool
	var code, name, status string
	var desc sql.NullString
	var permissionsRaw, centersRaw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT builtin,code,name,description,status,permission_keys,center_ids FROM orp_role WHERE id=?", id).Scan(&builtin, &code, &name, &desc, &status, &permissionsRaw, &centersRaw); err != nil {
		orpFailure(w, 404, "角色不存在")
		return
	}
	if builtin {
		orpFailure(w, 409, "内置角色不可删除")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT role_ids FROM orp_account")
	if err != nil {
		orpFailure(w, 500, "检查角色引用失败")
		return
	}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			rows.Close()
			orpFailure(w, 500, "检查角色引用失败")
			return
		}
		var ids []int64
		_ = json.Unmarshal(raw, &ids)
		for _, value := range ids {
			if value == id {
				rows.Close()
				orpFailure(w, 409, "角色仍被用户引用")
				return
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		orpFailure(w, 500, "检查角色引用失败")
		return
	}
	rows.Close()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建事务失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM orp_role WHERE id=?", id); err != nil {
		orpFailure(w, 500, "删除角色失败")
		return
	}
	var permissions, centers []string
	_ = json.Unmarshal(permissionsRaw, &permissions)
	_ = json.Unmarshal(centersRaw, &centers)
	if err := auditAdminChange(r.Context(), tx, r, usernameFromToken(r.Header.Get("Authorization")), "delete", "rbac-role", id, map[string]any{"code": code, "name": name, "description": orpNullableString(desc), "status": status, "permissionKeys": permissions, "centerIds": centers}, nil); err != nil {
		orpFailure(w, 500, "记录角色审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交角色删除失败")
		return
	}
	orpReply(w, 200, nil)
}

func (s *Server) rbacPermissions(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
		return
	}
	orpReply(w, 200, []map[string]any{
		{"key": "orp:read", "title": "平台配置", "type": "menu", "children": []map[string]any{
			{"key": "orp:read", "title": "查看配置与运行信息", "type": "action"},
			{"key": "orp:config:write", "title": "编辑配置资源", "type": "action"},
		}},
		{"key": "orp:publish", "title": "发布管理", "type": "menu", "children": []map[string]any{
			{"key": "orp:publish", "title": "预检、发布、推进、中止与回滚", "type": "action"},
		}},
		{"key": "orp:node:operate", "title": "节点运维", "type": "menu", "children": []map[string]any{
			{"key": "orp:node:operate", "title": "节点状态、指标与 Agent 操作", "type": "action"},
		}},
		{"key": "orp:system:manage", "title": "系统运维", "type": "menu", "children": []map[string]any{
			{"key": "orp:system:manage", "title": "告警、TLS 提醒与 GeoIP 管理", "type": "action"},
		}},
		{"key": "orp:audit:read", "title": "审计记录", "type": "menu", "children": []map[string]any{
			{"key": "orp:audit:read", "title": "查看审计记录", "type": "action"},
		}},
		{"key": "orp:secrets:read", "title": "敏感数据", "type": "menu", "children": []map[string]any{
			{"key": "orp:secrets:read", "title": "查看敏感配置与证书私钥", "type": "action"},
		}},
		{"key": "*", "title": "账号与角色管理", "type": "menu", "children": []map[string]any{
			{"key": "*", "title": "用户、角色和权限目录管理（仅超级管理员）", "type": "action"},
		}},
	})
}

var assignableRolePermissions = map[string]bool{
	"orp:read": true, "orp:config:write": true, "orp:publish": true,
	"orp:node:operate": true, "orp:system:manage": true,
	"orp:audit:read": true, "orp:secrets:read": true,
}

func validatedRolePermissions(value any) ([]string, error) {
	if value == nil {
		return []string{}, nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, errors.New("权限必须为权限标识数组")
	}
	seen := map[string]bool{}
	keys := make([]string, 0, len(raw))
	for _, item := range raw {
		key, ok := item.(string)
		if !ok || !assignableRolePermissions[key] {
			return nil, fmt.Errorf("不支持的权限标识：%v", item)
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (s *Server) rbacRolePermissionsGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT permission_keys FROM orp_role WHERE id=?", id).Scan(&raw); err != nil {
		orpFailure(w, 404, "角色不存在")
		return
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		orpFailure(w, 500, "权限数据损坏")
		return
	}
	orpReply(w, 200, map[string]any{"permissionKeys": normalizePermissionKeys(keys)})
}

func (s *Server) rbacRolePermissionsPut(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuper(w, r) {
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
	var builtin bool
	var oldRaw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT builtin,permission_keys FROM orp_role WHERE id=?", id).Scan(&builtin, &oldRaw); err != nil {
		orpFailure(w, 404, "角色不存在")
		return
	}
	if builtin {
		orpFailure(w, 409, "内置角色权限不可修改")
		return
	}
	keys, err := validatedRolePermissions(body["permissionKeys"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	raw, _ := json.Marshal(keys)
	var oldKeys []string
	_ = json.Unmarshal(oldRaw, &oldKeys)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建事务失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_role SET permission_keys=? WHERE id=?", raw, id); err != nil {
		orpFailure(w, 500, "保存权限失败")
		return
	}
	if err := auditAdminChange(r.Context(), tx, r, usernameFromToken(r.Header.Get("Authorization")), "update", "rbac-role", id, map[string]any{"permissionKeys": oldKeys}, map[string]any{"permissionKeys": keys}); err != nil {
		orpFailure(w, 500, "记录权限审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交角色权限失败")
		return
	}
	orpReply(w, 200, map[string]any{"permissionKeys": keys})
}

func randomPassword() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buffer), nil
}
