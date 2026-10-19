package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const apiKeyPrefix = "orp_live_"

type apiConsumerInput struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Status      *string `json:"status"`
}

type apiConsumerView struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Status         string    `json:"status"`
	ActiveKeyCount int       `json:"activeKeyCount"`
	CreatedBy      string    `json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type apiKeyView struct {
	ID            int64      `json:"id"`
	ConsumerID    int64      `json:"consumerId"`
	KeyPrefix     string     `json:"keyPrefix"`
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	CreatedBy     string     `json:"createdBy"`
	CreatedAt     time.Time  `json:"createdAt"`
	RevokedBy     string     `json:"revokedBy,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
	RotatedFromID *int64     `json:"rotatedFromId,omitempty"`
}

type apiKeyIssueInput struct {
	ExpiresAt string `json:"expiresAt"`
}

type apiKeyIssueView struct {
	apiKeyView
	Secret string `json:"secret"`
}

type apiConsumerIdentity struct {
	ConsumerID int64
	KeyID      int64
}

func (s *Server) apiConsumersList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:system:manage"); !ok {
		return
	}
	page, pageSize := credentialPage(r)
	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_api_consumer").Scan(&total); err != nil {
		orpFailure(w, 500, "读取调用方失败")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT c.id,c.name,c.description,c.status,c.created_by,c.created_at,c.updated_at,
		(SELECT COUNT(*) FROM orp_api_key k WHERE k.consumer_id=c.id AND k.status='active' AND (k.expires_at IS NULL OR k.expires_at>NOW(6)))
		FROM orp_api_consumer c ORDER BY c.id DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		orpFailure(w, 500, "读取调用方失败")
		return
	}
	defer rows.Close()
	items := []apiConsumerView{}
	for rows.Next() {
		var item apiConsumerView
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt, &item.ActiveKeyCount); err != nil {
			orpFailure(w, 500, "读取调用方失败")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取调用方失败")
		return
	}
	orpReply(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": pageSize})
}

func (s *Server) apiConsumerCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:system:manage")
	if !ok {
		return
	}
	var input apiConsumerInput
	if !decodeCredentialBody(w, r, &input) {
		return
	}
	name, err := normalizeConsumerName(input.Name)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	description := strings.TrimSpace(input.Description)
	if len(description) > 500 {
		orpFailure(w, 400, "调用方描述不能超过 500 个字符")
		return
	}
	status := "enabled"
	if input.Status != nil {
		status = strings.TrimSpace(*input.Status)
		if !validConsumerStatus(status) {
			orpFailure(w, 400, "调用方状态仅支持 enabled 或 disabled")
			return
		}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建调用方失败")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), `INSERT INTO orp_api_consumer(name,description,status,created_by) VALUES(?,?,?,?)`, name, description, status, actor)
	if err != nil {
		orpFailure(w, 409, "创建调用方失败：名称可能已存在")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		orpFailure(w, 500, "读取调用方编号失败")
		return
	}
	var item apiConsumerView
	if err := tx.QueryRowContext(r.Context(), `SELECT id,name,description,status,created_by,created_at,updated_at FROM orp_api_consumer WHERE id=?`, id).Scan(&item.ID, &item.Name, &item.Description, &item.Status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		orpFailure(w, 500, "读取调用方失败")
		return
	}
	if err := auditCredentialAction(r.Context(), tx, actor, "create", "api-consumer", id, nil, item, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录调用方审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "创建调用方失败")
		return
	}
	orpReply(w, 201, item)
}

func (s *Server) apiConsumerGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:system:manage"); !ok {
		return
	}
	id, ok := credentialPathID(w, r, "id")
	if !ok {
		return
	}
	item, err := loadAPIConsumer(r.Context(), s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "调用方不存在")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取调用方失败")
		return
	}
	orpReply(w, 200, item)
}

func (s *Server) apiConsumerUpdate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:system:manage")
	if !ok {
		return
	}
	id, ok := credentialPathID(w, r, "id")
	if !ok {
		return
	}
	var input apiConsumerInput
	if !decodeCredentialBody(w, r, &input) {
		return
	}
	name, err := normalizeConsumerName(input.Name)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	description := strings.TrimSpace(input.Description)
	if len(description) > 500 {
		orpFailure(w, 400, "调用方描述不能超过 500 个字符")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "更新调用方失败")
		return
	}
	defer tx.Rollback()
	var before apiConsumerView
	if err := tx.QueryRowContext(r.Context(), `SELECT id,name,description,status,created_by,created_at,updated_at FROM orp_api_consumer WHERE id=? FOR UPDATE`, id).Scan(&before.ID, &before.Name, &before.Description, &before.Status, &before.CreatedBy, &before.CreatedAt, &before.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			orpFailure(w, 404, "调用方不存在")
		} else {
			orpFailure(w, 500, "读取调用方失败")
		}
		return
	}
	status := before.Status
	if input.Status != nil {
		status = strings.TrimSpace(*input.Status)
		if !validConsumerStatus(status) {
			orpFailure(w, 400, "调用方状态仅支持 enabled 或 disabled")
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE orp_api_consumer SET name=?,description=?,status=? WHERE id=?`, name, description, status, id); err != nil {
		orpFailure(w, 409, "更新调用方失败：名称可能已存在")
		return
	}
	revokedCount := int64(0)
	if status == "disabled" {
		result, err := tx.ExecContext(r.Context(), `UPDATE orp_api_key SET status='revoked',revoked_by=?,revoked_at=NOW(6) WHERE consumer_id=? AND status='active'`, actor, id)
		if err != nil {
			orpFailure(w, 500, "停用调用方密钥失败")
			return
		}
		revokedCount, _ = result.RowsAffected()
	}
	after, err := loadAPIConsumer(r.Context(), tx, id)
	if err != nil {
		orpFailure(w, 500, "读取调用方失败")
		return
	}
	if err := auditCredentialAction(r.Context(), tx, actor, "update", "api-consumer", id, before, map[string]any{"consumer": after, "revokedKeyCount": revokedCount}, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录调用方审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "更新调用方失败")
		return
	}
	orpReply(w, 200, after)
}

// Deleting a consumer is a soft delete: it disables the consumer and revokes all
// active keys in the same transaction so no credential remains usable.
func (s *Server) apiConsumerDelete(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:system:manage")
	if !ok {
		return
	}
	id, ok := credentialPathID(w, r, "id")
	if !ok {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "停用调用方失败")
		return
	}
	defer tx.Rollback()
	var before apiConsumerView
	if err := tx.QueryRowContext(r.Context(), `SELECT id,name,description,status,created_by,created_at,updated_at FROM orp_api_consumer WHERE id=? FOR UPDATE`, id).Scan(&before.ID, &before.Name, &before.Description, &before.Status, &before.CreatedBy, &before.CreatedAt, &before.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			orpFailure(w, 404, "调用方不存在")
		} else {
			orpFailure(w, 500, "读取调用方失败")
		}
		return
	}
	result, err := tx.ExecContext(r.Context(), `UPDATE orp_api_key SET status='revoked',revoked_by=?,revoked_at=NOW(6) WHERE consumer_id=? AND status='active'`, actor, id)
	if err != nil {
		orpFailure(w, 500, "撤销调用方密钥失败")
		return
	}
	revokedCount, _ := result.RowsAffected()
	if _, err := tx.ExecContext(r.Context(), `UPDATE orp_api_consumer SET status='disabled' WHERE id=?`, id); err != nil {
		orpFailure(w, 500, "停用调用方失败")
		return
	}
	after, err := loadAPIConsumer(r.Context(), tx, id)
	if err != nil {
		orpFailure(w, 500, "读取调用方失败")
		return
	}
	if err := auditCredentialAction(r.Context(), tx, actor, "delete", "api-consumer", id, before, map[string]any{"consumer": after, "revokedKeyCount": revokedCount}, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录调用方审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "停用调用方失败")
		return
	}
	orpReply(w, 200, after)
}

func (s *Server) apiConsumerKeysList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:system:manage"); !ok {
		return
	}
	consumerID, ok := credentialPathID(w, r, "id")
	if !ok {
		return
	}
	if _, err := loadAPIConsumer(r.Context(), s.db, consumerID); errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "调用方不存在")
		return
	} else if err != nil {
		orpFailure(w, 500, "读取调用方失败")
		return
	}
	page, pageSize := credentialPage(r)
	var total int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM orp_api_key WHERE consumer_id=?`, consumerID).Scan(&total); err != nil {
		orpFailure(w, 500, "读取密钥列表失败")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,consumer_id,key_prefix,status,expires_at,last_used_at,created_by,created_at,revoked_by,revoked_at,rotated_from_id
		FROM orp_api_key WHERE consumer_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, consumerID, pageSize, (page-1)*pageSize)
	if err != nil {
		orpFailure(w, 500, "读取密钥列表失败")
		return
	}
	defer rows.Close()
	items := []apiKeyView{}
	for rows.Next() {
		item, err := scanAPIKeyView(rows)
		if err != nil {
			orpFailure(w, 500, "读取密钥列表失败")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取密钥列表失败")
		return
	}
	orpReply(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": pageSize})
}

func (s *Server) apiConsumerKeyCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:system:manage")
	if !ok {
		return
	}
	consumerID, ok := credentialPathID(w, r, "id")
	if !ok {
		return
	}
	var input apiKeyIssueInput
	if !decodeCredentialBody(w, r, &input) {
		return
	}
	var expiresAt *time.Time
	if input.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, input.ExpiresAt)
		if err != nil || !parsed.After(time.Now()) {
			orpFailure(w, 400, "expiresAt 必须为未来时间的 RFC3339 时间戳")
			return
		}
		expiresAt = &parsed
	}
	issued, err := s.issueAPIKey(r.Context(), actor, consumerID, nil, expiresAt, orpClientIP(r))
	if err != nil {
		writeCredentialError(w, err)
		return
	}
	writeCredentialSecret(w, 201, issued)
}

func (s *Server) apiConsumerKeyRotate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:system:manage")
	if !ok {
		return
	}
	consumerID, ok := credentialPathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := credentialPathID(w, r, "keyID")
	if !ok {
		return
	}
	var input apiKeyIssueInput
	if !decodeCredentialBody(w, r, &input) {
		return
	}
	var expiresAt *time.Time
	if input.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, input.ExpiresAt)
		if err != nil || !parsed.After(time.Now()) {
			orpFailure(w, 400, "expiresAt 必须为未来时间的 RFC3339 时间戳")
			return
		}
		expiresAt = &parsed
	}
	issued, err := s.issueAPIKey(r.Context(), actor, consumerID, &keyID, expiresAt, orpClientIP(r))
	if err != nil {
		writeCredentialError(w, err)
		return
	}
	writeCredentialSecret(w, 201, issued)
}

func (s *Server) apiConsumerKeyRevoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:system:manage")
	if !ok {
		return
	}
	consumerID, ok := credentialPathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := credentialPathID(w, r, "keyID")
	if !ok {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "撤销密钥失败")
		return
	}
	defer tx.Rollback()
	var before apiKeyView
	if err := tx.QueryRowContext(r.Context(), `SELECT id,consumer_id,key_prefix,status,expires_at,last_used_at,created_by,created_at,revoked_by,revoked_at,rotated_from_id
		FROM orp_api_key WHERE id=? AND consumer_id=? FOR UPDATE`, keyID, consumerID).Scan(&before.ID, &before.ConsumerID, &before.KeyPrefix, &before.Status, &before.ExpiresAt, &before.LastUsedAt, &before.CreatedBy, &before.CreatedAt, &before.RevokedBy, &before.RevokedAt, &before.RotatedFromID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			orpFailure(w, 404, "密钥不存在")
		} else {
			orpFailure(w, 500, "读取密钥失败")
		}
		return
	}
	if before.Status == "active" {
		if _, err := tx.ExecContext(r.Context(), `UPDATE orp_api_key SET status='revoked',revoked_by=?,revoked_at=NOW(6) WHERE id=?`, actor, keyID); err != nil {
			orpFailure(w, 500, "撤销密钥失败")
			return
		}
	}
	var after apiKeyView
	if err := tx.QueryRowContext(r.Context(), `SELECT id,consumer_id,key_prefix,status,expires_at,last_used_at,created_by,created_at,revoked_by,revoked_at,rotated_from_id
		FROM orp_api_key WHERE id=?`, keyID).Scan(&after.ID, &after.ConsumerID, &after.KeyPrefix, &after.Status, &after.ExpiresAt, &after.LastUsedAt, &after.CreatedBy, &after.CreatedAt, &after.RevokedBy, &after.RevokedAt, &after.RotatedFromID); err != nil {
		orpFailure(w, 500, "读取密钥失败")
		return
	}
	if err := auditCredentialAction(r.Context(), tx, actor, "revoke", "api-key", keyID, before, after, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录密钥审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "撤销密钥失败")
		return
	}
	orpReply(w, 200, after)
}

func (s *Server) issueAPIKey(ctx context.Context, actor string, consumerID int64, rotateFrom *int64, expiresAt *time.Time, clientIP string) (apiKeyIssueView, error) {
	var issued apiKeyIssueView
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return issued, credentialError{status: 500, message: "创建密钥失败"}
	}
	defer tx.Rollback()
	var consumerStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM orp_api_consumer WHERE id=? FOR UPDATE`, consumerID).Scan(&consumerStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return issued, credentialError{status: 404, message: "调用方不存在"}
		}
		return issued, credentialError{status: 500, message: "读取调用方失败"}
	}
	if consumerStatus != "enabled" {
		return issued, credentialError{status: 409, message: "调用方已停用，不能签发密钥"}
	}
	if rotateFrom != nil {
		var oldStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM orp_api_key WHERE id=? AND consumer_id=? FOR UPDATE`, *rotateFrom, consumerID).Scan(&oldStatus); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return issued, credentialError{status: 404, message: "待轮换密钥不存在"}
			}
			return issued, credentialError{status: 500, message: "读取待轮换密钥失败"}
		}
		if oldStatus != "active" {
			return issued, credentialError{status: 409, message: "只能轮换有效密钥"}
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return issued, credentialError{status: 500, message: "生成密钥失败"}
	}
	secret := apiKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(secret))
	var rotatedFrom any
	if rotateFrom != nil {
		rotatedFrom = *rotateFrom
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO orp_api_key(consumer_id,key_prefix,secret_hash,status,expires_at,created_by,rotated_from_id)
		VALUES(?,?,?,'active',?,?,?)`, consumerID, secret[:20], hash[:], expiresAt, actor, rotatedFrom)
	if err != nil {
		return issued, credentialError{status: 500, message: "保存密钥哈希失败"}
	}
	keyID, err := result.LastInsertId()
	if err != nil {
		return issued, credentialError{status: 500, message: "读取密钥编号失败"}
	}
	if err := tx.QueryRowContext(ctx, `SELECT id,consumer_id,key_prefix,status,expires_at,last_used_at,created_by,created_at,revoked_by,revoked_at,rotated_from_id
		FROM orp_api_key WHERE id=?`, keyID).Scan(&issued.ID, &issued.ConsumerID, &issued.KeyPrefix, &issued.Status, &issued.ExpiresAt, &issued.LastUsedAt, &issued.CreatedBy, &issued.CreatedAt, &issued.RevokedBy, &issued.RevokedAt, &issued.RotatedFromID); err != nil {
		return issued, credentialError{status: 500, message: "读取密钥记录失败"}
	}
	issued.Secret = secret
	if err := auditCredentialAction(ctx, tx, actor, "issue", "api-key", keyID, nil, issued.apiKeyView, clientIP); err != nil {
		return issued, credentialError{status: 500, message: "记录密钥审计失败"}
	}
	if err := tx.Commit(); err != nil {
		return issued, credentialError{status: 500, message: "签发密钥失败"}
	}
	return issued, nil
}

type credentialError struct {
	status  int
	message string
}

func (e credentialError) Error() string { return e.message }

func writeCredentialError(w http.ResponseWriter, err error) {
	var credentialErr credentialError
	if errors.As(err, &credentialErr) {
		orpFailure(w, credentialErr.status, credentialErr.message)
		return
	}
	orpFailure(w, 500, "调用方凭据操作失败")
}

func writeCredentialSecret(w http.ResponseWriter, status int, issued apiKeyIssueView) {
	w.Header().Set("Cache-Control", "no-store")
	orpReply(w, status, issued)
}

func scanAPIKeyView(rows *sql.Rows) (apiKeyView, error) {
	var item apiKeyView
	err := rows.Scan(&item.ID, &item.ConsumerID, &item.KeyPrefix, &item.Status, &item.ExpiresAt, &item.LastUsedAt, &item.CreatedBy, &item.CreatedAt, &item.RevokedBy, &item.RevokedAt, &item.RotatedFromID)
	return item, err
}

type credentialQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadAPIConsumer(ctx context.Context, query credentialQuerier, id int64) (apiConsumerView, error) {
	var item apiConsumerView
	err := query.QueryRowContext(ctx, `SELECT c.id,c.name,c.description,c.status,c.created_by,c.created_at,c.updated_at,
		(SELECT COUNT(*) FROM orp_api_key k WHERE k.consumer_id=c.id AND k.status='active' AND (k.expires_at IS NULL OR k.expires_at>NOW(6)))
		FROM orp_api_consumer c WHERE c.id=?`, id).Scan(&item.ID, &item.Name, &item.Description, &item.Status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt, &item.ActiveKeyCount)
	return item, err
}

func normalizeConsumerName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "", errors.New("调用方名称不能为空且不能超过 128 个字符")
	}
	return value, nil
}

func validConsumerStatus(value string) bool { return value == "enabled" || value == "disabled" }

func credentialPathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "无效的调用方或密钥 ID")
		return 0, false
	}
	return id, true
}

func credentialPage(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func decodeCredentialBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		orpFailure(w, 400, "请求内容不是有效的 JSON")
		return false
	}
	return true
}

func auditCredentialAction(ctx context.Context, tx *sql.Tx, actor, action, kind string, id int64, before, after any, clientIP string) error {
	var beforeJSON, afterJSON []byte
	var err error
	if before != nil {
		beforeJSON, err = json.Marshal(before)
		if err != nil {
			return fmt.Errorf("encode audit before state: %w", err)
		}
	}
	if after != nil {
		afterJSON, err = json.Marshal(after)
		if err != nil {
			return fmt.Errorf("encode audit after state: %w", err)
		}
	}
	return orpAudit(ctx, tx, actor, action, kind, id, beforeJSON, afterJSON, clientIP)
}

// lookupAPIKey is the future data-plane authentication primitive. It only
// accepts platform-issued keys, hashes the presented value, and returns an
// identity for enabled consumers with active and unexpired credentials.
func (s *Server) lookupAPIKey(ctx context.Context, secret string) (apiConsumerIdentity, bool, error) {
	if !strings.HasPrefix(secret, apiKeyPrefix) || len(secret) != len(apiKeyPrefix)+43 {
		return apiConsumerIdentity{}, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, apiKeyPrefix))
	if err != nil || len(raw) != 32 {
		return apiConsumerIdentity{}, false, nil
	}
	hash := sha256.Sum256([]byte(secret))
	var identity apiConsumerIdentity
	var storedHash []byte
	err = s.db.QueryRowContext(ctx, `SELECT k.id,k.consumer_id,k.secret_hash
		FROM orp_api_key k JOIN orp_api_consumer c ON c.id=k.consumer_id
		WHERE k.secret_hash=? AND k.status='active' AND c.status='enabled' AND (k.expires_at IS NULL OR k.expires_at>NOW(6))`, hash[:]).Scan(&identity.KeyID, &identity.ConsumerID, &storedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return apiConsumerIdentity{}, false, nil
	}
	if err != nil {
		return apiConsumerIdentity{}, false, err
	}
	if !hmac.Equal(storedHash, hash[:]) {
		return apiConsumerIdentity{}, false, nil
	}
	return identity, true, nil
}
