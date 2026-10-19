package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type dashboardInput struct {
	events     []accessEvent
	since, now time.Time
	centerIDs  []int64
}

func (s *Server) dashboardInput(w http.ResponseWriter, r *http.Request) (dashboardInput, bool) {
	cache, cached := r.Context().Value(dashboardSnapshotContextKey{}).(*dashboardInputCache)
	cacheKey := r.URL.RawQuery
	if cached {
		if input, ok := cache.values[cacheKey]; ok {
			return input, true
		}
	}
	input := dashboardInput{now: time.Now()}
	switch r.URL.Query().Get("range") {
	case "", "24h":
		input.since = input.now.Add(-24 * time.Hour)
	case "30m":
		input.since = input.now.Add(-30 * time.Minute)
	case "1h":
		input.since = input.now.Add(-time.Hour)
	case "6h":
		input.since = input.now.Add(-6 * time.Hour)
	case "7d":
		input.since = input.now.Add(-7 * 24 * time.Hour)
	case "today":
		year, month, day := input.now.Date()
		input.since = time.Date(year, month, day, 0, 0, 0, 0, input.now.Location())
	default:
		orpFailure(w, 400, "统计时间范围无效")
		return input, false
	}
	if isPublicScreenContext(r.Context()) {
		if r.URL.Query().Get("centerId") != "" {
			orpFailure(w, http.StatusBadRequest, "公开大屏不支持按中心查询")
			return input, false
		}
		centers, err := s.orpLoad(r.Context(), "centers")
		if err != nil {
			orpFailure(w, 500, "读取中心失败")
			return input, false
		}
		for _, center := range centers {
			id, _ := resourceNumber(center["id"])
			if id > 0 {
				input.centerIDs = append(input.centerIDs, id)
			}
		}
	} else if raw := r.URL.Query().Get("centerId"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			orpFailure(w, 400, "中心 ID 无效")
			return input, false
		}
		roles, err := s.accountRoles(r, usernameFromToken(r.Header.Get("Authorization")))
		if err != nil {
			orpFailure(w, http.StatusUnauthorized, "用户不存在或已禁用")
			return input, false
		}
		if !accountHasCenterPermission(roles, "orp:read", strconv.FormatInt(id, 10)) {
			orpFailure(w, http.StatusForbidden, "当前角色无权访问该中心")
			return input, false
		}
		input.centerIDs = []int64{id}
	} else {
		roles, err := s.accountRoles(r, usernameFromToken(r.Header.Get("Authorization")))
		if err != nil {
			orpFailure(w, 500, "读取权限失败")
			return input, false
		}
		centers, err := s.orpLoad(r.Context(), "centers")
		if err != nil {
			orpFailure(w, 500, "读取中心失败")
			return input, false
		}
		for _, center := range centers {
			id, _ := resourceNumber(center["id"])
			if accountHasCenterPermission(roles, "orp:read", strconv.FormatInt(id, 10)) {
				input.centerIDs = append(input.centerIDs, id)
			}
		}
	}
	input.events = []accessEvent{}
	eventReadStarted := time.Now()
	for _, id := range input.centerIDs {
		events, err := s.accessEvents(r.Context(), id, 0, input.since)
		if err != nil {
			orpFailure(w, 500, "读取访问日志失败")
			return input, false
		}
		input.events = append(input.events, events...)
	}
	if time.Since(eventReadStarted) >= time.Second {
		log.Printf("dashboard access events centers=%d count=%d duration=%s", len(input.centerIDs), len(input.events), time.Since(eventReadStarted).Round(time.Millisecond))
	}
	if cached {
		cache.values[cacheKey] = input
	}
	return input, true
}

func (s *Server) orpDashboardMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	input, ok := s.dashboardInput(w, r)
	if !ok {
		return
	}
	statusCounts := map[int]int{}
	latencies := make([]float64, 0, len(input.events))
	var bytesIn, bytesOut int64
	peakBuckets := map[int64]int{}
	for _, event := range input.events {
		statusCounts[event.Status]++
		latencies = append(latencies, event.RT*1000)
		bytesIn += event.RequestBytes
		bytesOut += event.Bytes
		if timestamp, err := time.Parse(time.RFC3339, event.TS); err == nil {
			peakBuckets[timestamp.Unix()/60]++
		}
	}
	sort.Float64s(latencies)
	pick := func(percent float64) float64 {
		if len(latencies) == 0 {
			return 0
		}
		index := int(math.Ceil(percent*float64(len(latencies)))) - 1
		if index < 0 {
			index = 0
		}
		return latencies[index]
	}
	labels := []string{"<10ms", "10-50ms", "50-100ms", "100-500ms", ">=500ms"}
	bucketCount := []int{0, 0, 0, 0, 0}
	for _, value := range latencies {
		switch {
		case value < 10:
			bucketCount[0]++
		case value < 50:
			bucketCount[1]++
		case value < 100:
			bucketCount[2]++
		case value < 500:
			bucketCount[3]++
		default:
			bucketCount[4]++
		}
	}
	buckets := []map[string]any{}
	for index, label := range labels {
		buckets = append(buckets, map[string]any{"label": label, "count": bucketCount[index]})
	}
	var codes2, codes3, codes4, codes5 int
	details := []map[string]any{}
	for code, count := range statusCounts {
		switch code / 100 {
		case 2:
			codes2 += count
		case 3:
			codes3 += count
		case 4:
			codes4 += count
		case 5:
			codes5 += count
		}
		details = append(details, map[string]any{"code": code, "count": count})
	}
	sort.Slice(details, func(i, j int) bool { return details[i]["code"].(int) < details[j]["code"].(int) })
	duration := input.now.Sub(input.since).Seconds()
	if duration < 1 {
		duration = 1
	}
	peak := 0.0
	for _, count := range peakBuckets {
		if float64(count)/60 > peak {
			peak = float64(count) / 60
		}
	}
	fast, _ := r.Context().Value(dashboardFastSnapshotContextKey{}).(bool)
	var availability *float64
	var err error
	nodeLoad := []map[string]any{}
	upstream := map[string]any{"groups": []any{}, "healthy": 0, "degraded": 0, "down": 0}
	geography := dashboardGeographyPending()
	activeConns := 0
	if !fast {
		probeStarted := time.Now()
		availability, err = dashboardAvailability(r.Context(), s.db, input.since, input.centerIDs)
		if err != nil {
			orpFailure(w, 500, "计算服务可用率失败")
			return
		}
		if time.Since(probeStarted) >= 250*time.Millisecond {
			log.Printf("dashboard availability duration=%s", time.Since(probeStarted).Round(time.Millisecond))
		}
		probeStarted = time.Now()
		nodeLoad = s.dashboardNodeLoad(r.Context(), input.centerIDs)
		if time.Since(probeStarted) >= 250*time.Millisecond {
			log.Printf("dashboard node probes duration=%s", time.Since(probeStarted).Round(time.Millisecond))
		}
		probeStarted = time.Now()
		upstream = s.dashboardUpstreamHealth(r.Context(), input.centerIDs)
		if time.Since(probeStarted) >= 250*time.Millisecond {
			log.Printf("dashboard upstream probes duration=%s", time.Since(probeStarted).Round(time.Millisecond))
		}
		probeStarted = time.Now()
		geography = s.dashboardGeography(input.events)
		if time.Since(probeStarted) >= 250*time.Millisecond {
			log.Printf("dashboard geography duration=%s events=%d", time.Since(probeStarted).Round(time.Millisecond), len(input.events))
		}
		activeConns = sumNodeConnections(r.Context(), s, input.centerIDs)
	}
	orpReply(w, 200, map[string]any{"summary": map[string]any{"totalRequests": len(input.events), "qpsAvg": float64(len(input.events)) / duration, "qpsPeak": peak, "bandwidthInMbps": float64(bytesIn) * 8 / duration / 1e6, "bandwidthOutMbps": float64(bytesOut) * 8 / duration / 1e6, "activeConns": activeConns, "errorRate": percent(codes5, len(input.events)), "availability": availability}, "latency": map[string]any{"p50Ms": pick(.50), "p95Ms": pick(.95), "p99Ms": pick(.99), "buckets": buckets}, "statusCodes": map[string]any{"c2xx": codes2, "c3xx": codes3, "c4xx": codes4, "c5xx": codes5, "details": details}, "nodeLoad": nodeLoad, "upstreamHealth": upstream, "geography": geography})
}

func dashboardGeographyPending() map[string]any {
	return map[string]any{"state": "disabled", "reason": "首屏其余数据正在加载", "databaseType": "", "sampleLimit": maxGeoSampleEvents, "points": []any{}, "centers": []any{}, "flows": []any{}, "unknownRequests": 0}
}

func dashboardAvailability(ctx context.Context, database *sql.DB, since time.Time, centerIDs []int64) (*float64, error) {
	if len(centerIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(centerIDs))
	args := make([]any, 0, len(centerIDs)+1)
	args = append(args, since)
	for index, centerID := range centerIDs {
		placeholders[index] = "?"
		args = append(args, strconv.FormatInt(centerID, 10))
	}
	query := `SELECT COUNT(*),COALESCE(SUM(h.ok),0)
		FROM orp_node_health_sample h
		JOIN orp_resource n ON n.id=h.node_id AND n.kind='nodes'
		WHERE h.sampled_at>=? AND JSON_UNQUOTE(JSON_EXTRACT(n.document,'$.centerId')) IN (` + strings.Join(placeholders, ",") + ")"
	var total, successful int64
	if err := database.QueryRowContext(ctx, query, args...).Scan(&total, &successful); err != nil {
		return nil, err
	}
	return availabilityPercent(successful, total), nil
}

func availabilityPercent(successful, total int64) *float64 {
	if total <= 0 {
		return nil
	}
	value := math.Round(float64(successful)*10000/float64(total)) / 100
	return &value
}

func percent(count, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) * 100 / float64(total)
}

func sumNodeConnections(ctx context.Context, s *Server, centers []int64) int {
	total := 0
	for _, centerID := range centers {
		var count int
		_ = s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(active_connections),0) FROM orp_node_health_sample h JOIN (SELECT node_id,MAX(id) AS latest FROM orp_node_health_sample GROUP BY node_id) latest ON latest.latest=h.id JOIN orp_resource n ON n.id=h.node_id AND n.kind='nodes' WHERE JSON_UNQUOTE(JSON_EXTRACT(n.document,'$.centerId'))=?", strconv.FormatInt(centerID, 10)).Scan(&count)
		total += count
	}
	return total
}

func (s *Server) dashboardNodeLoad(ctx context.Context, centers []int64) []map[string]any {
	nodes, err := s.orpLoad(ctx, "nodes")
	if err != nil {
		return []map[string]any{}
	}
	s.decorateNodeHealth(ctx, nodes)
	allowed := map[int64]bool{}
	for _, id := range centers {
		allowed[id] = true
	}
	result := make([]map[string]any, len(nodes))
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, node := range nodes {
		centerID, _ := resourceNumber(node["centerId"])
		if !allowed[centerID] {
			continue
		}
		target, err := targetForNode(node)
		if err != nil {
			continue
		}
		wait.Add(1)
		go func(index int, node orpDocument, centerID int64, target demoTarget) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			output, err := exec.CommandContext(probeCtx, "docker", "stats", "--no-stream", "--format", "{{json .}}", target.container).Output()
			if err != nil {
				return
			}
			var stats struct {
				CPU string `json:"CPUPerc"`
				Mem string `json:"MemPerc"`
			}
			if json.Unmarshal(output, &stats) != nil {
				return
			}
			cpu, _ := strconv.ParseFloat(strings.TrimSuffix(stats.CPU, "%"), 64)
			mem, _ := strconv.ParseFloat(strings.TrimSuffix(stats.Mem, "%"), 64)
			var connections int
			id, _ := resourceNumber(node["id"])
			_ = s.db.QueryRowContext(ctx, "SELECT active_connections FROM orp_node_health_sample WHERE node_id=? ORDER BY id DESC LIMIT 1", id).Scan(&connections)
			result[index] = map[string]any{"name": node["name"], "centerName": s.publishCenterName(ctx, centerID), "status": node["status"], "healthStatus": node["healthStatus"], "cpuPercent": cpu, "memPercent": mem, "conns": connections}
		}(index, node, centerID, target)
	}
	wait.Wait()
	rows := make([]map[string]any, 0, len(nodes))
	for _, row := range result {
		if row != nil {
			rows = append(rows, row)
		}
	}
	return rows
}

func (s *Server) dashboardUpstreamHealth(ctx context.Context, centers []int64) map[string]any {
	groups, err := s.orpLoad(ctx, "upstream-groups")
	if err != nil {
		return map[string]any{"groups": []map[string]any{}, "healthy": 0, "degraded": 0, "down": 0}
	}
	result := make([]map[string]any, len(groups))
	healthy, degraded, down := 0, 0, 0
	allowed := map[int64]bool{}
	for _, id := range centers {
		allowed[id] = true
	}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 32)
	for index, group := range groups {
		centerID, _ := resourceNumber(group["centerId"])
		if !allowed[centerID] {
			continue
		}
		members, _ := group["nodes"].([]any)
		wait.Add(1)
		go func(index int, group orpDocument, centerID int64, members []any) {
			defer wait.Done()
			var memberWait sync.WaitGroup
			var onlineMu sync.Mutex
			online := 0
			for _, value := range members {
				member, _ := value.(map[string]any)
				host := toString(member["host"])
				port, err := resourcePort(member["port"])
				if err != nil {
					continue
				}
				memberWait.Add(1)
				go func(host, port string) {
					defer memberWait.Done()
					select {
					case semaphore <- struct{}{}:
						defer func() { <-semaphore }()
					case <-ctx.Done():
						return
					}
					dialer := net.Dialer{Timeout: 300 * time.Millisecond}
					connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
					if err == nil {
						_ = connection.Close()
						onlineMu.Lock()
						online++
						onlineMu.Unlock()
					}
				}(host, port)
			}
			memberWait.Wait()
			status := "healthy"
			if online == 0 {
				status = "down"
			} else if online < len(members) {
				status = "degraded"
			}
			result[index] = map[string]any{"name": group["name"], "centerName": s.publishCenterName(ctx, centerID), "lbPolicy": group["lbPolicy"], "healthyCount": online, "total": len(members), "onlineRate": percent(online, len(members)), "status": status}
		}(index, group, centerID, members)
	}
	wait.Wait()
	groupsOut := make([]map[string]any, 0, len(result))
	for _, item := range result {
		if item == nil {
			continue
		}
		groupsOut = append(groupsOut, item)
		switch toString(item["status"]) {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
		case "down":
			down++
		}
	}
	return map[string]any{"groups": groupsOut, "healthy": healthy, "degraded": degraded, "down": down}
}

func (s *Server) orpDashboardTrends(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	input, ok := s.dashboardInput(w, r)
	if !ok {
		return
	}
	step := time.Minute
	if input.now.Sub(input.since) > 6*time.Hour {
		step = time.Hour
	}
	if input.now.Sub(input.since) > 48*time.Hour {
		step = 6 * time.Hour
	}
	type bin struct {
		count   int
		latency float64
		in, out int64
	}
	bins := map[int64]*bin{}
	for _, event := range input.events {
		stamp, err := time.Parse(time.RFC3339, event.TS)
		if err != nil {
			continue
		}
		key := stamp.Unix() / int64(step.Seconds())
		item := bins[key]
		if item == nil {
			item = &bin{}
			bins[key] = item
		}
		item.count++
		item.latency += event.RT * 1000
		item.in += event.RequestBytes
		item.out += event.Bytes
	}
	points := []map[string]any{}
	for cursor := input.since.Truncate(step); !cursor.After(input.now); cursor = cursor.Add(step) {
		item := bins[cursor.Unix()/int64(step.Seconds())]
		if item == nil {
			item = &bin{}
		}
		avg := 0.0
		if item.count > 0 {
			avg = item.latency / float64(item.count)
		}
		points = append(points, map[string]any{"time": cursor.Local().Format("01-02 15:04"), "qps": float64(item.count) / step.Seconds(), "avgLatencyMs": avg, "inMbps": float64(item.in) * 8 / step.Seconds() / 1e6, "outMbps": float64(item.out) * 8 / step.Seconds() / 1e6})
	}
	orpReply(w, 200, map[string]any{"points": points})
}

func (s *Server) orpDashboardTopRankings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	input, ok := s.dashboardInput(w, r)
	if !ok {
		return
	}
	type count struct {
		total   int
		latency float64
	}
	domains := map[string]*count{}
	routes := map[string]*count{}
	for _, event := range input.events {
		domain := event.Host
		if domain == "" {
			continue
		}
		item := domains[domain]
		if item == nil {
			item = &count{}
			domains[domain] = item
		}
		item.total++
		item.latency += event.RT * 1000
		key := domain + "\x00" + event.URI
		route := routes[key]
		if route == nil {
			route = &count{}
			routes[key] = route
		}
		route.total++
		route.latency += event.RT * 1000
	}
	domainRows := []map[string]any{}
	for name, item := range domains {
		domainRows = append(domainRows, map[string]any{"domain": name, "requests": item.total, "percent": percent(item.total, len(input.events)), "avgLatencyMs": item.latency / float64(item.total)})
	}
	routeRows := []map[string]any{}
	for key, item := range routes {
		parts := strings.SplitN(key, "\x00", 2)
		routeRows = append(routeRows, map[string]any{"domain": parts[0], "path": parts[1], "requests": item.total, "percent": percent(item.total, len(input.events)), "avgLatencyMs": item.latency / float64(item.total)})
	}
	sort.Slice(domainRows, func(i, j int) bool { return domainRows[i]["requests"].(int) > domainRows[j]["requests"].(int) })
	sort.Slice(routeRows, func(i, j int) bool { return routeRows[i]["requests"].(int) > routeRows[j]["requests"].(int) })
	if len(domainRows) > 10 {
		domainRows = domainRows[:10]
	}
	if len(routeRows) > 10 {
		routeRows = routeRows[:10]
	}
	orpReply(w, 200, map[string]any{"domains": domainRows, "routes": routeRows})
}
