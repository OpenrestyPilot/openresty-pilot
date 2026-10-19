package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type publishedNode struct {
	ReleaseUUID string
	Config      string
	Digest      string
	Snapshot    []byte
	FinishedAt  time.Time
	BatchID     int64
}

func (s *Server) lastPublishedNode(ctx context.Context, nodeID int64) (publishedNode, error) {
	var item publishedNode
	err := s.db.QueryRowContext(ctx, `SELECT c.release_uuid,c.config_text,c.digest,c.snapshot,i.finished_at,i.batch_id
FROM orp_publish_item i JOIN orp_publish_candidate c ON c.id=i.candidate_id
WHERE i.node_id=? AND i.status='success' ORDER BY i.finished_at DESC,i.id DESC LIMIT 1`, nodeID).Scan(&item.ReleaseUUID, &item.Config, &item.Digest, &item.Snapshot, &item.FinishedAt, &item.BatchID)
	return item, err
}

func (s *Server) currentCenterRelease(ctx context.Context, node orpDocument, centerID int64, releaseID string) (orpRelease, releaseSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return orpRelease{}, releaseSnapshot{}, err
	}
	defer tx.Rollback()
	resources, singletons, err := loadReleaseInput(tx, centerID)
	if err != nil {
		return orpRelease{}, releaseSnapshot{}, err
	}
	snapshot := releaseSnapshot{Resources: resources, Singletons: singletons}
	target, err := targetForNode(node)
	if err != nil {
		return orpRelease{}, releaseSnapshot{}, err
	}
	release, err := renderDemoReleaseForTarget(centerID, releaseID, resources, singletons, target.healthPort)
	if err != nil {
		return orpRelease{}, releaseSnapshot{}, err
	}
	// Candidate digests represent the center configuration before local demo
	// port mapping, so keep that stable while showing the exact node config.
	canonical, err := renderRelease(centerID, releaseID, resources, singletons)
	if err != nil {
		return orpRelease{}, releaseSnapshot{}, err
	}
	release.Digest = canonical.Digest
	if err := tx.Commit(); err != nil {
		return orpRelease{}, releaseSnapshot{}, err
	}
	return release, snapshot, nil
}

func (s *Server) orpPublishSummary(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:read")
	if !ok {
		return
	}
	nodes, err := s.orpLoad(r.Context(), "nodes")
	if err != nil {
		orpFailure(w, 500, "读取节点失败")
		return
	}
	groupsByID := map[int64][]map[string]any{}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	for _, node := range nodes {
		nodeID, _ := resourceNumber(node["id"])
		centerID, _ := resourceNumber(node["centerId"])
		if requested := r.URL.Query().Get("centerId"); requested != "" && requested != strconv.FormatInt(centerID, 10) {
			continue
		}
		if !accountHasCenterPermission(roles, "orp:publish", strconv.FormatInt(centerID, 10)) {
			continue
		}
		last, err := s.lastPublishedNode(r.Context(), nodeID)
		lastChange := any(nil)
		pending := errors.Is(err, sql.ErrNoRows)
		if err != nil && !pending {
			orpFailure(w, 500, "读取发布状态失败")
			return
		}
		if !pending {
			lastChange = last.FinishedAt.Format("2006-01-02 15:04:05")
			current, _, err := s.currentCenterRelease(r.Context(), node, centerID, last.ReleaseUUID)
			pending = err != nil || current.Digest != last.Digest
			if !pending {
				observed, err := observedDemoRelease(r.Context(), node)
				pending = err != nil || observed != last.ReleaseUUID
			}
		}
		if directives, ok := node["directives"].([]any); ok && len(directives) > 0 {
			pending = true
		}
		if !pending {
			continue
		}
		groupsByID[centerID] = append(groupsByID[centerID], map[string]any{
			"id": nodeID, "name": node["name"], "host": node["host"], "status": node["status"],
			"lastChangeAt": lastChange, "eventCount": s.publishEventCount(r.Context(), centerID, lastChange),
		})
	}
	centerIDs := make([]int64, 0, len(groupsByID))
	for id := range groupsByID {
		centerIDs = append(centerIDs, id)
	}
	sort.Slice(centerIDs, func(i, j int) bool { return centerIDs[i] < centerIDs[j] })
	groups := []map[string]any{}
	for _, centerID := range centerIDs {
		groups = append(groups, map[string]any{"centerId": centerID, "centerName": s.publishCenterName(r.Context(), centerID), "nodes": groupsByID[centerID]})
	}
	var activeID sql.NullInt64
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM orp_publish_batch WHERE phase IN ('running','canary_observing') ORDER BY id DESC LIMIT 1").Scan(&activeID)
	var active any
	if activeID.Valid {
		active = activeID.Int64
	}
	orpReply(w, 200, map[string]any{"activeBatchId": active, "groups": groups, "pendingCount": lenPendingGroups(groups)})
}

func lenPendingGroups(groups []map[string]any) int {
	total := 0
	for _, group := range groups {
		total += len(group["nodes"].([]map[string]any))
	}
	return total
}

const publishConfigAuditKinds = `'http-listeners','stream-services','upstream-groups','dns-resolvers','certificates','http-directives','stream-directives','http-log-format','stream-log-format','http-error-pages'`
const publishCenterlessAuditKinds = `'http-directives','stream-directives','http-log-format','stream-log-format','http-error-pages'`

func publishAuditScopeSQL() string {
	return `kind IN (` + publishConfigAuditKinds + `) AND (kind IN (` + publishCenterlessAuditKinds + `) OR JSON_UNQUOTE(JSON_EXTRACT(after_document,'$.centerId'))=? OR JSON_UNQUOTE(JSON_EXTRACT(before_document,'$.centerId'))=?)`
}

func publishAuditScopeArgs(centerID int64) []any {
	center := strconv.FormatInt(centerID, 10)
	return []any{center, center}
}

func (s *Server) publishEventCount(ctx context.Context, centerID int64, since any) int {
	if since == nil {
		return 0
	}
	var count int
	args := []any{since}
	args = append(args, publishAuditScopeArgs(centerID)...)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orp_audit WHERE created_at>? AND `+publishAuditScopeSQL(), args...).Scan(&count)
	return count
}

func (s *Server) orpPublishDiff(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "orp:read"); !ok {
		return
	}
	nodeID, err := strconv.ParseInt(r.URL.Query().Get("nodeId"), 10, 64)
	if err != nil || nodeID < 1 {
		orpFailure(w, 400, "节点 ID 无效")
		return
	}
	nodes, _, err := s.publishNodes(r.Context(), []int64{nodeID})
	if err != nil {
		orpFailure(w, 404, err.Error())
		return
	}
	if !s.publishNodesWithinScope(r, usernameFromToken(r.Header.Get("Authorization")), nodes) {
		orpFailure(w, http.StatusForbidden, "无权查看该节点所属中心")
		return
	}
	node := nodes[nodeID]
	if err := demoNodeReady(r.Context(), node); err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	centerID, _ := resourceNumber(node["centerId"])
	last, err := s.lastPublishedNode(r.Context(), nodeID)
	var lastPublishedAt any
	var runningText string
	var version int64 = 1
	releaseID := uuid.NewString()
	var baselineSnapshot *releaseSnapshot
	if err == nil {
		baseline, snapshotErr := snapshotConfig(last.Snapshot)
		if snapshotErr != nil {
			orpFailure(w, 500, "读取最近下发的资源快照失败")
			return
		}
		baselineSnapshot = &baseline
		observed, probeErr := observedDemoRelease(r.Context(), node)
		if probeErr != nil || observed != last.ReleaseUUID {
			orpFailure(w, 409, "节点实际运行版本与发布记录不一致，请检查节点")
			return
		}
		lastPublishedAt = last.FinishedAt.Format("2006-01-02 15:04:05")
		version = last.BatchID + 1
		releaseID = last.ReleaseUUID
		target, _ := targetForNode(node)
		root, pathErr := demoReleaseRoot(target)
		if pathErr != nil {
			orpFailure(w, 500, pathErr.Error())
			return
		}
		content, readErr := os.ReadFile(filepath.Join(root, "candidates", last.ReleaseUUID, "nginx.conf"))
		if readErr != nil {
			orpFailure(w, 500, "读取节点当前制品失败")
			return
		}
		runningText = string(content)
	} else if errors.Is(err, sql.ErrNoRows) {
		target, _ := targetForNode(node)
		root, pathErr := demoReleaseRoot(target)
		if pathErr != nil {
			orpFailure(w, 500, pathErr.Error())
			return
		}
		content, readErr := os.ReadFile(filepath.Join(root, "bootstrap", "nginx.conf"))
		if readErr != nil {
			orpFailure(w, 500, "读取节点初始配置失败")
			return
		}
		runningText = string(content)
	} else {
		orpFailure(w, 500, "读取节点发布记录失败")
		return
	}
	current, currentSnapshot, err := s.currentCenterRelease(r.Context(), node, centerID, releaseID)
	if err != nil {
		orpFailure(w, 409, err.Error())
		return
	}
	lines, addCount, delCount := publishTextDiff(runningText, current.Config)
	resourceDiff := []map[string]any{}
	if baselineSnapshot != nil {
		resourceDiff = semanticReleaseDiff(*baselineSnapshot, currentSnapshot)
	}
	events := s.publishEvents(r.Context(), centerID, lastPublishedAt)
	orpReply(w, 200, map[string]any{"centerId": centerID, "centerName": s.publishCenterName(r.Context(), centerID), "node": map[string]any{"id": nodeID, "name": node["name"], "host": node["host"], "status": node["status"]}, "version": version, "lastPublishedAt": lastPublishedAt, "runningText": runningText, "expectedText": current.Config, "diff": lines, "addCount": addCount, "delCount": delCount, "resourceDiffAvailable": baselineSnapshot != nil, "resourceDiff": resourceDiff, "events": events})
}

func publishTextDiff(before, after string) ([]map[string]any, int, int) {
	a, b := strings.Split(strings.TrimSuffix(before, "\n"), "\n"), strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	if len(a)*len(b) > 2000000 {
		return []map[string]any{{"t": -1, "s": before}, {"t": 1, "s": after}}, len(b), len(a)
	}
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] > dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	lines := []map[string]any{}
	i, j, added, deleted := 0, 0, 0, 0
	for i < len(a) || j < len(b) {
		if i < len(a) && j < len(b) && a[i] == b[j] {
			lines = append(lines, map[string]any{"t": 0, "s": a[i]})
			i, j = i+1, j+1
		} else if j < len(b) && (i == len(a) || dp[i][j+1] >= dp[i+1][j]) {
			lines = append(lines, map[string]any{"t": 1, "s": b[j]})
			j, added = j+1, added+1
		} else {
			lines = append(lines, map[string]any{"t": -1, "s": a[i]})
			i, deleted = i+1, deleted+1
		}
	}
	return lines, added, deleted
}

func (s *Server) publishEvents(ctx context.Context, centerID int64, since any) []map[string]any {
	// Without a successful publish there is no event baseline. Keep the drawer
	// consistent with the summary count instead of presenting all historical
	// center activity as changes since an unknown time.
	if since == nil {
		return []map[string]any{}
	}
	query := `SELECT action,kind,resource_id,created_at FROM orp_audit WHERE ` + publishAuditScopeSQL() + ` AND created_at>?`
	args := publishAuditScopeArgs(centerID)
	args = append(args, since)
	query += " ORDER BY id DESC LIMIT 100"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var action, kind string
		var id int64
		var at time.Time
		if rows.Scan(&action, &kind, &id, &at) == nil {
			result = append(result, map[string]any{"action": action, "module": kind, "target": fmt.Sprintf("%s #%d", kind, id), "ts": at.Format("2006-01-02 15:04:05")})
		}
	}
	return result
}

func (s *Server) orpPublishHistory(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:read")
	if !ok {
		return
	}
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("pageSize"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 200 {
		pageSize = 200
	}
	where := " WHERE i.status IN ('success','failed','aborted')"
	args := []any{}
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		orpFailure(w, 500, "读取权限失败")
		return
	}
	if requested := query.Get("centerId"); requested != "" && !accountHasCenterPermission(roles, "orp:publish", requested) {
		orpFailure(w, http.StatusForbidden, "无权查看该中心的发布历史")
		return
	}
	if !accountHasGlobalCenterPermission(roles, "orp:publish") && query.Get("centerId") == "" {
		centerIDs, _ := accountCenterIDs(roles, "orp:publish")
		if len(centerIDs) == 0 {
			where += " AND 1=0"
		} else {
			where += " AND i.center_id IN (" + strings.TrimRight(strings.Repeat("?,", len(centerIDs)), ",") + ")"
			for _, centerID := range centerIDs {
				args = append(args, centerID)
			}
		}
	}
	for _, field := range []string{"centerId", "nodeId"} {
		if raw := query.Get(field); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id < 1 {
				orpFailure(w, 400, "筛选 ID 无效")
				return
			}
			column := "i.center_id"
			if field == "nodeId" {
				column = "i.node_id"
			}
			where += " AND " + column + "=?"
			args = append(args, id)
		}
	}
	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM orp_publish_item i"+where, args...).Scan(&total); err != nil {
		orpFailure(w, 500, "读取发布历史失败")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT i.id,i.batch_id,i.candidate_id,i.node_id,i.center_id,i.status,b.actor,b.created_at,i.started_at,i.finished_at
FROM orp_publish_item i JOIN orp_publish_batch b ON b.id=i.batch_id`+where+` ORDER BY i.id DESC LIMIT ? OFFSET ?`, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		orpFailure(w, 500, "读取发布历史失败")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, batchID, candidateID, nodeID, centerID int64
		var status, actor string
		var created time.Time
		var started, finished sql.NullTime
		if err := rows.Scan(&id, &batchID, &candidateID, &nodeID, &centerID, &status, &actor, &created, &started, &finished); err != nil {
			orpFailure(w, 500, "读取发布历史失败")
			return
		}
		var nodeName string
		_ = s.db.QueryRowContext(r.Context(), "SELECT JSON_UNQUOTE(JSON_EXTRACT(document,'$.name')) FROM orp_resource WHERE kind='nodes' AND id=?", nodeID).Scan(&nodeName)
		duration := int64(0)
		if started.Valid && finished.Valid {
			duration = finished.Time.Sub(started.Time).Milliseconds()
		}
		var semanticRaw, textRaw []byte
		_ = s.db.QueryRowContext(r.Context(), "SELECT semantic_diff,text_diff FROM orp_publish_precheck WHERE candidate_id=? AND node_id=? ORDER BY id DESC LIMIT 1", candidateID, nodeID).Scan(&semanticRaw, &textRaw)
		semantic := []map[string]any{}
		lines := []map[string]any{}
		_ = json.Unmarshal(semanticRaw, &semantic)
		_ = json.Unmarshal(textRaw, &lines)
		modulesSet := map[string]bool{}
		events := []map[string]any{}
		for _, change := range semantic {
			module := toString(change["kind"])
			modulesSet[module] = true
			events = append(events, map[string]any{"action": change["action"], "module": module, "target": change["id"], "ts": created.Format("2006-01-02 15:04:05")})
		}
		modules := []string{}
		for module := range modulesSet {
			modules = append(modules, module)
		}
		sort.Strings(modules)
		addCount, delCount := 0, 0
		for _, line := range lines {
			switch line["t"] {
			case float64(1):
				addCount++
			case float64(-1):
				delCount++
			}
		}
		if len(modules) == 0 && addCount+delCount > 0 {
			modules = append(modules, "rendered-config")
			events = append(events, map[string]any{"action": "update", "module": "rendered-config", "target": "nginx.conf", "ts": created.Format("2006-01-02 15:04:05")})
		}
		items = append(items, map[string]any{"id": id, "batchId": batchID, "centerId": centerID, "centerName": s.publishCenterName(r.Context(), centerID), "nodeId": nodeID, "nodeName": nodeName, "operator": actor, "createdAt": created.Format("2006-01-02 15:04:05"), "durationMs": duration, "result": status, "addCount": addCount, "delCount": delCount, "modules": modules, "events": events})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取发布历史失败")
		return
	}
	orpReply(w, 200, map[string]any{"items": items, "total": total})
}

func (s *Server) orpPublishHistoryStats(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "orp:read")
	if !ok {
		return
	}
	var total, success, failed, batches, abortedBatches int
	centerID := r.URL.Query().Get("centerId")
	where, args := "", []any{}
	if centerID != "" {
		roles, roleErr := s.accountRoles(r, actor)
		if roleErr != nil || !accountHasCenterPermission(roles, "orp:publish", centerID) {
			orpFailure(w, http.StatusForbidden, "无权查看该中心的发布统计")
			return
		}
		where, args = " AND center_id=?", []any{centerID}
	} else {
		roles, err := s.accountRoles(r, actor)
		if err != nil {
			orpFailure(w, 500, "读取权限失败")
			return
		}
		if !accountHasGlobalCenterPermission(roles, "orp:publish") {
			centerIDs, _ := accountCenterIDs(roles, "orp:publish")
			if len(centerIDs) == 0 {
				where = " AND 1=0"
			} else {
				where = " AND center_id IN (" + strings.TrimRight(strings.Repeat("?,", len(centerIDs)), ",") + ")"
				for _, id := range centerIDs {
					args = append(args, id)
				}
			}
		}
	}
	query := "SELECT COUNT(*),COALESCE(SUM(status='success'),0),COALESCE(SUM(status='failed'),0) FROM orp_publish_item WHERE status IN ('success','failed','aborted')" + where
	if err := s.db.QueryRowContext(r.Context(), query, args...).Scan(&total, &success, &failed); err != nil && !errors.Is(err, sql.ErrNoRows) {
		orpFailure(w, 500, "读取统计失败")
		return
	}
	batchQuery := "SELECT COUNT(DISTINCT batch_id) FROM orp_publish_item WHERE 1=1" + where
	_ = s.db.QueryRowContext(r.Context(), batchQuery, args...).Scan(&batches)
	abortedQuery := "SELECT COUNT(DISTINCT b.id) FROM orp_publish_batch b JOIN orp_publish_item i ON i.batch_id=b.id WHERE b.phase='aborted'" + strings.ReplaceAll(where, "center_id", "i.center_id")
	_ = s.db.QueryRowContext(r.Context(), abortedQuery, args...).Scan(&abortedBatches)
	rate := float64(0)
	if total > 0 {
		rate = float64(success) * 100 / float64(total)
	}
	topCenters := []map[string]any{}
	rows, err := s.db.QueryContext(r.Context(), "SELECT center_id,COUNT(*) AS amount FROM orp_publish_item WHERE status IN ('success','failed','aborted') GROUP BY center_id ORDER BY amount DESC LIMIT 5")
	if err != nil {
		orpFailure(w, 500, "读取中心统计失败")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var centerID int64
		var count int
		if err := rows.Scan(&centerID, &count); err != nil {
			orpFailure(w, 500, "读取中心统计失败")
			return
		}
		percent := float64(0)
		if total > 0 {
			percent = float64(count) * 100 / float64(total)
		}
		topCenters = append(topCenters, map[string]any{"name": s.publishCenterName(r.Context(), centerID), "count": count, "percent": percent})
	}
	if err := rows.Err(); err != nil {
		orpFailure(w, 500, "读取中心统计失败")
		return
	}
	orpReply(w, 200, map[string]any{"totalCount": total, "successCount": success, "failCount": failed, "successRate": rate, "totalBatches": batches, "abortedBatches": abortedBatches, "topCenters": topCenters})
}

func snapshotConfig(value []byte) (releaseSnapshot, error) {
	var snapshot releaseSnapshot
	err := json.Unmarshal(value, &snapshot)
	return snapshot, err
}

func semanticReleaseDiff(before, after releaseSnapshot) []map[string]any {
	result := []map[string]any{}
	kinds := map[string]bool{}
	for kind := range before.Resources {
		kinds[kind] = true
	}
	for kind := range after.Resources {
		kinds[kind] = true
	}
	orderedKinds := make([]string, 0, len(kinds))
	for kind := range kinds {
		orderedKinds = append(orderedKinds, kind)
	}
	sort.Strings(orderedKinds)
	for _, kind := range orderedKinds {
		oldByID, newByID := map[string]orpDocument{}, map[string]orpDocument{}
		for _, item := range before.Resources[kind] {
			oldByID[fmt.Sprint(item["id"])] = item
		}
		for _, item := range after.Resources[kind] {
			newByID[fmt.Sprint(item["id"])] = item
		}
		ids := map[string]bool{}
		for id := range oldByID {
			ids[id] = true
		}
		for id := range newByID {
			ids[id] = true
		}
		orderedIDs := make([]string, 0, len(ids))
		for id := range ids {
			orderedIDs = append(orderedIDs, id)
		}
		sort.Strings(orderedIDs)
		for _, id := range orderedIDs {
			old, hasOld := oldByID[id]
			fresh, hasNew := newByID[id]
			if !hasOld {
				result = append(result, map[string]any{"kind": kind, "id": id, "action": "create"})
				continue
			}
			if !hasNew {
				result = append(result, map[string]any{"kind": kind, "id": id, "action": "delete"})
				continue
			}
			fields := map[string]bool{}
			for key := range old {
				fields[key] = true
			}
			for key := range fresh {
				fields[key] = true
			}
			changed := []string{}
			for key := range fields {
				left, _ := json.Marshal(old[key])
				right, _ := json.Marshal(fresh[key])
				if string(left) != string(right) {
					changed = append(changed, key)
				}
			}
			sort.Strings(changed)
			if len(changed) > 0 {
				result = append(result, map[string]any{"kind": kind, "id": id, "action": "update", "fields": changed})
			}
		}
	}
	for _, name := range []string{"http-directives", "stream-directives", "http-log-format", "stream-log-format", "http-error-pages"} {
		left, _ := json.Marshal(before.Singletons[name])
		right, _ := json.Marshal(after.Singletons[name])
		if string(left) != string(right) {
			result = append(result, map[string]any{"kind": name, "id": "global", "action": "update"})
		}
	}
	return result
}
