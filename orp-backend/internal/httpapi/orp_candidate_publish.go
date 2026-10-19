package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func parseCandidatePublishStrategy(value any) (map[string]any, error) {
	strategy := map[string]any{"mode": "all"}
	if value == nil {
		return strategy, nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("publishStrategy 格式无效")
	}
	mode := toString(raw["mode"])
	if mode == "" {
		mode = "all"
	}
	if mode != "all" && mode != "weighted" {
		return nil, errors.New("发布模式无效")
	}
	strategy["mode"] = mode
	if mode == "weighted" {
		count, err := resourceNumber(raw["batchCount"])
		if err != nil || count < 1 || count > 5 {
			return nil, errors.New("分批数必须介于 1 到 5")
		}
		strategy["batchCount"] = count
		canary, _ := raw["canary"].(map[string]any)
		enabled, _ := canary["enabled"].(bool)
		strategy["canary"] = map[string]any{"enabled": enabled}
		if enabled {
			seconds, err := resourceNumber(canary["waitSeconds"])
			if err != nil || seconds < 1 || seconds > 300 {
				return nil, errors.New("金丝雀观察时间必须为 1 到 300 秒")
			}
			strategy["canary"].(map[string]any)["waitSeconds"] = seconds
		}
	}
	return strategy, nil
}

func (s *Server) orpCandidatePublish(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:publish")
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "候选 ID 无效")
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	strategy, err := parseCandidatePublishStrategy(body["strategy"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	result, err := s.createConfigurationCandidateBatch(r.Context(), id, actor, strategy, false)
	if err != nil {
		status := http.StatusConflict
		if strings.Contains(err.Error(), "无权") {
			status = http.StatusForbidden
		}
		orpFailure(w, status, err.Error())
		return
	}
	orpReply(w, 200, result)
}

type candidatePublishBaseline struct {
	releaseUUID string
	digest      string
	candidate   publishCandidate
}

// createConfigurationCandidateBatch turns one validated immutable V44
// candidate into the legacy publish-candidate record consumed by the existing
// durable Agent/Quickstart batch runner. The source and target IDs remain
// explicitly linked and are never treated as interchangeable.
func (s *Server) createConfigurationCandidateBatch(ctx context.Context, sourceID int64, actor string, strategy map[string]any, automatic bool) (map[string]any, error) {
	var centerID int64
	var candidateUUID, digest, publishState string
	var targetRaw, snapshotRaw, storedStrategy []byte
	var storedConfig string
	var archived sql.NullTime
	var priorBatch sql.NullInt64
	var publishOnSuccess bool
	err := s.db.QueryRowContext(ctx, `SELECT center_id,candidate_uuid,target_ids,snapshot,config_text,digest,archived_at,
publish_on_validation_success,publish_state,publish_batch_id,publish_strategy
FROM orp_configuration_candidate WHERE id=?`, sourceID).Scan(&centerID, &candidateUUID, &targetRaw, &snapshotRaw, &storedConfig, &digest, &archived, &publishOnSuccess, &publishState, &priorBatch, &storedStrategy)
	if err != nil {
		return nil, errors.New("候选不存在")
	}
	if automatic && !publishOnSuccess {
		return nil, errors.New("候选未启用校验通过后自动发布")
	}
	if archived.Valid {
		return nil, errors.New("已归档候选不能发布")
	}
	if priorBatch.Valid {
		return nil, fmt.Errorf("候选已关联发布批次 %d", priorBatch.Int64)
	}
	_ = publishState
	var targetIDs []int64
	if err := json.Unmarshal(targetRaw, &targetIDs); err != nil || len(targetIDs) == 0 {
		return nil, errors.New("候选冻结目标列表无效")
	}
	sort.Slice(targetIDs, func(i, j int) bool { return targetIDs[i] < targetIDs[j] })
	snapshot, err := snapshotConfig(snapshotRaw)
	if err != nil {
		return nil, errors.New("候选冻结快照无效")
	}
	if len(snapshot.Nodes) != len(targetIDs) {
		return nil, errors.New("候选冻结节点集合不完整")
	}
	roles, err := s.candidateActorRoles(ctx, actor)
	if err != nil || !accountHasCenterPermission(roles, "orp:publish", strconv.FormatInt(centerID, 10)) {
		return nil, errors.New("无权发布该中心候选")
	}
	if automatic && len(strategy) == 0 {
		if err := json.Unmarshal(storedStrategy, &strategy); err != nil || len(strategy) == 0 {
			strategy = map[string]any{"mode": "all"}
		}
	}
	strategy, err = parseCandidatePublishStrategy(strategy)
	if err != nil {
		return nil, err
	}
	mode := toString(strategy["mode"])
	batchCount := 1
	canaryEnabled, waitSeconds := false, 0
	if mode == "weighted" {
		count, _ := resourceNumber(strategy["batchCount"])
		if count < 1 || count > 5 || count > int64(len(targetIDs)) {
			return nil, errors.New("分批数不能超过冻结目标节点数")
		}
		batchCount = int(count)
		canary, _ := strategy["canary"].(map[string]any)
		canaryEnabled, _ = canary["enabled"].(bool)
		if canaryEnabled {
			seconds, _ := resourceNumber(canary["waitSeconds"])
			waitSeconds = int(seconds)
		}
	}
	var attemptID int64
	var attemptState string
	if err := s.db.QueryRowContext(ctx, "SELECT id,state FROM orp_validation_attempt WHERE candidate_id=? ORDER BY attempt_no DESC LIMIT 1", sourceID).Scan(&attemptID, &attemptState); err != nil {
		return nil, errors.New("候选尚无校验尝试")
	}
	if attemptState != "completed" {
		return nil, errors.New("只有最新校验尝试完成后才能发布")
	}
	jobs, err := s.db.QueryContext(ctx, "SELECT node_id,state FROM orp_validation_job WHERE attempt_id=? ORDER BY node_id", attemptID)
	if err != nil {
		return nil, errors.New("读取冻结目标校验结果失败")
	}
	jobStates := map[int64]string{}
	for jobs.Next() {
		var nodeID int64
		var state string
		if err := jobs.Scan(&nodeID, &state); err != nil {
			jobs.Close()
			return nil, err
		}
		jobStates[nodeID] = state
	}
	jobsErr := jobs.Err()
	jobs.Close()
	if jobsErr != nil || len(jobStates) != len(targetIDs) {
		return nil, errors.New("校验结果与冻结目标不一致")
	}
	for _, nodeID := range targetIDs {
		if jobStates[nodeID] != "passed" {
			return nil, fmt.Errorf("节点 %d 校验未通过，拒绝发布", nodeID)
		}
	}

	// Re-render the live center and compare the exact frozen node documents.
	readTx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	resources, singletons, err := loadReleaseInput(readTx, centerID)
	if err != nil {
		readTx.Rollback()
		return nil, errors.New("无法读取当前中心配置")
	}
	current, err := renderRelease(centerID, candidateUUID, resources, singletons)
	if err != nil || current.Digest != digest || current.Config != storedConfig {
		readTx.Rollback()
		return nil, errors.New("中心配置已变化，请重新创建并校验候选")
	}
	liveNodes := map[int64]orpDocument{}
	baselines := map[int64]candidatePublishBaseline{}
	for _, nodeID := range targetIDs {
		var raw []byte
		if err := readTx.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", nodeID).Scan(&raw); err != nil {
			readTx.Rollback()
			return nil, fmt.Errorf("节点 %d 已不存在", nodeID)
		}
		var node orpDocument
		if json.Unmarshal(raw, &node) != nil {
			readTx.Rollback()
			return nil, fmt.Errorf("节点 %d 数据损坏", nodeID)
		}
		frozen := snapshot.Nodes[nodeID]
		if frozen == nil {
			readTx.Rollback()
			return nil, fmt.Errorf("冻结快照缺少节点 %d", nodeID)
		}
		freshRaw, _ := json.Marshal(node)
		frozenCopy := orpDocument{}
		frozenRaw, _ := json.Marshal(frozen)
		_ = json.Unmarshal(frozenRaw, &frozenCopy)
		delete(frozenCopy, "id")
		frozenRaw, _ = json.Marshal(frozenCopy)
		if string(freshRaw) != string(frozenRaw) {
			readTx.Rollback()
			return nil, fmt.Errorf("节点 %d 身份或属性已变化，请重新校验", nodeID)
		}
		node["id"] = nodeID
		liveNodes[nodeID] = node
		var baselineVersion, baselineDigest sql.NullString
		if err := readTx.QueryRowContext(ctx, "SELECT baseline_version,baseline_digest FROM orp_configuration_candidate_diff WHERE candidate_id=? AND node_id=?", sourceID, nodeID).Scan(&baselineVersion, &baselineDigest); err != nil {
			readTx.Rollback()
			return nil, fmt.Errorf("节点 %d 缺少冻结基线证据", nodeID)
		}
		last, lastErr := s.lastPublishedNode(ctx, nodeID)
		if errors.Is(lastErr, sql.ErrNoRows) {
			if baselineVersion.Valid || baselineDigest.Valid {
				readTx.Rollback()
				return nil, fmt.Errorf("节点 %d 发布基线已变化", nodeID)
			}
		} else if lastErr != nil || !baselineVersion.Valid || !baselineDigest.Valid || last.ReleaseUUID != baselineVersion.String || last.Digest != baselineDigest.String {
			readTx.Rollback()
			return nil, fmt.Errorf("节点 %d 发布基线已变化，请重新校验", nodeID)
		} else {
			var base candidatePublishBaseline
			base.releaseUUID, base.digest = last.ReleaseUUID, last.Digest
			if _, err := targetForNode(node); err == nil {
				ctxProbe, cancel := context.WithTimeout(ctx, 3*time.Second)
				observed, probeErr := observedDemoRelease(ctxProbe, node)
				cancel()
				if probeErr != nil || observed != last.ReleaseUUID {
					readTx.Rollback()
					return nil, fmt.Errorf("节点 %d 实际版本与冻结发布基线不一致", nodeID)
				}
			} else if nodeHasAgent(node) {
				var baseTargets []byte
				if err := s.db.QueryRowContext(ctx, "SELECT id,release_uuid,center_id,target_ids,snapshot,config_text,digest,status FROM orp_publish_candidate WHERE release_uuid=?", last.ReleaseUUID).Scan(&base.candidate.ID, &base.candidate.ReleaseUUID, &base.candidate.CenterID, &baseTargets, &base.candidate.Snapshot, &base.candidate.Config, &base.candidate.Digest, &base.candidate.Status); err != nil {
					readTx.Rollback()
					return nil, fmt.Errorf("节点 %d 的 Agent 发布基线制品不存在", nodeID)
				}
				if json.Unmarshal(baseTargets, &base.candidate.TargetIDs) != nil {
					readTx.Rollback()
					return nil, fmt.Errorf("节点 %d 的 Agent 发布基线目标无效", nodeID)
				}
				ctxProbe, cancel := context.WithTimeout(ctx, 3*time.Minute)
				_, probeErr := s.probeAgentCandidate(ctxProbe, nodeID, base.candidate, actor)
				cancel()
				if probeErr != nil {
					readTx.Rollback()
					return nil, fmt.Errorf("节点 %d Agent 基线探针未通过", nodeID)
				}
			} else {
				readTx.Rollback()
				return nil, fmt.Errorf("节点 %d 缺少可用的活动版本探针", nodeID)
			}
			baselines[nodeID] = base
		}
		if _, targetErr := targetForNode(node); targetErr != nil && !nodeHasAgent(node) {
			readTx.Rollback()
			return nil, fmt.Errorf("节点 %d 没有支持的固定发布通道", nodeID)
		}
	}
	if err := readTx.Commit(); err != nil {
		return nil, err
	}

	selected := map[int64]selectedPublish{}
	releases := make(map[int64]orpRelease, len(targetIDs))
	for _, nodeID := range targetIDs {
		node := snapshot.Nodes[nodeID]
		node["id"] = nodeID
		var release orpRelease
		if target, targetErr := targetForNode(node); targetErr == nil {
			release, err = renderDemoReleaseForTarget(centerID, candidateUUID, snapshot.Resources, snapshot.Singletons, target.healthPort)
		} else {
			release, err = renderReleaseForTarget(centerID, candidateUUID, snapshot.Resources, snapshot.Singletons, agentProbePort)
		}
		if err != nil {
			return nil, fmt.Errorf("节点 %d 发布制品渲染失败: %w", nodeID, err)
		}
		release.Digest = digest
		releases[nodeID] = release
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var lockedBatch sql.NullInt64
	var lockedState string
	var lockedArchived sql.NullTime
	if err := tx.QueryRowContext(ctx, "SELECT publish_batch_id,publish_state,archived_at FROM orp_configuration_candidate WHERE id=? FOR UPDATE", sourceID).Scan(&lockedBatch, &lockedState, &lockedArchived); err != nil {
		return nil, errors.New("候选不存在")
	}
	if lockedArchived.Valid {
		return nil, errors.New("候选已归档，拒绝发布")
	}
	if lockedBatch.Valid {
		return nil, fmt.Errorf("候选已关联发布批次 %d", lockedBatch.Int64)
	}
	_ = publishOnSuccess
	// Lock target rows, then recheck active batches and their frozen identities.
	for _, nodeID := range targetIDs {
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=? FOR UPDATE", nodeID).Scan(&raw); err != nil {
			return nil, fmt.Errorf("节点 %d 已不存在", nodeID)
		}
		var node orpDocument
		if json.Unmarshal(raw, &node) != nil {
			return nil, fmt.Errorf("节点 %d 数据损坏", nodeID)
		}
		freshRaw, _ := json.Marshal(node)
		frozenCopy := orpDocument{}
		frozenRaw, _ := json.Marshal(snapshot.Nodes[nodeID])
		_ = json.Unmarshal(frozenRaw, &frozenCopy)
		delete(frozenCopy, "id")
		frozenRaw, _ = json.Marshal(frozenCopy)
		if string(freshRaw) != string(frozenRaw) {
			return nil, fmt.Errorf("节点 %d 已变化，请重新校验", nodeID)
		}
		var active int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM orp_publish_item WHERE node_id=? AND status IN ('queued','deploying','validating')", nodeID).Scan(&active); err != nil {
			return nil, err
		}
		if active > 0 {
			return nil, fmt.Errorf("节点 %d 正在执行其他发布批次", nodeID)
		}
	}
	// Recheck the last successful history while holding the target locks.
	for _, nodeID := range targetIDs {
		var actualVersion, actualDigest sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT c.release_uuid,c.digest FROM orp_publish_item i JOIN orp_publish_candidate c ON c.id=i.candidate_id
WHERE i.node_id=? AND i.status='success' ORDER BY i.finished_at DESC,i.id DESC LIMIT 1 FOR UPDATE`, nodeID).Scan(&actualVersion, &actualDigest)
		base := baselines[nodeID]
		if errors.Is(err, sql.ErrNoRows) {
			if base.releaseUUID != "" {
				return nil, fmt.Errorf("节点 %d 发布基线在探针后变化", nodeID)
			}
		} else if err != nil || actualVersion.String != base.releaseUUID || actualDigest.String != base.digest {
			return nil, fmt.Errorf("节点 %d 发布基线在探针后变化", nodeID)
		}
	}
	publishCandidateID := int64(0)
	result, err := tx.ExecContext(ctx, `INSERT INTO orp_publish_candidate(release_uuid,center_id,actor,target_ids,snapshot,config_text,digest,status,configuration_candidate_id)
VALUES(?,?,?,?,?,?,?,'validated',?)`, candidateUUID, centerID, actor, targetRaw, snapshotRaw, storedConfig, digest, sourceID)
	if err != nil {
		return nil, errors.New("保存发布候选关联失败")
	}
	publishCandidateID, err = result.LastInsertId()
	if err != nil {
		return nil, err
	}
	for _, nodeID := range targetIDs {
		base := baselines[nodeID]
		command, output := "validated immutable configuration candidate", "all frozen targets passed validation"
		var validationCommand, validationOutput sql.NullString
		var validationState string
		if err := tx.QueryRowContext(ctx, "SELECT command_text,output_text,state FROM orp_validation_job WHERE attempt_id=? AND node_id=?", attemptID, nodeID).Scan(&validationCommand, &validationOutput, &validationState); err != nil || validationState != "passed" {
			return nil, fmt.Errorf("节点 %d 校验记录发生变化", nodeID)
		}
		if validationCommand.Valid {
			command = clipCandidateEvidence(validationCommand.String, 500)
		}
		if validationOutput.Valid {
			output = clipCandidateEvidence(validationOutput.String, 60000)
		}
		var baselineVersion, baselineDigest any
		if base.releaseUUID != "" {
			baselineVersion, baselineDigest = base.releaseUUID, base.digest
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO orp_publish_precheck(candidate_id,node_id,passed,command_text,output_text,baseline_release_uuid,baseline_digest)
VALUES(?,?,TRUE,?,?,?,?)`, publishCandidateID, nodeID, command, output, baselineVersion, baselineDigest); err != nil {
			return nil, err
		}
	}
	batchIndex := publishBatchIndexes(targetIDs, liveNodes, batchCount)
	batchResult, err := tx.ExecContext(ctx, "INSERT INTO orp_publish_batch(actor,mode,phase,canary_enabled,canary_wait_seconds) VALUES(?,?,'running',?,?)", actor, mode, canaryEnabled, waitSeconds)
	if err != nil {
		return nil, errors.New("创建发布批次失败")
	}
	batchID, err := batchResult.LastInsertId()
	if err != nil {
		return nil, err
	}
	for _, nodeID := range targetIDs {
		weight, weightErr := resourceNumber(liveNodes[nodeID]["weight"])
		if weightErr != nil || weight < 1 {
			weight = 10
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO orp_publish_item(batch_id,candidate_id,node_id,center_id,batch_index,weight,status) VALUES(?,?,?,?,?,?,'queued')", batchID, publishCandidateID, nodeID, centerID, batchIndex[nodeID], weight); err != nil {
			return nil, errors.New("创建节点发布任务失败")
		}
	}
	publishState = "started"
	if _, err := tx.ExecContext(ctx, "UPDATE orp_configuration_candidate SET publish_state=?,publish_batch_id=?,publish_error=NULL,publish_claim_until=NULL WHERE id=?", publishState, batchID, sourceID); err != nil {
		return nil, err
	}
	before, _ := json.Marshal(map[string]any{"publishState": lockedState})
	after, _ := json.Marshal(map[string]any{"sourceCandidateId": sourceID, "publishCandidateId": publishCandidateID, "batchId": batchID, "targetIds": targetIDs, "mode": mode, "canaryEnabled": canaryEnabled, "automatic": automatic})
	if err := orpAudit(ctx, tx, actor, "publish-candidate", "configuration-candidate", sourceID, before, after, ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	selected[centerID] = selectedPublish{candidate: publishCandidate{ID: publishCandidateID, ReleaseUUID: candidateUUID, CenterID: centerID, TargetIDs: targetIDs, Snapshot: snapshotRaw, Config: storedConfig, Digest: digest, Status: "validated"}, releases: releases}
	go s.runDemoBatch(batchID, targetIDs, liveNodes, selected, batchIndex, batchCount, canaryEnabled, waitSeconds)
	return map[string]any{"candidateId": sourceID, "batchId": batchID, "total": len(targetIDs), "mode": mode, "phase": "running"}, nil
}

func clipCandidateEvidence(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	return strings.ToValidUTF8(value, "")
}

func syncConfigurationCandidatePublishState(ctx context.Context, db *sql.DB, batchID int64) error {
	rows, err := db.QueryContext(ctx, `SELECT pc.configuration_candidate_id,b.phase,
SUM(i.status='success'),SUM(i.status='failed'),SUM(i.status='aborted'),
COALESCE(GROUP_CONCAT(NULLIF(i.error_text,'') ORDER BY i.id SEPARATOR '; '),'')
FROM orp_publish_batch b JOIN orp_publish_item i ON i.batch_id=b.id
JOIN orp_publish_candidate pc ON pc.id=i.candidate_id
WHERE b.id=? AND pc.configuration_candidate_id IS NOT NULL
GROUP BY pc.configuration_candidate_id,b.phase`, batchID)
	if err != nil {
		return err
	}
	type stateRow struct {
		candidateID int64
		phase       string
		success     int
		failed      int
		aborted     int
		errorText   string
	}
	items := []stateRow{}
	for rows.Next() {
		var item stateRow
		if err := rows.Scan(&item.candidateID, &item.phase, &item.success, &item.failed, &item.aborted, &item.errorText); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range items {
		state, message := "started", ""
		switch item.phase {
		case "recovery_required":
			state, message = "recovery_required", "批次中断，需人工核对节点活动版本后继续"
		case "aborted":
			state, message = "aborted", "发布批次已由操作员中止"
		case "done":
			if item.failed > 0 || item.aborted > 0 {
				state, message = "failed", item.errorText
				if message == "" {
					message = "部分目标发布失败或被中止"
				}
			} else if item.success > 0 {
				state = "succeeded"
			} else {
				state, message = "failed", "批次结束但没有成功发布目标"
			}
		}
		message = clipCandidateEvidence(message, 4000)
		var errorValue any
		if message != "" {
			errorValue = message
		}
		if _, err := db.ExecContext(ctx, "UPDATE orp_configuration_candidate SET publish_state=?,publish_error=?,publish_claim_until=NULL WHERE id=? AND publish_batch_id=?", state, errorValue, item.candidateID, batchID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) candidateActorRoles(ctx context.Context, actor string) ([]accountRole, error) {
	var roleIDsJSON []byte
	if err := s.db.QueryRowContext(ctx, "SELECT role_ids FROM orp_account WHERE username=? AND status='enabled'", actor).Scan(&roleIDsJSON); err != nil {
		return nil, err
	}
	var ids []int64
	if err := json.Unmarshal(roleIDsJSON, &ids); err != nil {
		return nil, err
	}
	roles := []accountRole{}
	for _, id := range ids {
		var role accountRole
		var permissionsRaw, centersRaw []byte
		err := s.db.QueryRowContext(ctx, "SELECT code,permission_keys,center_ids FROM orp_role WHERE id=? AND status='enabled'", id).Scan(&role.Code, &permissionsRaw, &centersRaw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(permissionsRaw, &role.Permissions) != nil || json.Unmarshal(centersRaw, &role.CenterIDs) != nil {
			return nil, errors.New("账号权限配置损坏")
		}
		role.Permissions = normalizePermissionKeys(role.Permissions)
		roles = append(roles, role)
	}
	return roles, nil
}

func (s *Server) autoPublishValidatedCandidate(candidateID, attemptID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var actor, state string
	var enabled bool
	var latestAttempt int64
	var strategyRaw []byte
	err := s.db.QueryRowContext(ctx, `SELECT c.actor,c.publish_on_validation_success,c.publish_state,c.publish_strategy,
(SELECT MAX(a.id) FROM orp_validation_attempt a WHERE a.candidate_id=c.id)
FROM orp_configuration_candidate c WHERE c.id=?`, candidateID).Scan(&actor, &enabled, &state, &strategyRaw, &latestAttempt)
	if err != nil || !enabled || latestAttempt != attemptID || state != "waiting_validation" {
		return
	}
	claimed, claimErr := s.db.ExecContext(ctx, "UPDATE orp_configuration_candidate SET publish_state='preparing',publish_claim_until=DATE_ADD(CURRENT_TIMESTAMP(6),INTERVAL 15 MINUTE) WHERE id=? AND publish_state='waiting_validation' AND publish_batch_id IS NULL AND (publish_claim_until IS NULL OR publish_claim_until<CURRENT_TIMESTAMP(6))", candidateID)
	if claimErr != nil {
		return
	}
	claimedRows, _ := claimed.RowsAffected()
	if claimedRows == 0 {
		return
	}
	var strategy map[string]any
	_ = json.Unmarshal(strategyRaw, &strategy)
	if _, err := s.createConfigurationCandidateBatch(ctx, candidateID, actor, strategy, true); err != nil {
		message := err.Error()
		if len(message) > 1000 {
			message = message[:1000]
		}
		_, _ = s.db.ExecContext(ctx, "UPDATE orp_configuration_candidate SET publish_state='failed',publish_error=?,publish_claim_until=NULL WHERE id=? AND publish_batch_id IS NULL AND publish_state='preparing'", message, candidateID)
		if auditTx, txErr := s.db.BeginTx(ctx, nil); txErr == nil {
			_ = orpAudit(ctx, auditTx, actor, "auto-publish-blocked", "configuration-candidate", candidateID, nil, adminAuditDocument(map[string]any{"attemptId": attemptID, "error": message}), "")
			_ = auditTx.Commit()
		}
	}
}

// Recover successful opt-in attempts after a control-plane restart. A failed
// start is recorded once and requires an operator-triggered retry.
func (s *Server) recoverAutoCandidatePublishes(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE orp_configuration_candidate SET publish_state='waiting_validation',publish_claim_until=NULL
WHERE publish_state='preparing' AND publish_batch_id IS NULL AND publish_claim_until<CURRENT_TIMESTAMP(6)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE orp_configuration_candidate c
JOIN orp_validation_attempt a ON a.id=(SELECT MAX(a2.id) FROM orp_validation_attempt a2 WHERE a2.candidate_id=c.id)
SET c.publish_state='failed',c.publish_error='校验未全部通过或结果未知，不会自动发布'
WHERE c.publish_on_validation_success=TRUE AND c.publish_state='waiting_validation' AND c.publish_batch_id IS NULL
AND a.state IN ('failed','paused','stopped')`); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,a.id FROM orp_configuration_candidate c
JOIN orp_validation_attempt a ON a.id=(SELECT MAX(a2.id) FROM orp_validation_attempt a2 WHERE a2.candidate_id=c.id)
WHERE c.publish_on_validation_success=TRUE AND c.publish_state='waiting_validation' AND a.state='completed'
ORDER BY c.id LIMIT 20`)
	if err != nil {
		return err
	}
	items := [][2]int64{}
	for rows.Next() {
		var candidateID, attemptID int64
		if err := rows.Scan(&candidateID, &attemptID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, [2]int64{candidateID, attemptID})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range items {
		go s.autoPublishValidatedCandidate(item[0], item[1])
	}
	return nil
}
