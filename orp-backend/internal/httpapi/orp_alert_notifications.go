package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const tlsAlertSweepInterval = 5 * time.Minute

type alertChannelInput struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	MessageFormat string `json:"messageFormat"`
	WebhookURL    string `json:"webhookUrl"`
	Secret        string `json:"secret"`
	Enabled       *bool  `json:"enabled"`
}

type alertChannelView struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	MessageFormat string    `json:"messageFormat"`
	TargetHint    string    `json:"targetHint"`
	HasSecret     bool      `json:"hasSecret"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type alertChannelRecord struct {
	alertChannelView
	TargetEncrypted string
	SecretEncrypted sql.NullString
}

type tlsAlertRuleInput struct {
	Enabled    bool    `json:"enabled"`
	DaysBefore int     `json:"daysBefore"`
	ChannelIDs []int64 `json:"channelIds"`
}

type tlsAlertRuleView struct {
	CertificateID int64    `json:"certificateId"`
	Name          string   `json:"name"`
	Domains       string   `json:"domains"`
	NotAfter      string   `json:"notAfter"`
	DaysRemaining int      `json:"daysRemaining"`
	Enabled       bool     `json:"enabled"`
	DaysBefore    int      `json:"daysBefore"`
	ChannelIDs    []int64  `json:"channelIds"`
	ChannelNames  []string `json:"channelNames"`
}

type alertDeliveryView struct {
	ID              int64      `json:"id"`
	EventType       string     `json:"eventType"`
	ChannelID       int64      `json:"channelId"`
	ChannelName     string     `json:"channelName"`
	CertificateID   *int64     `json:"certificateId,omitempty"`
	CertificateName string     `json:"certificateName"`
	Title           string     `json:"title"`
	Content         string     `json:"content"`
	Status          string     `json:"status"`
	AttemptCount    int        `json:"attemptCount"`
	ResponseStatus  *int       `json:"responseStatus,omitempty"`
	ResponseText    string     `json:"responseText"`
	ErrorText       string     `json:"errorText"`
	DeliveredAt     *time.Time `json:"deliveredAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type tlsAlertMessage struct {
	EventType     string `json:"eventType"`
	Title         string `json:"title"`
	Message       string `json:"message"`
	CertificateID int64  `json:"certificateId,omitempty"`
	Certificate   string `json:"certificate,omitempty"`
	Domains       string `json:"domains,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	DaysRemaining int    `json:"daysRemaining,omitempty"`
	ThresholdDays int    `json:"thresholdDays,omitempty"`
	OccurredAt    string `json:"occurredAt"`
}

type alertSweepResult struct {
	CertificatesChecked int      `json:"certificatesChecked"`
	NotificationsSent   int      `json:"notificationsSent"`
	NotificationsFailed int      `json:"notificationsFailed"`
	Errors              []string `json:"errors,omitempty"`
}

func (s *Server) orpAlertChannelsList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.orpIdentity(w, r); !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,channel_type,message_format,target_encrypted,secret_encrypted,enabled,created_at,updated_at
		FROM orp_alert_channel ORDER BY id DESC`)
	if err != nil {
		orpFailure(w, 500, "读取告警通道失败")
		return
	}
	defer rows.Close()
	items := []alertChannelView{}
	for rows.Next() {
		var row alertChannelRecord
		if err := rows.Scan(&row.ID, &row.Name, &row.Type, &row.MessageFormat, &row.TargetEncrypted, &row.SecretEncrypted, &row.Enabled, &row.CreatedAt, &row.UpdatedAt); err != nil {
			orpFailure(w, 500, "读取告警通道失败")
			return
		}
		urlValue, err := orpOpen(row.TargetEncrypted)
		if err != nil {
			orpFailure(w, 500, "解密告警通道地址失败，请检查服务端数据密钥")
			return
		}
		row.TargetHint = alertTargetHint(urlValue)
		row.HasSecret = row.SecretEncrypted.Valid && row.SecretEncrypted.String != ""
		items = append(items, row.alertChannelView)
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取告警通道失败")
		return
	}
	orpReply(w, 200, items)
}

func (s *Server) orpAlertChannelCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	var input alertChannelInput
	if !decodeAlertBody(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 {
		orpFailure(w, 400, "通道名称不能为空且不能超过 128 个字符")
		return
	}
	if !validAlertChannelType(input.Type) {
		orpFailure(w, 400, "通道类型仅支持 webhook 或 feishu")
		return
	}
	messageFormat, err := normalizeAlertMessageFormat(input.Type, input.MessageFormat)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if err := validateAlertTarget(input.WebhookURL); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	targetEncrypted, err := orpSeal(strings.TrimSpace(input.WebhookURL))
	if err != nil {
		orpFailure(w, 500, "加密告警通道地址失败")
		return
	}
	var secretEncrypted any
	if strings.TrimSpace(input.Secret) != "" {
		sealed, err := orpSeal(input.Secret)
		if err != nil {
			orpFailure(w, 500, "加密告警签名密钥失败")
			return
		}
		secretEncrypted = sealed
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	now := time.Now()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建告警通道")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), `INSERT INTO orp_alert_channel(name,channel_type,message_format,target_encrypted,secret_encrypted,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)`, name, input.Type, messageFormat, targetEncrypted, secretEncrypted, enabled, now, now)
	if err != nil {
		orpFailure(w, 409, "保存告警通道失败：名称可能已存在")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		orpFailure(w, 500, "读取告警通道编号失败")
		return
	}
	view := alertChannelView{ID: id, Name: name, Type: input.Type, MessageFormat: messageFormat, TargetHint: alertTargetHint(input.WebhookURL), HasSecret: secretEncrypted != nil, Enabled: enabled, CreatedAt: now, UpdatedAt: now}
	if err := auditAlertAction(r.Context(), tx, actor, "create", "alert-channel", id, nil, view, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录告警通道审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "保存告警通道失败")
		return
	}
	orpReply(w, 201, view)
}

func (s *Server) orpAlertChannelUpdate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	var input alertChannelInput
	if !decodeAlertBody(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 || !validAlertChannelType(input.Type) {
		orpFailure(w, 400, "通道名称或类型无效")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法更新告警通道")
		return
	}
	defer tx.Rollback()
	var oldName, oldType, oldFormat, targetEncrypted string
	var secretEncrypted sql.NullString
	var enabled bool
	var createdAt, updatedAt time.Time
	err = tx.QueryRowContext(r.Context(), `SELECT name,channel_type,message_format,target_encrypted,secret_encrypted,enabled,created_at,updated_at
		FROM orp_alert_channel WHERE id=? FOR UPDATE`, id).
		Scan(&oldName, &oldType, &oldFormat, &targetEncrypted, &secretEncrypted, &enabled, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "告警通道不存在")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取告警通道失败")
		return
	}
	oldTargetEncrypted := targetEncrypted
	oldHasSecret := secretEncrypted.Valid && secretEncrypted.String != ""
	oldEnabled := enabled
	messageFormat := input.MessageFormat
	if messageFormat == "" {
		if input.Type == "feishu" {
			messageFormat = oldFormat
		} else {
			messageFormat = "text"
		}
	}
	messageFormat, err = normalizeAlertMessageFormat(input.Type, messageFormat)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	urlValue := strings.TrimSpace(input.WebhookURL)
	if urlValue == "" {
		urlValue, err = orpOpen(targetEncrypted)
		if err != nil {
			orpFailure(w, 500, "解密告警通道地址失败")
			return
		}
	} else if err := validateAlertTarget(urlValue); err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(input.WebhookURL) != "" {
		sealed, err := orpSeal(urlValue)
		if err != nil {
			orpFailure(w, 500, "加密告警通道地址失败")
			return
		}
		targetEncrypted = sealed
	}
	if strings.TrimSpace(input.Secret) != "" {
		sealed, err := orpSeal(input.Secret)
		if err != nil {
			orpFailure(w, 500, "加密告警签名密钥失败")
			return
		}
		secretEncrypted = sql.NullString{String: sealed, Valid: true}
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	now := time.Now()
	if _, err := tx.ExecContext(r.Context(), `UPDATE orp_alert_channel SET name=?,channel_type=?,message_format=?,target_encrypted=?,secret_encrypted=?,enabled=?,updated_at=? WHERE id=?`,
		name, input.Type, messageFormat, targetEncrypted, nullString(secretEncrypted), enabled, now, id); err != nil {
		orpFailure(w, 409, "保存告警通道失败：名称可能已存在")
		return
	}
	before := alertChannelView{ID: id, Name: oldName, Type: oldType, MessageFormat: oldFormat, TargetHint: alertTargetHintFromEncrypted(oldTargetEncrypted), HasSecret: oldHasSecret, Enabled: oldEnabled, CreatedAt: createdAt, UpdatedAt: updatedAt}
	after := alertChannelView{ID: id, Name: name, Type: input.Type, MessageFormat: messageFormat, TargetHint: alertTargetHint(urlValue), HasSecret: secretEncrypted.Valid, Enabled: enabled, CreatedAt: createdAt, UpdatedAt: now}
	if err := auditAlertAction(r.Context(), tx, actor, "update", "alert-channel", id, before, after, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录告警通道审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "更新告警通道失败")
		return
	}
	orpReply(w, 200, after)
}

func (s *Server) orpAlertChannelDelete(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法删除告警通道")
		return
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(r.Context(), "SELECT name FROM orp_alert_channel WHERE id=? FOR UPDATE", id).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "告警通道不存在")
		return
	} else if err != nil {
		orpFailure(w, 500, "读取告警通道失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM orp_alert_channel WHERE id=?", id); err != nil {
		orpFailure(w, 500, "删除告警通道失败")
		return
	}
	if err := auditAlertAction(r.Context(), tx, actor, "delete", "alert-channel", id, map[string]any{"id": id, "name": name}, nil, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录告警通道审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "删除告警通道失败")
		return
	}
	orpReply(w, 200, map[string]any{"id": id, "name": name})
}

func (s *Server) orpAlertChannelTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.orpIdentity(w, r); !ok {
		return
	}
	id, ok := orpID(w, r)
	if !ok {
		return
	}
	channel, err := loadAlertChannel(r.Context(), s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "告警通道不存在")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取告警通道失败")
		return
	}
	now := time.Now()
	message := tlsAlertMessage{
		EventType:  "test",
		Title:      "ORP 告警通道测试",
		Message:    fmt.Sprintf("告警通道「%s」连接测试成功。", channel.Name),
		OccurredAt: now.Format(time.RFC3339),
	}
	result, _, sendErr := sendAlertDelivery(r.Context(), s.db, channel, "test:"+uuid.NewString(), "test", nil, "", message)
	if sendErr != nil {
		orpFailure(w, 502, "测试消息发送失败："+sendErr.Error())
		return
	}
	orpReply(w, 200, result)
}

func (s *Server) orpTLSAlertRulesList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.orpIdentity(w, r); !ok {
		return
	}
	certificates, err := s.orpLoad(r.Context(), "certificates")
	if err != nil {
		orpFailure(w, 500, "读取 TLS 证书失败")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT r.certificate_id,r.enabled,r.days_before,rc.channel_id,c.name
		FROM orp_tls_alert_rule r
		LEFT JOIN orp_tls_alert_rule_channel rc ON rc.certificate_id=r.certificate_id
		LEFT JOIN orp_alert_channel c ON c.id=rc.channel_id
		ORDER BY r.certificate_id,rc.channel_id`)
	if err != nil {
		orpFailure(w, 500, "读取 TLS 到期提醒规则失败")
		return
	}
	type ruleData struct {
		enabled bool
		days    int
		ids     []int64
		names   []string
	}
	rules := map[int64]*ruleData{}
	for rows.Next() {
		var certID int64
		var enabled bool
		var days int
		var channelID sql.NullInt64
		var channelName sql.NullString
		if err := rows.Scan(&certID, &enabled, &days, &channelID, &channelName); err != nil {
			rows.Close()
			orpFailure(w, 500, "读取 TLS 到期提醒规则失败")
			return
		}
		if rules[certID] == nil {
			rules[certID] = &ruleData{enabled: enabled, days: days, ids: []int64{}, names: []string{}}
		}
		if channelID.Valid {
			rules[certID].ids = append(rules[certID].ids, channelID.Int64)
			if channelName.Valid {
				rules[certID].names = append(rules[certID].names, channelName.String)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		orpFailure(w, 500, "读取 TLS 到期提醒规则失败")
		return
	}
	rows.Close()
	now := time.Now()
	items := make([]tlsAlertRuleView, 0, len(certificates))
	for _, cert := range certificates {
		certID, _ := resourceNumber(cert["id"])
		if _, err := s.db.ExecContext(r.Context(), `INSERT IGNORE INTO orp_tls_alert_rule(certificate_id,enabled,days_before) VALUES(?,FALSE,30)`, certID); err != nil {
			orpFailure(w, 500, "初始化 TLS 证书提醒配置失败")
			return
		}
	}
	for _, cert := range certificates {
		certID, _ := resourceNumber(cert["id"])
		expiresAt, err := parseCertificateExpiry(toString(cert["notAfter"]))
		if err != nil {
			continue
		}
		days := certificateDaysRemaining(expiresAt, now)
		item := tlsAlertRuleView{
			CertificateID: certID,
			Name:          toString(cert["name"]),
			Domains:       toString(cert["domains"]),
			NotAfter:      expiresAt.Format("2006-01-02"),
			DaysRemaining: days,
			DaysBefore:    30,
			ChannelIDs:    []int64{},
			ChannelNames:  []string{},
		}
		if rule := rules[certID]; rule != nil {
			item.Enabled = rule.enabled
			item.DaysBefore = rule.days
			item.ChannelIDs = rule.ids
			item.ChannelNames = rule.names
		}
		items = append(items, item)
	}
	orpReply(w, 200, items)
}

func (s *Server) orpTLSAlertRuleSave(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	certID, err := strconv.ParseInt(r.PathValue("certificateID"), 10, 64)
	if err != nil || certID < 1 {
		orpFailure(w, 400, "证书 ID 无效")
		return
	}
	var input tlsAlertRuleInput
	if !decodeAlertBody(w, r, &input) {
		return
	}
	if input.DaysBefore < 1 || input.DaysBefore > 365 {
		orpFailure(w, 400, "提前提醒天数必须在 1 到 365 天之间")
		return
	}
	channelIDs := uniquePositiveIDs(input.ChannelIDs)
	if input.Enabled && len(channelIDs) == 0 {
		orpFailure(w, 400, "启用提醒时至少关联一个告警通道")
		return
	}
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind='certificates' AND id=?", certID).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "TLS 证书不存在")
		return
	} else if err != nil {
		orpFailure(w, 500, "读取 TLS 证书失败")
		return
	}
	for _, channelID := range channelIDs {
		var exists bool
		if err := s.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM orp_alert_channel WHERE id=?)", channelID).Scan(&exists); err != nil {
			orpFailure(w, 500, "读取告警通道失败")
			return
		}
		if !exists {
			orpFailure(w, 400, fmt.Sprintf("告警通道 %d 不存在", channelID))
			return
		}
	}
	var document orpDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		orpFailure(w, 500, "TLS 证书数据损坏")
		return
	}
	var before []byte
	_ = s.db.QueryRowContext(r.Context(), `SELECT JSON_OBJECT('enabled',enabled,'daysBefore',days_before)
		FROM orp_tls_alert_rule WHERE certificate_id=?`, certID).Scan(&before)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法保存 TLS 到期提醒规则")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO orp_tls_alert_rule(certificate_id,enabled,days_before) VALUES(?,?,?)
		ON DUPLICATE KEY UPDATE enabled=VALUES(enabled),days_before=VALUES(days_before)`, certID, input.Enabled, input.DaysBefore); err != nil {
		orpFailure(w, 500, "保存 TLS 到期提醒规则失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM orp_tls_alert_rule_channel WHERE certificate_id=?", certID); err != nil {
		orpFailure(w, 500, "更新提醒通道关联失败")
		return
	}
	for _, channelID := range channelIDs {
		if _, err := tx.ExecContext(r.Context(), "INSERT INTO orp_tls_alert_rule_channel(certificate_id,channel_id) VALUES(?,?)", certID, channelID); err != nil {
			orpFailure(w, 500, "更新提醒通道关联失败")
			return
		}
	}
	after := map[string]any{"certificateId": certID, "enabled": input.Enabled, "daysBefore": input.DaysBefore, "channelIds": channelIDs, "name": document["name"]}
	if err := auditAlertAction(r.Context(), tx, actor, "update", "tls-alert-rule", certID, json.RawMessage(before), after, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录 TLS 到期提醒审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "保存 TLS 到期提醒规则失败")
		return
	}
	orpReply(w, 200, after)
}

func (s *Server) orpAlertDeliveriesList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.orpIdentity(w, r); !ok {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_alert_delivery").Scan(&total); err != nil {
		orpFailure(w, 500, "读取告警发送记录失败")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT d.id,d.event_type,d.channel_id,COALESCE(c.name,d.channel_name),d.certificate_id,d.certificate_name,d.title,d.content,
		d.status,d.attempt_count,d.response_status,COALESCE(d.response_text,''),COALESCE(d.error_text,''),d.delivered_at,d.created_at
		FROM orp_alert_delivery d LEFT JOIN orp_alert_channel c ON c.id=d.channel_id
		ORDER BY d.created_at DESC,d.id DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		orpFailure(w, 500, "读取告警发送记录失败")
		return
	}
	defer rows.Close()
	items := []alertDeliveryView{}
	for rows.Next() {
		var item alertDeliveryView
		var certID sql.NullInt64
		var responseStatus sql.NullInt64
		var deliveredAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.EventType, &item.ChannelID, &item.ChannelName, &certID, &item.CertificateName, &item.Title, &item.Content,
			&item.Status, &item.AttemptCount, &responseStatus, &item.ResponseText, &item.ErrorText, &deliveredAt, &item.CreatedAt); err != nil {
			orpFailure(w, 500, "读取告警发送记录失败")
			return
		}
		if certID.Valid {
			value := certID.Int64
			item.CertificateID = &value
		}
		if responseStatus.Valid {
			value := int(responseStatus.Int64)
			item.ResponseStatus = &value
		}
		if deliveredAt.Valid {
			value := deliveredAt.Time
			item.DeliveredAt = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取告警发送记录失败")
		return
	}
	orpReply(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": pageSize})
}

func (s *Server) orpTLSAlertsCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.orpIdentity(w, r); !ok {
		return
	}
	result, err := runTLSAlertSweep(r.Context(), s.db, time.Now())
	if err != nil {
		orpFailure(w, 500, "检查 TLS 到期提醒失败："+err.Error())
		return
	}
	orpReply(w, 200, result)
}

func StartTLSExpiryAlertScheduler(ctx context.Context, database *sql.DB) {
	go func() {
		ticker := time.NewTicker(tlsAlertSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				result, err := runTLSAlertSweep(sweepCtx, database, time.Now())
				cancel()
				if err != nil {
					log.Printf("TLS expiry alert sweep failed: %v", err)
					continue
				}
				if result.NotificationsSent > 0 || result.NotificationsFailed > 0 {
					log.Printf("TLS expiry alert sweep: checked=%d sent=%d failed=%d", result.CertificatesChecked, result.NotificationsSent, result.NotificationsFailed)
				}
			}
		}
	}()
}

func runTLSAlertSweep(ctx context.Context, database *sql.DB, now time.Time) (alertSweepResult, error) {
	result := alertSweepResult{Errors: []string{}}
	server := &Server{db: database}
	certificates, err := server.orpLoad(ctx, "certificates")
	if err != nil {
		return result, err
	}
	certByID := make(map[int64]orpDocument, len(certificates))
	for _, cert := range certificates {
		id, _ := resourceNumber(cert["id"])
		certByID[id] = cert
	}
	rows, err := database.QueryContext(ctx, `SELECT r.certificate_id,r.days_before,c.id,c.name,c.channel_type,c.message_format,c.target_encrypted,c.secret_encrypted
		FROM orp_tls_alert_rule r
		JOIN orp_tls_alert_rule_channel rc ON rc.certificate_id=r.certificate_id
		JOIN orp_alert_channel c ON c.id=rc.channel_id
		WHERE r.enabled=TRUE AND c.enabled=TRUE ORDER BY r.certificate_id,c.id`)
	if err != nil {
		return result, err
	}
	type dispatchTarget struct {
		certID  int64
		days    int
		channel alertChannelRecord
	}
	targets := []dispatchTarget{}
	for rows.Next() {
		var target dispatchTarget
		if err := rows.Scan(&target.certID, &target.days, &target.channel.ID, &target.channel.Name, &target.channel.Type, &target.channel.MessageFormat,
			&target.channel.TargetEncrypted, &target.channel.SecretEncrypted); err != nil {
			rows.Close()
			return result, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	result.CertificatesChecked = len(certByID)
	for _, target := range targets {
		cert := certByID[target.certID]
		if cert == nil {
			continue
		}
		expiresAt, err := parseCertificateExpiry(toString(cert["notAfter"]))
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("证书 %d 的到期时间无效", target.certID))
			continue
		}
		daysRemaining := certificateDaysRemaining(expiresAt, now)
		if daysRemaining > target.days {
			continue
		}
		content := fmt.Sprintf("TLS 证书「%s」将于 %s 到期，剩余 %d 天。关联域名：%s。请及时续期并发布新证书。",
			toString(cert["name"]), expiresAt.Format("2006-01-02"), daysRemaining, toString(cert["domains"]))
		message := tlsAlertMessage{
			EventType:     "tls_expiry",
			Title:         fmt.Sprintf("TLS 证书到期提醒：%s", toString(cert["name"])),
			Message:       content,
			CertificateID: target.certID,
			Certificate:   toString(cert["name"]),
			Domains:       toString(cert["domains"]),
			ExpiresAt:     expiresAt.Format("2006-01-02"),
			DaysRemaining: daysRemaining,
			ThresholdDays: target.days,
			OccurredAt:    now.Format(time.RFC3339),
		}
		eventKey := fmt.Sprintf("tls-expiry:%d:%s:%s", target.certID, expiresAt.Format("20060102"), now.Format("20060102"))
		certID := target.certID
		_, attempted, sendErr := sendAlertDelivery(ctx, database, target.channel, eventKey, "tls_expiry", &certID, toString(cert["name"]), message)
		if sendErr != nil {
			if attempted {
				result.NotificationsFailed++
				result.Errors = append(result.Errors, fmt.Sprintf("%s：%v", target.channel.Name, sendErr))
			}
		} else if attempted {
			result.NotificationsSent++
		}
	}
	return result, nil
}

func sendAlertDelivery(ctx context.Context, database *sql.DB, channel alertChannelRecord, eventKey, eventType string, certificateID *int64, certificateName string, message tlsAlertMessage) (alertDeliveryView, bool, error) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return alertDeliveryView{}, false, err
	}
	now := time.Now()
	_, err = database.ExecContext(ctx, `INSERT IGNORE INTO orp_alert_delivery(event_key,event_type,channel_id,channel_name,certificate_id,certificate_name,title,content,status,next_attempt_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,'pending',?,?,?)`, eventKey, eventType, channel.ID, channel.Name, certificateID, certificateName, message.Title, message.Message, now, now, now)
	if err != nil {
		return alertDeliveryView{}, false, fmt.Errorf("记录发送任务失败: %w", err)
	}
	var deliveryID int64
	var status string
	if err := database.QueryRowContext(ctx, "SELECT id,status FROM orp_alert_delivery WHERE event_key=? AND channel_id=?", eventKey, channel.ID).Scan(&deliveryID, &status); err != nil {
		return alertDeliveryView{}, false, err
	}
	leaseUntil := now.Add(2 * time.Minute)
	claim, err := database.ExecContext(ctx, `UPDATE orp_alert_delivery SET status='sending',attempt_count=attempt_count+1,next_attempt_at=?,error_text=NULL
		WHERE id=? AND ((status IN ('pending','failed') AND next_attempt_at<=?) OR (status='sending' AND updated_at<=?))`,
		leaseUntil, deliveryID, now, now.Add(-2*time.Minute))
	if err != nil {
		return alertDeliveryView{}, false, err
	}
	claimed, _ := claim.RowsAffected()
	if claimed == 0 {
		return alertDeliveryView{ID: deliveryID, EventType: eventType, ChannelID: channel.ID, ChannelName: channel.Name, CertificateID: certificateID,
			CertificateName: certificateName, Title: message.Title, Content: message.Message, Status: status}, false, nil
	}
	responseStatus, responseText, sendErr := postAlertMessage(ctx, channel, encoded, message)
	if sendErr != nil {
		_, _ = database.ExecContext(ctx, `UPDATE orp_alert_delivery SET status='failed',response_status=?,response_text=?,error_text=?,next_attempt_at=? WHERE id=?`,
			nullInt(responseStatus), truncateAlertText(responseText, 2048), truncateAlertText(sendErr.Error(), 2048), now.Add(5*time.Minute), deliveryID)
		return alertDeliveryView{ID: deliveryID, EventType: eventType, ChannelID: channel.ID, ChannelName: channel.Name, CertificateID: certificateID,
			CertificateName: certificateName, Title: message.Title, Content: message.Message, Status: "failed", AttemptCount: 1,
			ResponseStatus: intPointer(responseStatus), ResponseText: truncateAlertText(responseText, 2048), ErrorText: truncateAlertText(sendErr.Error(), 2048)}, true, sendErr
	}
	_, err = database.ExecContext(ctx, `UPDATE orp_alert_delivery SET status='sent',response_status=?,response_text=?,error_text=NULL,delivered_at=?,next_attempt_at=? WHERE id=?`,
		responseStatus, truncateAlertText(responseText, 2048), now, now, deliveryID)
	if err != nil {
		return alertDeliveryView{}, true, err
	}
	return alertDeliveryView{ID: deliveryID, EventType: eventType, ChannelID: channel.ID, ChannelName: channel.Name, CertificateID: certificateID,
		CertificateName: certificateName, Title: message.Title, Content: message.Message, Status: "sent", AttemptCount: 1,
		ResponseStatus: intPointer(responseStatus), ResponseText: truncateAlertText(responseText, 2048), DeliveredAt: &now, CreatedAt: now}, true, nil
}

func postAlertMessage(ctx context.Context, channel alertChannelRecord, genericPayload []byte, message tlsAlertMessage) (int, string, error) {
	urlValue, err := orpOpen(channel.TargetEncrypted)
	if err != nil {
		return 0, "", errors.New("无法解密推送地址")
	}
	secret := ""
	if channel.SecretEncrypted.Valid && channel.SecretEncrypted.String != "" {
		secret, err = orpOpen(channel.SecretEncrypted.String)
		if err != nil {
			return 0, "", errors.New("无法解密签名密钥")
		}
	}
	body := genericPayload
	if channel.Type == "feishu" {
		bodyValue := map[string]any{"msg_type": "text", "content": map[string]string{"text": message.Title + "\n" + message.Message}}
		if channel.MessageFormat == "card" {
			bodyValue = map[string]any{"msg_type": "interactive", "card": buildFeishuAlertCard(message)}
		}
		if secret != "" {
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			bodyValue["timestamp"] = timestamp
			bodyValue["sign"] = signFeishu(timestamp, secret)
		}
		body, err = json.Marshal(bodyValue)
		if err != nil {
			return 0, "", err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, urlValue, bytes.NewReader(body))
	if err != nil {
		return 0, "", errors.New("推送地址无效")
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("User-Agent", "ORP-Alert/1.0")
	if channel.Type == "webhook" {
		request.Header.Set("X-ORP-Event", message.EventType)
		if secret != "" {
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write(body)
			request.Header.Set("X-ORP-Timestamp", timestamp)
			request.Header.Set("X-ORP-Signature", "sha256="+hexString(mac.Sum(nil)))
		}
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("请求推送地址失败: %w", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	responseText := strings.TrimSpace(string(responseBody))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, responseText, fmt.Errorf("推送服务返回 HTTP %d", response.StatusCode)
	}
	if channel.Type == "feishu" {
		if len(responseBody) == 0 {
			return response.StatusCode, responseText, errors.New("飞书返回了空响应")
		}
		var feishuReply struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(responseBody, &feishuReply); err != nil {
			return response.StatusCode, responseText, errors.New("飞书返回了无法识别的响应")
		}
		if feishuReply.Code != 0 {
			return response.StatusCode, responseText, fmt.Errorf("飞书返回错误 %d：%s", feishuReply.Code, feishuReply.Msg)
		}
	}
	return response.StatusCode, responseText, nil
}

func signFeishu(timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
	_, _ = mac.Write(nil)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func normalizeAlertMessageFormat(channelType, messageFormat string) (string, error) {
	if channelType != "feishu" {
		if messageFormat != "" && messageFormat != "text" {
			return "", errors.New("消息卡片格式仅适用于飞书通道")
		}
		return "text", nil
	}
	if messageFormat == "" {
		return "text", nil
	}
	if messageFormat != "text" && messageFormat != "card" {
		return "", errors.New("飞书消息格式仅支持 text 或 card")
	}
	return messageFormat, nil
}

func buildFeishuAlertCard(message tlsAlertMessage) map[string]any {
	template := "blue"
	if message.EventType == "tls_expiry" {
		template = "orange"
		if message.DaysRemaining <= 7 {
			template = "red"
		}
	}
	content := []string{escapeFeishuMarkdown(message.Message)}
	if message.Certificate != "" {
		content = append(content,
			"", "**证书详情**",
			"证书："+escapeFeishuMarkdown(message.Certificate),
			"域名："+escapeFeishuMarkdown(message.Domains),
			"到期时间："+escapeFeishuMarkdown(message.ExpiresAt),
			fmt.Sprintf("剩余天数：%d 天（提前 %d 天提醒）", message.DaysRemaining, message.ThresholdDays),
		)
	}
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]string{"tag": "plain_text", "content": message.Title},
			"template": template,
		},
		"elements": []map[string]any{
			{"tag": "div", "text": map[string]string{"tag": "lark_md", "content": strings.Join(content, "\n")}},
			{"tag": "note", "elements": []map[string]string{{"tag": "plain_text", "content": "ORP · " + message.OccurredAt}}},
		},
	}
}

func escapeFeishuMarkdown(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	for _, character := range []string{"*", "_", "~", "`", "[", "]", "(", ")", "#", ">"} {
		value = strings.ReplaceAll(value, character, "\\"+character)
	}
	return value
}

func loadAlertChannel(ctx context.Context, database *sql.DB, id int64) (alertChannelRecord, error) {
	var channel alertChannelRecord
	err := database.QueryRowContext(ctx, `SELECT id,name,channel_type,message_format,target_encrypted,secret_encrypted,enabled,created_at,updated_at
		FROM orp_alert_channel WHERE id=?`, id).Scan(&channel.ID, &channel.Name, &channel.Type, &channel.MessageFormat, &channel.TargetEncrypted,
		&channel.SecretEncrypted, &channel.Enabled, &channel.CreatedAt, &channel.UpdatedAt)
	return channel, err
}

func decodeAlertBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		orpFailure(w, 400, "请求内容不是有效的 JSON")
		return false
	}
	return true
}

func validAlertChannelType(value string) bool {
	return value == "webhook" || value == "feishu"
}

func validateAlertTarget(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 {
		return errors.New("请填写有效的 Webhook 地址")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("推送地址必须是有效的 HTTP 或 HTTPS URL，且不能包含用户名密码")
	}
	return nil
}

func alertTargetHint(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return "地址已配置"
	}
	return parsed.Scheme + "://" + parsed.Hostname() + "/•••"
}

// Kept separate so audit payloads never include encrypted or plaintext credentials.
func auditAlertAction(ctx context.Context, tx *sql.Tx, actor, action, kind string, id int64, before, after any, clientIP string) error {
	var beforeJSON, afterJSON []byte
	var err error
	if before != nil {
		beforeJSON, err = json.Marshal(before)
		if err != nil {
			return err
		}
	}
	if after != nil {
		afterJSON, err = json.Marshal(after)
		if err != nil {
			return err
		}
	}
	return orpAudit(ctx, tx, actor, action, kind, id, beforeJSON, afterJSON, clientIP)
}

func parseCertificateExpiry(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("invalid expiry date")
}

func certificateDaysRemaining(expiresAt, now time.Time) int {
	localExpiry := expiresAt.In(time.Local)
	localNow := now.In(time.Local)
	date := time.Date(localExpiry.Year(), localExpiry.Month(), localExpiry.Day(), 0, 0, 0, 0, time.UTC)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
	return int(math.Round(date.Sub(today).Hours() / 24))
}

func uniquePositiveIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

func nullInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func intPointer(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func truncateAlertText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func hexString(value []byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for i, b := range value {
		result[i*2] = digits[b>>4]
		result[i*2+1] = digits[b&0x0f]
	}
	return string(result)
}

func alertTargetHintFromEncrypted(value string) string {
	decrypted, err := orpOpen(value)
	if err != nil {
		return "地址已配置"
	}
	return alertTargetHint(decrypted)
}
