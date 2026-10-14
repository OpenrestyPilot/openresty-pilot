package config

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"net.daoke/orp-backend/internal/store"
)

type EffectiveSetting struct{ Value, Source string }
type RuntimeSettings map[string]EffectiveSetting

func LoadRuntimeSettings(ctx context.Context, db *sql.DB) (RuntimeSettings, error) {
	settings := RuntimeSettings{}
	for _, definition := range store.RuntimeSettings {
		value, source := definition.Value, "default"
		var raw []byte
		err := db.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='settings' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.key'))=?", definition.Key).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("read runtime setting %s: %w", definition.Key, err)
		}
		if err == nil {
			var document struct {
				Value     string `json:"value"`
				Status    string `json:"status"`
				Sensitive bool   `json:"isSensitive"`
			}
			if err := json.Unmarshal(raw, &document); err != nil {
				return nil, fmt.Errorf("decode runtime setting %s: %w", definition.Key, err)
			}
			if document.Status == "enabled" {
				value, source = document.Value, "database"
			}
		}
		if envValue, ok := lookupEnv(definition.EnvKey); ok {
			value, source = envValue, "environment"
		}
		if definition.Key == "system.redis_addr" && strings.TrimSpace(os.Getenv("ORP_REDIS_ADDR")) == "" {
			host, port := strings.TrimSpace(os.Getenv("ORP_REDIS_HOST")), strings.TrimSpace(os.Getenv("ORP_REDIS_PORT"))
			if host != "" || port != "" {
				if host == "" {
					host = "127.0.0.1"
				}
				if port == "" {
					port = "6379"
				}
				value, source = net.JoinHostPort(host, port), "environment"
			}
		}
		if source == "database" && definition.Sensitive && value != "" {
			plain, err := decryptSetting(value)
			if err != nil {
				return nil, fmt.Errorf("decrypt runtime setting %s: %w", definition.Key, err)
			}
			value = plain
		}
		if !definition.Optional && strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("runtime setting %s (%s) cannot be empty", definition.Key, definition.EnvKey)
		}
		if definition.Key == "security.captcha_enabled" && !strings.EqualFold(strings.TrimSpace(value), "true") && !strings.EqualFold(strings.TrimSpace(value), "false") && strings.TrimSpace(value) != "1" && strings.TrimSpace(value) != "0" {
			return nil, fmt.Errorf("runtime setting %s must be true or false", definition.Key)
		}
		settings[definition.Key] = EffectiveSetting{Value: value, Source: source}
	}
	return settings, nil
}

// lookupEnv isolates process configuration from the rest of the resolver.
var lookupEnv = os.LookupEnv
var decryptSetting = openSetting

func openSetting(value string) (string, error) {
	const prefix = "enc:v1:"
	if !strings.HasPrefix(value, prefix) {
		return "", fmt.Errorf("stored secret is not encrypted")
	}
	key, err := hex.DecodeString(os.Getenv("ORP_DATA_KEY"))
	if err != nil || len(key) != 32 {
		return "", fmt.Errorf("invalid encryption key")
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < aead.NonceSize() {
		return "", fmt.Errorf("encrypted value is truncated")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s RuntimeSettings) value(key string) string { return s[key].Value }

func (s RuntimeSettings) Apply(cfg Config) (Config, error) {
	cfg.HTTPAddress = settingOr(s, "system.http_addr", cfg.HTTPAddress)
	cfg.AgentHTTPSAddress = settingOr(s, "system.agent_https_addr", cfg.AgentHTTPSAddress)
	cfg.TLSCertFile = settingOr(s, "system.agent_tls_cert_file", cfg.TLSCertFile)
	cfg.TLSKeyFile = settingOr(s, "system.agent_tls_key_file", cfg.TLSKeyFile)
	cfg.AgentClientCAFile = settingOr(s, "system.agent_client_ca_file", cfg.AgentClientCAFile)
	cfg.RedisAddress = settingOr(s, "system.redis_addr", cfg.RedisAddress)
	cfg.RedisPassword = s.value("system.redis_password")
	rawDB := settingOr(s, "system.redis_db", strconv.Itoa(cfg.RedisDB))
	redisDB, err := strconv.Atoi(strings.TrimSpace(rawDB))
	if err != nil || redisDB < 0 {
		return Config{}, fmt.Errorf("system.redis_db must be a non-negative integer")
	}
	cfg.RedisDB = redisDB
	brokers := []string{}
	for _, broker := range strings.Split(s.value("system.kafka_bootstrap_servers"), ",") {
		if broker = strings.TrimSpace(broker); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	cfg.KafkaBrokers = brokers
	cfg.KafkaTopic = settingOr(s, "system.kafka_topic", cfg.KafkaTopic)
	cfg.KafkaConsumerID = settingOr(s, "system.kafka_consumer_group", cfg.KafkaConsumerID)
	if (cfg.AgentHTTPSAddress != "" || cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" || cfg.AgentClientCAFile != "") && (cfg.AgentHTTPSAddress == "" || cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" || cfg.AgentClientCAFile == "") {
		return Config{}, fmt.Errorf("Agent HTTPS address, TLS certificate, TLS key, and client CA must be configured together")
	}
	if strings.TrimSpace(cfg.RedisAddress) == "" {
		cfg.RedisAddress = net.JoinHostPort("127.0.0.1", "6379")
	}
	return cfg, nil
}

func settingOr(settings RuntimeSettings, key, fallback string) string {
	value, ok := settings[key]
	if ok {
		return value.Value
	}
	return fallback
}
