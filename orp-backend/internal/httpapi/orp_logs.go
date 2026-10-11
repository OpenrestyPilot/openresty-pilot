package httpapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type orpLogLine struct {
	ID    int64  `json:"id"`
	Level string `json:"level"`
	Line  string `json:"line"`
	TS    string `json:"ts"`
}

func (s *Server) orpLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.orpIdentity(w, r); !ok {
		return
	}
	parts := strings.Split(r.URL.Query().Get("target"), ":")
	if len(parts) != 2 {
		orpFailure(w, 400, "日志目标无效")
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "日志目标 ID 无效")
		return
	}
	var kind string
	switch parts[0] {
	case "node":
		kind = "nodes"
	case "http":
		kind = "http-listeners"
	case "stream":
		kind = "stream-services"
	default:
		orpFailure(w, 400, "日志目标类型无效")
		return
	}
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind=? AND id=?", kind, id).Scan(&raw); err != nil {
		orpFailure(w, 404, "日志目标不存在")
		return
	}
	var resource orpDocument
	if err := json.Unmarshal(raw, &resource); err != nil {
		orpFailure(w, 500, "日志目标损坏")
		return
	}
	roles, err := s.accountRoles(r, usernameFromToken(r.Header.Get("Authorization")))
	if err != nil || !s.documentCenterScopeAllowed(r, roles, "orp:read", kind, resource) {
		orpFailure(w, http.StatusForbidden, "无权访问该日志目标所属中心")
		return
	}
	kafkaLines, _, hasKafka, err := s.kafkaLogLines(r.Context(), kind, id, resource, 0, 100)
	if err != nil {
		orpFailure(w, 500, "读取 Kafka 日志失败")
		return
	}
	if hasKafka {
		orpReply(w, 200, map[string]any{"logs": kafkaLines})
		return
	}
	lines := []orpLogLine{}
	if kind == "nodes" {
		target, err := targetForNode(resource)
		if err != nil {
			orpFailure(w, 409, err.Error())
			return
		}
		command := exec.CommandContext(r.Context(), "docker", "logs", "--timestamps", "--tail", "100", target.container)
		output, err := command.CombinedOutput()
		if err != nil {
			orpFailure(w, 502, "读取节点容器日志失败")
			return
		}
		if len(output) > 1<<20 {
			output = output[len(output)-(1<<20):]
		}
		for _, rawLine := range strings.Split(string(output), "\n") {
			if rawLine == "" {
				continue
			}
			stamp, body, found := strings.Cut(rawLine, " ")
			if !found {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil {
				continue
			}
			lines = append(lines, orpLogLine{ID: logLineID(fmt.Sprintf("%d:%s", id, rawLine)), Level: logLevel(body), Line: body, TS: ts.Local().Format("2006-01-02 15:04:05")})
		}
	} else {
		centerID, err := resourceNumber(resource["centerId"])
		if err != nil {
			orpFailure(w, 500, "日志目标中心无效")
			return
		}
		kindPrefix := "http"
		if kind == "stream-services" {
			kindPrefix = "stream"
		}
		rows, err := s.db.QueryContext(r.Context(), "SELECT id,document FROM orp_resource WHERE kind='nodes' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.centerId'))=?", strconv.FormatInt(centerID, 10))
		if err != nil {
			orpFailure(w, 500, "查询目标节点失败")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var nodeID int64
			var nodeRaw []byte
			if err := rows.Scan(&nodeID, &nodeRaw); err != nil {
				orpFailure(w, 500, "读取目标节点失败")
				return
			}
			var node orpDocument
			if json.Unmarshal(nodeRaw, &node) != nil {
				continue
			}
			target, err := targetForNode(node)
			if err != nil {
				continue
			}
			root, err := demoReleaseRoot(target)
			if err != nil {
				continue
			}
			path := filepath.Join(filepath.Dir(root), target.name, "log", fmt.Sprintf("orp-%s-%d.access.log", kindPrefix, id))
			entries, err := tailNodeAccessLog(path, nodeID, target.name)
			if err == nil {
				lines = append(lines, entries...)
			}
		}
		if err := rows.Err(); err != nil {
			orpFailure(w, 500, "读取目标节点失败")
			return
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].TS == lines[j].TS {
			return lines[i].ID < lines[j].ID
		}
		return lines[i].TS < lines[j].TS
	})
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	orpReply(w, 200, map[string]any{"logs": lines})
}

func tailNodeAccessLog(path string, nodeID int64, nodeName string) ([]orpLogLine, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	start := stat.Size() - 256*1024
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(file)
	if start > 0 {
		discarded, _ := reader.ReadString('\n')
		start += int64(len(discarded))
	}
	result := []orpLogLine{}
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			length := len(line)
			line = strings.TrimSuffix(line, "\n")
			var event struct {
				TS     string `json:"ts"`
				Status int    `json:"status"`
			}
			_ = json.Unmarshal([]byte(line), &event)
			ts := event.TS
			if parsed, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil {
				ts = parsed.Local().Format("2006-01-02 15:04:05")
			}
			level := "INFO"
			if event.Status >= 500 {
				level = "ERROR"
			} else if event.Status >= 400 {
				level = "WARN"
			}
			result = append(result, orpLogLine{ID: logLineID(fmt.Sprintf("%d:%d:%s", nodeID, start, line)), Level: level, Line: "[" + nodeName + "] " + line, TS: ts})
			start += int64(length)
		}
		if err != nil {
			break
		}
	}
	if len(result) > 100 {
		result = result[len(result)-100:]
	}
	return result, nil
}

func logLineID(value string) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return int64(hash.Sum64() & ((1 << 53) - 1))
}

func logLevel(line string) string {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "[error]") || strings.Contains(lower, "[crit]") {
		return "ERROR"
	}
	if strings.Contains(lower, "[warn]") {
		return "WARN"
	}
	if strings.Contains(lower, "[debug]") {
		return "DEBUG"
	}
	return "INFO"
}
