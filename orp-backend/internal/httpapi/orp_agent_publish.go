package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const agentProbePort = 18080

func nodeHasAgent(node orpDocument) bool {
	return strings.TrimSpace(toString(node["agentId"])) != ""
}

func agentCandidateOperation(kind string, candidateID int64) string {
	if candidateID < 1 || (kind != "validate" && kind != "publish" && kind != "probe") {
		return ""
	}
	return kind + ":" + strconv.FormatInt(candidateID, 36)
}

func agentReconcileProbeOperation(candidateID, itemID int64, releaseID string) string {
	if candidateID < 1 || itemID < 1 || uuid.Validate(releaseID) != nil {
		return ""
	}
	return "probe-release:" + strconv.FormatInt(candidateID, 36) + ":" + strconv.FormatInt(itemID, 36) + ":" + releaseID
}

func parseAgentReconcileProbeOperation(operation string) (candidateID, itemID int64, releaseID string, ok bool) {
	parts := strings.Split(operation, ":")
	if len(parts) != 4 || parts[0] != "probe-release" || uuid.Validate(parts[3]) != nil {
		return 0, 0, "", false
	}
	candidate, candidateOK := new(big.Int).SetString(parts[1], 36)
	item, itemOK := new(big.Int).SetString(parts[2], 36)
	if !candidateOK || !itemOK || !candidate.IsInt64() || !item.IsInt64() || candidate.Sign() < 1 || item.Sign() < 1 {
		return 0, 0, "", false
	}
	return candidate.Int64(), item.Int64(), parts[3], true
}

// The V44 candidate workflow has its own immutable table and must not share
// numeric IDs with the older publish-candidate table. Its only Agent action
// is validation, which never changes the active release.
func agentAsyncCandidateOperation(candidateID int64) string {
	if candidateID < 1 {
		return ""
	}
	return "candidate-validate:" + strconv.FormatInt(candidateID, 36)
}

func parseAgentCandidateOperation(operation string) (string, int64, bool) {
	if candidateID, _, _, ok := parseAgentReconcileProbeOperation(operation); ok {
		return "probe", candidateID, true
	}
	parts := strings.Split(operation, ":")
	if len(parts) != 2 || (parts[0] != "validate" && parts[0] != "publish" && parts[0] != "probe" && parts[0] != "candidate-validate") || parts[1] == "" {
		return "", 0, false
	}
	value, ok := new(big.Int).SetString(parts[1], 36)
	if !ok || !value.IsInt64() || value.Sign() < 1 {
		return "", 0, false
	}
	return parts[0], value.Int64(), true
}

type agentPublishLink struct {
	batchID int64
	itemID  int64
}

func (s *Server) agentReleaseTaskCreate(ctx context.Context, nodeID, candidateID int64, operation, actor string, publishLink ...agentPublishLink) (string, error) {
	encoded := agentCandidateOperation(operation, candidateID)
	if operation == "candidate-validate" {
		encoded = agentAsyncCandidateOperation(candidateID)
	}
	if encoded == "" {
		return "", errors.New("Agent release operation is invalid")
	}
	if operation == "probe-release" {
		return "", errors.New("reconciliation probes must use agentReconcileProbeTaskCreate")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var nodeRaw []byte
	if err := tx.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=? FOR UPDATE", nodeID).Scan(&nodeRaw); err != nil {
		return "", errors.New("Agent node does not exist")
	}
	var node orpDocument
	if err := json.Unmarshal(nodeRaw, &node); err != nil || strings.TrimSpace(toString(node["agentId"])) == "" {
		return "", errors.New("node has no enrolled Agent identity")
	}
	if operation == "candidate-validate" {
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT snapshot FROM orp_configuration_candidate WHERE id=?", candidateID).Scan(&raw); err != nil {
			return "", errors.New("async configuration candidate does not exist")
		}
		frozen, err := snapshotConfig(raw)
		if err != nil || frozen.Nodes[nodeID] == nil {
			return "", errors.New("candidate does not contain this Agent node")
		}
		frozenNode := frozen.Nodes[nodeID]
		if toString(node["agentId"]) != toString(frozenNode["agentId"]) || toString(node["agentCertificateFingerprint"]) != toString(frozenNode["agentCertificateFingerprint"]) {
			return "", errors.New("Agent identity changed after candidate freeze")
		}
	}
	var heartbeatIdentity string
	if err := tx.QueryRowContext(ctx, "SELECT certificate_fingerprint FROM orp_agent_heartbeat WHERE node_id=? AND received_at>DATE_SUB(NOW(6),INTERVAL 2 MINUTE)", nodeID).Scan(&heartbeatIdentity); err != nil {
		return "", errors.New("Agent heartbeat is stale")
	}
	validIdentity := strings.EqualFold(heartbeatIdentity, toString(node["agentCertificateFingerprint"])) && toString(node["agentCertificateFingerprint"]) != ""
	if !validIdentity {
		var tokenHash string
		if err := tx.QueryRowContext(ctx, "SELECT token_hash FROM orp_agent_token WHERE node_id=?", nodeID).Scan(&tokenHash); err == nil {
			validIdentity = strings.EqualFold(heartbeatIdentity, tokenHash)
		}
	}
	if !validIdentity {
		return "", errors.New("Agent heartbeat does not match a currently registered credential")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE orp_agent_task SET status='expired' WHERE node_id=? AND status IN ('queued','claimed') AND expires_at<=NOW(6)", nodeID); err != nil {
		return "", err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM orp_agent_task WHERE node_id=? AND status IN ('queued','claimed')", nodeID).Scan(&count); err != nil || count != 0 {
		return "", errors.New("node already has a pending Agent task")
	}
	taskID := uuid.NewString()
	now := time.Now().UTC()
	if operation == "publish" {
		if len(publishLink) != 1 || publishLink[0].batchID < 1 || publishLink[0].itemID < 1 {
			return "", errors.New("Agent publish task requires a durable batch item link")
		}
		link := publishLink[0]
		var linkedNode, linkedCandidate int64
		var itemStatus string
		var linkedTask sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT node_id,candidate_id,status,agent_task_id FROM orp_publish_item WHERE id=? AND batch_id=? FOR UPDATE", link.itemID, link.batchID).Scan(&linkedNode, &linkedCandidate, &itemStatus, &linkedTask); err != nil || linkedNode != nodeID || linkedCandidate != candidateID || itemStatus != "deploying" || linkedTask.Valid {
			return "", errors.New("Agent publish item no longer matches its task")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO orp_agent_task(id,node_id,operation,status,issued_at,expires_at,created_by,publish_batch_id,publish_item_id) VALUES(?,?,?,'queued',?,?,?,?,?)", taskID, nodeID, encoded, now, now.Add(5*time.Minute), actor, link.batchID, link.itemID); err != nil {
			return "", err
		}
		linked, err := tx.ExecContext(ctx, "UPDATE orp_publish_item SET agent_task_id=? WHERE id=? AND batch_id=? AND status='deploying' AND agent_task_id IS NULL", taskID, link.itemID, link.batchID)
		if err != nil {
			return "", err
		}
		if count, _ := linked.RowsAffected(); count != 1 {
			return "", errors.New("Agent publish task could not be linked to its batch item")
		}
	} else if _, err := tx.ExecContext(ctx, "INSERT INTO orp_agent_task(id,node_id,operation,status,issued_at,expires_at,created_by) VALUES(?,?,?,'queued',?,?,?)", taskID, nodeID, encoded, now, now.Add(5*time.Minute), actor); err != nil {
		return "", err
	}
	audit, _ := json.Marshal(map[string]any{"taskId": taskID, "operation": operation, "candidateId": candidateID})
	if err := orpAudit(ctx, tx, actor, "agent_release_task_create", "nodes", nodeID, nil, audit, "local"); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return taskID, nil
}

func (s *Server) agentReleaseForTask(ctx context.Context, nodeID int64, taskOperation string) (orpRelease, string, error) {
	if candidateID, itemID, expectedRelease, ok := parseAgentReconcileProbeOperation(taskOperation); ok {
		var releaseID string
		var centerID int64
		var snapshotRaw []byte
		var candidateStatus, batchPhase, itemStatus string
		var itemNode, itemCandidate int64
		if err := s.db.QueryRowContext(ctx, `SELECT c.release_uuid,c.center_id,c.snapshot,c.status,b.phase,i.status,i.node_id,i.candidate_id
			FROM orp_publish_item i JOIN orp_publish_batch b ON b.id=i.batch_id
			JOIN orp_publish_candidate c ON c.id=i.candidate_id WHERE i.id=?`, itemID).Scan(&releaseID, &centerID, &snapshotRaw, &candidateStatus, &batchPhase, &itemStatus, &itemNode, &itemCandidate); err != nil {
			return orpRelease{}, "", errors.New("reconciliation probe item does not exist")
		}
		if itemNode != nodeID || itemCandidate != candidateID || batchPhase != "recovery_required" || (itemStatus != "failed" && itemStatus != "aborted") || candidateStatus != "validated" {
			return orpRelease{}, "", errors.New("reconciliation probe is not bound to an eligible recovery item")
		}
		var baseline sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT baseline_release_uuid FROM orp_publish_precheck
			WHERE candidate_id=? AND node_id=? AND passed=TRUE ORDER BY id DESC LIMIT 1`, candidateID, nodeID).Scan(&baseline); err != nil {
			return orpRelease{}, "", errors.New("recovery baseline is unavailable")
		}
		if expectedRelease != releaseID && (!baseline.Valid || expectedRelease != baseline.String) {
			return orpRelease{}, "", errors.New("reconciliation probe release is not the frozen desired release or precheck baseline")
		}
		snapshot, err := snapshotConfig(snapshotRaw)
		if err != nil {
			return orpRelease{}, "", err
		}
		if snapshot.Nodes[nodeID] == nil {
			return orpRelease{}, "", errors.New("frozen candidate node is missing")
		}
		release, err := renderReleaseForTarget(centerID, expectedRelease, snapshot.Resources, snapshot.Singletons, agentProbePort)
		if err != nil {
			return orpRelease{}, "", err
		}
		return release, "probe", nil
	}
	kind, candidateID, ok := parseAgentCandidateOperation(taskOperation)
	if !ok {
		return orpRelease{}, "", errors.New("Agent task operation is invalid")
	}
	if kind == "candidate-validate" {
		return s.agentReleaseForAsyncCandidate(ctx, nodeID, candidateID)
	}
	var releaseID string
	var centerID int64
	var snapshotRaw, targetRaw []byte
	var candidateStatus string
	if err := s.db.QueryRowContext(ctx, "SELECT release_uuid,center_id,snapshot,target_ids,status FROM orp_publish_candidate WHERE id=?", candidateID).Scan(&releaseID, &centerID, &snapshotRaw, &targetRaw, &candidateStatus); err != nil {
		return orpRelease{}, "", errors.New("frozen candidate does not exist")
	}
	if candidateStatus != "validated" && candidateStatus != "frozen" {
		return orpRelease{}, "", errors.New("frozen candidate is not available for Agent execution")
	}
	var targets []int64
	if err := json.Unmarshal(targetRaw, &targets); err != nil {
		return orpRelease{}, "", errors.New("candidate target list is invalid")
	}
	found := false
	for _, id := range targets {
		if id == nodeID {
			found = true
			break
		}
	}
	if !found {
		return orpRelease{}, "", errors.New("candidate does not include this node")
	}
	snapshot, err := snapshotConfig(snapshotRaw)
	if err != nil {
		return orpRelease{}, "", err
	}
	node := snapshot.Nodes[nodeID]
	if node == nil {
		return orpRelease{}, "", errors.New("candidate node snapshot is missing")
	}
	center, err := resourceNumber(node["centerId"])
	if err != nil || center != centerID {
		return orpRelease{}, "", errors.New("candidate node center does not match")
	}
	release, err := renderReleaseForTarget(centerID, releaseID, snapshot.Resources, snapshot.Singletons, agentProbePort)
	if err != nil {
		return orpRelease{}, "", err
	}
	return release, kind, nil
}

func (s *Server) agentPublishNextTask(w http.ResponseWriter, r *http.Request) {
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
	var taskID, taskOperation string
	var issuedAt, expiresAt time.Time
	err = tx.QueryRowContext(r.Context(), `SELECT id,operation,issued_at,expires_at FROM orp_agent_task
WHERE node_id=? AND status='queued' AND expires_at>NOW(6) ORDER BY issued_at,id LIMIT 1 FOR UPDATE`, nodeID).Scan(&taskID, &taskOperation, &issuedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeAgentError(w, 500, errors.New("读取任务失败"))
		return
	}
	operation := taskOperation
	var releaseID, digest string
	if taskOperation != "reload" {
		candidateOperation, _, ok := parseAgentCandidateOperation(taskOperation)
		if !ok {
			writeAgentError(w, 500, errors.New("任务操作无效"))
			return
		}
		release, _, releaseErr := s.agentReleaseForTask(r.Context(), nodeID, taskOperation)
		if releaseErr != nil {
			writeAgentError(w, 409, releaseErr)
			return
		}
		if candidateOperation == "candidate-validate" {
			operation = "validate"
		} else {
			operation = candidateOperation
		}
		releaseID, digest = release.ReleaseID, release.Digest
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_agent_task SET status='claimed',claimed_at=NOW(6),started_at=NOW(6) WHERE id=? AND status='queued'", taskID); err != nil {
		writeAgentError(w, 500, errors.New("领取任务失败"))
		return
	}
	if err := tx.Commit(); err != nil {
		writeAgentError(w, 500, errors.New("领取任务提交失败"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": taskID, "nodeId": presented, "operation": operation, "releaseId": releaseID, "digest": digest, "issuedAt": issuedAt.UTC(), "expiresAt": expiresAt.UTC()})
}

func (s *Server) agentPublishArtifact(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskId")
	if _, err := uuid.Parse(taskID); err != nil {
		writeAgentError(w, 400, errors.New("任务 ID 无效"))
		return
	}
	var presented string
	if r.Method == http.MethodGet {
		presented = r.URL.Query().Get("nodeId")
	}
	if presented == "" {
		writeAgentError(w, 400, errors.New("nodeId is required"))
		return
	}
	nodeID, _, err := s.authenticatedAgentNode(r, presented)
	if err != nil {
		writeAgentError(w, http.StatusUnauthorized, err)
		return
	}
	var owner int64
	var status, operation string
	var expires time.Time
	if err := s.db.QueryRowContext(r.Context(), "SELECT node_id,status,operation,expires_at FROM orp_agent_task WHERE id=?", taskID).Scan(&owner, &status, &operation, &expires); err != nil || owner != nodeID {
		writeAgentError(w, 404, errors.New("任务不存在"))
		return
	}
	if status != "claimed" || !expires.After(time.Now()) {
		writeAgentError(w, 409, errors.New("任务未领取或已过期"))
		return
	}
	kind, _, ok := parseAgentCandidateOperation(operation)
	if !ok || (kind != "publish" && kind != "validate" && kind != "candidate-validate") {
		writeAgentError(w, 409, errors.New("任务不包含候选制品"))
		return
	}
	release, _, err := s.agentReleaseForTask(r.Context(), nodeID, operation)
	if err != nil {
		writeAgentError(w, 409, err)
		return
	}
	encoded, err := json.Marshal(release)
	if err != nil || len(encoded) > 16<<20 {
		writeAgentError(w, 413, errors.New("release artifact exceeds the transport limit"))
		return
	}
	writeJSON(w, http.StatusOK, release)
}

func (s *Server) waitAgentReleaseTask(ctx context.Context, taskID string) (string, string, error) {
	deadline := time.NewTimer(2 * time.Minute)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		var status string
		var resultStatus, output, errorText sql.NullString
		if err := s.db.QueryRowContext(ctx, "SELECT status,result_status,output_text,error_text FROM orp_agent_task WHERE id=?", taskID).Scan(&status, &resultStatus, &output, &errorText); err != nil {
			return "", "", err
		}
		if status == "completed" {
			if resultStatus.String != "succeeded" {
				return output.String, errorText.String, errors.New("Agent task did not succeed")
			}
			return output.String, "", nil
		}
		if status == "expired" {
			return output.String, errorText.String, errors.New("Agent task expired")
		}
		select {
		case <-ctx.Done():
			_, _ = s.db.ExecContext(context.Background(), "UPDATE orp_agent_task SET status='expired' WHERE id=? AND status IN ('queued','claimed')", taskID)
			return "", "", ctx.Err()
		case <-deadline.C:
			_, _ = s.db.ExecContext(context.Background(), "UPDATE orp_agent_task SET status='expired' WHERE id=? AND status IN ('queued','claimed')", taskID)
			return "", "", errors.New("timed out waiting for Agent task")
		case <-ticker.C:
		}
	}
}

func (s *Server) validateAgentCandidate(ctx context.Context, nodeID int64, candidate publishCandidate, actor string) (string, error) {
	taskID, err := s.agentReleaseTaskCreate(ctx, nodeID, candidate.ID, "validate", actor)
	if err != nil {
		return "", err
	}
	output, taskError, err := s.waitAgentReleaseTask(ctx, taskID)
	if err != nil {
		return output, fmt.Errorf("Agent 候选校验失败: %s: %w", taskError, err)
	}
	return output, nil
}

func (s *Server) probeAgentCandidate(ctx context.Context, nodeID int64, candidate publishCandidate, actor string) (string, error) {
	taskID, err := s.agentReleaseTaskCreate(ctx, nodeID, candidate.ID, "probe", actor)
	if err != nil {
		return "", err
	}
	output, taskError, err := s.waitAgentReleaseTask(ctx, taskID)
	if err != nil {
		return output, fmt.Errorf("Agent 活动版本核对失败: %s: %w", taskError, err)
	}
	return output, nil
}

// probeAgentRecoveryRelease is read-only and bound to the exact immutable
// batch item. The requested release UUID is accepted only when it equals that
// candidate's desired UUID or its successful precheck baseline.
func (s *Server) probeAgentRecoveryRelease(ctx context.Context, nodeID, candidateID, itemID int64, releaseID, actor string) (string, error) {
	encoded := agentReconcileProbeOperation(candidateID, itemID, releaseID)
	if encoded == "" {
		return "", errors.New("reconciliation probe parameters are invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var batchID, linkedCandidate, linkedNode int64
	var batchPhase, itemStatus, candidateStatus, candidateUUID string
	var baseline sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT i.batch_id,i.candidate_id,i.node_id,b.phase,i.status,c.status,c.release_uuid
		FROM orp_publish_item i JOIN orp_publish_batch b ON b.id=i.batch_id
		JOIN orp_publish_candidate c ON c.id=i.candidate_id WHERE i.id=? FOR UPDATE`, itemID).Scan(&batchID, &linkedCandidate, &linkedNode, &batchPhase, &itemStatus, &candidateStatus, &candidateUUID); err != nil {
		return "", errors.New("recovery item does not exist")
	}
	if linkedNode != nodeID || linkedCandidate != candidateID || batchPhase != "recovery_required" || (itemStatus != "failed" && itemStatus != "aborted") || candidateStatus != "validated" {
		return "", errors.New("recovery item is not eligible for Agent reconciliation")
	}
	if err := tx.QueryRowContext(ctx, `SELECT baseline_release_uuid FROM orp_publish_precheck
		WHERE candidate_id=? AND node_id=? AND passed=TRUE ORDER BY id DESC LIMIT 1`, candidateID, nodeID).Scan(&baseline); err != nil {
		return "", errors.New("frozen precheck baseline is unavailable")
	}
	if releaseID != candidateUUID && (!baseline.Valid || releaseID != baseline.String) {
		return "", errors.New("requested probe release is not the frozen desired release or precheck baseline")
	}
	var nodeRaw []byte
	if err := tx.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=? FOR UPDATE", nodeID).Scan(&nodeRaw); err != nil {
		return "", errors.New("Agent node does not exist")
	}
	var node orpDocument
	if err := json.Unmarshal(nodeRaw, &node); err != nil || !nodeHasAgent(node) {
		return "", errors.New("node has no enrolled Agent identity")
	}
	var heartbeatIdentity string
	if err := tx.QueryRowContext(ctx, "SELECT certificate_fingerprint FROM orp_agent_heartbeat WHERE node_id=? AND received_at>DATE_SUB(NOW(6),INTERVAL 2 MINUTE)", nodeID).Scan(&heartbeatIdentity); err != nil {
		return "", errors.New("Agent heartbeat is stale")
	}
	validIdentity := strings.EqualFold(heartbeatIdentity, toString(node["agentCertificateFingerprint"])) && toString(node["agentCertificateFingerprint"]) != ""
	if !validIdentity {
		var tokenHash string
		if err := tx.QueryRowContext(ctx, "SELECT token_hash FROM orp_agent_token WHERE node_id=?", nodeID).Scan(&tokenHash); err == nil {
			validIdentity = strings.EqualFold(heartbeatIdentity, tokenHash)
		}
	}
	if !validIdentity {
		return "", errors.New("Agent heartbeat does not match a registered credential")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE orp_agent_task SET status='expired' WHERE node_id=? AND status IN ('queued','claimed') AND expires_at<=NOW(6)", nodeID); err != nil {
		return "", err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM orp_agent_task WHERE node_id=? AND status IN ('queued','claimed')", nodeID).Scan(&pending); err != nil || pending != 0 {
		return "", errors.New("node already has a pending Agent task")
	}
	taskID := uuid.NewString()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, "INSERT INTO orp_agent_task(id,node_id,operation,status,issued_at,expires_at,created_by,publish_batch_id,publish_item_id) VALUES(?,?,?,'queued',?,?,?,?,?)", taskID, nodeID, encoded, now, now.Add(5*time.Minute), actor, batchID, itemID); err != nil {
		return "", err
	}
	audit, _ := json.Marshal(map[string]any{"taskId": taskID, "operation": "probe", "candidateId": candidateID, "itemId": itemID, "expectedReleaseUuid": releaseID})
	if err := orpAudit(ctx, tx, actor, "agent_recovery_probe", "publish", itemID, nil, audit, "local"); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	output, taskError, err := s.waitAgentReleaseTask(ctx, taskID)
	if err != nil {
		return output, fmt.Errorf("Agent reconciliation probe failed: %s: %w", taskError, err)
	}
	return output, nil
}

func (s *Server) validateAsyncAgentCandidate(ctx context.Context, nodeID, candidateID int64, actor string) (string, error) {
	taskID, err := s.agentReleaseTaskCreate(ctx, nodeID, candidateID, "candidate-validate", actor)
	if err != nil {
		return "", err
	}
	output, taskError, err := s.waitAgentReleaseTask(ctx, taskID)
	if err != nil {
		return output, fmt.Errorf("Agent 候选校验失败: %s: %w", taskError, err)
	}
	return output, nil
}

func (s *Server) agentReleaseForAsyncCandidate(ctx context.Context, nodeID, candidateID int64) (orpRelease, string, error) {
	var releaseID, storedDigest string
	var centerID int64
	var snapshotRaw, targetRaw []byte
	if err := s.db.QueryRowContext(ctx, "SELECT candidate_uuid,center_id,snapshot,target_ids,digest FROM orp_configuration_candidate WHERE id=?", candidateID).Scan(&releaseID, &centerID, &snapshotRaw, &targetRaw, &storedDigest); err != nil {
		return orpRelease{}, "", errors.New("异步配置候选不存在")
	}
	var targets []int64
	if err := json.Unmarshal(targetRaw, &targets); err != nil {
		return orpRelease{}, "", errors.New("候选目标列表无效")
	}
	found := false
	for _, id := range targets {
		found = found || id == nodeID
	}
	if !found {
		return orpRelease{}, "", errors.New("候选未包含该 Agent 节点")
	}
	snapshot, err := snapshotConfig(snapshotRaw)
	if err != nil {
		return orpRelease{}, "", err
	}
	node := snapshot.Nodes[nodeID]
	if node == nil || !nodeHasAgent(node) {
		return orpRelease{}, "", errors.New("候选节点没有已登记的 Agent")
	}
	if _, err := targetForNode(node); err == nil {
		return orpRelease{}, "", errors.New("Quickstart 节点应由本地非激活校验器处理")
	}
	actualCenter, err := resourceNumber(node["centerId"])
	if err != nil || actualCenter != centerID {
		return orpRelease{}, "", errors.New("候选节点中心与候选中心不一致")
	}
	if directives, _ := node["directives"].([]any); len(directives) != 0 {
		return orpRelease{}, "", errors.New("节点级原生指令不受候选渲染器支持")
	}
	release, err := renderReleaseForTarget(centerID, releaseID, snapshot.Resources, snapshot.Singletons, agentProbePort)
	if err != nil {
		return orpRelease{}, "", err
	}
	if release.Digest != storedDigest {
		return orpRelease{}, "", errors.New("候选渲染摘要与冻结摘要不一致")
	}
	return release, "validate", nil
}

func (s *Server) publishAgentCandidate(ctx context.Context, nodeID int64, candidate publishCandidate, actor string, batchID, itemID int64) (string, error) {
	taskID, err := s.agentReleaseTaskCreate(ctx, nodeID, candidate.ID, "publish", actor, agentPublishLink{batchID: batchID, itemID: itemID})
	if err != nil {
		return "", err
	}
	output, taskError, err := s.waitAgentReleaseTask(ctx, taskID)
	if err != nil {
		return output, fmt.Errorf("Agent 发布失败: %s: %w", taskError, err)
	}
	return output, nil
}
