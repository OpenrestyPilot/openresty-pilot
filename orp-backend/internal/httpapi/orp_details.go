package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) orpRawDocument(r *http.Request, kind string, id int64) (orpDocument, error) {
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind=? AND id=?", kind, id).Scan(&raw); err != nil {
		return nil, err
	}
	var body orpDocument
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body["id"] = id
	return body, nil
}

func (s *Server) orpCertificateDetail(w http.ResponseWriter, r *http.Request) {
	if !s.orpSecretAccess(w, r) {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	body, err := s.orpRawDocument(r, "certificates", id)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "证书不存在")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取证书失败")
		return
	}
	key, err := orpOpen(toString(body["privateKey"]))
	if err != nil {
		orpFailure(w, 500, "解密证书私钥失败")
		return
	}
	orpReply(w, 200, map[string]any{"certificate": body["certificate"], "certificateChain": body["certificateChain"], "privateKey": key})
}

func (s *Server) orpCertificatePublicDetail(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	body, err := s.orpRawDocument(r, "certificates", id)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "证书不存在")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取证书失败")
		return
	}
	orpReply(w, 200, map[string]any{"certificate": body["certificate"], "certificateChain": body["certificateChain"]})
}

func (s *Server) orpCertificateStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	items, err := s.orpLoad(r.Context(), "certificates")
	if err != nil {
		orpFailure(w, 500, "读取证书失败")
		return
	}
	expired, critical, expiring := 0, 0, 0
	watchlist := []map[string]any{}
	for _, item := range items {
		date, err := time.Parse("2006-01-02", toString(item["notAfter"]))
		if err != nil {
			continue
		}
		days := int(time.Until(date).Hours() / 24)
		if days < 0 {
			expired++
		} else if days <= 7 {
			critical++
		} else if days <= 30 {
			expiring++
		}
		if days <= 30 {
			watchlist = append(watchlist, map[string]any{"id": item["id"], "name": item["name"], "domains": item["domains"], "notAfter": item["notAfter"], "days": days})
		}
	}
	orpReply(w, 200, map[string]any{"total": len(items), "expiredCount": expired, "criticalCount": critical, "expiringCount": expiring, "watchlist": watchlist})
}

func (s *Server) orpSettingPlain(w http.ResponseWriter, r *http.Request) {
	if !s.orpSecretAccess(w, r) {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	body, err := s.orpRawDocument(r, "settings", id)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "配置项不存在")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取配置项失败")
		return
	}
	if body["isSensitive"] == true {
		plain := ""
		var err error
		if toString(body["value"]) != "" {
			plain, err = orpOpen(toString(body["value"]))
		}
		if err != nil {
			orpFailure(w, 500, "解密配置项失败")
			return
		}
		body["value"] = plain
	}
	orpReply(w, 200, body)
}

func (s *Server) orpSecretAccess(w http.ResponseWriter, r *http.Request) bool {
	_, ok := s.requirePermission(w, r, "orp:secrets:read")
	return ok
}

func (s *Server) orpAuditList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:audit:read"); !ok {
		return
	}
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
	query := r.URL.Query()
	where := []string{}
	args := []any{}
	roles, err := s.accountRoles(r, usernameFromToken(r.Header.Get("Authorization")))
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	if !accountHasGlobalCenterPermission(roles, "orp:audit:read") {
		centerID := query.Get("centerId")
		centerIDs, _ := accountCenterIDs(roles, "orp:audit:read")
		if centerID != "" {
			centerIDs = []string{centerID}
		}
		if len(centerIDs) == 0 {
			where = append(where, "1=0")
		} else {
			placeholders := strings.TrimRight(strings.Repeat("?,", len(centerIDs)), ",")
			where = append(where, "((kind='centers' AND resource_id IN ("+placeholders+")) OR JSON_UNQUOTE(JSON_EXTRACT(before_document,'$.centerId')) IN ("+placeholders+") OR JSON_UNQUOTE(JSON_EXTRACT(after_document,'$.centerId')) IN ("+placeholders+"))")
			for range 3 {
				for _, id := range centerIDs {
					args = append(args, id)
				}
			}
		}
	}
	for _, filter := range []struct{ param, column string }{{"action", "action"}, {"operator", "actor"}} {
		if value := strings.TrimSpace(query.Get(filter.param)); value != "" {
			where = append(where, filter.column+"=?")
			args = append(args, value)
		}
	}
	if module := strings.TrimSpace(query.Get("module")); module != "" {
		where = append(where, "kind=?")
		args = append(args, orpAuditKind(module))
	}
	if start := strings.TrimSpace(query.Get("startTime")); start != "" {
		where = append(where, "created_at>=?")
		args = append(args, start)
	}
	if end := strings.TrimSpace(query.Get("endTime")); end != "" {
		where = append(where, "created_at<=?")
		args = append(args, end)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_audit"+whereSQL, args...).Scan(&total); err != nil {
		orpFailure(w, 500, "读取审计日志失败")
		return
	}
	listArgs := append(append([]any{}, args...), pageSize, (pageNo-1)*pageSize)
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,actor,action,kind,resource_id,client_ip,created_at FROM orp_audit"+whereSQL+" ORDER BY id DESC LIMIT ? OFFSET ?", listArgs...)
	if err != nil {
		orpFailure(w, 500, "读取审计日志失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, resourceID int64
		var actor, action, kind, clientIP string
		var created time.Time
		if err := rows.Scan(&id, &actor, &action, &kind, &resourceID, &clientIP, &created); err != nil {
			orpFailure(w, 500, "读取审计日志失败")
			return
		}
		items = append(items, map[string]any{"id": id, "operator": actor, "action": action, "module": orpAuditModule(kind), "target": kind + ":" + strconv.FormatInt(resourceID, 10), "detail": fmt.Sprintf("%s %s #%d", action, kind, resourceID), "ip": clientIP, "createdAt": created.Format("2006-01-02 15:04:05")})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取审计日志失败")
		return
	}
	orpReply(w, 200, map[string]any{"items": items, "total": total})
}

func orpAuditModule(kind string) string {
	modules := map[string]string{"centers": "center", "nodes": "node", "http-listeners": "http", "stream-services": "stream", "certificates": "tls", "dns-resolvers": "dns", "upstream-groups": "upstream", "settings": "setting", "setting-groups": "setting"}
	if value := modules[kind]; value != "" {
		return value
	}
	return kind
}

func orpAuditKind(module string) string {
	for _, kind := range []string{"centers", "nodes", "http-listeners", "stream-services", "certificates", "dns-resolvers", "upstream-groups", "settings", "setting-groups"} {
		if orpAuditModule(kind) == module {
			return kind
		}
	}
	return module
}
