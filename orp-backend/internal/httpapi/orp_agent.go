package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type agentHeartbeat struct {
	NodeID string          `json:"nodeId"`
	State  json.RawMessage `json:"state"`
}

type agentTaskResult struct {
	TaskID      string    `json:"taskId"`
	NodeID      string    `json:"nodeId"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	Output      string    `json:"output,omitempty"`
	Error       string    `json:"error,omitempty"`
}

func (s *Server) authenticatedAgentNode(r *http.Request, presented string) (int64, string, error) {
	var id int64
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT id,document FROM orp_resource WHERE kind='nodes' AND JSON_UNQUOTE(JSON_EXTRACT(document,'$.agentId'))=?", presented).Scan(&id, &raw); err != nil {
		return 0, "", errors.New("Agent 节点未登记")
	}
	var node orpDocument
	if err := json.Unmarshal(raw, &node); err != nil {
		return 0, "", errors.New("节点登记数据无效")
	}
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		cert := r.TLS.PeerCertificates[0]
		sum := sha256.Sum256(cert.Raw)
		fingerprint := strings.ToLower(hex.EncodeToString(sum[:]))
		registered := strings.ToLower(toString(node["agentCertificateFingerprint"]))
		if validateAgentFingerprint(registered) && registered == fingerprint {
			return id, fingerprint, nil
		}
	}
	if r.TLS == nil {
		return 0, "", errors.New("Agent 请求必须通过 HTTPS 发送")
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		return 0, "", errors.New("需要已登记的节点客户端证书或 Agent Token")
	}
	digest := sha256.Sum256([]byte(token))
	var stored string
	if err := s.db.QueryRowContext(r.Context(), "SELECT token_hash FROM orp_agent_token WHERE node_id=?", id).Scan(&stored); err != nil || !strings.EqualFold(stored, hex.EncodeToString(digest[:])) {
		return 0, "", errors.New("Agent Token 无效或已撤销")
	}
	return id, hex.EncodeToString(digest[:]), nil
}

// agentBootstrapCreate rotates the node's long-lived Agent credential. The raw
// token is returned only in this response; storage and authentication use its SHA-256 digest.
func (s *Server) agentBootstrapCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	nodeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || nodeID < 1 {
		orpFailure(w, 400, "节点 ID 无效")
		return
	}
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", nodeID).Scan(&raw); err != nil {
		orpFailure(w, 404, "节点不存在")
		return
	}
	var node orpDocument
	if err := json.Unmarshal(raw, &node); err != nil {
		orpFailure(w, 500, "读取节点失败")
		return
	}
	roles, roleErr := s.accountRoles(r, actor)
	if roleErr != nil || !accountHasCenterPermission(roles, "orp:node:operate", centerIDString(node["centerId"])) {
		orpFailure(w, 403, "当前角色无权操作该节点")
		return
	}
	controlURL := strings.TrimSpace(s.runtimeValue("system.agent_public_url"))
	if s.runtimeSettings == nil {
		controlURL = strings.TrimSpace(os.Getenv("ORP_AGENT_PUBLIC_URL"))
	}
	if controlURL == "" {
		orpFailure(w, 409, "请先配置节点可访问的 ORP_AGENT_PUBLIC_URL")
		return
	}
	parsedURL, parseErr := url.Parse(controlURL)
	if parseErr != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		orpFailure(w, 500, "ORP_AGENT_PUBLIC_URL 必须是有效的 HTTPS 地址")
		return
	}
	credential := make([]byte, 32)
	if _, err := rand.Read(credential); err != nil {
		orpFailure(w, 500, "生成 Agent Token 失败")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(credential)
	digest := sha256.Sum256([]byte(token))
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "签发 Agent Token 失败")
		return
	}
	defer tx.Rollback()
	if toString(node["agentId"]) == "" {
		node["agentId"] = "node-" + strconv.FormatInt(nodeID, 10)
	}
	delete(node, "agentCertificateFingerprint")
	updated, _ := json.Marshal(node)
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=?,updated_at=NOW(6) WHERE kind='nodes' AND id=?", updated, nodeID); err != nil {
		orpFailure(w, 500, "保存 Agent 节点身份失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO orp_agent_token(node_id,token_hash,created_at) VALUES(?,?,NOW(6)) ON DUPLICATE KEY UPDATE token_hash=VALUES(token_hash),created_at=VALUES(created_at)`, nodeID, hex.EncodeToString(digest[:])); err != nil {
		orpFailure(w, 500, "保存 Agent Token 失败")
		return
	}
	audit, _ := json.Marshal(map[string]string{"agentId": toString(node["agentId"]), "credential": "rotated"})
	if err := orpAudit(r.Context(), tx, actor, "agent_token_rotate", "nodes", nodeID, nil, audit, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录 Agent Token 审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交 Agent Token 失败")
		return
	}
	base := strings.TrimRight(controlURL, "/")
	packageURL := "https://gitlab.com/OpenrestyPilot/orp-node-agent/-/releases/permalink/latest/downloads/orp-node-agent-linux-amd64.tar.gz"
	nodeAgentID := toString(node["agentId"])
	configJSON, _ := json.Marshal(map[string]string{"nodeId": nodeAgentID, "controlPlaneUrl": base, "agentToken": token, "serverCAFile": "/etc/ssl/certs/ca-certificates.crt", "nginxBinary": "/usr/local/openresty/nginx/sbin/nginx", "nginxConfig": "/usr/local/openresty/nginx/conf/nginx.conf", "reloadHelper": "/usr/local/sbin/orp-openresty-reload", "stateDir": "/var/lib/orp-node-agent", "pidFile": "/usr/local/openresty/nginx/logs/nginx.pid"})
	dockerConfig, _ := json.Marshal(map[string]string{"nodeId": nodeAgentID, "controlPlaneUrl": base, "agentToken": token, "serverCAFile": "/etc/ssl/cert.pem", "nginxBinary": "/usr/local/openresty/nginx/sbin/nginx", "nginxConfig": "/usr/local/openresty/nginx/conf/nginx.conf", "stateDir": "/var/lib/orp-node-agent", "pidFile": "/usr/local/openresty/nginx/logs/nginx.pid"})
	archive := "curl -fsSL " + shellQuote(packageURL) + " -o /tmp/orp-node-agent.tgz || exit 1;"
	install := archive + " sudo tar -xzf /tmp/orp-node-agent.tgz -C /tmp && sudo install -m 0755 /tmp/orp-node-agent /usr/local/bin/orp-node-agent && sudo install -m 0755 /tmp/deploy/systemd/orp-openresty-reload /usr/local/sbin/orp-openresty-reload && sudo install -m 0440 /tmp/deploy/systemd/orp-node-agent.sudoers /etc/sudoers.d/orp-node-agent && sudo install -m 0644 /tmp/deploy/systemd/orp-node-agent.service /etc/systemd/system/orp-node-agent.service && (id -u orp-agent >/dev/null 2>&1 || sudo useradd --system --home-dir /var/lib/orp-node-agent --shell /usr/sbin/nologin orp-agent) && sudo install -d -o root -g orp-agent -m 0750 /etc/orp-node-agent && printf %s " + shellQuote(string(configJSON)) + " | sudo tee /etc/orp-node-agent/config.json >/dev/null && sudo chown root:orp-agent /etc/orp-node-agent/config.json && sudo chmod 0640 /etc/orp-node-agent/config.json && sudo systemctl daemon-reload && sudo systemctl enable --now orp-node-agent"
	wget := strings.Replace(install, "curl -fsSL "+shellQuote(packageURL)+" -o /tmp/orp-node-agent.tgz || exit 1;", "wget -qO /tmp/orp-node-agent.tgz "+shellQuote(packageURL)+" || exit 1;", 1)
	docker := "docker run -d --name openresty-pilot-" + strconv.FormatInt(nodeID, 10) + " --restart unless-stopped -p 80:80 --entrypoint /bin/sh openresty/openresty:alpine -c " + shellQuote("apk add --no-cache ca-certificates wget tar >/dev/null && wget -qO /tmp/orp-node-agent.tgz "+shellQuote(packageURL)+" && tar -xzf /tmp/orp-node-agent.tgz -C /tmp && install -m 0755 /tmp/orp-node-agent /usr/local/bin/orp-node-agent && mkdir -p /etc/orp-node-agent /var/lib/orp-node-agent && umask 077 && printf %s "+shellQuote(string(dockerConfig))+" > /etc/orp-node-agent/config.json && ( /usr/local/bin/orp-node-agent -config /etc/orp-node-agent/config.json & ) && exec /usr/local/openresty/bin/openresty -g 'daemon off;'")
	orpReply(w, 200, map[string]any{"nodeId": nodeID, "agentId": node["agentId"], "token": token, "controlPlaneUrl": base, "commands": map[string]string{"curl": install, "wget": wget, "docker": docker}})
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func writeAgentError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var body agentHeartbeat
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeID == "" || len(body.State) == 0 || !json.Valid(body.State) || !validAgentState(body.State) {
		writeAgentError(w, http.StatusBadRequest, errors.New("心跳请求无效"))
		return
	}
	nodeID, fingerprint, err := s.authenticatedAgentNode(r, body.NodeID)
	if err != nil {
		writeAgentError(w, http.StatusUnauthorized, err)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO orp_agent_heartbeat(node_id,certificate_fingerprint,state,received_at)
VALUES(?,?,?,NOW(6)) ON DUPLICATE KEY UPDATE certificate_fingerprint=VALUES(certificate_fingerprint),state=VALUES(state),received_at=NOW(6)`, nodeID, fingerprint, body.State); err != nil {
		writeAgentError(w, http.StatusInternalServerError, errors.New("保存心跳失败"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentNextTask(w http.ResponseWriter, r *http.Request) {
	presented := r.URL.Query().Get("nodeId")
	nodeID, _, err := s.authenticatedAgentNode(r, presented)
	if err != nil {
		writeAgentError(w, http.StatusUnauthorized, err)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAgentError(w, 500, errors.New("领取任务失败"))
		return
	}
	defer tx.Rollback()
	var taskID, operation string
	var issuedAt, expiresAt time.Time
	err = tx.QueryRowContext(r.Context(), `SELECT id,operation,issued_at,expires_at FROM orp_agent_task
WHERE node_id=? AND status='queued' AND expires_at>NOW(6) ORDER BY issued_at,id LIMIT 1 FOR UPDATE`, nodeID).Scan(&taskID, &operation, &issuedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeAgentError(w, 500, errors.New("读取任务失败"))
		return
	}
	if operation != "reload" {
		writeAgentError(w, 500, errors.New("任务操作无效"))
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_agent_task SET status='claimed',claimed_at=NOW(6),started_at=NOW(6) WHERE id=? AND status='queued'", taskID); err != nil {
		writeAgentError(w, 500, errors.New("领取任务失败"))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAgentError(w, 500, errors.New("领取任务提交失败"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": taskID, "nodeId": presented, "operation": operation, "issuedAt": issuedAt.UTC(), "expiresAt": expiresAt.UTC()})
}

func (s *Server) agentTaskResult(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskId")
	if _, err := uuid.Parse(taskID); err != nil {
		writeAgentError(w, 400, errors.New("任务 ID 无效"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var result agentTaskResult
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		writeAgentError(w, 400, errors.New("任务结果无效"))
		return
	}
	if result.TaskID != taskID || result.NodeID == "" || (result.Status != "succeeded" && result.Status != "failed" && result.Status != "rejected") {
		writeAgentError(w, 400, errors.New("任务结果字段无效"))
		return
	}
	nodeID, _, err := s.authenticatedAgentNode(r, result.NodeID)
	if err != nil {
		writeAgentError(w, 401, err)
		return
	}
	output := result.Output
	if len(output) > 4096 {
		output = output[:4096]
	}
	errorText := result.Error
	if len(errorText) > 2048 {
		errorText = errorText[:2048]
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeAgentError(w, 500, errors.New("保存任务结果失败"))
		return
	}
	defer tx.Rollback()
	var owner int64
	var status string
	if err := tx.QueryRowContext(r.Context(), "SELECT node_id,status FROM orp_agent_task WHERE id=? FOR UPDATE", taskID).Scan(&owner, &status); err != nil {
		writeAgentError(w, 404, errors.New("任务不存在"))
		return
	}
	if owner != nodeID {
		writeAgentError(w, 403, errors.New("任务不属于该节点"))
		return
	}
	if status == "completed" {
		if err := tx.Commit(); err != nil {
			writeAgentError(w, 500, errors.New("确认重复结果失败"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if status != "claimed" {
		writeAgentError(w, 409, errors.New("任务尚未领取或已过期"))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE orp_agent_task SET status='completed',completed_at=NOW(6),result_status=?,output_text=?,error_text=? WHERE id=?`, result.Status, output, errorText, taskID); err != nil {
		writeAgentError(w, 500, errors.New("保存任务结果失败"))
		return
	}
	audit, _ := json.Marshal(map[string]string{"taskId": taskID, "status": result.Status})
	if err := orpAudit(r.Context(), tx, "agent:"+result.NodeID, "agent_task_result", "nodes", nodeID, nil, audit, orpClientIP(r)); err != nil {
		writeAgentError(w, 500, errors.New("记录 Agent 结果审计失败"))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAgentError(w, 500, errors.New("提交任务结果失败"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentReloadTaskCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	if !s.requireSuper(w, r) {
		return
	}
	nodeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || nodeID < 1 {
		orpFailure(w, 400, "节点 ID 无效")
		return
	}
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", nodeID).Scan(&raw); err != nil {
		orpFailure(w, 404, "节点不存在")
		return
	}
	var node orpDocument
	if err := json.Unmarshal(raw, &node); err != nil {
		orpFailure(w, 500, "读取节点失败")
		return
	}
	roles, roleErr := s.accountRoles(r, actor)
	if roleErr != nil || !accountHasCenterPermission(roles, "orp:node:operate", centerIDString(node["centerId"])) {
		orpFailure(w, http.StatusForbidden, "当前角色无权操作该节点")
		return
	}
	agentID := toString(node["agentId"])
	fingerprint := strings.ToLower(toString(node["agentCertificateFingerprint"]))
	if agentID == "" {
		orpFailure(w, 409, "节点尚未登记 Agent 身份")
		return
	}
	if !validateAgentFingerprint(fingerprint) {
		if err := s.db.QueryRowContext(r.Context(), "SELECT token_hash FROM orp_agent_token WHERE node_id=?", nodeID).Scan(&fingerprint); err != nil {
			orpFailure(w, 409, "节点尚未登记 Agent 凭据")
			return
		}
	}
	var heartbeat time.Time
	if err := s.db.QueryRowContext(r.Context(), "SELECT received_at FROM orp_agent_heartbeat WHERE node_id=? AND certificate_fingerprint=?", nodeID, fingerprint).Scan(&heartbeat); err != nil || time.Since(heartbeat) > 2*time.Minute {
		orpFailure(w, 409, "Agent 未在线")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建 Agent reload 任务失败")
		return
	}
	defer tx.Rollback()
	var lockedID int64
	if err := tx.QueryRowContext(r.Context(), "SELECT id FROM orp_resource WHERE kind='nodes' AND id=? FOR UPDATE", nodeID).Scan(&lockedID); err != nil {
		orpFailure(w, 404, "节点不存在")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_agent_task SET status='expired' WHERE node_id=? AND status IN ('queued','claimed') AND expires_at<=NOW(6)", nodeID); err != nil {
		orpFailure(w, 500, "清理过期 Agent 任务失败")
		return
	}
	var count int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_agent_task WHERE node_id=? AND status IN ('queued','claimed')", nodeID).Scan(&count); err != nil || count > 0 {
		orpFailure(w, 409, "节点已有未完成 Agent 任务")
		return
	}
	taskID := uuid.NewString()
	issued, expires := time.Now().UTC(), time.Now().UTC().Add(2*time.Minute)
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO orp_agent_task(id,node_id,operation,status,issued_at,expires_at,created_by) VALUES(?,?, 'reload','queued',?,?,?)", taskID, nodeID, issued, expires, actor); err != nil {
		orpFailure(w, 500, "创建 Agent reload 任务失败")
		return
	}
	audit, _ := json.Marshal(map[string]string{"taskId": taskID, "operation": "reload"})
	if err := orpAudit(r.Context(), tx, actor, "agent_task_create", "nodes", nodeID, nil, audit, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录 Agent 任务审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交 Agent reload 任务失败")
		return
	}
	orpReply(w, http.StatusCreated, map[string]any{"id": taskID, "nodeId": agentID, "operation": "reload", "issuedAt": issued, "expiresAt": expires})
}

func (s *Server) agentTasksList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.orpIdentity(w, r); !ok {
		return
	}
	if !s.requireSuper(w, r) {
		return
	}
	nodeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || nodeID < 1 {
		orpFailure(w, 400, "节点 ID 无效")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,operation,status,issued_at,expires_at,claimed_at,completed_at,result_status,output_text,error_text
FROM orp_agent_task WHERE node_id=? ORDER BY issued_at DESC LIMIT 50`, nodeID)
	if err != nil {
		orpFailure(w, 500, "读取 Agent 任务失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, operation, status string
		var issued, expires time.Time
		var claimed, completed sql.NullTime
		var resultStatus, output, errorText sql.NullString
		if err := rows.Scan(&id, &operation, &status, &issued, &expires, &claimed, &completed, &resultStatus, &output, &errorText); err != nil {
			orpFailure(w, 500, "读取 Agent 任务失败")
			return
		}
		items = append(items, map[string]any{"id": id, "operation": operation, "status": status, "issuedAt": issued.UTC(), "expiresAt": expires.UTC(), "claimedAt": nullTime(claimed), "completedAt": nullTime(completed), "resultStatus": nullString(resultStatus), "output": nullString(output), "error": nullString(errorText)})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取 Agent 任务失败")
		return
	}
	orpReply(w, 200, map[string]any{"items": items})
}

func (s *Server) agentCertificateRegister(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	if !s.requireSuper(w, r) {
		return
	}
	nodeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || nodeID < 1 {
		orpFailure(w, 400, "节点 ID 无效")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var body struct {
		NodeID                 string `json:"nodeId"`
		CertificateFingerprint string `json:"certificateFingerprint"`
		Revoke                 bool   `json:"revoke"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || body.NodeID == "" || (!body.Revoke && !validateAgentFingerprint(body.CertificateFingerprint)) {
		orpFailure(w, 400, "Agent 节点 ID 或 SHA-256 证书指纹无效")
		return
	}
	var raw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", nodeID).Scan(&raw); err != nil {
		orpFailure(w, 404, "节点不存在")
		return
	}
	var node orpDocument
	if err := json.Unmarshal(raw, &node); err != nil {
		orpFailure(w, 500, "节点登记数据损坏")
		return
	}
	before := append([]byte(nil), raw...)
	if existing := toString(node["agentId"]); existing != "" && existing != body.NodeID && toString(node["agentCertificateFingerprint"]) != "" {
		orpFailure(w, 409, "已登记 Agent ID 不可更换；请先撤销旧证书后重新登记")
		return
	}
	var duplicate int
	fingerprint := strings.ToLower(body.CertificateFingerprint)
	if !body.Revoke {
		if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_resource WHERE kind='nodes' AND id<>? AND (JSON_UNQUOTE(JSON_EXTRACT(document,'$.agentId'))=? OR LOWER(JSON_UNQUOTE(JSON_EXTRACT(document,'$.agentCertificateFingerprint')))=?)", nodeID, body.NodeID, fingerprint).Scan(&duplicate); err != nil || duplicate > 0 {
			orpFailure(w, 409, "Agent ID 或证书指纹已绑定到其他节点")
			return
		}
	}
	node["agentId"] = body.NodeID
	if body.Revoke {
		fingerprint = ""
	}
	node["agentCertificateFingerprint"] = fingerprint
	updated, _ := json.Marshal(node)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "登记 Agent 证书失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=?,updated_at=NOW(6) WHERE kind='nodes' AND id=?", updated, nodeID); err != nil {
		orpFailure(w, 500, "更新节点 Agent 身份失败")
		return
	}
	if body.Revoke {
		if _, err := tx.ExecContext(r.Context(), "UPDATE orp_agent_task SET status='expired' WHERE node_id=? AND status IN ('queued','claimed')", nodeID); err != nil {
			orpFailure(w, 500, "撤销节点任务失败")
			return
		}
	}
	if err := orpAudit(r.Context(), tx, actor, "agent_certificate_register", "nodes", nodeID, before, updated, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录 Agent 证书审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交 Agent 证书登记失败")
		return
	}
	orpReply(w, 200, map[string]any{"nodeId": nodeID, "agentId": body.NodeID, "certificateFingerprint": fingerprint, "revoked": body.Revoke})
}

func validateAgentFingerprint(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size
}

func validAgentState(raw json.RawMessage) bool {
	var state struct {
		CollectedAt      time.Time `json:"collectedAt"`
		Hostname         string    `json:"hostname"`
		OS               string    `json:"os"`
		Arch             string    `json:"arch"`
		OpenRestyVersion string    `json:"openrestyVersion"`
		OpenRestyRunning bool      `json:"openrestyRunning"`
		OpenRestyPID     int       `json:"openrestyPid"`
		ConfigReadable   bool      `json:"configReadable"`
	}
	return json.Unmarshal(raw, &state) == nil && !state.CollectedAt.IsZero() && len(state.Hostname) <= 255 && len(state.OS) <= 64 && len(state.Arch) <= 64 && len(state.OpenRestyVersion) <= 255 && state.OpenRestyPID >= 0
}
