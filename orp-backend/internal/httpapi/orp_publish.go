package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type publishCandidate struct {
	ID          int64
	ReleaseUUID string
	CenterID    int64
	TargetIDs   []int64
	Snapshot    []byte
	Config      string
	Digest      string
	Status      string
}

type selectedPublish struct {
	candidate publishCandidate
	releases  map[int64]orpRelease
}

type releaseSnapshot struct {
	Resources  map[string][]orpDocument `json:"resources"`
	Singletons map[string]any           `json:"singletons"`
	Nodes      map[int64]orpDocument    `json:"nodes"`
}

func publishNodeIDs(body orpDocument) ([]int64, error) {
	items, ok := body["nodeIds"].([]any)
	if !ok || len(items) == 0 || len(items) > 100 {
		return nil, errors.New("请选择 1 到 100 个节点")
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(items))
	for _, value := range items {
		id, err := resourceNumber(value)
		if err != nil || id < 1 || seen[id] {
			return nil, errors.New("节点 ID 无效或重复")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (s *Server) publishNodes(ctx context.Context, ids []int64) (map[int64]orpDocument, map[int64][]int64, error) {
	nodes := map[int64]orpDocument{}
	groups := map[int64][]int64{}
	for _, id := range ids {
		var raw []byte
		if err := s.db.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", id).Scan(&raw); err != nil {
			return nil, nil, fmt.Errorf("节点 %d 不存在", id)
		}
		var node orpDocument
		if err := json.Unmarshal(raw, &node); err != nil {
			return nil, nil, err
		}
		centerID, err := resourceNumber(node["centerId"])
		if err != nil || centerID < 1 {
			return nil, nil, fmt.Errorf("节点 %d 未归属有效中心", id)
		}
		node["id"] = id
		nodes[id] = node
		groups[centerID] = append(groups[centerID], id)
	}
	return nodes, groups, nil
}

func (s *Server) publishNodesWithinScope(r *http.Request, actor string, nodes map[int64]orpDocument) bool {
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		return false
	}
	if accountHasGlobalCenterPermission(roles, "orp:publish") {
		return true
	}
	for _, node := range nodes {
		if !accountHasCenterPermission(roles, "orp:publish", centerIDString(node["centerId"])) {
			return false
		}
	}
	return true
}

func (s *Server) freezeCandidate(ctx context.Context, actor string, centerID int64, targetIDs []int64) (publishCandidate, orpRelease, error) {
	var candidate publishCandidate
	var release orpRelease
	releaseID := uuid.NewString()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return candidate, release, err
	}
	defer tx.Rollback()
	resources, singletons, err := loadReleaseInput(tx, centerID)
	if err != nil {
		return candidate, release, err
	}
	frozenNodes := map[int64]orpDocument{}
	for _, id := range targetIDs {
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", id).Scan(&raw); err != nil {
			return candidate, release, errors.New("冻结快照时节点不存在")
		}
		var node orpDocument
		if err := json.Unmarshal(raw, &node); err != nil {
			return candidate, release, err
		}
		actualCenter, err := resourceNumber(node["centerId"])
		if err != nil || actualCenter != centerID {
			return candidate, release, errors.New("冻结快照时节点中心已变化")
		}
		node["id"] = id
		if _, err := targetForNode(node); err != nil && !nodeHasAgent(node) {
			return candidate, release, err
		}
		if directives, ok := node["directives"].([]any); ok && len(directives) > 0 {
			return candidate, release, errors.New("节点级原生指令尚未进入渲染器，不能发布该节点")
		}
		frozenNodes[id] = node
	}
	snapshot := releaseSnapshot{Resources: resources, Singletons: singletons, Nodes: frozenNodes}
	serialized, err := json.Marshal(snapshot)
	if err != nil {
		return candidate, release, err
	}
	release, err = renderRelease(centerID, releaseID, resources, singletons)
	if err != nil {
		return candidate, release, err
	}
	targetJSON, _ := json.Marshal(targetIDs)
	result, err := tx.ExecContext(ctx, "INSERT INTO orp_publish_candidate(release_uuid,center_id,actor,target_ids,snapshot,config_text,digest,status) VALUES(?,?,?,?,?,?,?,'frozen')", releaseID, centerID, actor, targetJSON, serialized, release.Config, release.Digest)
	if err != nil {
		return candidate, release, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return candidate, release, err
	}
	if err := tx.Commit(); err != nil {
		return candidate, release, err
	}
	candidate = publishCandidate{ID: id, ReleaseUUID: releaseID, CenterID: centerID, TargetIDs: targetIDs, Snapshot: serialized, Config: release.Config, Digest: release.Digest, Status: "frozen"}
	return candidate, release, nil
}

func (s *Server) orpPublishPrecheck(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	ids, err := publishNodeIDs(body)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	nodes, groups, err := s.publishNodes(r.Context(), ids)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if !s.publishNodesWithinScope(r, actor, nodes) {
		orpFailure(w, http.StatusForbidden, "所选节点包含无权访问的中心")
		return
	}
	centerIDs := make([]int64, 0, len(groups))
	for id := range groups {
		centerIDs = append(centerIDs, id)
	}
	sort.Slice(centerIDs, func(i, j int) bool { return centerIDs[i] < centerIDs[j] })
	items := []map[string]any{}
	allPassed := true
	for _, centerID := range centerIDs {
		candidate, _, err := s.freezeCandidate(r.Context(), actor, centerID, groups[centerID])
		if err != nil {
			orpFailure(w, 409, "无法冻结候选配置: "+err.Error())
			return
		}
		centerName := s.publishCenterName(r.Context(), centerID)
		frozen, _ := snapshotConfig(candidate.Snapshot)
		centerPassed := true
		for _, nodeID := range groups[centerID] {
			target, targetErr := targetForNode(frozen.Nodes[nodeID])
			agentTarget := targetErr != nil && nodeHasAgent(frozen.Nodes[nodeID])
			if targetErr != nil && !agentTarget {
				allPassed, centerPassed = false, false
				orpFailure(w, 409, targetErr.Error())
				return
			}
			var nodeRelease orpRelease
			var renderErr error
			if agentTarget {
				nodeRelease, renderErr = renderReleaseForTarget(centerID, candidate.ReleaseUUID, frozen.Resources, frozen.Singletons, agentProbePort)
			} else {
				nodeRelease, renderErr = renderDemoReleaseForTarget(centerID, candidate.ReleaseUUID, frozen.Resources, frozen.Singletons, target.healthPort)
			}
			if renderErr != nil {
				allPassed, centerPassed = false, false
				orpFailure(w, 409, "无法为节点渲染候选配置: "+renderErr.Error())
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			var output string
			var checkErr error
			if agentTarget {
				output, checkErr = s.validateAgentCandidate(ctx, nodeID, candidate, actor)
			} else {
				output, checkErr = validateDemoRelease(ctx, frozen.Nodes[nodeID], candidate.ReleaseUUID, nodeRelease)
			}
			cancel()
			passed := checkErr == nil
			if !passed {
				allPassed, centerPassed = false, false
				output += "\n" + checkErr.Error()
			}
			command := demoNginx + " -t -c " + demoConfigRoot + "/candidates/" + candidate.ReleaseUUID + "/nginx.conf"
			if agentTarget {
				command = "Agent validate: fixed candidate nginx.conf (no shell or task-supplied paths)"
			}
			baseline, baselineErr := s.lastPublishedNode(r.Context(), nodeID)
			if baselineErr != nil && !errors.Is(baselineErr, sql.ErrNoRows) {
				orpFailure(w, 500, "读取节点发布基线失败")
				return
			}
			oldSnapshot := releaseSnapshot{Resources: map[string][]orpDocument{}, Singletons: map[string]any{}}
			baselineText := ""
			var baselineUUID, baselineDigest any
			if baselineErr == nil {
				baselineUUID, baselineDigest, baselineText = baseline.ReleaseUUID, baseline.Digest, baseline.Config
				var oldRaw []byte
				if err := s.db.QueryRowContext(r.Context(), "SELECT snapshot FROM orp_publish_candidate WHERE release_uuid=?", baseline.ReleaseUUID).Scan(&oldRaw); err == nil {
					oldSnapshot, _ = snapshotConfig(oldRaw)
				}
			}
			semanticChanges := semanticReleaseDiff(oldSnapshot, frozen)
			semanticJSON, _ := json.Marshal(semanticChanges)
			textLines, addCount, delCount := publishTextDiff(baselineText, nodeRelease.Config)
			textJSON, _ := json.Marshal(textLines)
			planJSON, _ := json.Marshal(map[string]any{"nodeId": nodeID, "centerId": centerID, "action": "reload", "candidateDigest": nodeRelease.Digest, "baselineRelease": baselineUUID})
			if _, err := s.db.ExecContext(r.Context(), "INSERT INTO orp_publish_precheck(candidate_id,node_id,passed,command_text,output_text,baseline_release_uuid,baseline_digest,semantic_diff,text_diff,publish_plan) VALUES(?,?,?,?,?,?,?,?,?,?)", candidate.ID, nodeID, passed, command, output, baselineUUID, baselineDigest, semanticJSON, textJSON, planJSON); err != nil {
				orpFailure(w, 500, "保存节点校验结果失败")
				return
			}
			errorsList := []map[string]any{}
			if !passed {
				errorsList = append(errorsList, map[string]any{"line": 0, "message": strings.TrimSpace(output)})
			}
			items = append(items, map[string]any{"centerId": centerID, "centerName": centerName, "nodeId": nodeID, "nodeName": frozen.Nodes[nodeID]["name"], "command": command, "output": output, "passed": passed, "errors": errorsList, "candidateDigest": nodeRelease.Digest, "baselineRelease": baselineUUID, "semanticDiff": semanticChanges, "addCount": addCount, "delCount": delCount})
		}
		status := "validated"
		if !centerPassed {
			status = "failed"
		}
		if _, err := s.db.ExecContext(r.Context(), "UPDATE orp_publish_candidate SET status=? WHERE id=?", status, candidate.ID); err != nil {
			orpFailure(w, 500, "保存候选校验状态失败")
			return
		}
	}
	orpReply(w, 200, map[string]any{"items": items, "passed": allPassed})
}

func (s *Server) publishCenterName(ctx context.Context, id int64) string {
	var name string
	_ = s.db.QueryRowContext(ctx, "SELECT JSON_UNQUOTE(JSON_EXTRACT(document,'$.name')) FROM orp_resource WHERE kind='centers' AND id=?", id).Scan(&name)
	return name
}

func (s *Server) latestCandidate(ctx context.Context, actor string, centerID int64, ids []int64) (publishCandidate, releaseSnapshot, orpRelease, error) {
	var candidate publishCandidate
	var snapshot releaseSnapshot
	var release orpRelease
	var targets []byte
	err := s.db.QueryRowContext(ctx, "SELECT id,release_uuid,target_ids,snapshot,config_text,digest,status FROM orp_publish_candidate WHERE center_id=? AND actor=? AND status='validated' ORDER BY id DESC LIMIT 1", centerID, actor).Scan(&candidate.ID, &candidate.ReleaseUUID, &targets, &candidate.Snapshot, &candidate.Config, &candidate.Digest, &candidate.Status)
	if err != nil {
		return candidate, snapshot, release, errors.New("请先对选定节点完成预检")
	}
	if err := json.Unmarshal(targets, &candidate.TargetIDs); err != nil {
		return candidate, snapshot, release, err
	}
	if len(candidate.TargetIDs) != len(ids) {
		return candidate, snapshot, release, errors.New("节点集合已变化，请重新预检")
	}
	for index := range ids {
		if ids[index] != candidate.TargetIDs[index] {
			return candidate, snapshot, release, errors.New("节点集合已变化，请重新预检")
		}
	}
	if err := json.Unmarshal(candidate.Snapshot, &snapshot); err != nil {
		return candidate, snapshot, release, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return candidate, snapshot, release, err
	}
	defer tx.Rollback()
	currentResources, currentSingletons, err := loadReleaseInput(tx, centerID)
	if err != nil {
		return candidate, snapshot, release, err
	}
	current, err := renderRelease(centerID, candidate.ReleaseUUID, currentResources, currentSingletons)
	if err != nil {
		return candidate, snapshot, release, err
	}
	if current.Digest != candidate.Digest {
		return candidate, snapshot, release, errors.New("配置已变化，请重新预检")
	}
	for _, id := range ids {
		var passed bool
		var baselineUUID, baselineDigest sql.NullString
		if err := s.db.QueryRowContext(ctx, "SELECT passed,baseline_release_uuid,baseline_digest FROM orp_publish_precheck WHERE candidate_id=? AND node_id=? ORDER BY id DESC LIMIT 1", candidate.ID, id).Scan(&passed, &baselineUUID, &baselineDigest); err != nil || !passed {
			return candidate, snapshot, release, errors.New("缺少该节点的成功预检证据")
		}
		currentBaseline, baselineErr := s.lastPublishedNode(ctx, id)
		if errors.Is(baselineErr, sql.ErrNoRows) {
			if baselineUUID.Valid {
				return candidate, snapshot, release, errors.New("节点发布基线已变化，请重新预检")
			}
		} else if baselineErr != nil || !baselineUUID.Valid || currentBaseline.ReleaseUUID != baselineUUID.String || currentBaseline.Digest != baselineDigest.String {
			return candidate, snapshot, release, errors.New("节点发布基线已变化，请重新预检")
		} else {
			observed, err := observedDemoRelease(ctx, snapshot.Nodes[id])
			if err != nil || observed != currentBaseline.ReleaseUUID {
				return candidate, snapshot, release, errors.New("节点实际版本与发布基线不一致")
			}
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT document FROM orp_resource WHERE kind='nodes' AND id=?", id).Scan(&raw); err != nil {
			return candidate, snapshot, release, errors.New("节点已变化，请重新预检")
		}
		var node orpDocument
		if err := json.Unmarshal(raw, &node); err != nil {
			return candidate, snapshot, release, err
		}
		original, _ := json.Marshal(snapshot.Nodes[id])
		fresh, _ := json.Marshal(node)
		var originalDocument orpDocument
		_ = json.Unmarshal(original, &originalDocument)
		delete(originalDocument, "id")
		original, _ = json.Marshal(originalDocument)
		if string(original) != string(fresh) {
			return candidate, snapshot, release, errors.New("节点属性已变化，请重新预检")
		}
	}
	if err := tx.Commit(); err != nil {
		return candidate, snapshot, release, err
	}
	return candidate, snapshot, current, nil
}

func (s *Server) orpPublishCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	body, ok := orpBody(w, r)
	if !ok {
		return
	}
	ids, err := publishNodeIDs(body)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	strategy, _ := body["strategy"].(map[string]any)
	mode := toString(strategy["mode"])
	if mode == "" {
		mode = "all"
	}
	if mode != "all" && mode != "weighted" {
		orpFailure(w, 400, "下发模式无效")
		return
	}
	batchCount := 1
	canaryEnabled, waitSeconds := false, 0
	if mode == "weighted" {
		count, err := resourceNumber(strategy["batchCount"])
		if err != nil || count < 1 || count > 5 || count > int64(len(ids)) {
			orpFailure(w, 400, "分批数必须介于 1 到 5 且不超过节点数")
			return
		}
		batchCount = int(count)
		canary, _ := strategy["canary"].(map[string]any)
		canaryEnabled, _ = canary["enabled"].(bool)
		if canaryEnabled {
			seconds, err := resourceNumber(canary["waitSeconds"])
			if err != nil || seconds < 1 || seconds > 300 {
				orpFailure(w, 400, "观察等待时间必须为 1 到 300 秒")
				return
			}
			waitSeconds = int(seconds)
		}
	}
	nodes, groups, err := s.publishNodes(r.Context(), ids)
	if err != nil {
		orpFailure(w, 400, err.Error())
		return
	}
	if !s.publishNodesWithinScope(r, actor, nodes) {
		orpFailure(w, http.StatusForbidden, "所选节点包含无权访问的中心")
		return
	}
	selectedByCenter := map[int64]selectedPublish{}
	for centerID, targetIDs := range groups {
		candidate, snapshot, _, err := s.latestCandidate(r.Context(), actor, centerID, targetIDs)
		if err != nil {
			orpFailure(w, 409, err.Error())
			return
		}
		resources, singletons := snapshot.Resources, snapshot.Singletons
		releases := make(map[int64]orpRelease, len(targetIDs))
		for _, nodeID := range targetIDs {
			target, err := targetForNode(snapshot.Nodes[nodeID])
			agentTarget := err != nil && nodeHasAgent(snapshot.Nodes[nodeID])
			if err != nil && !agentTarget {
				orpFailure(w, 409, err.Error())
				return
			}
			var release orpRelease
			if agentTarget {
				release, err = renderReleaseForTarget(centerID, candidate.ReleaseUUID, resources, singletons, agentProbePort)
			} else {
				release, err = renderDemoReleaseForTarget(centerID, candidate.ReleaseUUID, resources, singletons, target.healthPort)
			}
			if err != nil {
				orpFailure(w, 409, "无法为节点渲染候选配置: "+err.Error())
				return
			}
			releases[nodeID] = release
		}
		selectedByCenter[centerID] = selectedPublish{candidate: candidate, releases: releases}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "创建批次失败")
		return
	}
	defer tx.Rollback()
	for _, nodeID := range ids {
		var lockedID int64
		if err := tx.QueryRowContext(r.Context(), "SELECT id FROM orp_resource WHERE kind='nodes' AND id=? FOR UPDATE", nodeID).Scan(&lockedID); err != nil {
			orpFailure(w, 409, "节点已不存在，请重新预检")
			return
		}
		var active int
		if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_publish_item WHERE node_id=? AND status IN ('queued','deploying','validating')", nodeID).Scan(&active); err != nil {
			orpFailure(w, 500, "读取节点下发状态失败")
			return
		}
		if active > 0 {
			orpFailure(w, 409, "节点正在下发，请等待当前批次完成")
			return
		}
	}
	result, err := tx.ExecContext(r.Context(), "INSERT INTO orp_publish_batch(actor,mode,phase,canary_enabled,canary_wait_seconds) VALUES(?,?,'running',?,?)", actor, mode, canaryEnabled, waitSeconds)
	if err != nil {
		orpFailure(w, 500, "创建批次失败")
		return
	}
	batchID, _ := result.LastInsertId()
	batchIndex := publishBatchIndexes(ids, nodes, batchCount)
	for _, nodeID := range ids {
		centerID, _ := resourceNumber(nodes[nodeID]["centerId"])
		candidate := selectedByCenter[centerID].candidate
		weight, err := resourceNumber(nodes[nodeID]["weight"])
		if err != nil || weight < 1 {
			weight = 10
		}
		if _, err := tx.ExecContext(r.Context(), "INSERT INTO orp_publish_item(batch_id,candidate_id,node_id,center_id,batch_index,weight,status) VALUES(?,?,?,?,?,?,'queued')", batchID, candidate.ID, nodeID, centerID, batchIndex[nodeID], weight); err != nil {
			orpFailure(w, 500, "创建节点任务失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交批次失败")
		return
	}
	go s.runDemoBatch(batchID, ids, nodes, selectedByCenter, batchIndex, batchCount, canaryEnabled, waitSeconds)
	orpReply(w, 200, map[string]any{"batchId": batchID, "total": len(ids)})
}

func publishBatchIndexes(ids []int64, nodes map[int64]orpDocument, count int) map[int64]int {
	ordered := append([]int64(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool {
		left, leftErr := resourceNumber(nodes[ordered[i]]["weight"])
		right, rightErr := resourceNumber(nodes[ordered[j]]["weight"])
		if leftErr != nil || left < 1 {
			left = 10
		}
		if rightErr != nil || right < 1 {
			right = 10
		}
		if left == right {
			return ordered[i] < ordered[j]
		}
		return left < right
	})
	indexes := map[int64]int{}
	position := 0
	for group := 0; group < count; group++ {
		size := len(ordered) / count
		if group >= count-len(ordered)%count {
			size++
		}
		for end := position + size; position < end; position++ {
			indexes[ordered[position]] = group
		}
	}
	return indexes
}

func (s *Server) runDemoBatch(batchID int64, ids []int64, nodes map[int64]orpDocument, selectedByCenter map[int64]selectedPublish, indexes map[int64]int, batchCount int, canaryEnabled bool, waitSeconds int) {
	var actor string
	if err := s.db.QueryRow("SELECT actor FROM orp_publish_batch WHERE id=?", batchID).Scan(&actor); err != nil {
		log.Printf("publish batch %d: read actor: %v", batchID, err)
		return
	}
	ordered := append([]int64(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool {
		if indexes[ordered[i]] == indexes[ordered[j]] {
			return ordered[i] < ordered[j]
		}
		return indexes[ordered[i]] < indexes[ordered[j]]
	})
	currentGroup := -1
	failedInGroup := false
	for _, nodeID := range ordered {
		group := indexes[nodeID]
		if group != currentGroup {
			if currentGroup >= 0 {
				if failedInGroup {
					s.stopRemainingDemoBatch(batchID, "上一批节点下发失败，停止后续批次")
					break
				}
				if canaryEnabled && currentGroup == 0 && batchCount > 1 && !s.waitDemoCanary(batchID, actor, nodes, selectedByCenter, ordered, indexes, waitSeconds) {
					break
				}
			}
			currentGroup = group
			_, _ = s.db.Exec("UPDATE orp_publish_batch SET current_batch_index=? WHERE id=? AND phase='running'", group, batchID)
		}
		var phase, itemStatus string
		var itemID int64
		if err := s.db.QueryRow("SELECT phase FROM orp_publish_batch WHERE id=?", batchID).Scan(&phase); err != nil || phase == "aborted" {
			break
		}
		if err := s.db.QueryRow("SELECT id,status FROM orp_publish_item WHERE batch_id=? AND node_id=?", batchID, nodeID).Scan(&itemID, &itemStatus); err != nil || itemStatus != "queued" {
			continue
		}
		centerID, _ := resourceNumber(nodes[nodeID]["centerId"])
		selected := selectedByCenter[centerID]
		previous, _ := s.lastPublishedNode(context.Background(), nodeID)
		var frozenBaseline sql.NullString
		if err := s.db.QueryRow("SELECT previous_release_uuid FROM orp_publish_item WHERE id=? AND batch_id=?", itemID, batchID).Scan(&frozenBaseline); err == nil && frozenBaseline.Valid {
			previous.ReleaseUUID = frozenBaseline.String
		}
		if _, err := s.db.Exec("UPDATE orp_publish_item SET status='deploying',previous_release_uuid=?,started_at=NOW(6) WHERE batch_id=? AND node_id=?", nullRelease(previous.ReleaseUUID), batchID, nodeID); err != nil {
			log.Printf("publish batch %d node %d: start task: %v", batchID, nodeID, err)
			_, _ = s.db.Exec("UPDATE orp_publish_item SET status='failed',error_text='无法开始节点下发任务',finished_at=NOW(6) WHERE batch_id=? AND node_id=?", batchID, nodeID)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		release := selected.releases[nodeID]
		var err error
		if _, targetErr := targetForNode(nodes[nodeID]); targetErr != nil && nodeHasAgent(nodes[nodeID]) {
			_, err = s.publishAgentCandidate(ctx, nodeID, selected.candidate, actor, batchID, itemID)
		} else {
			_, err = activateDemoRelease(ctx, nodes[nodeID], selected.candidate.ReleaseUUID, release)
		}
		cancel()
		status := "success"
		message := ""
		if err != nil {
			status, message = "failed", err.Error()
			failedInGroup = true
		}
		tx, txErr := s.db.Begin()
		if txErr != nil {
			failedInGroup = true
			log.Printf("publish batch %d node %d: save result: %v", batchID, nodeID, txErr)
			continue
		}
		before, _ := json.Marshal(map[string]any{"release": previous.ReleaseUUID})
		after, _ := json.Marshal(map[string]any{"release": selected.candidate.ReleaseUUID, "digest": release.Digest, "status": status, "error": message})
		if _, txErr = tx.Exec("UPDATE orp_publish_item SET status=?,error_text=?,finished_at=NOW(6) WHERE batch_id=? AND node_id=?", status, message, batchID, nodeID); txErr == nil && err == nil {
			_, txErr = tx.Exec("UPDATE orp_resource SET document=JSON_SET(document,'$.activeVersion',?) WHERE kind='nodes' AND id=?", selected.candidate.ReleaseUUID, nodeID)
		}
		if txErr == nil {
			txErr = orpAudit(context.Background(), tx, actor, "publish", "publish", nodeID, before, after, "local")
		}
		if txErr == nil {
			txErr = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if txErr != nil {
			log.Printf("publish batch %d node %d: commit result: %v", batchID, nodeID, txErr)
			_, _ = s.db.Exec("UPDATE orp_publish_item SET status='failed',error_text='保存发布结果失败，节点实际版本未知',finished_at=NOW(6) WHERE batch_id=? AND node_id=? AND status='deploying'", batchID, nodeID)
		}
	}
	if failedInGroup {
		s.stopRemainingDemoBatch(batchID, "节点下发失败，停止后续批次")
	}
	if _, err := s.db.Exec("UPDATE orp_publish_batch SET phase='done',finished_at=NOW(6) WHERE id=? AND phase='running'", batchID); err != nil {
		log.Printf("publish batch %d: finish batch: %v", batchID, err)
	}
	if err := syncConfigurationCandidatePublishState(context.Background(), s.db, batchID); err != nil {
		log.Printf("publish batch %d: sync source candidate state: %v", batchID, err)
	}
}

func (s *Server) stopRemainingDemoBatch(batchID int64, reason string) {
	_, _ = s.db.Exec("UPDATE orp_publish_item SET status='aborted',error_text=?,finished_at=NOW(6) WHERE batch_id=? AND status='queued'", reason, batchID)
}

func (s *Server) waitDemoCanary(batchID int64, actor string, nodes map[int64]orpDocument, selected map[int64]selectedPublish, ordered []int64, indexes map[int64]int, seconds int) bool {
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	_, err := s.db.Exec("UPDATE orp_publish_batch SET phase='canary_observing',canary_observe_until=? WHERE id=? AND phase='running'", deadline, batchID)
	if err != nil {
		return false
	}
	for {
		var phase string
		if err := s.db.QueryRow("SELECT phase FROM orp_publish_batch WHERE id=?", batchID).Scan(&phase); err != nil || phase == "aborted" {
			return false
		}
		if phase == "running" || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	for _, id := range ordered {
		if indexes[id] != 0 {
			continue
		}
		centerID, _ := resourceNumber(nodes[id]["centerId"])
		var probeErr error
		if _, targetErr := targetForNode(nodes[id]); targetErr != nil && nodeHasAgent(nodes[id]) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			_, probeErr = s.probeAgentCandidate(ctx, id, selected[centerID].candidate, actor)
			cancel()
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			observed, err := observedDemoRelease(ctx, nodes[id])
			cancel()
			if err != nil || observed != selected[centerID].candidate.ReleaseUUID {
				probeErr = errors.New("demo node active release differs from candidate")
			}
		}
		if probeErr != nil {
			s.stopRemainingDemoBatch(batchID, "金丝雀节点版本探针异常，停止后续批次")
			_, _ = s.db.Exec("UPDATE orp_publish_batch SET phase='done',finished_at=NOW(6) WHERE id=?", batchID)
			return false
		}
	}
	_, err = s.db.Exec("UPDATE orp_publish_batch SET phase='running' WHERE id=? AND phase='canary_observing'", batchID)
	return err == nil
}

func nullRelease(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Server) orpPublishBatch(w http.ResponseWriter, r *http.Request) {
	requestActor, ok := s.orpIdentity(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		orpFailure(w, 400, "批次 ID 无效")
		return
	}
	if !s.publishBatchWithinScope(r, requestActor, id) {
		orpFailure(w, http.StatusForbidden, "无权查看该批次中的节点")
		return
	}
	var actor, mode, phase string
	var currentIndex, waitSeconds int
	var canaryEnabled bool
	var observeUntil sql.NullTime
	var created time.Time
	err = s.db.QueryRowContext(r.Context(), "SELECT actor,mode,phase,created_at,current_batch_index,canary_enabled,canary_wait_seconds,canary_observe_until FROM orp_publish_batch WHERE id=?", id).Scan(&actor, &mode, &phase, &created, &currentIndex, &canaryEnabled, &waitSeconds, &observeUntil)
	if err != nil {
		orpFailure(w, 404, "批次不存在")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,node_id,center_id,batch_index,weight,status,error_text,started_at,finished_at FROM orp_publish_item WHERE batch_id=? ORDER BY id", id)
	if err != nil {
		orpFailure(w, 500, "读取批次任务失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	successCount, failCount, abortedCount := 0, 0, 0
	for rows.Next() {
		var itemID, nodeID, centerID int64
		var index, weight int
		var status string
		var errorText sql.NullString
		var started, finished sql.NullTime
		if err := rows.Scan(&itemID, &nodeID, &centerID, &index, &weight, &status, &errorText, &started, &finished); err != nil {
			orpFailure(w, 500, "读取批次任务失败")
			return
		}
		switch status {
		case "success":
			successCount++
		case "failed":
			failCount++
		case "aborted":
			abortedCount++
		}
		var nodeName string
		_ = s.db.QueryRowContext(r.Context(), "SELECT JSON_UNQUOTE(JSON_EXTRACT(document,'$.name')) FROM orp_resource WHERE kind='nodes' AND id=?", nodeID).Scan(&nodeName)
		requiresReconciliation := phase == "recovery_required" && (status == "failed" || status == "aborted" && errorText.Valid && errorText.String == recoveryQueuedMessage)
		items = append(items, map[string]any{"itemId": itemID, "requiresReconciliation": requiresReconciliation, "nodeId": nodeID, "nodeName": nodeName, "centerId": centerID, "centerName": s.publishCenterName(r.Context(), centerID), "batchIndex": index, "weight": weight, "status": status, "error": nullString(errorText), "startedAt": nullTime(started), "finishedAt": nullTime(finished)})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取批次任务失败")
		return
	}
	var groups any
	totalBatches := 1
	if mode == "weighted" {
		groupMap := map[int][]map[string]any{}
		for _, item := range items {
			index := item["batchIndex"].(int)
			groupMap[index] = append(groupMap[index], item)
			if index+1 > totalBatches {
				totalBatches = index + 1
			}
		}
		built := []map[string]any{}
		for index := 0; index < totalBatches; index++ {
			members := groupMap[index]
			ids := []int64{}
			weightSum := 0
			status := "success"
			for _, item := range members {
				ids = append(ids, item["nodeId"].(int64))
				weightSum += item["weight"].(int)
				switch item["status"] {
				case "failed":
					status = "failed"
				case "aborted":
					if status != "failed" {
						status = "aborted"
					}
				case "deploying", "validating":
					if status != "failed" && status != "aborted" {
						status = "running"
					}
				case "queued":
					if status == "success" {
						status = "pending"
					}
				}
			}
			if phase == "canary_observing" && index == 0 {
				status = "observing"
			}
			built = append(built, map[string]any{"index": index, "nodeIds": ids, "weightSum": weightSum, "status": status})
		}
		groups = built
	}
	var remaining any
	if phase == "canary_observing" && observeUntil.Valid {
		seconds := int(time.Until(observeUntil.Time).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		remaining = seconds
	}
	orpReply(w, 200, map[string]any{"id": id, "operator": actor, "mode": mode, "phase": phase, "createdAt": created.UnixMilli(), "currentBatchIndex": currentIndex, "totalBatches": totalBatches, "batchGroups": groups, "canary": map[string]any{"enabled": canaryEnabled, "remaining": remaining, "waitSeconds": waitSeconds}, "done": phase == "done" || phase == "aborted" || phase == "recovery_required", "recoveryRequired": phase == "recovery_required", "items": items, "successCount": successCount, "failCount": failCount, "abortedCount": abortedCount})
}

func nullString(value sql.NullString) any {
	if !value.Valid || value.String == "" {
		return nil
	}
	return value.String
}

func nullTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time.Format("2006-01-02 15:04:05")
}

// Reconcile interrupted items against the target's worker probe. Never replay a
// deployment merely because the control process restarted.
func RecoverPendingPublishes(db *sql.DB) error {
	type interrupted struct {
		id, nodeID           int64
		status, releaseID    string
		nodeJSON             []byte
		taskID               sql.NullString
		batchID, candidateID int64
	}
	rows, err := db.Query(`SELECT i.id,i.node_id,i.status,c.release_uuid,n.document,i.agent_task_id,i.batch_id,i.candidate_id
		FROM orp_publish_item i
		JOIN orp_publish_candidate c ON c.id=i.candidate_id
		JOIN orp_resource n ON n.id=i.node_id AND n.kind='nodes'
		WHERE i.status IN ('queued','deploying','validating') ORDER BY i.id`)
	if err != nil {
		return err
	}
	pending := []interrupted{}
	for rows.Next() {
		var item interrupted
		if err := rows.Scan(&item.id, &item.nodeID, &item.status, &item.releaseID, &item.nodeJSON, &item.taskID, &item.batchID, &item.candidateID); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	type recoveredItem struct {
		id              int64
		status, message string
	}
	recovered := make([]recoveredItem, 0, len(pending))
	manualReviewBatches := map[int64]bool{}
	recoveredBatches := map[int64]bool{}
	for _, item := range pending {
		recoveredBatches[item.batchID] = true
		if item.status == "queued" {
			recovered = append(recovered, recoveredItem{item.id, "aborted", "控制面重启，尚未开始的下发已停止"})
			manualReviewBatches[item.batchID] = true
			continue
		}
		var node orpDocument
		if err := json.Unmarshal(item.nodeJSON, &node); err != nil {
			recovered = append(recovered, recoveredItem{item.id, "failed", "控制面重启后无法读取节点信息，发布结果未知"})
			manualReviewBatches[item.batchID] = true
			continue
		}
		if _, targetErr := targetForNode(node); targetErr != nil && nodeHasAgent(node) {
			status, message, err := recoverAgentPublishItem(db, item)
			if err != nil {
				return err
			}
			recovered = append(recovered, recoveredItem{item.id, status, message})
			if status == "failed" {
				manualReviewBatches[item.batchID] = true
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		observed, probeErr := observedDemoRelease(ctx, node)
		cancel()
		if probeErr == nil && observed == item.releaseID {
			recovered = append(recovered, recoveredItem{item.id, "success", ""})
		} else {
			recovered = append(recovered, recoveredItem{item.id, "failed", "控制面重启后无法确认候选已加载；请重新预检"})
			manualReviewBatches[item.batchID] = true
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range recovered {
		if _, err := tx.Exec("UPDATE orp_publish_item SET status=?,error_text=?,finished_at=NOW(6) WHERE id=? AND status IN ('queued','deploying','validating')", item.status, item.message, item.id); err != nil {
			return err
		}
	}
	for batchID := range recoveredBatches {
		if _, err := tx.Exec("UPDATE orp_publish_batch SET phase='done',finished_at=NOW(6) WHERE id=? AND phase IN ('running','canary_observing')", batchID); err != nil {
			return err
		}
	}
	for batchID := range manualReviewBatches {
		if _, err := tx.Exec("UPDATE orp_publish_batch SET phase='recovery_required',finished_at=NOW(6) WHERE id=? AND phase IN ('running','canary_observing')", batchID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for batchID := range recoveredBatches {
		if err := syncConfigurationCandidatePublishState(context.Background(), db, batchID); err != nil {
			return err
		}
	}
	for batchID := range manualReviewBatches {
		if err := syncConfigurationCandidatePublishState(context.Background(), db, batchID); err != nil {
			return err
		}
	}
	return nil
}

// recoverAgentPublishItem reconciles only a task that is transactionally linked
// to its item. A queued task can be safely retired if recovery wins the row lock;
// any claimed, expired, failed, or unlinked task may have activated the release
// and is therefore left for operator inspection without replay.
func recoverAgentPublishItem(db *sql.DB, item struct {
	id, nodeID           int64
	status, releaseID    string
	nodeJSON             []byte
	taskID               sql.NullString
	batchID, candidateID int64
}) (string, string, error) {
	unknown := "控制面重启后 Agent 发布结果未知；已暂停批次，不会自动重发。请核对节点活动版本与 Agent 任务结果后人工处置。"
	if !item.taskID.Valid || item.taskID.String == "" {
		_, err := db.Exec("UPDATE orp_publish_item SET status='failed',error_text=?,finished_at=NOW(6) WHERE id=? AND status IN ('deploying','validating')", unknown, item.id)
		return "failed", unknown, err
	}
	tx, err := db.Begin()
	if err != nil {
		return "failed", "", err
	}
	defer tx.Rollback()
	var taskNode, taskBatch, taskItem int64
	var operation, taskStatus string
	var resultStatus sql.NullString
	err = tx.QueryRow(`SELECT node_id,operation,status,result_status,publish_batch_id,publish_item_id
		FROM orp_agent_task WHERE id=? FOR UPDATE`, item.taskID.String).Scan(&taskNode, &operation, &taskStatus, &resultStatus, &taskBatch, &taskItem)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, updateErr := tx.Exec("UPDATE orp_publish_item SET status='failed',error_text=?,finished_at=NOW(6) WHERE id=? AND status IN ('deploying','validating')", unknown, item.id)
			if updateErr != nil {
				return "failed", "", updateErr
			}
			return "failed", unknown, tx.Commit()
		}
		return "failed", "", err
	}
	kind, candidateID, validOperation := parseAgentCandidateOperation(operation)
	associationValid := taskNode == item.nodeID && taskBatch == item.batchID && taskItem == item.id && kind == "publish" && validOperation && candidateID == item.candidateID
	if !associationValid {
		unknown = "Agent 任务与发布批次/节点/候选关联不匹配；已暂停批次，请人工核对。"
	}
	result := "failed"
	message := unknown
	if taskStatus == "queued" {
		update, updateErr := tx.Exec("UPDATE orp_agent_task SET status='expired',error_text='控制面重启，未领取的发布任务已安全取消' WHERE id=? AND status='queued'", item.taskID.String)
		if updateErr != nil {
			return "failed", "", updateErr
		}
		if affected, _ := update.RowsAffected(); affected == 1 && associationValid {
			result, message = "aborted", "控制面重启，Agent 发布任务尚未领取，已安全取消"
		}
	} else if taskStatus == "completed" && resultStatus.Valid && resultStatus.String == "succeeded" && associationValid {
		result, message = "success", ""
		if _, err := tx.Exec("UPDATE orp_resource SET document=JSON_SET(document,'$.activeVersion',?) WHERE kind='nodes' AND id=?", item.releaseID, item.nodeID); err != nil {
			return "failed", "", err
		}
	}
	if _, err := tx.Exec("UPDATE orp_publish_item SET status=?,error_text=?,finished_at=NOW(6) WHERE id=? AND status IN ('deploying','validating')", result, message, item.id); err != nil {
		return "failed", "", err
	}
	if err := tx.Commit(); err != nil {
		return "failed", "", err
	}
	return result, message, nil
}
