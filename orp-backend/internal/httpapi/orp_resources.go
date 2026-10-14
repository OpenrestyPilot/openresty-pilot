package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The new console uses numeric resource identifiers. This store keeps its API
// documents separate from the older UUID schema while the two models coexist.
var orpKinds = map[string]bool{
	"centers": true, "nodes": true, "http-listeners": true,
	"stream-services": true, "certificates": true, "dns-resolvers": true,
	"upstream-groups": true, "ip-groups": true, "settings": true,
	"setting-groups": true,
}

type orpDocument map[string]any

func orpReply(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"code": 0, "data": data, "message": "ok"})
}

func orpFailure(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"code": -1, "data": nil, "message": message})
}

func (s *Server) orpIdentity(w http.ResponseWriter, r *http.Request) (string, bool) {
	return s.requirePermission(w, r, requiredORPPermission(r))
}

func accountCenterIDs(roles []accountRole, permission string) ([]string, bool) {
	set := map[string]bool{}
	for _, role := range roles {
		if !roleHasPermission(role, permission) {
			continue
		}
		for _, id := range role.CenterIDs {
			if id == "*" {
				return nil, true
			}
			set[id] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, false
}

func accountHasCenterScope(roles []accountRole, centerID string) bool {
	centerID = strings.TrimSpace(centerID)
	if centerID == "" {
		return true // Shared global resources are not scoped to a center.
	}
	for _, role := range roles {
		for _, allowed := range role.CenterIDs {
			if allowed == "*" || allowed == centerID {
				return true
			}
		}
	}
	return false
}

func roleHasPermission(role accountRole, permission string) bool {
	for _, key := range role.Permissions {
		if key == "*" || key == permission || key == "orp:write" && permission == "orp:config:write" {
			return true
		}
	}
	return false
}

func accountHasCenterPermission(roles []accountRole, permission, centerID string) bool {
	for _, role := range roles {
		if !roleHasPermission(role, permission) {
			continue
		}
		// Centerless resources are platform-wide. Access to them requires a
		// role whose permission is explicitly granted across all centers.
		if strings.TrimSpace(centerID) == "" {
			return true // The permission itself is checked on the same role.
		}
		if accountHasCenterScope([]accountRole{role}, centerID) {
			return true
		}
	}
	return false
}

func accountHasGlobalCenterPermission(roles []accountRole, permission string) bool {
	return accountHasCenterPermission(roles, permission, "*")
}

func centerIDString(value any) string {
	switch id := value.(type) {
	case string:
		return id
	case float64:
		if id == float64(int64(id)) {
			return strconv.FormatInt(int64(id), 10)
		}
	case int64:
		return strconv.FormatInt(id, 10)
	case int:
		return strconv.Itoa(id)
	case json.Number:
		return id.String()
	}
	return ""
}

// requestCenterScopeAllowed protects direct resource and legacy center routes.
// Collection filtering and write-body validation are performed in the CRUD handlers.
func (s *Server) requestCenterScopeAllowed(r *http.Request, roles []accountRole, permission string) bool {
	if centerID := r.PathValue("centerID"); centerID != "" {
		if accountHasCenterPermission(roles, permission, centerID) {
			return true
		}
		var numericID string
		err := s.db.QueryRowContext(r.Context(), "SELECT CAST(id AS CHAR) FROM orp_resource WHERE kind='centers' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.legacyId'))=? LIMIT 1", centerID).Scan(&numericID)
		return err == nil && accountHasCenterPermission(roles, permission, numericID)
	}
	kind, rawID := r.PathValue("kind"), r.PathValue("id")
	if kind == "" && rawID != "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 4 && parts[0] == "api" && parts[1] == "orp" && orpKinds[parts[2]] && parts[3] == rawID {
			kind = parts[2]
		}
	}
	if rawID == "" || !orpKinds[kind] {
		return true
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		return false
	}
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind=? AND id=?", kind, id).Scan(&raw); err != nil {
		return false
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	if kind == "centers" {
		return accountHasCenterPermission(roles, permission, rawID) || accountHasCenterPermission(roles, permission, centerIDString(doc["legacyId"]))
	}
	return accountHasCenterPermission(roles, permission, centerIDString(doc["centerId"]))
}

func (s *Server) documentCenterScopeAllowed(r *http.Request, roles []accountRole, permission, kind string, doc map[string]any) bool {
	if kind == "centers" {
		return accountHasCenterPermission(roles, permission, centerIDString(doc["id"])) || accountHasCenterPermission(roles, permission, centerIDString(doc["legacyId"]))
	}
	return accountHasCenterPermission(roles, permission, centerIDString(doc["centerId"]))
}

func accountHasPermission(roles []accountRole, permission string) bool {
	for _, role := range roles {
		if roleHasPermission(role, permission) {
			return true
		}
	}
	return false
}

func (s *Server) requirePermission(w http.ResponseWriter, r *http.Request, permission string) (string, bool) {
	if isPublicScreenContext(r.Context()) && permission == "orp:read" && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/orp/dashboard/") {
		return dashboardPublicScreenUser, true
	}
	user := usernameFromToken(r.Header.Get("Authorization"))
	if user == "" {
		orpFailure(w, http.StatusUnauthorized, "登录状态已失效")
		return "", false
	}
	roles, err := s.accountRoles(r, user)
	if err != nil {
		orpFailure(w, http.StatusUnauthorized, "用户不存在或已禁用")
		return "", false
	}
	if !accountHasPermission(roles, permission) {
		orpFailure(w, http.StatusForbidden, "当前角色缺少所需权限："+permission)
		return "", false
	}
	if centerID := r.URL.Query().Get("centerId"); centerID != "" && !accountHasCenterPermission(roles, permission, centerID) {
		orpFailure(w, http.StatusForbidden, "当前角色无权访问该中心")
		return "", false
	}
	if !s.requestCenterScopeAllowed(r, roles, permission) {
		orpFailure(w, http.StatusForbidden, "当前角色无权访问该中心")
		return "", false
	}
	return user, true
}

// requiredORPPermission centralizes authorization for handlers that call
// orpIdentity. Keep the rules aligned with the permission catalog in
// rbacPermissions so the UI labels describe real server-side checks.
func requiredORPPermission(r *http.Request) string {
	path, method := r.URL.Path, r.Method
	if strings.HasPrefix(path, "/api/orp/rbac/") {
		return "*"
	}
	if strings.HasPrefix(path, "/api/orp/audit-logs") || strings.Contains(path, "/audit-events") || strings.HasPrefix(path, "/api/user/profile/audit") {
		return "orp:audit:read"
	}
	if strings.HasPrefix(path, "/api/orp/certificates/") && (strings.HasSuffix(path, "/detail") || strings.HasSuffix(path, "/public-detail")) {
		if strings.HasSuffix(path, "/detail") {
			return "orp:secrets:read"
		}
		return "orp:read"
	}
	if path == "/api/orp/certificates/stats" {
		return "orp:read"
	}
	if strings.HasPrefix(path, "/api/orp/geoip-database") ||
		strings.HasPrefix(path, "/api/orp/alert-channels") ||
		strings.HasPrefix(path, "/api/orp/tls-alert-rules") ||
		strings.HasPrefix(path, "/api/orp/alert-deliveries") ||
		strings.HasPrefix(path, "/api/orp/tls-alerts") {
		return "orp:system:manage"
	}
	if strings.HasPrefix(path, "/api/orp/publish") {
		if method == http.MethodGet {
			return "orp:read"
		}
		return "orp:publish"
	}
	if strings.HasSuffix(path, "/runtime-configurations") || strings.Contains(path, "/runtime-configurations/") {
		if method == http.MethodGet || method == http.MethodHead {
			return "orp:read"
		}
		return "orp:publish"
	}
	if strings.Contains(path, "/agent/") || strings.HasSuffix(path, "/status") && strings.Contains(path, "/nodes/") {
		return "orp:node:operate"
	}
	if strings.HasSuffix(path, "/metrics") && strings.Contains(path, "/nodes/") || strings.HasSuffix(path, "/latency-history") || strings.HasPrefix(path, "/api/orp/dashboard/") || strings.HasPrefix(path, "/api/orp/nodes/") {
		return "orp:read"
	}
	if method == http.MethodGet || method == http.MethodHead {
		return "orp:read"
	}
	return "orp:config:write"
}

func (s *Server) secureLegacy(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.orpIdentity(w, r)
		if !ok {
			return
		}
		if r.PathValue("centerID") != "" {
			next(w, r)
			return
		}
		roles, err := s.accountRoles(r, user)
		if err != nil {
			orpFailure(w, 500, "读取权限失败")
			return
		}
		permission := requiredORPPermission(r)
		if !accountHasGlobalCenterPermission(roles, permission) {
			orpFailure(w, http.StatusForbidden, "该旧版接口不支持按中心范围筛选")
			return
		}
		next(w, r)
	}
}

func (s *Server) orpList(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !orpKinds[kind] || kind == "rbac-users" || kind == "rbac-roles" {
		orpFailure(w, 404, "资源类型不存在")
		return
	}
	items, err := s.orpLoad(r.Context(), kind)
	if err != nil {
		orpFailure(w, 500, "读取资源失败")
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	permission := requiredORPPermission(r)
	if !accountHasGlobalCenterPermission(roles, permission) {
		filtered := items[:0]
		for _, item := range items {
			if s.documentCenterScopeAllowed(r, roles, permission, kind, item) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if kind == "centers" {
		for _, item := range items {
			for _, field := range []string{"latitude", "longitude"} {
				if value, ok := numericFloat(item[field]); ok {
					item[field] = value
				}
			}
		}
	}
	if kind != "centers" && kind != "rbac-users" && kind != "rbac-roles" {
		centers, err := s.orpLoad(r.Context(), "centers")
		if err != nil {
			orpFailure(w, 500, "读取中心失败")
			return
		}
		centerNames := map[string]string{}
		for _, center := range centers {
			centerNames[fmt.Sprint(center["id"])] = toString(center["name"])
		}
		certificateNames := map[string]string{}
		if kind == "http-listeners" {
			certificates, err := s.orpLoad(r.Context(), "certificates")
			if err != nil {
				orpFailure(w, 500, "读取证书失败")
				return
			}
			for _, certificate := range certificates {
				certificateNames[fmt.Sprint(certificate["id"])] = toString(certificate["name"])
			}
		}
		for _, item := range items {
			if centerID, hasCenter := item["centerId"]; hasCenter {
				item["centerName"] = centerNames[fmt.Sprint(centerID)]
			}
			if kind == "http-listeners" {
				item["certName"] = certificateNames[fmt.Sprint(item["tlsCertId"])]
				if routes, ok := item["routes"].([]any); ok {
					item["routeCount"] = len(routes)
				} else {
					item["routeCount"] = 0
				}
			}
		}
	}
	if kind == "settings" {
		for _, item := range items {
			s.decorateRuntimeSetting(item)
			if item["isSensitive"] == true {
				item["value"] = "********"
			}
		}
	}
	if kind == "nodes" {
		s.decorateNodeHealth(r.Context(), items)
	}
	query := r.URL.Query()
	filtered := make([]orpDocument, 0, len(items))
	keyword := strings.ToLower(strings.TrimSpace(query.Get("keyword")))
	for _, item := range items {
		if keyword != "" {
			encoded, _ := json.Marshal(item)
			if !strings.Contains(strings.ToLower(string(encoded)), keyword) {
				continue
			}
		}
		matched := true
		for _, field := range []string{"centerId", "status", "roleId", "group", "protocol"} {
			if value := query.Get(field); value != "" && fmt.Sprint(item[field]) != value {
				matched = false
				break
			}
		}
		if matched {
			filtered = append(filtered, item)
		}
	}
	pageNo, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("pageSize"))
	if pageNo < 1 {
		pageNo = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if kind == "setting-groups" {
		settings, err := s.orpLoad(r.Context(), "settings")
		if err != nil {
			orpFailure(w, 500, "读取配置项失败")
			return
		}
		for _, group := range filtered {
			count := 0
			for _, setting := range settings {
				if toString(setting["group"]) == toString(group["name"]) {
					count++
				}
			}
			group["settingCount"] = count
		}
		sort.Slice(filtered, func(i, j int) bool {
			a, _ := strconv.ParseFloat(fmt.Sprint(filtered[i]["sortOrder"]), 64)
			b, _ := strconv.ParseFloat(fmt.Sprint(filtered[j]["sortOrder"]), 64)
			return a < b
		})
		orpReply(w, 200, filtered)
		return
	}
	start := (pageNo - 1) * pageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	orpReply(w, 200, map[string]any{"items": filtered[start:end], "total": len(filtered)})
}

func (s *Server) orpLoad(ctx context.Context, kind string) ([]orpDocument, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, document, created_at, updated_at FROM orp_resource WHERE kind=? ORDER BY id DESC", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []orpDocument{}
	for rows.Next() {
		var id int64
		var raw []byte
		var created, updated time.Time
		if err := rows.Scan(&id, &raw, &created, &updated); err != nil {
			return nil, err
		}
		var item orpDocument
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		item["id"] = id
		item["createdAt"] = created.Format("2006-01-02 15:04:05")
		item["updatedAt"] = updated.Format("2006-01-02 15:04:05")
		orpRedact(kind, item)
		items = append(items, item)
	}
	return items, rows.Err()
}

func orpBody(w http.ResponseWriter, r *http.Request) (orpDocument, bool) {
	// Allow a 1 MiB error-page body plus JSON escaping and request metadata.
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var body orpDocument
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body == nil {
		orpFailure(w, 400, "请求内容不是有效的 JSON 对象")
		return nil, false
	}
	delete(body, "id")
	delete(body, "createdAt")
	delete(body, "updatedAt")
	return body, true
}

func orpID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "无效的资源 ID")
		return 0, false
	}
	return id, true
}

func (s *Server) orpCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !orpKinds[kind] {
		orpFailure(w, 404, "资源类型不存在")
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	if kind == "nodes" && (body["agentId"] != nil || body["agentCertificateFingerprint"] != nil) {
		orpFailure(w, 400, "请使用受限的 Agent 证书登记接口设置节点身份")
		return
	}
	if err := orpNormalize(kind, body); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if kind == "settings" && s.runtimeSettingDefinition(toString(body["key"])) != nil {
		orpFailure(w, 409, "系统预置运行配置由平台自动维护，不能重复新增")
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	permission := requiredORPPermission(r)
	if kind == "centers" && !accountHasGlobalCenterPermission(roles, permission) || !accountHasCenterPermission(roles, permission, centerIDString(body["centerId"])) {
		orpFailure(w, 403, "当前角色无权在该中心创建资源")
		return
	}
	if err := s.orpValidate(r.Context(), kind, 0, body); err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	if kind == "setting-groups" {
		body["isSystem"] = false
		if body["sortOrder"] == nil {
			body["sortOrder"] = 999
		}
	}
	if err := orpProtect(kind, body); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	var legacyCenterID uuid.UUID
	if kind == "centers" {
		legacyCenterID = uuid.New()
		body["legacyId"] = legacyCenterID.String()
	}
	raw, _ := json.Marshal(body)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建事务")
		return
	}
	defer tx.Rollback()
	if kind == "centers" {
		if _, err := tx.ExecContext(r.Context(), "INSERT INTO center(id,code,name,enabled,created_at) VALUES(?,?,?,true,?)", legacyCenterID[:], toString(body["code"]), toString(body["name"]), time.Now()); err != nil {
			orpFailure(w, 409, "中心标识已存在或保存失败")
			return
		}
	}
	result, err := tx.ExecContext(r.Context(), "INSERT INTO orp_resource(kind,document) VALUES(?,?)", kind, raw)
	if err != nil {
		orpFailure(w, 500, "保存资源失败")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		orpFailure(w, 500, "获取资源 ID 失败")
		return
	}
	if err := orpAudit(r.Context(), tx, actor, "create", kind, id, nil, raw, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交资源失败")
		return
	}
	orpReply(w, 201, orpPresented(kind, id, body))
}

func (s *Server) orpUpdate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !orpKinds[kind] {
		orpFailure(w, 404, "资源类型不存在")
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
	if err := orpNormalize(kind, body); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	permission := requiredORPPermission(r)
	if kind != "centers" && body["centerId"] != nil && !accountHasCenterPermission(roles, permission, centerIDString(body["centerId"])) {
		orpFailure(w, 403, "当前角色无权将资源调整到该中心")
		return
	}
	if err := s.orpValidate(r.Context(), kind, id, body); err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建事务")
		return
	}
	defer tx.Rollback()
	var previous []byte
	if err := tx.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind=? AND id=? FOR UPDATE", kind, id).Scan(&previous); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			orpFailure(w, 404, "资源不存在")
		} else {
			orpFailure(w, 500, "读取资源失败")
		}
		return
	}
	var old orpDocument
	if err := json.Unmarshal(previous, &old); err != nil {
		orpFailure(w, 500, "资源数据损坏")
		return
	}
	if kind == "settings" && (old["runtimeManaged"] == true || s.runtimeSettingDefinition(toString(old["key"])) != nil) {
		if definition := s.runtimeSettingDefinition(toString(old["key"])); definition != nil {
			old["runtimeManaged"] = true
			old["envKey"] = definition.EnvKey
			old["restartRequired"] = definition.RestartRequired
			old["optional"] = definition.Optional
			old["type"] = definition.Type
			old["isSensitive"] = definition.Sensitive
		}
		if toString(body["key"]) != toString(old["key"]) {
			body["key"] = old["key"]
		}
		for _, field := range []string{"runtimeManaged", "envKey", "restartRequired", "optional"} {
			if value, exists := old[field]; exists {
				body[field] = value
			}
		}
		if body["isSensitive"] != old["isSensitive"] || toString(body["type"]) != toString(old["type"]) {
			orpFailure(w, 400, "系统预置运行配置的类型和敏感标记不可修改")
			return
		}
	}
	if kind != "centers" {
		if body["centerId"] == nil && old["centerId"] != nil {
			body["centerId"] = old["centerId"]
		}
		if !accountHasCenterPermission(roles, permission, centerIDString(body["centerId"])) {
			orpFailure(w, 403, "当前角色无权将资源调整到该中心")
			return
		}
	}
	if kind == "centers" {
		if !accountHasCenterPermission(roles, permission, strconv.FormatInt(id, 10)) && !accountHasCenterPermission(roles, permission, centerIDString(old["legacyId"])) {
			orpFailure(w, 403, "当前角色无权修改该中心资源")
			return
		}
	} else if !accountHasCenterPermission(roles, permission, centerIDString(old["centerId"])) {
		orpFailure(w, 403, "当前角色无权修改该中心资源")
		return
	}
	if kind == "nodes" {
		if body["agentId"] != nil && toString(body["agentId"]) != toString(old["agentId"]) || body["agentCertificateFingerprint"] != nil && strings.ToLower(toString(body["agentCertificateFingerprint"])) != strings.ToLower(toString(old["agentCertificateFingerprint"])) {
			orpFailure(w, 403, "Agent 身份只能通过受限的证书登记接口修改")
			return
		}
		for _, field := range []string{"agentId", "agentCertificateFingerprint"} {
			if value, exists := old[field]; exists {
				body[field] = value
			}
		}
	}
	if kind == "upstream-groups" && toString(old["name"]) != toString(body["name"]) {
		if err := s.orpCheckReferences(r.Context(), kind, id); err != nil {
			orpFailure(w, 409, err.Error())
			return
		}
	}
	if kind == "centers" {
		body["legacyId"] = old["legacyId"]
		legacyID, err := uuid.Parse(toString(old["legacyId"]))
		if err != nil {
			orpFailure(w, 500, "中心映射损坏")
			return
		}
		if _, err := tx.ExecContext(r.Context(), "UPDATE center SET code=?,name=? WHERE id=?", toString(body["code"]), toString(body["name"]), legacyID[:]); err != nil {
			orpFailure(w, 409, "中心标识冲突或更新失败")
			return
		}
	}
	if kind == "setting-groups" {
		body["code"] = old["code"]
		body["isSystem"] = old["isSystem"]
		if old["isSystem"] == true {
			body["name"] = old["name"]
		}
		if toString(old["name"]) != toString(body["name"]) {
			if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=JSON_SET(document,'$.group',?) WHERE kind='settings' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.group'))=?", toString(body["name"]), toString(old["name"])); err != nil {
				orpFailure(w, 500, "迁移配置项分组失败")
				return
			}
		}
	}
	if kind == "certificates" && toString(body["privateKey"]) == "" {
		body["privateKey"] = old["privateKey"]
	}
	if kind == "certificates" && strings.HasPrefix(toString(body["privateKey"]), sealedPrefix) {
		key, err := orpOpen(toString(body["privateKey"]))
		if err != nil {
			orpFailure(w, 500, "解密原私钥失败")
			return
		}
		if _, err := tls.X509KeyPair([]byte(toString(body["certificate"])), []byte(key)); err != nil {
			orpFailure(w, 409, "证书与私钥不匹配")
			return
		}
	}
	optionalRuntimeSetting := kind == "settings" && (old["runtimeManaged"] == true || s.runtimeSettingDefinition(toString(old["key"])) != nil) && old["optional"] == true
	if kind == "settings" && body["isSensitive"] == true && toString(body["value"]) == "********" {
		body["value"] = old["value"]
	}
	if kind == "settings" && body["isSensitive"] == true && toString(body["value"]) == "" && !optionalRuntimeSetting {
		body["value"] = old["value"]
	}
	if err := orpProtect(kind, body); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	raw, _ := json.Marshal(body)
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=? WHERE kind=? AND id=?", raw, kind, id); err != nil {
		orpFailure(w, 500, "更新资源失败")
		return
	}
	if err := orpAudit(r.Context(), tx, actor, "update", kind, id, previous, raw, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交资源失败")
		return
	}
	orpReply(w, 200, orpPresented(kind, id, body))
}

func (s *Server) orpDelete(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !orpKinds[kind] {
		orpFailure(w, 404, "资源类型不存在")
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	if err := s.orpCheckReferences(r.Context(), kind, id); err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建事务")
		return
	}
	defer tx.Rollback()
	var previous []byte
	if err := tx.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind=? AND id=? FOR UPDATE", kind, id).Scan(&previous); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			orpFailure(w, 404, "资源不存在")
		} else {
			orpFailure(w, 500, "读取资源失败")
		}
		return
	}
	var old orpDocument
	if err := json.Unmarshal(previous, &old); err != nil {
		orpFailure(w, 500, "资源数据损坏")
		return
	}
	if kind == "settings" && (old["runtimeManaged"] == true || s.runtimeSettingDefinition(toString(old["key"])) != nil) {
		orpFailure(w, 409, "系统预置运行配置不能删除，可通过禁用回退到默认值")
		return
	}
	roles, roleErr := s.accountRoles(r, actor)
	permission := requiredORPPermission(r)
	if roleErr != nil || kind == "centers" && !accountHasCenterPermission(roles, permission, strconv.FormatInt(id, 10)) && !accountHasCenterPermission(roles, permission, centerIDString(old["legacyId"])) || kind != "centers" && !accountHasCenterPermission(roles, permission, centerIDString(old["centerId"])) {
		orpFailure(w, http.StatusForbidden, "当前角色无权删除该中心资源")
		return
	}
	if kind == "centers" {
		legacyID, err := uuid.Parse(toString(old["legacyId"]))
		if err != nil {
			orpFailure(w, 500, "中心映射损坏")
			return
		}
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM center WHERE id=?", legacyID[:]); err != nil {
			orpFailure(w, 409, "中心仍被旧配置引用，无法删除")
			return
		}
	}
	if kind == "nodes" {
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM orp_agent_token WHERE node_id=?", id); err != nil {
			orpFailure(w, 500, "撤销节点 Agent Token 失败")
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM orp_resource WHERE kind=? AND id=?", kind, id); err != nil {
		orpFailure(w, 500, "删除资源失败")
		return
	}
	if err := orpAudit(r.Context(), tx, actor, "delete", kind, id, previous, nil, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交删除失败")
		return
	}
	orpReply(w, 200, nil)
}

func orpPresented(kind string, id int64, body orpDocument) orpDocument {
	copy := orpDocument{}
	for key, value := range body {
		copy[key] = value
	}
	if kind == "centers" {
		for _, field := range []string{"latitude", "longitude"} {
			if value, ok := numericFloat(copy[field]); ok {
				copy[field] = value
			}
		}
	}
	copy["id"] = id
	now := time.Now().Format("2006-01-02 15:04:05")
	copy["createdAt"] = now
	copy["updatedAt"] = now
	orpRedact(kind, copy)
	return copy
}

func orpAudit(ctx context.Context, tx *sql.Tx, actor, action, kind string, id int64, before, after []byte, clientIP string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO orp_audit(actor,action,kind,resource_id,before_document,after_document,client_ip) VALUES(?,?,?,?,?,?,?)", actor, action, kind, id, before, after, clientIP)
	return err
}

func orpClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func requiredString(body orpDocument, key string) bool {
	value, ok := body[key].(string)
	return ok && strings.TrimSpace(value) != ""
}

func orpNormalize(kind string, body orpDocument) error {
	if kind != "http-listeners" {
		return nil
	}
	if body["errorPages"] == nil {
		body["errorPages"] = map[string]any{}
	}
	routes, ok := body["routes"].([]any)
	if !ok {
		body["routes"] = []any{}
		return nil
	}
	maxID := int64(0)
	seen := map[int64]bool{}
	for _, value := range routes {
		route, ok := value.(map[string]any)
		if !ok {
			return errors.New("路由必须是对象")
		}
		if strings.TrimSpace(toString(route["path"])) == "" {
			return errors.New("路由匹配路径不能为空")
		}
		if number, ok := route["id"].(float64); ok && number > 0 {
			id := int64(number)
			if seen[id] {
				return errors.New("路由 ID 重复")
			}
			seen[id] = true
			if id > maxID {
				maxID = id
			}
		}
	}
	for _, value := range routes {
		route := value.(map[string]any)
		if number, ok := route["id"].(float64); !ok || number <= 0 {
			maxID++
			route["id"] = maxID
		}
	}
	return nil
}

func (s *Server) orpValidate(ctx context.Context, kind string, id int64, body orpDocument) error {
	if kind == "http-listeners" {
		if raw, exists := body["errorPages"]; exists {
			pages, ok := raw.(map[string]any)
			if !ok {
				return errors.New("错误页面配置格式无效")
			}
			values := make(map[string]string, len(pages))
			for key, value := range pages {
				content, ok := value.(string)
				if !ok {
					return errors.New("错误页面内容必须是文本")
				}
				values[key] = content
			}
			if !validErrorPages(values) {
				return errors.New("错误页面配置无效：请检查状态码、Content-Type 和页面内容")
			}
		}
	}
	needed := map[string][]string{
		"centers": {"code", "name"}, "nodes": {"name", "host"},
		"http-listeners": {"domain"}, "certificates": {"name", "certificate"},
		"upstream-groups": {"name"}, "ip-groups": {"name"},
		"settings": {"key"}, "setting-groups": {"name"},
		"rbac-users": {"username", "realName"}, "rbac-roles": {"name"},
	}
	for _, field := range needed[kind] {
		if !requiredString(body, field) {
			return fmt.Errorf("%s 不能为空", field)
		}
	}
	if kind == "centers" {
		latitude := body["latitude"]
		longitude := body["longitude"]
		latEmpty := latitude == nil || latitude == ""
		lonEmpty := longitude == nil || longitude == ""
		if latEmpty != lonEmpty {
			return errors.New("纬度和经度需要同时填写")
		}
		if !latEmpty {
			lat, latOK := numericFloat(latitude)
			lon, lonOK := numericFloat(longitude)
			if !latOK || !lonOK || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
				return errors.New("纬度必须在 -90 到 90 之间，经度必须在 -180 到 180 之间")
			}
		}
	}
	if kind == "setting-groups" && id == 0 && !requiredString(body, "code") {
		return errors.New("分组编码不能为空")
	}
	if kind == "settings" {
		groups, err := s.orpLoad(ctx, "setting-groups")
		if err != nil {
			return err
		}
		found := false
		for _, group := range groups {
			if toString(group["name"]) == toString(body["group"]) {
				found = true
				break
			}
		}
		if !found {
			return errors.New("所属配置分组不存在")
		}
	}
	if kind == "dns-resolvers" {
		if net.ParseIP(fmt.Sprint(body["address"])) == nil {
			return errors.New("DNS 地址必须是有效 IP")
		}
	}
	if kind == "http-listeners" || kind == "dns-resolvers" || kind == "stream-services" {
		portField := "port"
		if kind == "stream-services" {
			portField = "listenPort"
		}
		port, ok := body[portField].(float64)
		if !ok || port < 1 || port > 65535 || port != float64(int(port)) {
			return errors.New("监听端口必须在 1 到 65535 之间")
		}
	}
	if kind == "nodes" || kind == "http-listeners" || kind == "stream-services" || kind == "dns-resolvers" || kind == "upstream-groups" {
		centerID := fmt.Sprint(body["centerId"])
		centers, err := s.orpLoad(ctx, "centers")
		if err != nil {
			return err
		}
		found := false
		for _, center := range centers {
			if fmt.Sprint(center["id"]) == centerID {
				found = true
				break
			}
		}
		if !found {
			return errors.New("所选中心不存在")
		}
	}
	if kind == "nodes" {
		items, err := s.orpLoad(ctx, "nodes")
		if err != nil {
			return err
		}
		for _, item := range items {
			if item["id"].(int64) == id {
				continue
			}
			if toString(item["name"]) == toString(body["name"]) {
				return errors.New("节点名称已存在")
			}
			endpoint := toString(body["controlEndpoint"])
			if endpoint != "" && toString(item["controlEndpoint"]) == endpoint {
				return errors.New("节点控制端点已被使用")
			}
		}
	}
	if kind == "certificates" {
		block, _ := pem.Decode([]byte(fmt.Sprint(body["certificate"])))
		if block == nil {
			return errors.New("证书 PEM 无效")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return errors.New("证书格式无效")
		}
		if key, ok := body["privateKey"].(string); ok && key != "" {
			if _, err := tls.X509KeyPair([]byte(fmt.Sprint(body["certificate"])), []byte(key)); err != nil {
				return errors.New("证书与私钥不匹配")
			}
		}
	}
	if kind == "http-listeners" && body["tlsCertId"] != nil {
		certificateID := fmt.Sprint(body["tlsCertId"])
		certificates, err := s.orpLoad(ctx, "certificates")
		if err != nil {
			return err
		}
		found := false
		for _, certificate := range certificates {
			if fmt.Sprint(certificate["id"]) == certificateID {
				if scope, ok := certificate["centerScope"].([]any); ok {
					allowed := false
					for _, center := range scope {
						if fmt.Sprint(center) == fmt.Sprint(body["centerId"]) {
							allowed = true
						}
					}
					if !allowed {
						return errors.New("证书不适用于所选中心")
					}
				}
				block, _ := pem.Decode([]byte(toString(certificate["certificate"])))
				if block == nil {
					return errors.New("证书格式无效")
				}
				parsed, err := x509.ParseCertificate(block.Bytes)
				if err != nil || parsed.VerifyHostname(toString(body["domain"])) != nil {
					return errors.New("证书不适用于监听域名")
				}
				found = true
				break
			}
		}
		if !found {
			return errors.New("所选证书不存在")
		}
	}
	if kind == "http-listeners" || kind == "stream-services" {
		groups, err := s.orpLoad(ctx, "upstream-groups")
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, group := range groups {
			if fmt.Sprint(group["centerId"]) == fmt.Sprint(body["centerId"]) {
				known[toString(group["name"])] = true
			}
		}
		if kind == "http-listeners" {
			if routes, ok := body["routes"].([]any); ok {
				for _, value := range routes {
					route := value.(map[string]any)
					if toString(route["type"]) != "proxy" {
						continue
					}
					target := strings.TrimPrefix(strings.TrimPrefix(toString(route["upstream"]), "http://"), "https://")
					if !known[strings.Split(target, ":")[0]] {
						return fmt.Errorf("路由 %s 引用的 Upstream 不存在", toString(route["path"]))
					}
				}
			}
		} else if backends, ok := body["backends"].([]any); ok {
			if len(backends) == 0 {
				return errors.New("Stream 至少需要一个后端上游组")
			}
			seen := map[string]bool{}
			for _, value := range backends {
				parts := strings.Split(toString(value), ":")
				if len(parts) != 2 || !nginxName.MatchString(parts[0]) {
					return errors.New("Stream 后端须为上游组名:端口")
				}
				port, err := resourcePort(parts[1])
				if err != nil {
					return errors.New("Stream 后端目标端口无效")
				}
				key := parts[0] + ":" + port
				if seen[key] {
					return errors.New("Stream 后端不能重复")
				}
				seen[key] = true
				if !known[parts[0]] {
					return errors.New("Stream 引用的 Upstream 不存在")
				}
			}
		} else {
			return errors.New("Stream 至少需要一个后端上游组")
		}
	}
	unique := map[string]string{"centers": "code", "nodes": "name", "upstream-groups": "name", "ip-groups": "name", "certificates": "name", "settings": "key", "setting-groups": "name", "rbac-users": "username", "rbac-roles": "name"}
	if field := unique[kind]; field != "" {
		items, err := s.orpLoad(ctx, kind)
		if err != nil {
			return err
		}
		for _, item := range items {
			if int64(item["id"].(int64)) == id {
				continue
			}
			if strings.EqualFold(fmt.Sprint(item[field]), fmt.Sprint(body[field])) {
				return fmt.Errorf("%s 已存在", field)
			}
		}
	}
	if kind == "setting-groups" && id == 0 {
		groups, err := s.orpLoad(ctx, kind)
		if err != nil {
			return err
		}
		for _, group := range groups {
			if toString(group["code"]) == toString(body["code"]) {
				return errors.New("分组编码已存在")
			}
		}
	}
	if kind == "http-listeners" || kind == "stream-services" || kind == "dns-resolvers" {
		items, err := s.orpLoad(ctx, kind)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item["id"].(int64) == id || fmt.Sprint(item["centerId"]) != fmt.Sprint(body["centerId"]) {
				continue
			}
			switch kind {
			case "http-listeners":
				if fmt.Sprint(item["port"]) == fmt.Sprint(body["port"]) {
					return errors.New("该中心的 HTTP 监听端口已被占用")
				}
			case "stream-services":
				if fmt.Sprint(item["listenPort"]) == fmt.Sprint(body["listenPort"]) && fmt.Sprint(item["protocol"]) == fmt.Sprint(body["protocol"]) {
					return errors.New("该中心同协议的 Stream 端口已被占用")
				}
			case "dns-resolvers":
				if fmt.Sprint(item["address"]) == fmt.Sprint(body["address"]) && fmt.Sprint(item["port"]) == fmt.Sprint(body["port"]) {
					return errors.New("该中心已存在相同 DNS 解析器")
				}
			}
		}
	}
	return nil
}

func (s *Server) orpCheckReferences(ctx context.Context, kind string, id int64) error {
	upstreamName := ""
	if kind == "upstream-groups" {
		var raw []byte
		if err := s.db.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind=? AND id=?", kind, id).Scan(&raw); err != nil {
			return err
		}
		var upstream orpDocument
		if err := json.Unmarshal(raw, &upstream); err != nil {
			return err
		}
		upstreamName = toString(upstream["name"])
	}
	childKinds := map[string][]string{
		"centers":         {"nodes", "http-listeners", "stream-services", "dns-resolvers"},
		"certificates":    {"http-listeners"},
		"upstream-groups": {"http-listeners", "stream-services"},
	}
	for _, childKind := range childKinds[kind] {
		items, err := s.orpLoad(ctx, childKind)
		if err != nil {
			return err
		}
		for _, item := range items {
			if kind == "centers" && fmt.Sprint(item["centerId"]) == strconv.FormatInt(id, 10) {
				return fmt.Errorf("资源正在被 %s 引用", childKind)
			}
			if kind == "certificates" && fmt.Sprint(item["tlsCertId"]) == strconv.FormatInt(id, 10) {
				return errors.New("证书正在被 HTTP 监听引用")
			}
			if kind == "upstream-groups" {
				if childKind == "http-listeners" {
					if routes, ok := item["routes"].([]any); ok {
						for _, value := range routes {
							route, ok := value.(map[string]any)
							if !ok {
								continue
							}
							target := strings.TrimPrefix(strings.TrimPrefix(toString(route["upstream"]), "http://"), "https://")
							if strings.Split(target, ":")[0] == upstreamName {
								return errors.New("Upstream 正在被 HTTP 路由引用")
							}
						}
					}
				} else if backends, ok := item["backends"].([]any); ok {
					for _, value := range backends {
						if strings.Split(toString(value), ":")[0] == upstreamName {
							return errors.New("Upstream 正在被 Stream 服务引用")
						}
					}
				}
			}
		}
	}
	return nil
}

func (s *Server) menuAll(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	// This API uses the frontend's server menu contract. Routes remain declared
	// locally, while permission filtered menus are assembled from the catalog.
	orpReply(w, 200, []any{})
}
