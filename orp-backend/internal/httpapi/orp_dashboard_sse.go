package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Dashboard snapshots are refreshed and pushed once per second for live views.
	dashboardPushInterval      = time.Second
	dashboardSnapshotTTL       = 30 * time.Second
	dashboardRefreshQueue      = "orp:dashboard:refresh"
	dashboardSnapshotCacheBase = "orp:dashboard:snapshot:"
	dashboardPublicScreenUser  = "__public-screen__"
)

var errDashboardStreamUnauthorized = errors.New("dashboard event stream authorization expired")

type dashboardSnapshotContextKey struct{}
type publicScreenContextKey struct{}

func isPublicScreenContext(ctx context.Context) bool {
	public, _ := ctx.Value(publicScreenContextKey{}).(bool)
	return public
}

type dashboardInputCache struct{ values map[string]dashboardInput }

type dashboardSnapshot struct {
	Metrics   json.RawMessage `json:"metrics"`
	Trends    json.RawMessage `json:"trends"`
	Rankings  json.RawMessage `json:"rankings"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type dashboardSnapshotQueueItem struct {
	Query     string `json:"query"`
	User      string `json:"user"`
	ExpiresAt int64  `json:"expiresAt"`
}

type dashboardSnapshotEntry struct {
	Snapshot dashboardSnapshot
	Err      error
	Ready    chan struct{}
}

var dashboardSnapshotFlights sync.Map

func StartDashboardSnapshotWorker(ctx context.Context, database *sql.DB, client *redis.Client) {
	if client == nil {
		return
	}
	go func() {
		for ctx.Err() == nil {
			item, err := client.BLPop(ctx, 2*time.Second, dashboardRefreshQueue).Result()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if !errors.Is(err, redis.Nil) && !strings.Contains(err.Error(), "redis: nil") {
					time.Sleep(time.Second)
				}
				continue
			}
			if len(item) != 2 {
				continue
			}
			var job dashboardSnapshotQueueItem
			if json.Unmarshal([]byte(item[1]), &job) != nil || job.ExpiresAt < time.Now().Unix() {
				continue
			}
			snapshot, buildErr := buildDashboardSnapshot(ctx, database, dashboardInputKey(job.Query), job.User)
			if buildErr != nil {
				log.Printf("dashboard snapshot refresh failed: %v", buildErr)
			}
			if buildErr == nil {
				if payload, marshalErr := json.Marshal(snapshot); marshalErr == nil {
					_ = client.Set(ctx, dashboardSnapshotKey(job.Query, job.User), payload, dashboardSnapshotTTL).Err()
					_ = client.Publish(ctx, dashboardSnapshotKey(job.Query, job.User)+":updates", payload).Err()
				}
			}
			_ = client.Del(ctx, dashboardSnapshotKey(job.Query, job.User)+":queued").Err()
		}
	}()
}

func (s *Server) orpDashboardEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE 不受当前 HTTP 服务支持", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	query, authorization := dashboardInputKey(r.URL.RawQuery), r.Header.Get("Authorization")
	var updates <-chan *redis.Message
	var pubsub *redis.PubSub
	if s.redis != nil {
		pubsub = s.redis.Subscribe(r.Context(), dashboardSnapshotKey(query, user)+":updates")
		defer pubsub.Close()
		// Channel establishes the Redis reader in the background. The periodic
		// cache read below covers updates published before the subscription is ready.
		updates = pubsub.Channel()
		snapshot, found, err := s.cachedDashboardSnapshot(r.Context(), query, user)
		if err != nil {
			writeDashboardStreamError(w, err)
		} else if found {
			writeDashboardSnapshot(w, snapshot)
		} else {
			s.enqueueDashboardRefresh(r.Context(), query, user)
			writeDashboardPending(w)
		}
	} else {
		snapshot, err := s.dashboardSnapshot(r.Context(), query, user)
		if err == nil {
			writeDashboardSnapshot(w, snapshot)
		} else {
			writeDashboardStreamError(w, err)
		}
	}
	flusher.Flush()

	ticker := time.NewTicker(dashboardPushInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if usernameFromToken(authorization) != user {
				writeDashboardStreamError(w, errDashboardStreamUnauthorized)
				flusher.Flush()
				return
			}
			roles, err := s.accountRoles(r, user)
			if err != nil || !accountHasPermission(roles, "orp:read") {
				writeDashboardStreamError(w, errDashboardStreamUnauthorized)
				flusher.Flush()
				return
			}
			if s.redis != nil {
				// Refresh is deduplicated in Redis; clients still receive the latest
				// available snapshot at one-second intervals while a refresh runs.
				s.enqueueDashboardRefresh(r.Context(), query, user)
				snapshot, found, err := s.cachedDashboardSnapshot(r.Context(), query, user)
				if err != nil {
					writeDashboardStreamError(w, err)
				} else if found {
					writeDashboardSnapshot(w, snapshot)
				} else {
					s.enqueueDashboardRefresh(r.Context(), query, user)
					writeDashboardPending(w)
				}
				flusher.Flush()
				continue
			}
			snapshot, err := s.dashboardSnapshot(r.Context(), query, user)
			if err != nil {
				writeDashboardStreamError(w, err)
				flusher.Flush()
				if errors.Is(err, errDashboardStreamUnauthorized) {
					return
				}
				continue
			}
			writeDashboardSnapshot(w, snapshot)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case message, ok := <-updates:
			if !ok {
				updates = nil
				continue
			}
			if message == nil {
				continue
			}
			var snapshot dashboardSnapshot
			if json.Unmarshal([]byte(message.Payload), &snapshot) == nil {
				writeDashboardSnapshot(w, snapshot)
				flusher.Flush()
			}
		}
	}
}

// orpPublicScreenEvents streams the small, read-only data contract used by the
// standalone public display. It deliberately omits detailed routes, trends,
// infrastructure metrics, and upstream configuration.
func (s *Server) orpPublicScreenEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE 不受当前 HTTP 服务支持", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	query := "range=1h"
	writeSnapshot := func() {
		if s.redis != nil {
			s.enqueueDashboardRefresh(r.Context(), query, dashboardPublicScreenUser)
		}
		snapshot, err := s.dashboardSnapshot(r.Context(), query, dashboardPublicScreenUser)
		if err != nil {
			writeDashboardStreamError(w, err)
			flusher.Flush()
			return
		}
		payload, err := publicScreenSnapshotPayload(snapshot)
		if err != nil {
			writeDashboardStreamError(w, err)
			flusher.Flush()
			return
		}
		_, _ = fmt.Fprintf(w, "event: dashboard\ndata: %s\n\n", payload)
		flusher.Flush()
	}
	writeSnapshot()
	ticker := time.NewTicker(dashboardPushInterval)
	heartbeat := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSnapshot()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func publicScreenSnapshotPayload(snapshot dashboardSnapshot) ([]byte, error) {
	var metrics map[string]any
	var rankings map[string]any
	if err := json.Unmarshal(snapshot.Metrics, &metrics); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(snapshot.Rankings, &rankings); err != nil {
		return nil, err
	}
	metrics = map[string]any{
		"summary":     selectObjectFields(metrics["summary"], "totalRequests", "qpsAvg", "errorRate"),
		"statusCodes": selectObjectFields(metrics["statusCodes"], "c2xx", "c3xx", "c4xx", "c5xx"),
		"nodeLoad":    publicScreenNodeLoad(metrics["nodeLoad"]),
		"geography":   metrics["geography"],
	}
	rankings = map[string]any{"domains": rankings["domains"]}
	return json.Marshal(map[string]any{
		"metrics":   metrics,
		"rankings":  rankings,
		"updatedAt": snapshot.UpdatedAt,
	})
}

func selectObjectFields(value any, fields ...string) map[string]any {
	object, _ := value.(map[string]any)
	selected := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, ok := object[field]; ok {
			selected[field] = value
		}
	}
	return selected
}

func publicScreenNodeLoad(value any) []map[string]any {
	rows, _ := value.([]any)
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := selectObjectFields(row, "name", "status", "healthStatus", "conns")
		if len(item) > 0 {
			result = append(result, item)
		}
	}
	return result
}

func dashboardSnapshotKey(query, user string) string {
	fingerprint := sha256.Sum256([]byte(user))
	return dashboardSnapshotCacheBase + fmt.Sprintf("%x:", fingerprint[:12]) + dashboardInputKey(query)
}

func dashboardInputKey(query string) string {
	values := map[string]string{}
	for _, part := range strings.Split(query, "&") {
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) == 2 {
			values[pair[0]] = pair[1]
		}
	}
	keys := []string{"range", "centerId"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := values[key]; value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return strings.Join(parts, "&")
}

func (s *Server) dashboardSnapshot(ctx context.Context, query, user string) (dashboardSnapshot, error) {
	query = dashboardInputKey(query)
	if s.redis != nil {
		cacheCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		payload, err := s.redis.Get(cacheCtx, dashboardSnapshotKey(query, user)).Bytes()
		cancel()
		if err == nil {
			var snapshot dashboardSnapshot
			if err := json.Unmarshal(payload, &snapshot); err == nil {
				return snapshot, nil
			}
		}
		if errors.Is(err, redis.Nil) {
			s.enqueueDashboardRefresh(ctx, query, user)
		}
	}
	flightKey := dashboardSnapshotKey(query, user)
	entryAny, loaded := dashboardSnapshotFlights.LoadOrStore(flightKey, &dashboardSnapshotEntry{Ready: make(chan struct{})})
	entry := entryAny.(*dashboardSnapshotEntry)
	if !loaded {
		entry.Snapshot, entry.Err = buildDashboardSnapshot(ctx, s.db, query, user)
		close(entry.Ready)
		dashboardSnapshotFlights.Delete(flightKey)
		if entry.Err == nil && s.redis != nil {
			cacheCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			if payload, err := json.Marshal(entry.Snapshot); err == nil {
				_ = s.redis.Set(cacheCtx, dashboardSnapshotKey(query, user), payload, dashboardSnapshotTTL).Err()
			}
			cancel()
		}
	} else {
		select {
		case <-entry.Ready:
		case <-ctx.Done():
			return dashboardSnapshot{}, ctx.Err()
		}
	}
	return entry.Snapshot, entry.Err
}

func (s *Server) cachedDashboardSnapshot(ctx context.Context, query, user string) (dashboardSnapshot, bool, error) {
	cacheCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	payload, err := s.redis.Get(cacheCtx, dashboardSnapshotKey(query, user)).Bytes()
	cancel()
	if errors.Is(err, redis.Nil) {
		return dashboardSnapshot{}, false, nil
	}
	if err != nil {
		return dashboardSnapshot{}, false, err
	}
	var snapshot dashboardSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return dashboardSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *Server) enqueueDashboardRefresh(ctx context.Context, query, user string) {
	if s.redis == nil {
		return
	}
	key := dashboardSnapshotKey(query, user)
	job := dashboardSnapshotQueueItem{Query: query, User: user, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	body, _ := json.Marshal(job)
	queueCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	created, err := s.redis.SetNX(queueCtx, key+":queued", "1", dashboardSnapshotTTL).Result()
	if err == nil && created {
		_ = s.redis.RPush(queueCtx, dashboardRefreshQueue, body).Err()
	}
}

func buildDashboardSnapshot(ctx context.Context, database *sql.DB, query, user string) (dashboardSnapshot, error) {
	ctx = context.WithValue(ctx, dashboardSnapshotContextKey{}, &dashboardInputCache{values: map[string]dashboardInput{}})
	if user == dashboardPublicScreenUser {
		ctx = context.WithValue(ctx, publicScreenContextKey{}, true)
	}
	server := &Server{db: database}
	call := func(path string, handler http.HandlerFunc) (json.RawMessage, error) {
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer "+localToken(user))
		request.URL.RawQuery = query
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		if recorder.Code == http.StatusUnauthorized || recorder.Code == http.StatusForbidden {
			return nil, errDashboardStreamUnauthorized
		}
		if recorder.Code != http.StatusOK {
			return nil, fmt.Errorf("dashboard snapshot returned HTTP %d", recorder.Code)
		}
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			return nil, err
		}
		if envelope.Code != 0 || len(envelope.Data) == 0 {
			return nil, errors.New("dashboard snapshot response is invalid")
		}
		return envelope.Data, nil
	}
	metrics, err := call("/api/orp/dashboard/metrics", server.orpDashboardMetrics)
	if err != nil {
		return dashboardSnapshot{}, err
	}
	trends, err := call("/api/orp/dashboard/trends", server.orpDashboardTrends)
	if err != nil {
		return dashboardSnapshot{}, err
	}
	rankings, err := call("/api/orp/dashboard/top-rankings", server.orpDashboardTopRankings)
	if err != nil {
		return dashboardSnapshot{}, err
	}
	return dashboardSnapshot{Metrics: metrics, Trends: trends, Rankings: rankings, UpdatedAt: time.Now()}, nil
}

func writeDashboardSnapshot(w http.ResponseWriter, snapshot dashboardSnapshot) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		writeDashboardStreamError(w, err)
		return
	}
	_, _ = fmt.Fprintf(w, "event: dashboard\ndata: %s\n\n", data)
}

func writeDashboardPending(w http.ResponseWriter) {
	_, _ = fmt.Fprint(w, "event: dashboard-pending\ndata: {}\n\n")
}

func writeDashboardStreamError(w http.ResponseWriter, err error) {
	event, message := "dashboard-error", "读取大盘数据失败"
	if errors.Is(err, errDashboardStreamUnauthorized) {
		event, message = "dashboard-auth-error", "登录状态已失效，请重新登录"
	}
	payload, _ := json.Marshal(map[string]string{"message": message})
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}
