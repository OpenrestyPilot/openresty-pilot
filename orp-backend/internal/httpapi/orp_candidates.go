package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// Candidate creation freezes a configuration. Publishing remains opt-in and
// is only chained after every frozen target has passed validation.
func (s *Server) orpCandidateCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:config:write")
	if !ok {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	centerID, err := resourceNumber(body["centerId"])
	if err != nil || centerID < 1 {
		orpFailure(w, 400, "中心 ID 无效")
		return
	}
	targetIDs, err := publishNodeIDs(body)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	concurrency := 1
	if value, exists := body["concurrency"]; exists {
		n, e := resourceNumber(value)
		if e != nil || n < 1 || n > 32 {
			orpFailure(w, 400, "并发数须为 1 到 32，且不能超过中心上限")
			return
		}
		concurrency = int(n)
	}
	publishOnSuccess := false
	if value, exists := body["publishOnValidationSuccess"]; exists {
		var valid bool
		publishOnSuccess, valid = value.(bool)
		if !valid {
			orpFailure(w, 400, "publishOnValidationSuccess 必须为布尔值")
			return
		}
	}
	publishStrategy, err := parseCandidatePublishStrategy(body["publishStrategy"])
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	if !accountHasCenterPermission(roles, "orp:config:write", strconv.FormatInt(centerID, 10)) {
		orpFailure(w, 403, "无权编辑该中心配置")
		return
	}
	if publishOnSuccess && !accountHasCenterPermission(roles, "orp:publish", strconv.FormatInt(centerID, 10)) {
		orpFailure(w, 403, "启用校验后自动发布需要该中心的发布权限")
		return
	}
	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		orpFailure(w, 500, "无法创建候选事务")
		return
	}
	defer tx.Rollback()
	resources, singletons, err := loadReleaseInput(tx, centerID)
	if err != nil {
		orpFailure(w, 409, "无法读取一致的配置草稿")
		return
	}
	nodes := map[int64]orpDocument{}
	for _, id := range targetIDs {
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", id).Scan(&raw); err != nil {
			orpFailure(w, 400, fmt.Sprintf("目标节点 %d 不存在", id))
			return
		}
		node := orpDocument{}
		if json.Unmarshal(raw, &node) != nil {
			orpFailure(w, 500, "节点数据损坏")
			return
		}
		actualCenter, e := resourceNumber(node["centerId"])
		if e != nil || actualCenter != centerID {
			orpFailure(w, 400, "所有目标节点必须属于所选中心")
			return
		}
		node["id"] = id
		if _, e := targetForNode(node); e != nil && !nodeHasAgent(node) {
			orpFailure(w, 409, e.Error())
			return
		}
		if directives, _ := node["directives"].([]any); len(directives) != 0 {
			orpFailure(w, 409, "节点级原生指令尚未进入候选渲染器")
			return
		}
		nodes[id] = node
	}
	candidateUUID := uuid.NewString()
	release, err := renderRelease(centerID, candidateUUID, resources, singletons)
	if err != nil {
		orpFailure(w, 409, "配置渲染失败: "+err.Error())
		return
	}
	snapshot, err := json.Marshal(releaseSnapshot{Resources: resources, Singletons: singletons, Nodes: nodes})
	if err != nil {
		orpFailure(w, 500, "序列化候选快照失败")
		return
	}
	targetJSON, _ := json.Marshal(targetIDs)
	strategyJSON, _ := json.Marshal(publishStrategy)
	publishState := "not_requested"
	if publishOnSuccess {
		publishState = "waiting_validation"
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO orp_configuration_candidate(candidate_uuid,center_id,actor,target_ids,snapshot,config_text,digest,publish_on_validation_success,publish_strategy,publish_state) VALUES(?,?,?,?,?,?,?,?,?,?)", candidateUUID, centerID, actor, targetJSON, snapshot, release.Config, release.Digest, publishOnSuccess, strategyJSON, publishState)
	if err != nil {
		orpFailure(w, 500, "保存候选失败")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		orpFailure(w, 500, "读取候选 ID 失败")
		return
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO orp_validation_center_policy(center_id,max_inflight) VALUES(?,4) ON DUPLICATE KEY UPDATE center_id=VALUES(center_id)", centerID); err != nil {
		orpFailure(w, 500, "初始化中心校验策略失败")
		return
	}
	var centerLimit int
	if err = tx.QueryRowContext(ctx, "SELECT max_inflight FROM orp_validation_center_policy WHERE center_id=? FOR UPDATE", centerID).Scan(&centerLimit); err != nil {
		orpFailure(w, 500, "读取中心校验并发上限失败")
		return
	}
	if concurrency > centerLimit {
		orpFailure(w, 400, fmt.Sprintf("候选并发数 %d 超过该中心上限 %d", concurrency, centerLimit))
		return
	}
	if err = buildCandidateDiffs(ctx, tx, id, centerID, candidateUUID, targetIDs, releaseSnapshot{Resources: resources, Singletons: singletons, Nodes: nodes}); err != nil {
		orpFailure(w, 409, "无法冻结候选基线 Diff: "+err.Error())
		return
	}
	frozenDiffs, err := loadCandidateDiffs(ctx, tx, id)
	if err != nil {
		orpFailure(w, 500, "读取刚冻结的候选 Diff 失败")
		return
	}
	result, err = tx.ExecContext(ctx, "INSERT INTO orp_validation_attempt(candidate_id,attempt_no,state,concurrency_limit) VALUES(?,1,'queued',?)", id, concurrency)
	if err != nil {
		orpFailure(w, 500, "创建校验尝试失败")
		return
	}
	attemptID, err := result.LastInsertId()
	if err != nil {
		orpFailure(w, 500, "读取校验尝试 ID 失败")
		return
	}
	for _, nodeID := range targetIDs {
		if _, err = tx.ExecContext(ctx, "INSERT INTO orp_validation_job(attempt_id,node_id,state) VALUES(?,?,'queued')", attemptID, nodeID); err != nil {
			orpFailure(w, 500, "创建节点校验任务失败")
			return
		}
	}
	audit := adminAuditDocument(map[string]any{"centerId": centerID, "candidateUuid": candidateUUID, "digest": release.Digest, "targetIds": targetIDs, "publishOnValidationSuccess": publishOnSuccess, "publishStrategy": publishStrategy})
	if err = orpAudit(ctx, tx, actor, "create", "configuration-candidate", id, nil, audit, orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录候选审计失败")
		return
	}
	if err = tx.Commit(); err != nil {
		orpFailure(w, 500, "提交候选失败")
		return
	}
	orpReply(w, 201, map[string]any{"id": id, "candidateUuid": candidateUUID, "centerId": centerID, "digest": release.Digest, "targetIds": targetIDs, "attemptId": attemptID, "status": "queued", "diffs": frozenDiffs, "maxInflight": centerLimit, "publishOnValidationSuccess": publishOnSuccess, "publishState": publishState, "publishBatchId": nil, "publishError": nil})
}

func (s *Server) orpCandidateList(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:read")
	if !ok {
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	requestedCenter := r.URL.Query().Get("centerId")
	archivedOnly := r.URL.Query().Get("archived") == "true"
	rows, err := s.db.QueryContext(r.Context(), "SELECT c.id,c.candidate_uuid,c.center_id,c.actor,c.target_ids,c.digest,c.archived_at,c.created_at,a.id,a.attempt_no,a.state,a.concurrency_limit,c.publish_on_validation_success,c.publish_state,c.publish_batch_id,c.publish_error FROM orp_configuration_candidate c LEFT JOIN orp_validation_attempt a ON a.id=(SELECT MAX(a2.id) FROM orp_validation_attempt a2 WHERE a2.candidate_id=c.id) WHERE (?=1 OR c.center_id=?) AND (?='' OR c.center_id=?) AND ((?=1 AND c.archived_at IS NOT NULL) OR (?=0 AND c.archived_at IS NULL)) ORDER BY c.id DESC",
		accountHasGlobalCenterPermission(roles, "orp:read"), requestedCenter, requestedCenter, requestedCenter, archivedOnly, archivedOnly)
	if err != nil {
		orpFailure(w, 500, "读取候选列表失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, centerID int64
		var candidateUUID, creator, digest string
		var targets []byte
		var archived, created sql.NullTime
		var attemptID, attemptNo, limit sql.NullInt64
		var state sql.NullString
		var publishOnSuccess bool
		var publishState string
		var publishBatchID sql.NullInt64
		var publishError sql.NullString
		if err := rows.Scan(&id, &candidateUUID, &centerID, &creator, &targets, &digest, &archived, &created, &attemptID, &attemptNo, &state, &limit, &publishOnSuccess, &publishState, &publishBatchID, &publishError); err != nil {
			orpFailure(w, 500, "读取候选列表失败")
			return
		}
		if !accountHasCenterPermission(roles, "orp:read", strconv.FormatInt(centerID, 10)) {
			continue
		}
		var targetIDs []int64
		_ = json.Unmarshal(targets, &targetIDs)
		items = append(items, map[string]any{"id": id, "candidateUuid": candidateUUID, "centerId": centerID, "creator": creator, "targetIds": targetIDs, "digest": digest, "archived": archived.Valid, "createdAt": created.Time, "attemptId": candidateNullInt(attemptID), "attemptNo": candidateNullInt(attemptNo), "attemptState": nullString(state), "blocked": state.Valid && state.String == "adapter_required", "blocker": candidateAdapterBlocker(state.String), "concurrency": candidateNullInt(limit), "publishOnValidationSuccess": publishOnSuccess, "publishState": publishState, "publishBatchId": candidateNullInt(publishBatchID), "publishError": nullString(publishError)})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取候选列表失败")
		return
	}
	orpReply(w, 200, orpPage(r, items))
}

func (s *Server) orpCandidateGet(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:read")
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "候选 ID 无效")
		return
	}
	var centerID int64
	var candidateUUID, creator, digest string
	var targets []byte
	var archived sql.NullTime
	var publishOnSuccess bool
	var publishState string
	var publishBatchID sql.NullInt64
	var publishError sql.NullString
	var created string
	if err := s.db.QueryRowContext(r.Context(), "SELECT center_id,candidate_uuid,actor,target_ids,digest,archived_at,created_at,publish_on_validation_success,publish_state,publish_batch_id,publish_error FROM orp_configuration_candidate WHERE id=?", id).Scan(&centerID, &candidateUUID, &creator, &targets, &digest, &archived, &created, &publishOnSuccess, &publishState, &publishBatchID, &publishError); err != nil {
		orpFailure(w, 404, "候选不存在")
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	if !accountHasCenterPermission(roles, "orp:read", strconv.FormatInt(centerID, 10)) {
		orpFailure(w, 403, "无权读取该中心候选")
		return
	}
	canReadDiff := accountHasCenterPermission(roles, "orp:config:write", strconv.FormatInt(centerID, 10))
	var targetIDs []int64
	_ = json.Unmarshal(targets, &targetIDs)
	attempts, err := s.db.QueryContext(r.Context(), "SELECT id,attempt_no,state,concurrency_limit,created_at,finished_at FROM orp_validation_attempt WHERE candidate_id=? ORDER BY attempt_no", id)
	if err != nil {
		orpFailure(w, 500, "读取候选尝试失败")
		return
	}
	defer attempts.Close()
	items := []map[string]any{}
	for attempts.Next() {
		var attemptID int64
		var attemptNo, concurrency int
		var state string
		var attemptCreated sql.NullTime
		var finished sql.NullTime
		if err := attempts.Scan(&attemptID, &attemptNo, &state, &concurrency, &attemptCreated, &finished); err != nil {
			orpFailure(w, 500, "读取候选尝试失败")
			return
		}
		jobs, err := s.db.QueryContext(r.Context(), "SELECT node_id,state,command_text,output_text,started_at,finished_at FROM orp_validation_job WHERE attempt_id=? ORDER BY node_id,id", attemptID)
		if err != nil {
			orpFailure(w, 500, "读取节点校验结果失败")
			return
		}
		jobItems := []map[string]any{}
		for jobs.Next() {
			var nodeID int64
			var jobState string
			var command, output sql.NullString
			var startedAt, finishedAt sql.NullTime
			if err := jobs.Scan(&nodeID, &jobState, &command, &output, &startedAt, &finishedAt); err != nil {
				jobs.Close()
				orpFailure(w, 500, "读取节点校验结果失败")
				return
			}
			jobItems = append(jobItems, map[string]any{"nodeId": nodeID, "state": jobState, "blocked": jobState == "adapter_required", "blocker": candidateAdapterBlocker(jobState), "command": nullString(command), "output": nullString(output), "startedAt": nullTime(startedAt), "finishedAt": nullTime(finishedAt)})
		}
		if err := jobs.Err(); err != nil {
			jobs.Close()
			orpFailure(w, 500, "读取节点校验结果失败")
			return
		}
		jobs.Close()
		items = append(items, map[string]any{"id": attemptID, "attemptNo": attemptNo, "state": state, "blocked": state == "adapter_required", "blocker": candidateAdapterBlocker(state), "concurrency": concurrency, "createdAt": attemptCreated.Time, "finishedAt": nullTime(finished), "jobs": jobItems})
	}
	if err := attempts.Err(); err != nil {
		orpFailure(w, 500, "读取候选尝试失败")
		return
	}
	response := map[string]any{"id": id, "candidateUuid": candidateUUID, "centerId": centerID, "creator": creator, "targetIds": targetIDs, "digest": digest, "archived": archived.Valid, "createdAt": created, "attempts": items, "publishOnValidationSuccess": publishOnSuccess, "publishState": publishState, "publishBatchId": candidateNullInt(publishBatchID), "publishError": nullString(publishError)}
	if canReadDiff {
		diffs, err := loadCandidateDiffs(r.Context(), s.db, id)
		if err != nil {
			orpFailure(w, 500, "读取候选基线 Diff 失败")
			return
		}
		response["diffs"] = diffs
	}
	orpReply(w, 200, response)
}

func candidateAdapterBlocker(state string) any {
	if state == "adapter_required" {
		return candidateAdapterBlockMessage
	}
	return nil
}

func candidateNullInt(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

// action is archive, restore, retry, continue, or stop. Retry creates a new
// immutable attempt containing only the requested node's retry job.
func (s *Server) orpCandidateAction(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:config:write")
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "候选 ID 无效")
		return
	}
	action := r.PathValue("action")
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "无法创建操作事务")
		return
	}
	defer tx.Rollback()
	var centerID int64
	var candidateUUID string
	var archived sql.NullTime
	if err := tx.QueryRowContext(r.Context(), "SELECT center_id,candidate_uuid,archived_at FROM orp_configuration_candidate WHERE id=? FOR UPDATE", id).Scan(&centerID, &candidateUUID, &archived); err != nil {
		orpFailure(w, 404, "候选不存在")
		return
	}
	roles, err := s.accountRoles(r, actor)
	if err != nil || !accountHasCenterPermission(roles, "orp:config:write", strconv.FormatInt(centerID, 10)) {
		orpFailure(w, 403, "无权编辑该中心配置")
		return
	}
	var latest int64
	var before, after map[string]any
	before = map[string]any{"candidateUuid": candidateUUID, "archived": archived.Valid}
	switch action {
	case "archive", "restore":
		if action == "archive" {
			_, err = tx.ExecContext(r.Context(), "UPDATE orp_configuration_candidate SET archived_at=CURRENT_TIMESTAMP(6) WHERE id=?", id)
		} else {
			_, err = tx.ExecContext(r.Context(), "UPDATE orp_configuration_candidate SET archived_at=NULL WHERE id=?", id)
		}
		after = map[string]any{"candidateUuid": candidateUUID, "archived": action == "archive"}
	case "continue", "stop":
		var state string
		err = tx.QueryRowContext(r.Context(), "SELECT id,state FROM orp_validation_attempt WHERE candidate_id=? ORDER BY attempt_no DESC LIMIT 1 FOR UPDATE", id).Scan(&latest, &state)
		if err == nil && state != "paused" {
			err = errors.New("只有暂停中的校验尝试可以继续或停止")
		}
		if err == nil && action == "continue" {
			_, err = tx.ExecContext(r.Context(), "UPDATE orp_validation_attempt SET state='running' WHERE id=?", latest)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), "UPDATE orp_validation_job SET state='queued' WHERE attempt_id=? AND state='paused'", latest)
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), "UPDATE orp_configuration_candidate SET publish_state=IF(publish_on_validation_success,'waiting_validation',publish_state),publish_error=NULL WHERE id=? AND publish_batch_id IS NULL", id)
			}
		} else if err == nil {
			_, err = tx.ExecContext(r.Context(), "UPDATE orp_validation_attempt SET state='stopped',finished_at=CURRENT_TIMESTAMP(6) WHERE id=?", latest)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), "UPDATE orp_validation_job SET state='cancelled',finished_at=CURRENT_TIMESTAMP(6) WHERE attempt_id=? AND state IN ('queued','paused')", latest)
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), "UPDATE orp_configuration_candidate SET publish_state=IF(publish_on_validation_success,'failed',publish_state),publish_error=IF(publish_on_validation_success,'校验已停止，不会自动发布',publish_error) WHERE id=? AND publish_batch_id IS NULL", id)
			}
		}
		after = map[string]any{"candidateUuid": candidateUUID, "action": action}
	case "retry":
		body, good := orpBody(w, r)
		if !good {
			return
		}
		nodeID, e := resourceNumber(body["nodeId"])
		if e != nil || nodeID < 1 {
			orpFailure(w, 400, "nodeId 无效")
			return
		}
		var frozenTargetsJSON []byte
		err = tx.QueryRowContext(r.Context(), "SELECT target_ids FROM orp_configuration_candidate WHERE id=?", id).Scan(&frozenTargetsJSON)
		var frozenTargets []int64
		if err == nil {
			err = json.Unmarshal(frozenTargetsJSON, &frozenTargets)
		}
		targetIncluded := false
		for _, frozenNodeID := range frozenTargets {
			targetIncluded = targetIncluded || frozenNodeID == nodeID
		}
		if err == nil && !targetIncluded {
			err = errors.New("节点不属于候选冻结目标")
		}
		if err == nil {
			err = tx.QueryRowContext(r.Context(), "SELECT id FROM orp_validation_attempt WHERE candidate_id=? ORDER BY attempt_no DESC LIMIT 1 FOR UPDATE", id).Scan(&latest)
		}
		var state string
		if err == nil {
			err = tx.QueryRowContext(r.Context(), "SELECT state FROM orp_validation_job WHERE attempt_id=? AND node_id=?", latest, nodeID).Scan(&state)
		}
		if err == nil && state != "failed" && state != "unknown" {
			err = errors.New("只能重试失败或结果未知的节点")
		}
		var attemptNo, concurrency int
		if err == nil {
			err = tx.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(attempt_no),0)+1 FROM orp_validation_attempt WHERE candidate_id=?", id).Scan(&attemptNo)
		}
		if err == nil {
			err = tx.QueryRowContext(r.Context(), "SELECT concurrency_limit FROM orp_validation_attempt WHERE id=?", latest).Scan(&concurrency)
		}
		if err == nil {
			centerLimit := defaultValidationCenterMaxInflight
			policyErr := tx.QueryRowContext(r.Context(), "SELECT max_inflight FROM orp_validation_center_policy WHERE center_id=? FOR UPDATE", centerID).Scan(&centerLimit)
			if policyErr != nil && !errors.Is(policyErr, sql.ErrNoRows) {
				err = policyErr
			} else if concurrency > centerLimit {
				concurrency = centerLimit
			}
		}
		if err == nil {
			var result sql.Result
			result, err = tx.ExecContext(r.Context(), "INSERT INTO orp_validation_attempt(candidate_id,attempt_no,state,concurrency_limit) VALUES(?,?,'queued',?)", id, attemptNo, concurrency)
			if err == nil {
				latest, err = result.LastInsertId()
			}
			if err == nil {
				var autoPublish bool
				err = tx.QueryRowContext(r.Context(), "SELECT publish_on_validation_success FROM orp_configuration_candidate WHERE id=?", id).Scan(&autoPublish)
				if err == nil && autoPublish {
					for _, targetID := range frozenTargets {
						if _, err = tx.ExecContext(r.Context(), "INSERT INTO orp_validation_job(attempt_id,node_id,state) VALUES(?,?,'queued')", latest, targetID); err != nil {
							break
						}
					}
				} else if err == nil {
					_, err = tx.ExecContext(r.Context(), "INSERT INTO orp_validation_job(attempt_id,node_id,state) VALUES(?,?,'queued')", latest, nodeID)
				}
			}
			if err == nil {
				_, err = tx.ExecContext(r.Context(), "UPDATE orp_configuration_candidate SET publish_state=IF(publish_on_validation_success,'waiting_validation',publish_state),publish_error=NULL WHERE id=? AND publish_batch_id IS NULL", id)
			}
		}
		after = map[string]any{"candidateUuid": candidateUUID, "action": action, "nodeId": nodeID}
	default:
		orpFailure(w, 404, "未知的候选操作")
		return
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			orpFailure(w, 409, "找不到可操作的校验记录")
		} else {
			orpFailure(w, 409, err.Error())
		}
		return
	}
	if err := orpAudit(r.Context(), tx, actor, action, "configuration-candidate", id, adminAuditDocument(before), adminAuditDocument(after), orpClientIP(r)); err != nil {
		orpFailure(w, 500, "记录候选审计失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交候选操作失败")
		return
	}
	orpReply(w, 200, map[string]any{"id": id, "action": action})
}
