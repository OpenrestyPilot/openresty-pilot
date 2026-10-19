package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	defaultKafkaTopic       = "orp-access-logs"
	defaultKafkaConsumerID  = "orp-control-plane-v1"
	maxKafkaLogMessageBytes = 1 << 20
)

var logResourceIDPattern = regexp.MustCompile(`orp-(http|stream)-(\d+)\.(?:access|error)\.log$`)

type KafkaLogConfig struct {
	Brokers []string
	Topic   string
	GroupID string
}

type filebeatLogEvent struct {
	Message   json.RawMessage `json:"message"`
	LogType   string          `json:"orp_log_type"`
	Timestamp string          `json:"@timestamp"`
	ORP       struct {
		NodeName string `json:"node_name"`
		NodeID   any    `json:"node_id"`
	} `json:"orp"`
	Log struct {
		File struct {
			Path string `json:"path"`
		} `json:"file"`
	} `json:"log"`
}

type KafkaLogRecord struct {
	ID           int64     `json:"id"`
	NodeID       int64     `json:"nodeId"`
	NodeName     string    `json:"nodeName"`
	LogType      string    `json:"logType"`
	ResourceID   int64     `json:"resourceId"`
	TS           time.Time `json:"ts"`
	Status       int       `json:"status"`
	RT           float64   `json:"rt"`
	Host         string    `json:"host"`
	RemoteAddr   string    `json:"remoteAddr"`
	URI          string    `json:"uri"`
	Bytes        int64     `json:"bytes"`
	RequestBytes int64     `json:"requestBytes"`
	Message      string    `json:"message"`
}

func KafkaLogConfigFromEnv() KafkaLogConfig {
	brokers := []string{}
	for _, broker := range strings.Split(os.Getenv("ORP_KAFKA_BOOTSTRAP_SERVERS"), ",") {
		if broker = strings.TrimSpace(broker); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	topic := strings.TrimSpace(os.Getenv("ORP_KAFKA_TOPIC"))
	if topic == "" {
		topic = defaultKafkaTopic
	}
	groupID := strings.TrimSpace(os.Getenv("ORP_KAFKA_CONSUMER_GROUP"))
	if groupID == "" {
		groupID = defaultKafkaConsumerID
	}
	return KafkaLogConfig{Brokers: brokers, Topic: topic, GroupID: groupID}
}

func StartKafkaLogConsumer(ctx context.Context, db *sql.DB, config KafkaLogConfig) {
	if len(config.Brokers) == 0 {
		log.Printf("Kafka log consumer disabled: ORP_KAFKA_BOOTSTRAP_SERVERS is empty")
		return
	}
	if config.Topic == "" || config.GroupID == "" {
		log.Printf("Kafka log consumer disabled: topic or consumer group is empty")
		return
	}
	go runKafkaLogConsumer(ctx, db, config)
}

func runKafkaLogConsumer(ctx context.Context, db *sql.DB, config KafkaLogConfig) {
	backoff := time.Second
	for ctx.Err() == nil {
		reader := kafka.NewReader(kafka.ReaderConfig{
			Brokers:     config.Brokers,
			Topic:       config.Topic,
			GroupID:     config.GroupID,
			StartOffset: kafka.FirstOffset,
			MinBytes:    1,
			MaxBytes:    10 << 20,
			MaxWait:     500 * time.Millisecond,
		})
		log.Printf("Kafka log consumer connecting to %s topic %s group %s", strings.Join(config.Brokers, ","), config.Topic, config.GroupID)
		nodeIDs, err := loadKafkaNodeIDs(ctx, db)
		if err != nil {
			log.Printf("Kafka log consumer could not load node mapping: %v", err)
		}
		refreshNodeIDsAt := time.Now().Add(time.Minute)
		for ctx.Err() == nil {
			message, err := reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("Kafka log fetch failed: %v", err)
				}
				break
			}
			if time.Now().After(refreshNodeIDsAt) {
				if refreshed, refreshErr := loadKafkaNodeIDs(ctx, db); refreshErr == nil {
					nodeIDs = refreshed
					refreshNodeIDsAt = time.Now().Add(time.Minute)
				}
			}
			if err := persistKafkaLog(ctx, db, message, nodeIDs); err != nil {
				log.Printf("Kafka log persist failed at %s/%d/%d: %v", message.Topic, message.Partition, message.Offset, err)
				break
			}
			if err := reader.CommitMessages(ctx, message); err != nil {
				log.Printf("Kafka log offset commit failed at %s/%d/%d: %v", message.Topic, message.Partition, message.Offset, err)
				break
			}
			backoff = time.Second
		}
		_ = reader.Close()
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func persistKafkaLog(ctx context.Context, db *sql.DB, kafkaMessage kafka.Message, nodeIDs map[string]int64) error {
	if len(kafkaMessage.Value) > maxKafkaLogMessageBytes {
		return persistKafkaLogRejection(ctx, db, kafkaMessage, "message_too_large")
	}
	var envelope filebeatLogEvent
	if err := json.Unmarshal(kafkaMessage.Value, &envelope); err != nil {
		return persistKafkaLogRejection(ctx, db, kafkaMessage, "invalid_json")
	}
	nodeName := strings.TrimSpace(envelope.ORP.NodeName)
	logType := strings.ToLower(strings.TrimSpace(envelope.LogType))
	if nodeName == "" {
		return persistKafkaLogRejection(ctx, db, kafkaMessage, "missing_node_name")
	}
	if logType != "access" && logType != "error" {
		return persistKafkaLogRejection(ctx, db, kafkaMessage, "invalid_log_type")
	}
	message := strings.TrimSpace(rawMessage(envelope.Message))
	if message == "" {
		return persistKafkaLogRejection(ctx, db, kafkaMessage, "missing_message")
	}
	eventTime := time.Now()
	if parsed, err := time.Parse(time.RFC3339Nano, envelope.Timestamp); err == nil {
		eventTime = parsed
	}
	if logType == "access" {
		var access accessEvent
		if json.Unmarshal([]byte(message), &access) == nil {
			if parsed, err := time.Parse(time.RFC3339Nano, access.TS); err == nil {
				eventTime = parsed
			}
		}
	}
	var access accessEvent
	if logType == "access" {
		if err := json.Unmarshal([]byte(message), &access); err != nil {
			return persistKafkaLogRejection(ctx, db, kafkaMessage, "invalid_access_message")
		}
	}
	var nodeID int64
	if id, ok := numericFloat(envelope.ORP.NodeID); ok && id > 0 {
		nodeID = int64(id)
	} else {
		nodeID = nodeIDs[nodeName]
	}
	var resourceID int64
	var resourceKind string
	if matches := logResourceIDPattern.FindStringSubmatch(envelope.Log.File.Path); len(matches) == 3 {
		resourceKind = matches[1]
		resourceID, _ = strconv.ParseInt(matches[2], 10, 64)
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", kafkaMessage.Topic, kafkaMessage.Partition, kafkaMessage.Offset)))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `INSERT IGNORE INTO orp_log_event
		(event_key,kafka_topic,kafka_partition,kafka_offset,node_id,node_name,log_type,resource_kind,resource_id,event_ts,status,response_time,host,remote_addr,request_uri,bytes_sent,request_bytes,message)
		VALUES(?,?,?,?,NULLIF(?,0),?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		hex.EncodeToString(key[:]), kafkaMessage.Topic, kafkaMessage.Partition, kafkaMessage.Offset, nodeID, nodeName, logType, resourceKind, resourceID, eventTime,
		access.Status, access.RT, access.Host, access.RemoteAddr, access.URI, access.Bytes, access.RequestBytes, message)
	if err != nil {
		return err
	}
	return nil
}

// persistKafkaLogRejection stores only bounded metadata. Kafka payloads can contain
// client data and credentials, so rejected payloads must never be copied to this
// table or included in returned errors/log messages.
func persistKafkaLogRejection(ctx context.Context, db *sql.DB, message kafka.Message, reason string) error {
	digest := sha256.Sum256(message.Value)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `INSERT IGNORE INTO orp_log_rejection
		(kafka_topic,kafka_partition,kafka_offset,payload_bytes,payload_sha256,reason)
		VALUES(?,?,?,?,?,?)`, message.Topic, message.Partition, message.Offset, len(message.Value), hex.EncodeToString(digest[:]), reason)
	if err != nil {
		return fmt.Errorf("persist rejected Kafka log metadata: %w", err)
	}
	return nil
}

func rawMessage(value json.RawMessage) string {
	if len(value) == 0 || string(value) == "null" {
		return ""
	}
	var message string
	if json.Unmarshal(value, &message) == nil {
		return message
	}
	return string(value)
}

func loadKafkaNodeIDs(ctx context.Context, db *sql.DB) (map[string]int64, error) {
	rows, err := db.QueryContext(ctx, "SELECT id,document FROM orp_resource WHERE kind='nodes'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var node orpDocument
		if json.Unmarshal(raw, &node) == nil && toString(node["name"]) != "" {
			result[toString(node["name"])] = id
		}
	}
	return result, rows.Err()
}

func (s *Server) kafkaAccessEvents(ctx context.Context, nodeNames map[int64]string, since time.Time) ([]accessEvent, bool, error) {
	names := make([]string, 0, len(nodeNames))
	idsByName := make(map[string]int64, len(nodeNames))
	for id, name := range nodeNames {
		if name == "" {
			continue
		}
		names = append(names, name)
		idsByName[name] = id
	}
	if len(names) == 0 {
		return nil, false, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(names)), ",")
	args := make([]any, len(names))
	for i, name := range names {
		args[i] = name
	}
	var hasData bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM orp_log_event WHERE log_type='access' AND node_name IN ("+placeholders+"))", args...).Scan(&hasData); err != nil {
		return nil, false, err
	}
	if !hasData {
		return nil, false, nil
	}
	queryArgs := append([]any{since}, args...)
	rows, err := s.db.QueryContext(ctx, "SELECT node_id,node_name,event_ts,status,response_time,host,remote_addr,request_uri,bytes_sent,request_bytes FROM orp_log_event WHERE log_type='access' AND event_ts>=? AND node_name IN ("+placeholders+") ORDER BY event_ts", queryArgs...)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	events := []accessEvent{}
	for rows.Next() {
		var id sql.NullInt64
		var nodeName string
		var timestamp time.Time
		var event accessEvent
		if err := rows.Scan(&id, &nodeName, &timestamp, &event.Status, &event.RT, &event.Host, &event.RemoteAddr, &event.URI, &event.Bytes, &event.RequestBytes); err != nil {
			return nil, true, err
		}
		event.NodeID = id.Int64
		if event.NodeID == 0 {
			event.NodeID = idsByName[nodeName]
		}
		event.TS = timestamp.UTC().Format(time.RFC3339Nano)
		events = append(events, event)
	}
	return events, true, rows.Err()
}

func (s *Server) kafkaLogPredicate(ctx context.Context, kind string, id int64, resource orpDocument) (string, []any, error) {
	if kind == "nodes" {
		return "(node_id=? OR node_name=?) AND log_type IN ('access','error')", []any{id, toString(resource["name"])}, nil
	}
	centerID, err := resourceNumber(resource["centerId"])
	if err != nil {
		return "", nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,document FROM orp_resource WHERE kind='nodes' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.centerId'))=?", strconv.FormatInt(centerID, 10))
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var nodeID int64
		var raw []byte
		if err := rows.Scan(&nodeID, &raw); err != nil {
			return "", nil, err
		}
		var node orpDocument
		if json.Unmarshal(raw, &node) == nil && toString(node["name"]) != "" {
			names = append(names, toString(node["name"]))
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	if len(names) == 0 {
		return "1=0", nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(names)), ",")
	resourceKind := "http"
	if kind == "stream-services" {
		resourceKind = "stream"
	}
	args := []any{resourceKind, id}
	for _, name := range names {
		args = append(args, name)
	}
	return "resource_kind=? AND resource_id=? AND log_type='access' AND node_name IN (" + placeholders + ")", args, nil
}

func (s *Server) kafkaLogLines(ctx context.Context, kind string, id int64, resource orpDocument, afterID int64, limit int) ([]orpLogLine, int64, bool, error) {
	where, args, err := s.kafkaLogPredicate(ctx, kind, id, resource)
	if err != nil {
		return nil, afterID, false, err
	}
	if afterID == 0 {
		var exists bool
		if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM orp_log_event WHERE "+where+")", args...).Scan(&exists); err != nil {
			return nil, afterID, false, err
		}
		if !exists {
			return nil, afterID, false, nil
		}
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	queryArgs := append(append([]any{}, args...), afterID, limit)
	rows, err := s.db.QueryContext(ctx, "SELECT id,node_name,log_type,event_ts,status,message FROM orp_log_event WHERE "+where+" AND id>? ORDER BY id DESC LIMIT ?", queryArgs...)
	if err != nil {
		return nil, afterID, false, err
	}
	defer rows.Close()
	lines := []orpLogLine{}
	for rows.Next() {
		var eventID int64
		var nodeName, logType, message string
		var timestamp time.Time
		var status int
		if err := rows.Scan(&eventID, &nodeName, &logType, &timestamp, &status, &message); err != nil {
			return nil, afterID, false, err
		}
		level := logLevel(message)
		if logType == "access" {
			switch {
			case status >= 500:
				level = "ERROR"
			case status >= 400:
				level = "WARN"
			default:
				level = "INFO"
			}
		}
		lines = append(lines, orpLogLine{ID: eventID, Level: level, Line: "[" + nodeName + "] " + message, TS: timestamp.Local().Format("2006-01-02 15:04:05")})
		if eventID > afterID {
			afterID = eventID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, afterID, false, err
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
	return lines, afterID, true, nil
}
