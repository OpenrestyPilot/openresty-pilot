package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Rollback freezes a new candidate from the selected historical snapshot.
// The historical release is never reactivated directly: it is validated on
// the target node again and receives a new release identity and audit trail.
func (s *Server) orpPublishRollback(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID < 1 {
		orpFailure(w, 400, "历史记录 ID 无效")
		return
	}
	historyID := body.ID
	var nodeID, centerID int64
	var sourceRelease string
	var raw []byte
	err := s.db.QueryRowContext(r.Context(), `SELECT i.node_id,i.center_id,c.release_uuid,c.snapshot
FROM orp_publish_item i JOIN orp_publish_candidate c ON c.id=i.candidate_id
WHERE i.id=? AND i.status='success'`, historyID).Scan(&nodeID, &centerID, &sourceRelease, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 404, "只能回滚已成功发布的历史版本")
		return
	}
	if err != nil {
		orpFailure(w, 500, "读取历史快照失败")
		return
	}
	nodes, _, err := s.publishNodes(r.Context(), []int64{nodeID})
	if err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	if !s.publishNodesWithinScope(r, actor, nodes) {
		orpFailure(w, http.StatusForbidden, "无权回滚该节点所属中心")
		return
	}
	node := nodes[nodeID]
	if err := demoNodeReady(r.Context(), node); err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	actualCenter, _ := resourceNumber(node["centerId"])
	if actualCenter != centerID {
		orpFailure(w, 409, "节点中心已变化，不能使用该历史版本")
		return
	}
	baseline, err := s.lastPublishedNode(r.Context(), nodeID)
	if err != nil {
		orpFailure(w, 409, "节点没有可核对的活动发布基线")
		return
	}
	observed, err := observedDemoRelease(r.Context(), node)
	if err != nil || observed != baseline.ReleaseUUID {
		orpFailure(w, 409, "节点实际版本与发布基线不一致")
		return
	}
	snapshot, err := snapshotConfig(raw)
	if err != nil {
		orpFailure(w, 500, "历史快照损坏")
		return
	}
	releaseID := uuid.NewString()
	release, err := renderRelease(centerID, releaseID, snapshot.Resources, snapshot.Singletons)
	if err != nil {
		orpFailure(w, 409, "历史配置无法渲染: "+err.Error())
		return
	}
	target, err := targetForNode(node)
	if err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	nodeRelease, err := renderDemoReleaseForTarget(centerID, releaseID, snapshot.Resources, snapshot.Singletons, target.healthPort)
	if err != nil {
		orpFailure(w, 409, "无法为节点渲染回滚配置: "+err.Error())
		return
	}
	snapshot.Nodes = map[int64]orpDocument{nodeID: node}
	serialized, _ := json.Marshal(snapshot)
	targetIDs, _ := json.Marshal([]int64{nodeID})
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建回滚候选失败")
		return
	}
	defer tx.Rollback()
	var lockedID int64
	if err := tx.QueryRowContext(r.Context(), "SELECT id FROM orp_resource WHERE kind='nodes' AND id=? FOR UPDATE", nodeID).Scan(&lockedID); err != nil {
		orpFailure(w, 409, "节点已不存在")
		return
	}
	var active int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_publish_item WHERE node_id=? AND status IN ('queued','deploying','validating')", nodeID).Scan(&active); err != nil {
		orpFailure(w, 500, "读取节点下发状态失败")
		return
	}
	if active > 0 {
		orpFailure(w, 409, "节点正在下发")
		return
	}
	result, err := tx.ExecContext(r.Context(), "INSERT INTO orp_publish_candidate(release_uuid,center_id,actor,target_ids,snapshot,config_text,digest,status) VALUES(?,?,?,?,?,?,?,'frozen')", releaseID, centerID, actor, targetIDs, serialized, release.Config, release.Digest)
	if err != nil {
		orpFailure(w, 500, "保存回滚候选失败")
		return
	}
	candidateID, _ := result.LastInsertId()
	if err := orpAudit(r.Context(), tx, actor, "rollback", "publish", nodeID, raw, serialized, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录回滚审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交回滚候选失败")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	output, checkErr := validateDemoRelease(ctx, node, releaseID, nodeRelease)
	cancel()
	command := demoNginx + " -t -c " + demoConfigRoot + "/candidates/" + releaseID + "/nginx.conf"
	passed := checkErr == nil
	if !passed {
		output += "\n" + checkErr.Error()
	}
	var baselineRaw []byte
	if err := s.db.QueryRowContext(r.Context(), "SELECT snapshot FROM orp_publish_candidate WHERE release_uuid=?", baseline.ReleaseUUID).Scan(&baselineRaw); err != nil {
		orpFailure(w, 500, "读取当前发布快照失败")
		return
	}
	baselineSnapshot, err := snapshotConfig(baselineRaw)
	if err != nil {
		orpFailure(w, 500, "当前发布快照损坏")
		return
	}
	semanticJSON, _ := json.Marshal(semanticReleaseDiff(baselineSnapshot, snapshot))
	textLines, _, _ := publishTextDiff(baseline.Config, nodeRelease.Config)
	textJSON, _ := json.Marshal(textLines)
	planJSON, _ := json.Marshal(map[string]any{"nodeId": nodeID, "centerId": centerID, "action": "reload", "candidateDigest": nodeRelease.Digest, "baselineRelease": baseline.ReleaseUUID, "rollbackSource": sourceRelease})
	if _, err := s.db.ExecContext(r.Context(), "INSERT INTO orp_publish_precheck(candidate_id,node_id,passed,command_text,output_text,baseline_release_uuid,baseline_digest,semantic_diff,text_diff,publish_plan) VALUES(?,?,?,?,?,?,?,?,?,?)", candidateID, nodeID, passed, command, output, baseline.ReleaseUUID, baseline.Digest, semanticJSON, textJSON, planJSON); err != nil {
		orpFailure(w, 500, "保存回滚校验结果失败")
		return
	}
	if !passed {
		_, _ = s.db.ExecContext(r.Context(), "UPDATE orp_publish_candidate SET status='failed' WHERE id=?", candidateID)
		orpFailure(w, 409, "目标节点未通过回滚预检: "+output)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE orp_publish_candidate SET status='validated' WHERE id=?", candidateID); err != nil {
		orpFailure(w, 500, "保存回滚校验状态失败")
		return
	}
	tx, err = s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建回滚批次失败")
		return
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(r.Context(), "SELECT id FROM orp_resource WHERE kind='nodes' AND id=? FOR UPDATE", nodeID).Scan(&lockedID); err != nil {
		orpFailure(w, 409, "节点已不存在")
		return
	}
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_publish_item WHERE node_id=? AND status IN ('queued','deploying','validating')", nodeID).Scan(&active); err != nil {
		orpFailure(w, 500, "读取节点下发状态失败")
		return
	}
	if active > 0 {
		orpFailure(w, 409, "节点正在下发")
		return
	}
	currentBaseline, err := s.lastPublishedNode(r.Context(), nodeID)
	if err != nil || currentBaseline.ReleaseUUID != baseline.ReleaseUUID {
		orpFailure(w, 409, "节点发布基线已变化")
		return
	}
	observed, err = observedDemoRelease(r.Context(), node)
	if err != nil || observed != baseline.ReleaseUUID {
		orpFailure(w, 409, "节点实际版本已变化")
		return
	}
	result, err = tx.ExecContext(r.Context(), "INSERT INTO orp_publish_batch(actor,mode,phase) VALUES(?,'all','running')", actor)
	if err != nil {
		orpFailure(w, 500, "创建回滚批次失败")
		return
	}
	batchID, _ := result.LastInsertId()
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO orp_publish_item(batch_id,candidate_id,node_id,center_id,weight,status) VALUES(?,?,?,?,10,'queued')", batchID, candidateID, nodeID, centerID); err != nil {
		orpFailure(w, 500, "创建回滚节点任务失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交回滚批次失败")
		return
	}
	selected := map[int64]selectedPublish{centerID: {candidate: publishCandidate{ID: candidateID, ReleaseUUID: releaseID, CenterID: centerID, TargetIDs: []int64{nodeID}, Snapshot: serialized, Config: release.Config, Digest: release.Digest, Status: "validated"}, releases: map[int64]orpRelease{nodeID: nodeRelease}}}
	go s.runDemoBatch(batchID, []int64{nodeID}, nodes, selected, map[int64]int{nodeID: 0}, 1, false, 0)
	orpReply(w, 200, map[string]any{"batchId": batchID, "nodeId": nodeID, "nodeName": node["name"], "rollbackTo": sourceRelease})
}
