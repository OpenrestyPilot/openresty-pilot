package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddress       string
	AgentHTTPSAddress string
	TLSCertFile       string
	TLSKeyFile        string
	AgentClientCAFile string
	MySQLDSN          string
	RedisAddress      string
	RedisPassword     string
	RedisDB           int
	KafkaBrokers      []string
	KafkaTopic        string
	KafkaConsumerID   string
}

func Load() (Config, error) {
	address := os.Getenv("ORP_HTTP_ADDR")
	if address == "" {
		address = ":8081"
	}
	jdbcURL := strings.TrimPrefix(os.Getenv("ORP_DB_URL"), "jdbc:")
	if jdbcURL == "" {
		return Config{}, fmt.Errorf("ORP_DB_URL is required")
	}
	parsed, err := url.Parse(jdbcURL)
	if err != nil {
		return Config{}, fmt.Errorf("parse ORP_DB_URL: %w", err)
	}
	user := url.QueryEscape(os.Getenv("ORP_DB_USERNAME"))
	password := url.QueryEscape(os.Getenv("ORP_DB_PASSWORD"))
	if user == "" {
		return Config{}, fmt.Errorf("ORP_DB_USERNAME is required")
	}
	if strings.TrimSpace(os.Getenv("ORP_ADMIN_PASSWORD")) == "" {
		return Config{}, fmt.Errorf("ORP_ADMIN_PASSWORD is required")
	}
	if len(strings.TrimSpace(os.Getenv("ORP_DATA_KEY"))) != 64 {
		return Config{}, fmt.Errorf("ORP_DATA_KEY must be 32 random bytes encoded as 64 hex characters")
	}
	agentHTTPSAddress := strings.TrimSpace(os.Getenv("ORP_AGENT_HTTPS_ADDR"))
	tlsCert := strings.TrimSpace(os.Getenv("ORP_AGENT_TLS_CERT_FILE"))
	tlsKey := strings.TrimSpace(os.Getenv("ORP_AGENT_TLS_KEY_FILE"))
	clientCA := strings.TrimSpace(os.Getenv("ORP_AGENT_CLIENT_CA_FILE"))
	brokers := []string{}
	for _, broker := range strings.Split(os.Getenv("ORP_KAFKA_BOOTSTRAP_SERVERS"), ",") {
		if broker = strings.TrimSpace(broker); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	topic := strings.TrimSpace(os.Getenv("ORP_KAFKA_TOPIC"))
	if topic == "" {
		topic = "orp-access-logs"
	}
	consumerID := strings.TrimSpace(os.Getenv("ORP_KAFKA_CONSUMER_GROUP"))
	if consumerID == "" {
		consumerID = "orp-control-plane-v1"
	}
	redisAddress := strings.TrimSpace(os.Getenv("ORP_REDIS_ADDR"))
	if redisAddress == "" {
		host := strings.TrimSpace(os.Getenv("ORP_REDIS_HOST"))
		port := strings.TrimSpace(os.Getenv("ORP_REDIS_PORT"))
		if host == "" {
			host = "127.0.0.1"
		}
		if port == "" {
			port = "6379"
		}
		redisAddress = net.JoinHostPort(host, port)
	}
	redisDB := 0
	if raw := strings.TrimSpace(os.Getenv("ORP_REDIS_DB")); raw != "" {
		parsedDB, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsedDB < 0 {
			return Config{}, fmt.Errorf("ORP_REDIS_DB must be a non-negative integer")
		}
		redisDB = parsedDB
	}
	return Config{HTTPAddress: address, AgentHTTPSAddress: agentHTTPSAddress, TLSCertFile: tlsCert, TLSKeyFile: tlsKey, AgentClientCAFile: clientCA, MySQLDSN: fmt.Sprintf("%s:%s@tcp(%s)%s?parseTime=true&charset=utf8mb4&multiStatements=true", user, password, parsed.Host, parsed.EscapedPath()), RedisAddress: redisAddress, RedisPassword: os.Getenv("ORP_REDIS_PASSWORD"), RedisDB: redisDB, KafkaBrokers: brokers, KafkaTopic: topic, KafkaConsumerID: consumerID}, nil
}
