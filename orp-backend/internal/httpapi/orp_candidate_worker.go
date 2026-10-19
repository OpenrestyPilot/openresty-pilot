package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

// CandidateValidationExecutor keeps durable job scheduling independent from
// the concrete, allowlisted target validation adapters. A nil executor keeps
// work explicitly blocked instead of fabricating a successful result.
type CandidateValidationExecutor interface {
	ValidateCandidate(context.Context, string, []byte, int64) (command string, output string, err error)
}

// candidateValidationExecutor reuses only the fixed non-activating validators:
// the local Quickstart nginx -t path or the Agent's allowlisted validate task.
type candidateValidationExecutor struct{ server *Server }

func NewCandidateValidationExecutor(db *sql.DB) CandidateValidationExecutor {
	return candidateValidationExecutor{server: &Server{db: db}}
}

func (e candidateValidationExecutor) ValidateCandidate(ctx context.Context, candidateUUID string, snapshotRaw []byte, nodeID int64) (string, string, error) {
	var candidateID, centerID int64
	var storedSnapshot, targetsRaw []byte
	var storedConfig, storedDigest, actor string
	if err := e.server.db.QueryRowContext(ctx, "SELECT id,center_id,snapshot,target_ids,config_text,digest,actor FROM orp_configuration_candidate WHERE candidate_uuid=?", candidateUUID).Scan(&candidateID, &centerID, &storedSnapshot, &targetsRaw, &storedConfig, &storedDigest, &actor); err != nil {
		return "", "", errors.New("冻结候选不存在")
	}
	if !bytes.Equal(storedSnapshot, snapshotRaw) {
		return "", "", errors.New("worker 快照与持久化候选不一致")
	}
	var targets []int64
	if err := json.Unmarshal(targetsRaw, &targets); err != nil {
		return "", "", errors.New("冻结目标列表无效")
	}
	found := false
	for _, id := range targets {
		found = found || id == nodeID
	}
	if !found {
		return "", "", errors.New("校验节点不在冻结目标中")
	}
	snapshot, err := snapshotConfig(storedSnapshot)
	if err != nil {
		return "", "", err
	}
	node := snapshot.Nodes[nodeID]
	if node == nil {
		return "", "", errors.New("冻结快照缺少目标节点")
	}
	actualCenter, err := resourceNumber(node["centerId"])
	if err != nil || actualCenter != centerID {
		return "", "", errors.New("冻结节点中心与候选中心不一致")
	}
	if directives, _ := node["directives"].([]any); len(directives) != 0 {
		return "", "", errors.New("节点级原生指令不受候选渲染器支持")
	}
	baseRelease, err := renderRelease(centerID, candidateUUID, snapshot.Resources, snapshot.Singletons)
	if err != nil {
		return "", "", err
	}
	if baseRelease.Config != storedConfig || baseRelease.Digest != storedDigest {
		return "", "", errors.New("冻结候选内容与重新渲染结果不一致")
	}
	if target, targetErr := targetForNode(node); targetErr == nil {
		release, err := renderDemoReleaseForTarget(centerID, candidateUUID, snapshot.Resources, snapshot.Singletons, target.healthPort)
		if err != nil {
			return "", "", err
		}
		command := demoNginx + " -t -c " + demoConfigRoot + "/candidates/" + candidateUUID + "/nginx.conf"
		output, err := validateDemoRelease(ctx, node, candidateUUID, release)
		return command, output, err
	}
	if !nodeHasAgent(node) {
		return "", "", errors.New("目标节点没有可用的固定校验通道；需要 Quickstart 受控节点或已登记 Agent")
	}
	command := "Agent validate: fixed candidate nginx.conf (no shell or task-supplied paths)"
	output, err := e.server.validateAsyncAgentCandidate(ctx, nodeID, candidateID, actor)
	return command, output, err
}

type validationLease struct {
	jobID, attemptID, candidateID, centerID, nodeID int64
	candidateUUID, owner                            string
	snapshot                                        []byte
}

const validationLeaseDuration = 3 * time.Minute

// StartCandidateValidationWorker starts MySQL-backed job recovery and dispatch.
// When no executor is provided, the dispatcher records an explicit blocker.
func StartCandidateValidationWorker(ctx context.Context, db *sql.DB, executor CandidateValidationExecutor) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if err := (&Server{db: db}).recoverAutoCandidatePublishes(ctx); err != nil && ctx.Err() == nil {
				log.Printf("candidate auto-publish recovery: %v", err)
			}
			if err := recoverExpiredValidationLeases(ctx, db); err != nil && ctx.Err() == nil {
				log.Printf("candidate validation lease recovery: %v", err)
			}
			if executor == nil {
				if err := blockCandidateJobsWithoutAdapter(ctx, db); err != nil && ctx.Err() == nil {
					log.Printf("candidate validation adapter gate: %v", err)
				}
			} else {
				if err := requeueCandidateJobsWithAdapter(ctx, db); err != nil && ctx.Err() == nil {
					log.Printf("candidate validation adapter recovery: %v", err)
				}
				for i := 0; i < 16; i++ {
					lease, err := claimCandidateValidation(ctx, db)
					if errors.Is(err, sql.ErrNoRows) {
						break
					}
					if err != nil {
						if ctx.Err() == nil {
							log.Printf("candidate validation claim: %v", err)
						}
						break
					}
					go executeCandidateValidation(ctx, db, executor, lease)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// adapter_required is a durable blocker, not a terminal result. When a safe
// executor is configured, restore those jobs and attempts to the scheduler.
func requeueCandidateJobsWithAdapter(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE orp_validation_job SET state='queued',output_text=NULL WHERE state='adapter_required'"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE orp_validation_attempt SET state='queued',finished_at=NULL WHERE state='adapter_required'"); err != nil {
		return err
	}
	return tx.Commit()
}

func blockCandidateJobsWithoutAdapter(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE orp_validation_job SET state='adapter_required',output_text=? WHERE state='queued'", candidateAdapterBlockMessage); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE orp_validation_attempt SET state='adapter_required' WHERE state IN ('queued','running') AND NOT EXISTS (SELECT 1 FROM orp_validation_job j WHERE j.attempt_id=orp_validation_attempt.id AND j.state IN ('queued','running','leased'))"); err != nil {
		return err
	}
	return tx.Commit()
}

const candidateAdapterBlockMessage = "Non-activating target-node staging and nginx -t adapter is not configured"

// Expired work is not automatically retried: a remote command may have run
// before the control-plane lost its lease, so the result is explicitly unknown.
func recoverExpiredValidationLeases(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE orp_validation_job SET state='unknown',lease_owner=NULL,lease_until=NULL,finished_at=CURRENT_TIMESTAMP(6),output_text='Lease expired; node-side outcome is unknown' WHERE state='running' AND lease_until<CURRENT_TIMESTAMP(6)"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE orp_validation_attempt a SET state='paused',finished_at=NULL WHERE a.state='running' AND EXISTS(SELECT 1 FROM orp_validation_job j WHERE j.attempt_id=a.id AND j.state='unknown')"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE orp_validation_job j JOIN orp_validation_attempt a ON a.id=j.attempt_id SET j.state='paused' WHERE a.state='paused' AND j.state='queued'"); err != nil {
		return err
	}
	return tx.Commit()
}

func claimCandidateValidation(ctx context.Context, db *sql.DB) (validationLease, error) {
	var lease validationLease
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return lease, err
	}
	defer tx.Rollback()
	// Lock the center policy row to serialize per-center dispatch across control
	// plane instances and make the persisted cursor a shared round-robin cursor.
	var cursor int64
	var centerID int64
	err = tx.QueryRowContext(ctx, "SELECT p.center_id,p.last_candidate_id FROM orp_validation_center_policy p WHERE (SELECT COUNT(*) FROM orp_validation_job j0 JOIN orp_validation_attempt a0 ON a0.id=j0.attempt_id JOIN orp_configuration_candidate c0 ON c0.id=a0.candidate_id WHERE c0.center_id=p.center_id AND j0.state='running')<p.max_inflight AND EXISTS (SELECT 1 FROM orp_configuration_candidate c JOIN orp_validation_attempt a ON a.candidate_id=c.id JOIN orp_validation_job j ON j.attempt_id=a.id WHERE c.center_id=p.center_id AND a.state IN ('queued','running') AND j.state='queued') ORDER BY p.center_id LIMIT 1 FOR UPDATE SKIP LOCKED").Scan(&centerID, &cursor)
	if err != nil {
		return lease, err
	}
	var maxInflight int
	if err = tx.QueryRowContext(ctx, "SELECT max_inflight FROM orp_validation_center_policy WHERE center_id=?", centerID).Scan(&maxInflight); err != nil {
		return lease, err
	}
	var active int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM orp_validation_job j JOIN orp_validation_attempt a ON a.id=j.attempt_id JOIN orp_configuration_candidate c ON c.id=a.candidate_id WHERE c.center_id=? AND j.state='running'", centerID).Scan(&active); err != nil {
		return lease, err
	}
	if active >= maxInflight {
		return lease, sql.ErrNoRows
	}
	var candidateConcurrency int
	err = tx.QueryRowContext(ctx, "SELECT j.id,a.id,c.id,c.center_id,j.node_id,c.candidate_uuid,c.snapshot,a.concurrency_limit FROM orp_validation_job j JOIN orp_validation_attempt a ON a.id=j.attempt_id JOIN orp_configuration_candidate c ON c.id=a.candidate_id WHERE c.center_id=? AND a.state IN ('queued','running') AND j.state='queued' AND (SELECT COUNT(*) FROM orp_validation_job activeJob WHERE activeJob.attempt_id=a.id AND activeJob.state='running') < a.concurrency_limit ORDER BY CASE WHEN c.id>? THEN 0 ELSE 1 END,c.id,j.id LIMIT 1 FOR UPDATE SKIP LOCKED", centerID, cursor).Scan(&lease.jobID, &lease.attemptID, &lease.candidateID, &lease.centerID, &lease.nodeID, &lease.candidateUUID, &lease.snapshot, &candidateConcurrency)
	if err != nil {
		return lease, err
	}
	lease.owner = fmt.Sprintf("orp-control-plane-%d", time.Now().UnixNano())
	if _, err = tx.ExecContext(ctx, "UPDATE orp_validation_job SET state='running',lease_owner=?,lease_until=DATE_ADD(CURRENT_TIMESTAMP(6),INTERVAL 45 SECOND),started_at=COALESCE(started_at,CURRENT_TIMESTAMP(6)) WHERE id=?", lease.owner, lease.jobID); err != nil {
		return validationLease{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE orp_validation_attempt SET state='running',last_candidate_id=? WHERE id=?", lease.candidateID, lease.attemptID); err != nil {
		return validationLease{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE orp_validation_center_policy SET last_candidate_id=? WHERE center_id=?", lease.candidateID, lease.centerID); err != nil {
		return validationLease{}, err
	}
	if err = tx.Commit(); err != nil {
		return validationLease{}, err
	}
	return lease, nil
}

func executeCandidateValidation(ctx context.Context, db *sql.DB, executor CandidateValidationExecutor, lease validationLease) {
	jobCtx, cancel := context.WithTimeout(ctx, validationLeaseDuration)
	defer cancel()
	command, output, validationErr := executor.ValidateCandidate(jobCtx, lease.candidateUUID, lease.snapshot, lease.nodeID)
	state := "passed"
	if validationErr != nil {
		state = "failed"
		output += "\n" + validationErr.Error()
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("candidate validation result transaction: %v", err)
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE orp_validation_job SET state=?,command_text=?,output_text=?,lease_owner=NULL,lease_until=NULL,finished_at=CURRENT_TIMESTAMP(6) WHERE id=? AND state='running' AND lease_owner=?", state, command, output, lease.jobID, lease.owner)
	if err != nil {
		log.Printf("candidate validation result save: %v", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return
	} // A reaper or a newer lease already owns the truth.
	var attemptState string
	if err = tx.QueryRowContext(ctx, "SELECT state FROM orp_validation_attempt WHERE id=? FOR UPDATE", lease.attemptID).Scan(&attemptState); err != nil {
		log.Printf("candidate validation attempt read: %v", err)
		return
	}
	if state == "failed" && attemptState != "stopped" {
		if _, err = tx.ExecContext(ctx, "UPDATE orp_validation_attempt SET state='draining',failure_node_id=? WHERE id=?", lease.nodeID, lease.attemptID); err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE orp_validation_job SET state='paused' WHERE attempt_id=? AND state='queued'", lease.attemptID)
		}
		if err == nil {
			var inFlight int
			err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM orp_validation_job WHERE attempt_id=? AND state='running'", lease.attemptID).Scan(&inFlight)
			if err == nil && inFlight == 0 {
				_, err = tx.ExecContext(ctx, "UPDATE orp_validation_attempt SET state='paused' WHERE id=? AND state='draining'", lease.attemptID)
			}
		}
	} else {
		var unfinished int
		err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM orp_validation_job WHERE attempt_id=? AND state NOT IN ('passed','failed','unknown','cancelled','paused','adapter_required')", lease.attemptID).Scan(&unfinished)
		if err == nil && unfinished == 0 {
			if attemptState == "draining" {
				_, err = tx.ExecContext(ctx, "UPDATE orp_validation_attempt SET state='paused' WHERE id=?", lease.attemptID)
			} else if attemptState != "stopped" && attemptState != "paused" {
				var failures int
				err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM orp_validation_job WHERE attempt_id=? AND state IN ('failed','unknown')", lease.attemptID).Scan(&failures)
				if err == nil {
					finalState := "completed"
					if failures > 0 {
						finalState = "failed"
					}
					_, err = tx.ExecContext(ctx, "UPDATE orp_validation_attempt SET state=?,finished_at=CURRENT_TIMESTAMP(6) WHERE id=?", finalState, lease.attemptID)
				}
			}
		}
	}
	if err == nil {
		var actor string
		if err = tx.QueryRowContext(ctx, "SELECT actor FROM orp_configuration_candidate WHERE id=?", lease.candidateID).Scan(&actor); err == nil {
			evidence := adminAuditDocument(map[string]any{"candidateId": lease.candidateID, "attemptId": lease.attemptID, "nodeId": lease.nodeID, "result": state, "command": command, "output": output})
			err = orpAudit(ctx, tx, actor, "validation-result", "configuration-candidate", lease.candidateID, nil, evidence, "")
		}
	}
	if err != nil {
		log.Printf("candidate validation attempt update: %v", err)
		return
	}
	if err = tx.Commit(); err != nil {
		log.Printf("candidate validation result commit: %v", err)
		return
	}
	var finalAttemptState string
	if err := db.QueryRowContext(ctx, "SELECT state FROM orp_validation_attempt WHERE id=?", lease.attemptID).Scan(&finalAttemptState); err != nil {
		log.Printf("candidate validation final state read: %v", err)
		return
	}
	if finalAttemptState == "completed" {
		go (&Server{db: db}).autoPublishValidatedCandidate(lease.candidateID, lease.attemptID)
	} else if finalAttemptState == "failed" || finalAttemptState == "paused" || finalAttemptState == "stopped" {
		_, _ = db.ExecContext(ctx, `UPDATE orp_configuration_candidate SET publish_state='failed',publish_error='校验未全部通过或结果未知，不会自动发布'
WHERE id=? AND publish_on_validation_success=TRUE AND publish_batch_id IS NULL AND publish_state='waiting_validation'`, lease.candidateID)
	}
}
