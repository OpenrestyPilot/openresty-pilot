package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type candidateQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type candidateQueryer interface {
	candidateQueryRower
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type candidateQueryWriter interface {
	candidateQueryer
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func buildCandidateDiffs(ctx context.Context, q candidateQueryWriter, candidateID, centerID int64, candidateUUID string, targetIDs []int64, snapshot releaseSnapshot) error {
	for _, nodeID := range targetIDs {
		node := snapshot.Nodes[nodeID]
		if node == nil {
			return fmt.Errorf("候选快照缺少节点 %d", nodeID)
		}
		var baselineVersion, baselineDigest any
		baselineConfig := ""
		oldSnapshot := releaseSnapshot{Resources: map[string][]orpDocument{}, Singletons: map[string]any{}, Nodes: map[int64]orpDocument{}}
		baseline, err := readLastPublishedNode(ctx, q, nodeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("读取节点 %d 发布基线失败: %w", nodeID, err)
		}
		if err == nil {
			baselineVersion, baselineDigest, baselineConfig = baseline.ReleaseUUID, baseline.Digest, baseline.Config
			var oldRaw []byte
			if err := q.QueryRowContext(ctx, "SELECT snapshot FROM orp_publish_candidate WHERE release_uuid=?", baseline.ReleaseUUID).Scan(&oldRaw); err != nil {
				return fmt.Errorf("读取节点 %d 基线快照失败: %w", nodeID, err)
			}
			oldSnapshot, err = snapshotConfig(oldRaw)
			if err != nil {
				return fmt.Errorf("解析节点 %d 基线快照失败: %w", nodeID, err)
			}
		}
		var release orpRelease
		if target, targetErr := targetForNode(node); targetErr == nil {
			release, err = renderDemoReleaseForTarget(centerID, candidateUUID, snapshot.Resources, snapshot.Singletons, target.healthPort)
		} else {
			release, err = renderReleaseForTarget(centerID, candidateUUID, snapshot.Resources, snapshot.Singletons, agentProbePort)
		}
		if err != nil {
			return fmt.Errorf("为节点 %d 生成 Diff 配置失败: %w", nodeID, err)
		}
		semantic := semanticReleaseDiff(oldSnapshot, snapshot)
		textLines, added, deleted := publishTextDiff(baselineConfig, release.Config)
		semanticJSON, err := json.Marshal(semantic)
		if err != nil {
			return err
		}
		textJSON, err := json.Marshal(textLines)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO orp_configuration_candidate_diff
(candidate_id,node_id,baseline_version,baseline_digest,candidate_digest,semantic_diff,text_diff,added_lines,deleted_lines)
VALUES(?,?,?,?,?,?,?,?,?)`, candidateID, nodeID, baselineVersion, baselineDigest, release.Digest, semanticJSON, textJSON, added, deleted); err != nil {
			return fmt.Errorf("保存节点 %d 候选 Diff 失败: %w", nodeID, err)
		}
	}
	return nil
}

func readLastPublishedNode(ctx context.Context, q candidateQueryRower, nodeID int64) (publishedNode, error) {
	var item publishedNode
	err := q.QueryRowContext(ctx, `SELECT c.release_uuid,c.config_text,c.digest,i.finished_at,i.batch_id
FROM orp_publish_item i JOIN orp_publish_candidate c ON c.id=i.candidate_id
WHERE i.node_id=? AND i.status='success' ORDER BY i.finished_at DESC,i.id DESC LIMIT 1`, nodeID).Scan(&item.ReleaseUUID, &item.Config, &item.Digest, &item.FinishedAt, &item.BatchID)
	return item, err
}

func loadCandidateDiffs(ctx context.Context, q candidateQueryer, candidateID int64) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, `SELECT node_id,baseline_version,baseline_digest,candidate_digest,semantic_diff,text_diff,added_lines,deleted_lines
FROM orp_configuration_candidate_diff WHERE candidate_id=? ORDER BY node_id`, candidateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var nodeID int64
		var baselineVersion, baselineDigest sql.NullString
		var candidateDigest string
		var semanticRaw, textRaw []byte
		var added, deleted int
		if err := rows.Scan(&nodeID, &baselineVersion, &baselineDigest, &candidateDigest, &semanticRaw, &textRaw, &added, &deleted); err != nil {
			return nil, err
		}
		var semantic, text []map[string]any
		if err := json.Unmarshal(semanticRaw, &semantic); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(textRaw, &text); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"nodeId": nodeID, "baselineVersion": nullString(baselineVersion), "baselineDigest": nullString(baselineDigest), "candidateDigest": candidateDigest, "semanticDiff": semantic, "textDiff": text, "addedLines": added, "deletedLines": deleted})
	}
	return items, rows.Err()
}
