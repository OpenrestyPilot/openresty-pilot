package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

const (
	demoAlertCertificateName = "演示 TLS 到期提醒证书"
	demoAlertWebhookName     = "本地 Webhook 演示"
	demoAlertFeishuName      = "飞书机器人演示（待配置）"
)

// SeedDemoAlertData creates an isolated, repeatable data set for exercising the alert UI.
// Both channels start disabled; no notification is sent until an operator enables one.
func SeedDemoAlertData(ctx context.Context, database *sql.DB) error {
	webhookID, err := ensureDemoAlertChannel(ctx, database, demoAlertWebhookName, "webhook", "http://127.0.0.1:8099/webhook", false)
	if err != nil {
		return err
	}
	if _, err := ensureDemoAlertChannel(ctx, database, demoAlertFeishuName, "feishu", "https://open.feishu.cn/open-apis/bot/v2/hook/replace-with-real-token", false); err != nil {
		return err
	}

	certificateID, err := ensureDemoAlertCertificate(ctx, database)
	if err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO orp_tls_alert_rule(certificate_id,enabled,days_before) VALUES(?,TRUE,30)
		ON DUPLICATE KEY UPDATE enabled=TRUE,days_before=30`, certificateID); err != nil {
		return fmt.Errorf("保存演示 TLS 提醒规则: %w", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT IGNORE INTO orp_tls_alert_rule_channel(certificate_id,channel_id) VALUES(?,?)`, certificateID, webhookID); err != nil {
		return fmt.Errorf("关联演示 Webhook 通道: %w", err)
	}
	return nil
}

func ensureDemoAlertChannel(ctx context.Context, database *sql.DB, name, channelType, target string, enabled bool) (int64, error) {
	var id int64
	err := database.QueryRowContext(ctx, "SELECT id FROM orp_alert_channel WHERE name=?", name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("查询演示告警通道: %w", err)
	}
	encrypted, err := orpSeal(target)
	if err != nil {
		return 0, fmt.Errorf("加密演示告警地址: %w", err)
	}
	result, err := database.ExecContext(ctx, `INSERT INTO orp_alert_channel(name,channel_type,target_encrypted,enabled,created_at,updated_at)
		VALUES(?,?,?,?,NOW(6),NOW(6))`, name, channelType, encrypted, enabled)
	if err != nil {
		return 0, fmt.Errorf("创建演示告警通道: %w", err)
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("读取演示告警通道 ID: %w", err)
	}
	return id, nil
}

func ensureDemoAlertCertificate(ctx context.Context, database *sql.DB) (int64, error) {
	var id int64
	err := database.QueryRowContext(ctx, `SELECT id FROM orp_resource
		WHERE kind='certificates' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.name'))=? ORDER BY id DESC LIMIT 1`, demoAlertCertificateName).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("查询演示 TLS 证书: %w", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return 0, fmt.Errorf("生成演示证书私钥: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.AddDate(0, 0, 10)
	template := x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: "alerts-demo.local", Organization: []string{"ORP Demo"}},
		DNSNames:              []string{"alerts-demo.local"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              expires,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return 0, fmt.Errorf("生成演示 TLS 证书: %w", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	document := orpDocument{
		"certificate": string(certificatePEM), "certificateChain": "", "centerScope": "all",
		"domains": "alerts-demo.local", "name": demoAlertCertificateName,
		"notAfter": expires.Format("2006-01-02"), "notBefore": now.Format("2006-01-02"), "privateKey": string(privateKeyPEM),
	}
	if err := orpProtect("certificates", document); err != nil {
		return 0, fmt.Errorf("保护演示证书私钥: %w", err)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return 0, fmt.Errorf("序列化演示 TLS 证书: %w", err)
	}
	result, err := database.ExecContext(ctx, "INSERT INTO orp_resource(kind,document) VALUES('certificates',?)", raw)
	if err != nil {
		return 0, fmt.Errorf("保存演示 TLS 证书: %w", err)
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("读取演示 TLS 证书 ID: %w", err)
	}
	return id, nil
}
