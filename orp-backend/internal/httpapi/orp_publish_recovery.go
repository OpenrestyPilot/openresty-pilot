package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"time"
)

const (
	recoveryQueuedMessage = "控制面重启，尚未开始的下发已停止"
)

type recoveryConfirmation struct {
	ItemID              int64  `json:"itemId"`
	ObservedReleaseUUID string `json:"observedReleaseUuid"`
}

type recoveryContinueRequest struct {
	Confirmations []recoveryConfirmation `json:"confirmations"`
}

type recoveryItem struct {
	id, candidateID, nodeID, centerID int64
	batchIndex, weight                int
	status, errorText, releaseUUID    string
	nodeJSON                          []byte
	baseline                          sql.NullString
}

type recoveredBatchPlan struct {
	ids           []int64
	nodes         map[int64]orpDocument
	selected      map[int64]selectedPublish
	indexes       map[int64]int
	batchCount    int
	canaryEnabled bool
	canaryWait    int
}

// orpPublishRecoveryContinue reconciles every recovery-marked item against a
// server-issued fixed probe before it changes any item state. Failed items
// already in flight are never requeued; untouched items can resume only when
// their active version still equals their successful precheck baseline.
func (s *Server) orpPublishRecoveryContinue(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "批次 ID 无效")
		return
	}
	if !s.publishBatchWithinScope(r, actor, id) {
		orpFailure(w, http.StatusForbidden, "无权操作该批次中的节点")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var request recoveryContinueRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		orpFailure(w, 400, "人工核对清单无效")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		orpFailure(w, 400, "人工核对清单只能包含一个 JSON 对象")
		return
	}

	var phase string
	if err := s.db.QueryRowContext(r.Context(), "SELECT phase FROM orp_publish_batch WHERE id=?", id).Scan(&phase); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			orpFailure(w, 404, "批次不存在")
		} else {
			orpFailure(w, 500, "读取恢复批次失败")
		}
		return
	}
	if phase != "recovery_required" {
		orpFailure(w, 409, "批次未处于人工核对恢复状态")
		return
	}
	items, err := s.recoveryItems(r.Context(), id)
	if err != nil {
		orpFailure(w, 500, "读取恢复目标失败")
		return
	}
	var persistedItemCount int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_publish_item WHERE batch_id=?", id).Scan(&persistedItemCount); err != nil {
		orpFailure(w, 500, "核对恢复目标数量失败")
		return
	}
	if persistedItemCount != len(items) {
		orpFailure(w, 409, "批次存在已删除节点或候选的历史项，无法安全续跑")
		return
	}
	recoverable := map[int64]recoveryItem{}
	for _, item := range items {
		if item.status == "failed" {
			recoverable[item.id] = item
		} else if item.status == "aborted" && item.errorText == recoveryQueuedMessage {
			recoverable[item.id] = item
		}
	}
	if len(recoverable) == 0 || len(request.Confirmations) != len(recoverable) {
		orpFailure(w, 400, "必须逐一提交所有待核对与未开始目标的人工观察版本")
		return
	}
	confirmations := make(map[int64]string, len(request.Confirmations))
	for _, confirmation := range request.Confirmations {
		if confirmation.ItemID < 1 || confirmation.ObservedReleaseUUID == "" || confirmations[confirmation.ItemID] != "" {
			orpFailure(w, 400, "人工核对目标无效或重复")
			return
		}
		if _, exists := recoverable[confirmation.ItemID]; !exists {
			orpFailure(w, 400, "人工核对目标与待恢复批次不匹配")
			return
		}
		confirmations[confirmation.ItemID] = confirmation.ObservedReleaseUUID
	}
	for itemID := range recoverable {
		if confirmations[itemID] == "" {
			orpFailure(w, 400, "人工核对清单缺少恢复目标")
			return
		}
	}

	verified := make(map[int64]string, len(recoverable))
	for itemID, item := range recoverable {
		snapshot, candidate, err := s.recoveryCandidate(r.Context(), item.candidateID)
		if err != nil || candidate.ReleaseUUID != item.releaseUUID || candidate.Status != "validated" {
			orpFailure(w, 409, "冻结候选已不可用，请重新预检")
			return
		}
		frozenNode := snapshot.Nodes[item.nodeID]
		var current orpDocument
		if err := json.Unmarshal(item.nodeJSON, &current); err != nil {
			orpFailure(w, 409, fmt.Sprintf("节点 %d 的目标身份已变化，请重新预检", item.nodeID))
			return
		}
		current["id"] = item.nodeID
		if frozenNode == nil || !recoveryNodeIdentityMatches(current, frozenNode) {
			orpFailure(w, 409, fmt.Sprintf("节点 %d 的目标身份已变化，请重新预检", item.nodeID))
			return
		}
		expected := confirmations[itemID]
		if expected != item.releaseUUID && (!item.baseline.Valid || expected != item.baseline.String) {
			orpFailure(w, 409, fmt.Sprintf("节点 %d 的人工观察版本必须是候选版本或冻结基线版本", item.nodeID))
			return
		}
		if item.status == "aborted" && expected != item.releaseUUID && (!item.baseline.Valid || item.baseline.String == "") {
			orpFailure(w, 409, fmt.Sprintf("节点 %d 缺少冻结基线版本，无法安全续跑", item.nodeID))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		var probeErr error
		if nodeHasAgent(current) && func() bool { _, targetErr := targetForNode(current); return targetErr != nil }() {
			_, probeErr = s.probeAgentRecoveryRelease(ctx, item.nodeID, item.candidateID, item.id, expected, actor)
		} else {
			var observed string
			observed, probeErr = observedDemoRelease(ctx, current)
			if probeErr == nil && observed != expected {
				probeErr = fmt.Errorf("fixed local probe returned %q, operator recorded %q", observed, expected)
			}
		}
		cancel()
		if probeErr != nil {
			orpFailure(w, 409, fmt.Sprintf("节点 %d 的当前版本未通过固定探针核对: %s", item.nodeID, probeErr.Error()))
			return
		}
		verified[itemID] = expected
	}

	plan, err := s.buildRecoveredBatchPlan(r.Context(), id)
	if err != nil {
		orpFailure(w, 409, "无法从冻结候选重建发布计划: "+err.Error())
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "提交恢复操作失败")
		return
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(r.Context(), "SELECT phase FROM orp_publish_batch WHERE id=? FOR UPDATE", id).Scan(&phase); err != nil || phase != "recovery_required" {
		orpFailure(w, 409, "批次状态已变化，请重新读取")
		return
	}
	for itemID := range verified {
		item := recoverable[itemID]
		var currentRaw []byte
		if err := tx.QueryRowContext(r.Context(), "SELECT document FROM orp_resource WHERE kind='nodes' AND id=? FOR UPDATE", item.nodeID).Scan(&currentRaw); err != nil {
			orpFailure(w, 409, "恢复目标已不存在")
			return
		}
		var current orpDocument
		if err := json.Unmarshal(currentRaw, &current); err != nil {
			orpFailure(w, 409, "恢复目标信息无效")
			return
		}
		current["id"] = item.nodeID
		frozen, _, candidateErr := s.recoveryCandidate(r.Context(), item.candidateID)
		if candidateErr != nil || !recoveryNodeIdentityMatches(current, frozen.Nodes[item.nodeID]) {
			orpFailure(w, 409, "恢复目标身份已变化，请重新预检")
			return
		}
		var otherActive int
		if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_publish_item WHERE node_id=? AND batch_id<>? AND status IN ('queued','deploying','validating')", item.nodeID, id).Scan(&otherActive); err != nil {
			orpFailure(w, 500, "检查其他批次占用失败")
			return
		}
		if otherActive != 0 {
			orpFailure(w, 409, "节点已加入其他发布批次，无法安全续跑")
			return
		}
	}
	var resumed, applied, skipped int
	for itemID, observed := range verified {
		item := recoverable[itemID]
		if observed == item.releaseUUID {
			if _, err := tx.ExecContext(r.Context(), "UPDATE orp_publish_item SET status='success',error_text='人工核对并由固定探针确认候选版本已生效',finished_at=NOW(6) WHERE id=? AND batch_id=? AND status IN ('failed','aborted')", itemID, id); err != nil {
				orpFailure(w, 500, "保存人工核对结果失败")
				return
			}
			if _, err := tx.ExecContext(r.Context(), "UPDATE orp_resource SET document=JSON_SET(document,'$.activeVersion',?) WHERE kind='nodes' AND id=?", item.releaseUUID, item.nodeID); err != nil {
				orpFailure(w, 500, "更新节点活动版本失败")
				return
			}
			applied++
			continue
		}
		if item.status == "aborted" {
			if _, err := tx.ExecContext(r.Context(), "UPDATE orp_publish_item SET status='queued',error_text=NULL,finished_at=NULL,previous_release_uuid=? WHERE id=? AND batch_id=? AND status='aborted'", item.baseline.String, itemID, id); err != nil {
				orpFailure(w, 500, "恢复未开始目标失败")
				return
			}
			resumed++
		} else {
			if _, err := tx.ExecContext(r.Context(), "UPDATE orp_publish_item SET status='aborted',error_text='人工核对确认仍为冻结基线版本；不重放已开始的任务',finished_at=NOW(6) WHERE id=? AND batch_id=? AND status='failed'", itemID, id); err != nil {
				orpFailure(w, 500, "保存失败目标核对结果失败")
				return
			}
			skipped++
		}
	}
	if err := orpAudit(r.Context(), tx, actor, "publish_recovery_continue", "publish", id, nil, adminAuditDocument(map[string]any{"confirmations": request.Confirmations, "resumedCount": resumed, "alreadyAppliedCount": applied, "skippedFailedCount": skipped}), orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录批次恢复审计失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_publish_batch SET phase='running',finished_at=NULL WHERE id=? AND phase='recovery_required'", id); err != nil {
		orpFailure(w, 500, "恢复批次失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交批次恢复失败")
		return
	}
	_ = syncConfigurationCandidatePublishState(r.Context(), s.db, id)
	go s.runDemoBatch(id, plan.ids, plan.nodes, plan.selected, plan.indexes, plan.batchCount, plan.canaryEnabled, plan.canaryWait)
	orpReply(w, 200, map[string]any{"id": id, "phase": "running", "resumedCount": resumed, "alreadyAppliedCount": applied, "skippedFailedCount": skipped})
}

func (s *Server) recoveryItems(ctx context.Context, batchID int64) ([]recoveryItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.candidate_id,i.node_id,i.center_id,i.batch_index,i.weight,i.status,
		COALESCE(i.error_text,''),c.release_uuid,n.document,p.baseline_release_uuid
		FROM orp_publish_item i JOIN orp_publish_candidate c ON c.id=i.candidate_id
		JOIN orp_resource n ON n.id=i.node_id AND n.kind='nodes'
		LEFT JOIN orp_publish_precheck p ON p.id=(SELECT MAX(p2.id) FROM orp_publish_precheck p2 WHERE p2.candidate_id=i.candidate_id AND p2.node_id=i.node_id AND p2.passed=TRUE)
		WHERE i.batch_id=? ORDER BY i.batch_index,i.id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []recoveryItem{}
	for rows.Next() {
		var item recoveryItem
		if err := rows.Scan(&item.id, &item.candidateID, &item.nodeID, &item.centerID, &item.batchIndex, &item.weight, &item.status, &item.errorText, &item.releaseUUID, &item.nodeJSON, &item.baseline); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) recoveryCandidate(ctx context.Context, candidateID int64) (releaseSnapshot, publishCandidate, error) {
	var snapshot releaseSnapshot
	var candidate publishCandidate
	var targetJSON []byte
	if err := s.db.QueryRowContext(ctx, `SELECT id,release_uuid,center_id,target_ids,snapshot,config_text,digest,status
		FROM orp_publish_candidate WHERE id=?`, candidateID).Scan(&candidate.ID, &candidate.ReleaseUUID, &candidate.CenterID, &targetJSON, &candidate.Snapshot, &candidate.Config, &candidate.Digest, &candidate.Status); err != nil {
		return snapshot, candidate, err
	}
	if err := json.Unmarshal(targetJSON, &candidate.TargetIDs); err != nil {
		return snapshot, candidate, err
	}
	var err error
	snapshot, err = snapshotConfig(candidate.Snapshot)
	return snapshot, candidate, err
}

func recoveryNodeIdentityMatches(current, frozen orpDocument) bool {
	if current == nil || frozen == nil {
		return false
	}
	currentID, currentErr := resourceNumber(current["id"])
	frozenID, frozenErr := resourceNumber(frozen["id"])
	if currentErr != nil || frozenErr != nil || currentID != frozenID {
		return false
	}
	for _, key := range []string{"centerId", "host", "name", "controlEndpoint", "agentId", "agentCertificateFingerprint", "agentProfile", "directives"} {
		if !reflect.DeepEqual(current[key], frozen[key]) {
			return false
		}
	}
	if nodeHasAgent(frozen) {
		return toString(current["agentId"]) != "" && toString(current["agentCertificateFingerprint"]) != ""
	}
	currentTarget, currentErr := targetForNode(current)
	frozenTarget, frozenErr := targetForNode(frozen)
	return currentErr == nil && frozenErr == nil && currentTarget == frozenTarget
}

func (s *Server) buildRecoveredBatchPlan(ctx context.Context, batchID int64) (recoveredBatchPlan, error) {
	plan := recoveredBatchPlan{nodes: map[int64]orpDocument{}, selected: map[int64]selectedPublish{}, indexes: map[int64]int{}}
	if err := s.db.QueryRowContext(ctx, "SELECT canary_enabled,canary_wait_seconds FROM orp_publish_batch WHERE id=?", batchID).Scan(&plan.canaryEnabled, &plan.canaryWait); err != nil {
		return plan, err
	}
	items, err := s.recoveryItems(ctx, batchID)
	if err != nil {
		return plan, err
	}
	validationTx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return plan, err
	}
	defer validationTx.Rollback()
	verifiedCandidates := map[int64]bool{}
	maxIndex := 0
	for _, item := range items {
		plan.ids = append(plan.ids, item.nodeID)
		plan.indexes[item.nodeID] = item.batchIndex
		if item.batchIndex > maxIndex {
			maxIndex = item.batchIndex
		}
		var current orpDocument
		if err := json.Unmarshal(item.nodeJSON, &current); err != nil {
			return plan, err
		}
		current["id"] = item.nodeID
		snapshot, candidate, err := s.recoveryCandidate(ctx, item.candidateID)
		if err != nil || candidate.Status != "validated" || snapshot.Nodes[item.nodeID] == nil || !recoveryNodeIdentityMatches(current, snapshot.Nodes[item.nodeID]) {
			return plan, errors.New("frozen candidate or node identity changed")
		}
		containsNode := false
		for _, targetID := range candidate.TargetIDs {
			containsNode = containsNode || targetID == item.nodeID
		}
		if !containsNode {
			return plan, errors.New("batch item is not in its frozen candidate target list")
		}
		if !verifiedCandidates[candidate.ID] {
			currentResources, currentSingletons, err := loadReleaseInput(validationTx, candidate.CenterID)
			if err != nil {
				return plan, err
			}
			currentRelease, err := renderRelease(candidate.CenterID, candidate.ReleaseUUID, currentResources, currentSingletons)
			if err != nil || currentRelease.Digest != candidate.Digest {
				return plan, errors.New("center configuration changed after candidate validation")
			}
			verifiedCandidates[candidate.ID] = true
		}
		centerID := candidate.CenterID
		if existing, ok := plan.selected[centerID]; !ok || existing.candidate.ID != candidate.ID {
			plan.selected[centerID] = selectedPublish{candidate: candidate, releases: map[int64]orpRelease{}}
		}
		resources, singletons := snapshot.Resources, snapshot.Singletons
		var release orpRelease
		if nodeHasAgent(snapshot.Nodes[item.nodeID]) {
			if _, targetErr := targetForNode(snapshot.Nodes[item.nodeID]); targetErr != nil {
				release, err = renderReleaseForTarget(centerID, candidate.ReleaseUUID, resources, singletons, agentProbePort)
			} else {
				target, targetErr := targetForNode(snapshot.Nodes[item.nodeID])
				if targetErr != nil {
					return plan, targetErr
				}
				release, err = renderDemoReleaseForTarget(centerID, candidate.ReleaseUUID, resources, singletons, target.healthPort)
			}
		} else {
			target, targetErr := targetForNode(snapshot.Nodes[item.nodeID])
			if targetErr != nil {
				return plan, targetErr
			}
			release, err = renderDemoReleaseForTarget(centerID, candidate.ReleaseUUID, resources, singletons, target.healthPort)
		}
		if err != nil {
			return plan, err
		}
		selected := plan.selected[centerID]
		selected.releases[item.nodeID] = release
		plan.selected[centerID] = selected
		current["id"] = item.nodeID
		plan.nodes[item.nodeID] = current
	}
	sort.Slice(plan.ids, func(i, j int) bool {
		left, right := plan.ids[i], plan.ids[j]
		if plan.indexes[left] == plan.indexes[right] {
			return left < right
		}
		return plan.indexes[left] < plan.indexes[right]
	})
	plan.batchCount = maxIndex + 1
	return plan, nil
}
