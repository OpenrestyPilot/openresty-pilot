package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"
)

// orpNodeMetricEvents streams the same authorized metric snapshot as the REST
// endpoint. The first snapshot is sent immediately; later snapshots follow at
// a one-second cadence. Each snapshot reuses the REST handler so node and
// center-scope checks stay identical across both transports.
func (s *Server) orpNodeMetricEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requirePermission(w, r, "orp:read")
	if !ok {
		return
	}
	id := r.PathValue("id")
	if parsed, err := strconv.ParseInt(id, 10, 64); err != nil || parsed < 1 {
		orpFailure(w, http.StatusBadRequest, "节点 ID 无效")
		return
	}
	snapshot := s.nodeMetricSnapshot(r, id)
	if snapshot.Code != http.StatusOK {
		for key, values := range snapshot.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(snapshot.Code)
		_, _ = w.Write(snapshot.Body.Bytes())
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
	writeNodeMetricSnapshot(w, snapshot)
	flusher.Flush()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	authCheck := time.NewTicker(5 * time.Second)
	defer authCheck.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			snapshot = s.nodeMetricSnapshot(r, id)
			if snapshot.Code == http.StatusOK {
				writeNodeMetricSnapshot(w, snapshot)
			} else if snapshot.Code == http.StatusUnauthorized || snapshot.Code == http.StatusForbidden {
				_, _ = fmt.Fprint(w, "event: metrics-auth-error\ndata: {}\n\n")
				flusher.Flush()
				return
			} else {
				_, _ = fmt.Fprint(w, "event: metrics-error\ndata: {\"message\":\"读取节点指标失败\"}\n\n")
			}
			flusher.Flush()
		case <-authCheck.C:
			roles, err := s.accountRoles(r, user)
			if err != nil || usernameFromToken(r.Header.Get("Authorization")) != user || !accountHasPermission(roles, "orp:read") {
				_, _ = fmt.Fprint(w, "event: metrics-auth-error\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func (s *Server) nodeMetricSnapshot(r *http.Request, id string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/orp/nodes/"+id+"/metrics", nil).WithContext(r.Context())
	request.Header = r.Header.Clone()
	request.SetPathValue("id", id)
	s.orpNodeMetrics(recorder, request)
	return recorder
}

func writeNodeMetricSnapshot(w http.ResponseWriter, snapshot *httptest.ResponseRecorder) {
	_, _ = fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", snapshot.Body.String())
}
