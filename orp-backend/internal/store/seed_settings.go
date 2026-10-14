package store

import (
	"database/sql"
	"encoding/json"
)

type RuntimeSetting struct {
	Key, Name, EnvKey, Type, Value, Description string
	Sensitive, Optional, RestartRequired        bool
}

var RuntimeSettings = []RuntimeSetting{
	{Key: "system.http_addr", Name: "控制面 HTTP 监听地址", EnvKey: "ORP_HTTP_ADDR", Type: "string", Value: ":8081", RestartRequired: true, Description: "控制面 HTTP 服务监听地址。修改后重启控制面生效。"},
	{Key: "system.agent_https_addr", Name: "Agent HTTPS 监听地址", EnvKey: "ORP_AGENT_HTTPS_ADDR", Type: "string", Optional: true, RestartRequired: true, Description: "启用 Agent mTLS 接口的监听地址；需与证书和 CA 路径一并配置。"},
	{Key: "system.agent_tls_cert_file", Name: "Agent 服务端证书路径", EnvKey: "ORP_AGENT_TLS_CERT_FILE", Type: "string", Optional: true, RestartRequired: true, Description: "Agent HTTPS 服务端证书文件路径。"},
	{Key: "system.agent_tls_key_file", Name: "Agent 服务端私钥路径", EnvKey: "ORP_AGENT_TLS_KEY_FILE", Type: "string", Optional: true, RestartRequired: true, Description: "Agent HTTPS 服务端私钥文件路径；私钥内容应保留在文件中。"},
	{Key: "system.agent_client_ca_file", Name: "Agent 客户端 CA 路径", EnvKey: "ORP_AGENT_CLIENT_CA_FILE", Type: "string", Optional: true, RestartRequired: true, Description: "验证 Agent 客户端证书的 CA 文件路径。"},
	{Key: "system.agent_public_url", Name: "Agent 对外访问地址", EnvKey: "ORP_AGENT_PUBLIC_URL", Type: "string", Optional: true, RestartRequired: true, Description: "节点可访问的 Agent HTTPS 公网地址，用于生成快速接入命令。"},
	{Key: "system.redis_addr", Name: "Redis 地址", EnvKey: "ORP_REDIS_ADDR", Type: "string", Value: "127.0.0.1:6379", RestartRequired: true, Description: "控制面使用的 Redis 地址。不可用时仪表盘回退到直接读取。"},
	{Key: "system.redis_password", Name: "Redis 密码", EnvKey: "ORP_REDIS_PASSWORD", Type: "string", Sensitive: true, Optional: true, RestartRequired: true, Description: "Redis 认证密码，敏感值加密存储。"},
	{Key: "system.redis_db", Name: "Redis 数据库编号", EnvKey: "ORP_REDIS_DB", Type: "number", Value: "0", RestartRequired: true, Description: "Redis 数据库编号，必须是非负整数。"},
	{Key: "system.kafka_bootstrap_servers", Name: "Kafka Broker 地址", EnvKey: "ORP_KAFKA_BOOTSTRAP_SERVERS", Type: "string", Optional: true, RestartRequired: true, Description: "逗号分隔的 Kafka Broker 地址；留空时关闭日志消费者。"},
	{Key: "system.kafka_topic", Name: "Kafka 日志主题", EnvKey: "ORP_KAFKA_TOPIC", Type: "string", Value: "orp-access-logs", RestartRequired: true, Description: "访问日志 Kafka 主题。"},
	{Key: "system.kafka_consumer_group", Name: "Kafka 消费者组", EnvKey: "ORP_KAFKA_CONSUMER_GROUP", Type: "string", Value: "orp-control-plane-v1", RestartRequired: true, Description: "访问日志 Kafka 消费者组。"},
	{Key: "system.geoip_db_path", Name: "GeoIP 数据库路径", EnvKey: "ORP_GEOIP_DB_PATH", Type: "string", Optional: true, RestartRequired: true, Description: "控制台托管数据库不存在时使用的外部 City .mmdb 路径。"},
	{Key: "system.geoip_storage_dir", Name: "GeoIP 存储目录", EnvKey: "ORP_GEOIP_STORAGE_DIR", Type: "string", Value: "runtime/geoip", RestartRequired: true, Description: "控制台导入的 City .mmdb 数据库存储目录。"},
	{Key: "security.captcha_enabled", Name: "登录验证码开关", EnvKey: "ORP_CAPTCHA_ENABLED", Type: "boolean", Value: "false", RestartRequired: true, Description: "启用后，登录接口要求前端验证码通过。"},
}

func SeedRuntimeSettings(db *sql.DB) error {
	for _, setting := range RuntimeSettings {
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM orp_resource WHERE kind='settings' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.key'))=?)", setting.Key).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		value := setting.Value
		if setting.Sensitive {
			value = ""
		}
		document, err := json.Marshal(map[string]any{
			"key": setting.Key, "name": setting.Name, "group": map[bool]string{true: "安全策略", false: "系统基础"}[setting.Key == "security.captcha_enabled"],
			"type": setting.Type, "value": value, "isSensitive": setting.Sensitive, "status": "enabled", "description": setting.Description,
			"runtimeManaged": true, "envKey": setting.EnvKey, "restartRequired": setting.RestartRequired, "optional": setting.Optional,
		})
		if err != nil {
			return err
		}
		if _, err := db.Exec("INSERT INTO orp_resource(kind,document) VALUES('settings',?)", document); err != nil {
			return err
		}
	}
	return nil
}

func SeedSettingGroups(db *sql.DB) error {
	groups := []struct{ Code, Name string }{
		{"system", "系统基础"},
		{"business", "业务参数"},
		{"api-key", "接口密钥"},
		{"security", "安全策略"},
	}
	for index, group := range groups {
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM orp_resource WHERE kind='setting-groups' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.code'))=?)", group.Code).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		document, _ := json.Marshal(map[string]any{"code": group.Code, "name": group.Name, "description": "", "isSystem": true, "sortOrder": index + 1})
		if _, err := db.Exec("INSERT INTO orp_resource(kind,document) VALUES('setting-groups',?)", document); err != nil {
			return err
		}
	}
	return nil
}
