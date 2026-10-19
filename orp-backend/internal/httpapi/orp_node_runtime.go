package httpapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const nodeSampleInterval = 10 * time.Second

var nginxStatusPattern = regexp.MustCompile(`(?s)Active connections:\s*(\d+).*?server accepts handled requests\s*\n\s*\d+\s+\d+\s+(\d+)`)

func StartNodeSampler(ctx context.Context, db *sql.DB) {
	go func() {
		ticker := time.NewTicker(nodeSampleInterval)
		defer ticker.Stop()
		for {
			sampleAllLocalNodes(ctx, db)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func sampleAllLocalNodes(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, "SELECT id,document FROM orp_resource WHERE kind='nodes'")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw []byte
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var node orpDocument
		if json.Unmarshal(raw, &node) != nil {
			continue
		}
		target, err := targetForNode(node)
		if err != nil {
			continue
		}
		sampleLocalNode(ctx, db, id, target)
	}
	_, _ = db.ExecContext(ctx, "DELETE FROM orp_node_health_sample WHERE sampled_at < NOW(6) - INTERVAL 7 DAY")
}

func sampleLocalNode(ctx context.Context, db *sql.DB, id int64, target demoTarget) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/__orp/status", target.healthPort)
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	start := time.Now()
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	var rtt any
	var active, requests any
	ok := err == nil && response.StatusCode == 200
	if err == nil {
		defer response.Body.Close()
		var body [512]byte
		count, _ := response.Body.Read(body[:])
		if match := nginxStatusPattern.FindStringSubmatch(string(body[:count])); len(match) == 3 {
			active, _ = strconv.Atoi(match[1])
			requests, _ = strconv.ParseInt(match[2], 10, 64)
		} else {
			ok = false
		}
	}
	if ok {
		rtt = float64(time.Since(start).Microseconds()) / 1000
	}
	_, _ = db.ExecContext(ctx, "INSERT INTO orp_node_health_sample(node_id,ok,rtt_ms,active_connections,requests_total) VALUES(?,?,?,?,?)", id, ok, rtt, active, requests)
}

func (s *Server) orpNodeLatencyHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "节点 ID 无效")
		return
	}
	node, err := s.runtimeNode(r.Context(), id)
	if err != nil {
		orpFailure(w, 404, "节点不存在")
		return
	}
	roles, roleErr := s.accountRoles(r, usernameFromToken(r.Header.Get("Authorization")))
	if roleErr != nil || !accountHasCenterPermission(roles, "orp:read", centerIDString(node["centerId"])) {
		orpFailure(w, http.StatusForbidden, "当前角色无权访问该节点")
		return
	}
	if _, err := targetForNode(node); err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT sampled_at,ok,rtt_ms FROM orp_node_health_sample WHERE node_id=? ORDER BY id DESC LIMIT ?", id, limit)
	if err != nil {
		orpFailure(w, 500, "读取节点采样失败")
		return
	}
	defer rows.Close()
	type sample struct {
		TS  time.Time
		OK  bool
		RTT sql.NullFloat64
	}
	samples := []sample{}
	values := []float64{}
	failures := 0
	for rows.Next() {
		var item sample
		if rows.Scan(&item.TS, &item.OK, &item.RTT) != nil {
			orpFailure(w, 500, "读取节点采样失败")
			return
		}
		samples = append(samples, item)
		if item.OK && item.RTT.Valid {
			values = append(values, item.RTT.Float64)
		} else {
			failures++
		}
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取节点采样失败")
		return
	}
	result := []map[string]any{}
	for index := len(samples) - 1; index >= 0; index-- {
		item := samples[index]
		var latency any
		if item.RTT.Valid {
			latency = item.RTT.Float64
		}
		result = append(result, map[string]any{"ts": item.TS.Format("2006-01-02 15:04:05"), "ok": item.OK, "rtt": latency})
	}
	var avg, min, max, p95 any
	if len(values) > 0 {
		sort.Float64s(values)
		sum := 0.0
		for _, value := range values {
			sum += value
		}
		avg, min, max, p95 = sum/float64(len(values)), values[0], values[len(values)-1], values[(95*len(values)+99)/100-1]
	}
	status := "online"
	if len(samples) == 0 || failures == len(samples) {
		status = "offline"
	} else if failures > 0 {
		status = "degraded"
	}
	centerID, _ := resourceNumber(node["centerId"])
	loss := 0.0
	if len(samples) > 0 {
		loss = float64(failures) * 100 / float64(len(samples))
	}
	orpReply(w, 200, map[string]any{"nodeId": id, "nodeName": node["name"], "centerName": s.publishCenterName(r.Context(), centerID), "healthStatus": status, "intervalSec": int(nodeSampleInterval.Seconds()), "samples": result, "summary": map[string]any{"avgMs": avg, "minMs": min, "maxMs": max, "p95Ms": p95, "lossRate": loss, "sampleCount": len(samples), "timeoutMs": 2000}})
}

func (s *Server) runtimeNode(ctx context.Context, id int64) (orpDocument, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", id).Scan(&raw); err != nil {
		return nil, err
	}
	var node orpDocument
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	node["id"] = id
	return node, nil
}

func (s *Server) decorateNodeHealth(ctx context.Context, nodes []orpDocument) {
	for _, node := range nodes {
		id, err := resourceNumber(node["id"])
		if err != nil {
			continue
		}
		rows, err := s.db.QueryContext(ctx, "SELECT sampled_at,ok,rtt_ms FROM orp_node_health_sample WHERE node_id=? ORDER BY id DESC LIMIT 3", id)
		if err != nil {
			continue
		}
		count, failed := 0, 0
		for rows.Next() {
			var stamp time.Time
			var ok bool
			var rtt sql.NullFloat64
			if rows.Scan(&stamp, &ok, &rtt) != nil {
				continue
			}
			if count == 0 {
				node["lastCheckedAt"] = stamp.Format("2006-01-02 15:04:05")
				if rtt.Valid {
					node["lastLatencyMs"] = rtt.Float64
				} else {
					node["lastLatencyMs"] = nil
				}
			}
			count++
			if !ok {
				failed++
			}
		}
		rows.Close()
		status := "unknown"
		if count > 0 && failed == count {
			status = "offline"
		}
		if count > 0 && failed < count {
			status = "online"
			if failed > 0 {
				status = "degraded"
			}
		}
		node["healthStatus"] = status
		node["healthCheckIntervalSec"] = int(nodeSampleInterval.Seconds())
		node["healthRule"] = "最近 3 次探测全部成功为在线，部分失败为不稳定，全部失败为离线"
		var agentState []byte
		var agentSeen time.Time
		if err := s.db.QueryRowContext(ctx, "SELECT state,received_at FROM orp_agent_heartbeat WHERE node_id=?", id).Scan(&agentState, &agentSeen); err == nil && time.Since(agentSeen) <= 2*time.Minute {
			var state struct {
				OpenRestyRunning bool `json:"openrestyRunning"`
			}
			if json.Unmarshal(agentState, &state) == nil {
				node["healthStatus"] = "offline"
				if state.OpenRestyRunning {
					node["healthStatus"] = "online"
				}
				node["lastCheckedAt"] = agentSeen.Format("2006-01-02 15:04:05")
				node["healthRule"] = "由 Node Agent 心跳上报；超过 2 分钟未收到心跳后回退到主动探测结果"
			}
		}
	}
}

type accessEvent struct {
	TS           string  `json:"ts"`
	Status       int     `json:"status"`
	RT           float64 `json:"rt"`
	Host         string  `json:"host"`
	RemoteAddr   string  `json:"remoteAddr"`
	URI          string  `json:"uri"`
	Bytes        int64   `json:"bytes"`
	RequestBytes int64   `json:"requestBytes"`
	NodeID       int64   `json:"-"`
}

func (s *Server) accessEvents(ctx context.Context, centerID int64, nodeFilter int64, since time.Time) ([]accessEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,document FROM orp_resource WHERE kind='nodes' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.centerId'))=?", strconv.FormatInt(centerID, 10))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := map[int64]demoTarget{}
	nodeNames := map[int64]string{}
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		if nodeFilter != 0 && id != nodeFilter {
			continue
		}
		var node orpDocument
		if json.Unmarshal(raw, &node) != nil {
			continue
		}
		nodeNames[id] = toString(node["name"])
		if target, err := targetForNode(node); err == nil {
			nodes[id] = target
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	kafkaEvents, hasKafkaData, err := s.kafkaAccessEvents(ctx, nodeNames, since)
	if err != nil {
		return nil, err
	}
	if hasKafkaData {
		return kafkaEvents, nil
	}
	listeners, err := s.db.QueryContext(ctx, "SELECT id FROM orp_resource WHERE kind='http-listeners' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.centerId'))=?", strconv.FormatInt(centerID, 10))
	if err != nil {
		return nil, err
	}
	defer listeners.Close()
	ids := []int64{}
	for listeners.Next() {
		var id int64
		if listeners.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	events := []accessEvent{}
	for nodeID, target := range nodes {
		root, err := demoReleaseRoot(target)
		if err != nil {
			continue
		}
		for _, listenerID := range ids {
			path := filepath.Join(filepath.Dir(root), target.name, "log", fmt.Sprintf("orp-http-%d.access.log", listenerID))
			file, err := os.Open(path)
			if err != nil {
				continue
			}
			stat, err := file.Stat()
			if err != nil {
				file.Close()
				continue
			}
			if stat.Size() > 64*1024*1024 {
				file.Close()
				return nil, errors.New("访问日志超过单文件统计上限")
			}
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 4096), 1024*1024)
			for scanner.Scan() {
				var event accessEvent
				if json.Unmarshal(scanner.Bytes(), &event) != nil {
					continue
				}
				timestamp, err := time.Parse(time.RFC3339, event.TS)
				if err != nil || timestamp.Before(since) {
					continue
				}
				event.NodeID = nodeID
				events = append(events, event)
			}
			if err := scanner.Err(); err != nil {
				file.Close()
				return nil, err
			}
			file.Close()
		}
	}
	return events, nil
}

func (s *Server) orpNodeMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "节点 ID 无效")
		return
	}
	node, err := s.runtimeNode(r.Context(), id)
	if err != nil {
		orpFailure(w, 404, "节点不存在")
		return
	}
	roles, roleErr := s.accountRoles(r, usernameFromToken(r.Header.Get("Authorization")))
	if roleErr != nil || !accountHasCenterPermission(roles, "orp:read", centerIDString(node["centerId"])) {
		orpFailure(w, http.StatusForbidden, "当前角色无权访问该节点")
		return
	}
	target, err := targetForNode(node)
	if err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	centerID, _ := resourceNumber(node["centerId"])
	var inspectOutput []byte
	var cpu, memory float64
	var statsErr, inspectErr, dailyErr, recentErr error
	var connections int
	var requestTotal24h, errorCount24h, lastMinute, inBytes, outBytes int64
	var wg sync.WaitGroup
	launch := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	// Docker and database reads are independent. Collect them concurrently so
	// an SSE refresh is bounded by the slowest source instead of their sum.
	launch(func() {
		cpu, memory, statsErr = cachedContainerMetrics(r.Context(), target.container)
	})
	launch(func() {
		inspectOutput, inspectErr = exec.CommandContext(r.Context(), "docker", "inspect", "--format", `{{ index .Config.Labels "com.docker.compose.project" }} {{ index .Config.Labels "com.docker.compose.service" }} {{.State.StartedAt}}`, target.container).Output()
	})
	launch(func() {
		_ = s.db.QueryRowContext(r.Context(), "SELECT active_connections FROM orp_node_health_sample WHERE node_id=? AND ok=1 ORDER BY id DESC LIMIT 1", id).Scan(&connections)
	})
	launch(func() {
		requestTotal24h, errorCount24h, dailyErr = s.nodeDailyAccessTotals(r.Context(), strconv.FormatInt(id, 10), toString(node["name"]))
	})
	launch(func() {
		recentErr = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(request_bytes),0), COALESCE(SUM(bytes_sent),0)
			FROM orp_log_event WHERE log_type='access' AND event_ts >= UTC_TIMESTAMP(6) - INTERVAL 1 MINUTE
			AND (node_id=? OR node_name=?)`, id, toString(node["name"])).Scan(&lastMinute, &inBytes, &outBytes)
	})
	wg.Wait()
	if statsErr != nil {
		orpFailure(w, 502, "读取节点容器指标失败")
		return
	}
	inspectParts := strings.Fields(string(inspectOutput))
	if inspectErr != nil || len(inspectParts) != 3 || inspectParts[0] != "orp" || inspectParts[1] != target.name {
		orpFailure(w, 502, "本地 OpenResty 示例容器不存在或身份不匹配")
		return
	}
	if dailyErr != nil || recentErr != nil {
		orpFailure(w, 500, "读取节点访问数据失败")
		return
	}
	// The rolling QPS and bandwidth query above deliberately visits only one
	// minute of events. The 24-hour summary is cached separately for a few
	// seconds so an SSE subscriber does not rescan a full day on every tick.
	// Preserve the local-file fallback only when the persisted event pipeline has
	// no records for this node. Normal SSE updates then use indexed aggregates
	// instead of rescanning a full day of log lines once per second.
	if requestTotal24h == 0 {
		events, fallbackErr := s.accessEvents(r.Context(), centerID, id, time.Now().Add(-24*time.Hour))
		if fallbackErr != nil {
			orpFailure(w, 500, "读取节点访问数据失败")
			return
		}
		requestTotal24h = int64(len(events))
		for _, event := range events {
			if event.Status >= 500 {
				errorCount24h++
			}
			stamp, _ := time.Parse(time.RFC3339, event.TS)
			if time.Since(stamp) <= time.Minute {
				lastMinute++
				inBytes += event.RequestBytes
				outBytes += event.Bytes
			}
		}
	}
	started, err := time.Parse(time.RFC3339Nano, inspectParts[2])
	if err != nil {
		orpFailure(w, 502, "解析节点运行时间失败")
		return
	}
	errorRate := 0.0
	if requestTotal24h > 0 {
		errorRate = float64(errorCount24h) * 100 / float64(requestTotal24h)
	}
	node["centerName"] = s.publishCenterName(r.Context(), centerID)
	orpReply(w, 200, map[string]any{"node": node, "metrics": map[string]any{"cpuPercent": cpu, "memPercent": memory, "connections": connections, "qps": float64(lastMinute) / 60, "requestTotal24h": requestTotal24h, "errorRatePercent": errorRate, "bandwidthInMbps": float64(inBytes) * 8 / 60 / 1e6, "bandwidthOutMbps": float64(outBytes) * 8 / 60 / 1e6, "uptimeDays": time.Since(started).Hours() / 24}})
}
