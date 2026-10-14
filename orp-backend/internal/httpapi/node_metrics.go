package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

var (
	activeConnectionsPattern = regexp.MustCompile(`Active connections:\s*(\d+)`)
	countersPattern          = regexp.MustCompile(`\s(\d+)\s+(\d+)\s+(\d+)\s*`)
	statesPattern            = regexp.MustCompile(`Reading:\s*(\d+)\s+Writing:\s*(\d+)\s+Waiting:\s*(\d+)`)
)

type nodeMetricsView struct {
	NodeID            string  `json:"nodeId"`
	NodeName          string  `json:"nodeName"`
	Host              string  `json:"host"`
	Port              int     `json:"port"`
	Available         bool    `json:"available"`
	Message           *string `json:"message"`
	ActiveConnections *int64  `json:"activeConnections"`
	Accepts           *int64  `json:"accepts"`
	Handled           *int64  `json:"handled"`
	Requests          *int64  `json:"requests"`
	Reading           *int64  `json:"reading"`
	Writing           *int64  `json:"writing"`
	Waiting           *int64  `json:"waiting"`
}

func (server *Server) listNodeMetrics(w http.ResponseWriter, r *http.Request) {
	center, ok := pathUUID(w, r, "centerID")
	if !ok {
		return
	}
	nodes, err := server.nodes(center)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	values := make([]nodeMetricsView, 0, len(nodes))
	for _, node := range nodes {
		values = append(values, readNodeMetrics(r.Context(), client, node))
	}
	writeJSON(w, http.StatusOK, values)
}

func readNodeMetrics(parent context.Context, client *http.Client, node nodeView) nodeMetricsView {
	base := nodeMetricsView{NodeID: node.ID, NodeName: node.Name, Host: node.Host, Port: node.ServicePort}
	if !node.Enabled {
		return unavailableNodeMetrics(base, "该节点未启用")
	}
	requestContext, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, fmt.Sprintf("http://%s:%d/__orp/status", node.Host, node.ServicePort), nil)
	if err != nil {
		return unavailableNodeMetrics(base, "无法创建节点状态请求")
	}
	response, err := client.Do(request)
	if err != nil {
		return unavailableNodeMetrics(base, "无法连接节点状态接口")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return unavailableNodeMetrics(base, fmt.Sprintf("状态接口返回 HTTP %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	if err != nil {
		return unavailableNodeMetrics(base, "无法读取节点状态接口")
	}
	return parseNodeMetrics(base, string(body))
}

func parseNodeMetrics(base nodeMetricsView, body string) nodeMetricsView {
	active := activeConnectionsPattern.FindStringSubmatch(body)
	counters := countersPattern.FindStringSubmatch(body)
	states := statesPattern.FindStringSubmatch(body)
	if len(active) != 2 || len(counters) != 4 || len(states) != 4 {
		return unavailableNodeMetrics(base, "状态接口返回格式无效")
	}
	values := make([]int64, 0, 7)
	for _, raw := range []string{active[1], counters[1], counters[2], counters[3], states[1], states[2], states[3]} {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return unavailableNodeMetrics(base, "状态接口返回格式无效")
		}
		values = append(values, value)
	}
	base.Available = true
	base.ActiveConnections = &values[0]
	base.Accepts = &values[1]
	base.Handled = &values[2]
	base.Requests = &values[3]
	base.Reading = &values[4]
	base.Writing = &values[5]
	base.Waiting = &values[6]
	return base
}

func unavailableNodeMetrics(base nodeMetricsView, message string) nodeMetricsView {
	base.Message = &message
	return base
}
