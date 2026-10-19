package httpapi

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nodeDailyAccessTotals struct {
	requests int64
	errors   int64
	updated  time.Time
}

var nodeDailyAccessTotalsCache sync.Map

type containerMetricSample struct {
	cpu        float64
	memory     float64
	sampledAt  time.Time
	refreshing bool
}

var containerMetricSamples = struct {
	sync.Mutex
	byContainer map[string]*containerMetricSample
}{byContainer: make(map[string]*containerMetricSample)}

// cachedContainerMetrics keeps the Docker CLI's one-second sampling wait out
// of each SSE request. A background refresh maintains a recent shared sample.
func cachedContainerMetrics(ctx context.Context, container string) (float64, float64, error) {
	containerMetricSamples.Lock()
	sample := containerMetricSamples.byContainer[container]
	if sample != nil {
		if time.Since(sample.sampledAt) >= time.Second && !sample.refreshing {
			sample.refreshing = true
			go refreshContainerMetrics(container)
		}
		cpu, memory := sample.cpu, sample.memory
		containerMetricSamples.Unlock()
		return cpu, memory, nil
	}
	sample = &containerMetricSample{refreshing: true}
	containerMetricSamples.byContainer[container] = sample
	containerMetricSamples.Unlock()

	cpu, memory, err := readContainerMetrics(ctx, container)
	containerMetricSamples.Lock()
	defer containerMetricSamples.Unlock()
	if err != nil {
		delete(containerMetricSamples.byContainer, container)
		return 0, 0, err
	}
	sample.cpu, sample.memory, sample.sampledAt, sample.refreshing = cpu, memory, time.Now(), false
	return cpu, memory, nil
}

func refreshContainerMetrics(container string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cpu, memory, err := readContainerMetrics(ctx, container)
	containerMetricSamples.Lock()
	defer containerMetricSamples.Unlock()
	sample := containerMetricSamples.byContainer[container]
	if sample == nil {
		return
	}
	sample.refreshing = false
	if err == nil {
		sample.cpu, sample.memory, sample.sampledAt = cpu, memory, time.Now()
	}
}

func readContainerMetrics(ctx context.Context, container string) (float64, float64, error) {
	output, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{json .}}", container).Output()
	if err != nil {
		return 0, 0, err
	}
	var stats struct {
		CPU string `json:"CPUPerc"`
		Mem string `json:"MemPerc"`
	}
	if err := json.Unmarshal(output, &stats); err != nil {
		return 0, 0, err
	}
	cpu, err := strconv.ParseFloat(strings.TrimSuffix(stats.CPU, "%"), 64)
	if err != nil {
		return 0, 0, err
	}
	memory, err := strconv.ParseFloat(strings.TrimSuffix(stats.Mem, "%"), 64)
	return cpu, memory, err
}

// nodeDailyAccessTotals keeps the slow rolling 24-hour summary out of the
// one-second metrics path. Recent QPS and bandwidth are queried separately
// from only the last minute of events.
func (s *Server) nodeDailyAccessTotals(ctx context.Context, nodeID, nodeName string) (int64, int64, error) {
	key := nodeID + ":" + nodeName
	now := time.Now()
	if cached, ok := nodeDailyAccessTotalsCache.Load(key); ok {
		value := cached.(nodeDailyAccessTotals)
		if now.Sub(value.updated) < 10*time.Second {
			return value.requests, value.errors, nil
		}
	}
	var value nodeDailyAccessTotals
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status >= 500),0)
		FROM orp_log_event
		WHERE log_type='access' AND event_ts >= UTC_TIMESTAMP(6) - INTERVAL 24 HOUR
		AND (node_id=? OR node_name=?)`, nodeID, nodeName).Scan(&value.requests, &value.errors)
	if err != nil {
		return 0, 0, err
	}
	value.updated = now
	nodeDailyAccessTotalsCache.Store(key, value)
	return value.requests, value.errors, nil
}
