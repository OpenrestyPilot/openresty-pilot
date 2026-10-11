package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"
)

func (s *Server) orpLogEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	authorization := r.Header.Get("Authorization")
	var cursor int64
	if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(id),0) FROM orp_log_event").Scan(&cursor); err != nil {
		orpFailure(w, 500, "读取 Kafka 日志游标失败")
		return
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/orp/logs", nil).WithContext(r.Context())
	request.Header = r.Header.Clone()
	request.URL.RawQuery = r.URL.RawQuery
	s.orpLogs(recorder, request)
	if recorder.Code != http.StatusOK {
		for key, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
		return
	}
	var envelope struct {
		Data struct {
			Logs []orpLogLine `json:"logs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		orpFailure(w, 500, "解析初始日志快照失败")
		return
	}
	targetType, targetIDText, _ := strings.Cut(r.URL.Query().Get("target"), ":")
	targetID, _ := strconv.ParseInt(targetIDText, 10, 64)
	kind := logKindFromTarget(targetType)
	resource := logResourceFromTarget(r.Context(), s, kind, targetID)
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
	writeLogLinesEvent(w, envelope.Data.Logs)
	flusher.Flush()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	authCheck := time.NewTicker(5 * time.Second)
	defer authCheck.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			lines, nextCursor, _, err := s.kafkaLogLines(r.Context(), kind, targetID, resource, cursor, 500)
			if err != nil {
				payload, _ := json.Marshal(map[string]string{"message": "读取 Kafka 日志失败"})
				_, _ = fmt.Fprintf(w, "event: log-error\ndata: %s\n\n", payload)
				flusher.Flush()
				continue
			}
			if len(lines) > 0 {
				cursor = nextCursor
				writeLogLinesEvent(w, lines)
				flusher.Flush()
			}
		case <-authCheck.C:
			roles, err := s.accountRoles(r, user)
			if usernameFromToken(authorization) != user || err != nil || !accountHasPermission(roles, "orp:read") {
				_, _ = fmt.Fprint(w, "event: log-auth-error\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeLogLinesEvent(w http.ResponseWriter, lines []orpLogLine) {
	data, _ := json.Marshal(lines)
	_, _ = fmt.Fprintf(w, "event: logs\ndata: %s\n\n", data)
}

func logKindFromTarget(targetType string) string {
	if targetType == "http" {
		return "http-listeners"
	}
	if targetType == "stream" {
		return "stream-services"
	}
	return "nodes"
}

func logResourceFromTarget(ctx context.Context, server *Server, kind string, id int64) orpDocument {
	var raw []byte
	if server.db.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind=? AND id=?", kind, id).Scan(&raw) != nil {
		return orpDocument{}
	}
	var resource orpDocument
	_ = json.Unmarshal(raw, &resource)
	return resource
}
