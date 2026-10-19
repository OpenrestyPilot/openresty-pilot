package httpapi

import (
	"database/sql"
	"net/http"
	"strconv"
)

func (s *Server) publishBatchWithinScope(r *http.Request, actor string, batchID int64) bool {
	roles, err := s.accountRoles(r, actor)
	if err != nil {
		return false
	}
	if accountHasGlobalCenterPermission(roles, "orp:publish") {
		return true
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT DISTINCT CAST(center_id AS CHAR) FROM orp_publish_item WHERE batch_id=?", batchID)
	if err != nil {
		return false
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var centerID string
		if rows.Scan(&centerID) != nil || !accountHasCenterPermission(roles, "orp:publish", centerID) {
			return false
		}
		found = true
	}
	return found && rows.Err() == nil
}

func (s *Server) orpPublishAbort(w http.ResponseWriter, r *http.Request) {
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
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		orpFailure(w, 500, "中止批次失败")
		return
	}
	defer tx.Rollback()
	var phase string
	if err := tx.QueryRowContext(r.Context(), "SELECT phase FROM orp_publish_batch WHERE id=? FOR UPDATE", id).Scan(&phase); err != nil {
		if err == sql.ErrNoRows {
			orpFailure(w, 404, "批次不存在")
		} else {
			orpFailure(w, 500, "读取批次失败")
		}
		return
	}
	if phase == "done" || phase == "aborted" {
		orpFailure(w, 409, "批次已经结束")
		return
	}
	result, err := tx.ExecContext(r.Context(), "UPDATE orp_publish_item SET status='aborted',error_text='操作员中止批次',finished_at=NOW(6) WHERE batch_id=? AND status='queued'", id)
	if err != nil {
		orpFailure(w, 500, "中止节点任务失败")
		return
	}
	count, _ := result.RowsAffected()
	if _, err := tx.ExecContext(r.Context(), "UPDATE orp_publish_batch SET phase='aborted',finished_at=NOW(6) WHERE id=?", id); err != nil {
		orpFailure(w, 500, "中止批次失败")
		return
	}
	if err := tx.Commit(); err != nil {
		orpFailure(w, 500, "提交中止操作失败")
		return
	}
	if err := syncConfigurationCandidatePublishState(r.Context(), s.db, id); err != nil {
		orpFailure(w, 500, "批次已中止，但同步候选状态失败")
		return
	}
	orpReply(w, 200, map[string]any{"id": id, "phase": "aborted", "abortedCount": count})
}

func (s *Server) orpPublishAdvance(w http.ResponseWriter, r *http.Request) {
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
	result, err := s.db.ExecContext(r.Context(), "UPDATE orp_publish_batch SET phase='running',canary_observe_until=NOW(6) WHERE id=? AND phase='canary_observing'", id)
	if err != nil {
		orpFailure(w, 500, "推进批次失败")
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		orpFailure(w, 409, "批次不在观察期")
		return
	}
	orpReply(w, 200, map[string]any{"id": id, "phase": "running"})
}
