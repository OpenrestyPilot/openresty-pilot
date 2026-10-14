package httpapi

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func (s *Server) orpNodeStatus(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "节点 ID 无效")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		orpFailure(w, 400, "节点状态无效")
		return
	}
	if body.Status != "running" && body.Status != "paused" && body.Status != "stopped" && body.Status != "maintenance" {
		orpFailure(w, 400, "节点状态无效")
		return
	}
	node, err := s.runtimeNode(r.Context(), id)
	if err != nil {
		orpFailure(w, 404, "节点不存在")
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil || !accountHasCenterPermission(roles, "orp:node:operate", centerIDString(node["centerId"])) {
		orpFailure(w, http.StatusForbidden, "当前角色无权操作该节点")
		return
	}
	target, err := targetForNode(node)
	if err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	state, err := dockerNodeState(r, target)
	if err != nil {
		orpFailure(w, 502, "读取节点容器状态失败")
		return
	}
	var action string
	switch body.Status {
	case "running":
		if state == "paused" {
			action = "unpause"
		} else if state != "running" {
			action = "start"
		}
	case "paused", "maintenance":
		if state == "exited" {
			if err := dockerNodeAction(r, target, "start"); err != nil {
				orpFailure(w, 502, "启动节点失败")
				return
			}
			state = "running"
		}
		if state == "running" {
			action = "pause"
		}
	case "stopped":
		if state == "paused" {
			if err := dockerNodeAction(r, target, "unpause"); err != nil {
				orpFailure(w, 502, "解除节点暂停失败")
				return
			}
			state = "running"
		}
		if state == "running" {
			action = "stop"
		}
	}
	if action != "" && dockerNodeAction(r, target, action) != nil {
		orpFailure(w, 502, "执行节点状态操作失败")
		return
	}
	actual, err := dockerNodeState(r, target)
	if err != nil {
		orpFailure(w, 502, "核对节点状态失败")
		return
	}
	expected := "running"
	if body.Status == "paused" || body.Status == "maintenance" {
		expected = "paused"
	}
	if body.Status == "stopped" {
		expected = "exited"
	}
	if actual != expected {
		orpFailure(w, 502, "节点实际状态未达到目标状态")
		return
	}
	if body.Status == "running" {
		probe, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(target.healthPort)+"/health", nil)
		if err != nil {
			orpFailure(w, 502, "构造节点健康检查失败")
			return
		}
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Do(probe)
		if err != nil || response.StatusCode != 200 {
			if response != nil {
				response.Body.Close()
			}
			orpFailure(w, 502, "节点未通过健康检查")
			return
		}
		response.Body.Close()
	}
	before, _ := json.Marshal(node)
	node["status"] = body.Status
	delete(node, "id")
	delete(node, "centerName")
	after, _ := json.Marshal(node)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "保存节点状态失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=JSON_SET(document,'$.status',?),updated_at=NOW(6) WHERE kind='nodes' AND id=?", body.Status, id); err != nil {
		orpFailure(w, 500, "保存节点状态失败")
		return
	}
	if err := orpAudit(r.Context(), tx, actor, "update", "nodes", id, before, after, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录节点状态审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交节点状态失败")
		return
	}
	node["id"] = id
	centerID, _ := resourceNumber(node["centerId"])
	node["centerName"] = s.publishCenterName(r.Context(), centerID)
	orpReply(w, 200, node)
}

func dockerNodeState(r *http.Request, target demoTarget) (string, error) {
	output, err := exec.CommandContext(r.Context(), "docker", "inspect", "--format", "{{.State.Status}}", target.container).Output()
	return strings.TrimSpace(string(output)), err
}

func dockerNodeAction(r *http.Request, target demoTarget, action string) error {
	_, err := exec.CommandContext(r.Context(), "docker", action, target.container).CombinedOutput()
	return err
}
