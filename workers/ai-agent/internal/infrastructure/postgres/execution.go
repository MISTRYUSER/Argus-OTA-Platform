package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"time"
)

func (r *PostgresDiagnosisRepository) ExecuteOnce(ctx context.Context, id string, generate func(context.Context) (*domain.Diagnosis, error)) (result *domain.Diagnosis, err error) {
	batchID, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	id = batchID.String()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return nil, err
	}
	existing, err := find(ctx, tx, "batch_id", id)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Status == domain.DiagnosisStatusCompleted {
		if err = corepg.EnqueueEvent(ctx, tx, completionEvent(existing)); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if existing != nil && existing.Status == domain.DiagnosisStatusFailed {
		return nil, &core.PermanentTaskError{Err: fmt.Errorf("diagnosis terminally failed; reset required")}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO investigation_executions(batch_id) VALUES($1) ON CONFLICT DO NOTHING`, batchID); err != nil {
		return nil, err
	}
	var owner uuid.NullUUID
	var generation int64
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT owner,generation,COALESCE(lease_until>NOW(),FALSE) FROM investigation_executions WHERE batch_id=$1 FOR UPDATE`, batchID).Scan(&owner, &generation, &active); err != nil {
		return nil, err
	}
	// The prior owner may finalize while this claim waits for the execution row.
	// Re-read the result after acquiring that row lock before deciding to generate.
	existing, err = find(ctx, tx, "batch_id", id)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Status == domain.DiagnosisStatusCompleted {
		if err = corepg.EnqueueEvent(ctx, tx, completionEvent(existing)); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if existing != nil && existing.Status == domain.DiagnosisStatusFailed {
		return nil, &core.PermanentTaskError{Err: fmt.Errorf("diagnosis terminally failed; reset required")}
	}
	if owner.Valid && active {
		return nil, &core.DeferredTaskError{Reason: "another worker owns the investigation"}
	}
	lease := domain.ExecutionLease{BatchID: batchID, Owner: uuid.New(), Generation: generation + 1}
	if _, err = tx.ExecContext(ctx, `UPDATE investigation_executions SET owner=$2,generation=$3,lease_until=NOW()+INTERVAL '30 seconds',updated_at=NOW() WHERE batch_id=$1`, batchID, lease.Owner, lease.Generation); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			var deferred *core.DeferredTaskError
			var failure *domain.ExecutionFailure
			if !errors.As(err, &deferred) && !errors.As(err, &failure) {
				err = &domain.ExecutionFailure{Lease: lease, Err: err}
			}
		}
	}()
	runCtx, cancel := context.WithCancel(domain.WithExecutionLease(ctx, lease))
	done := make(chan struct{})
	go r.heartbeat(runCtx, cancel, lease, done)
	result, generateErr := generate(runCtx)
	cancel()
	<-done
	releaseCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	defer r.releaseExecution(releaseCtx, lease)
	if generateErr != nil {
		failureTx, e := r.db.BeginTx(releaseCtx, nil)
		if e != nil {
			return nil, &domain.ExecutionFailure{Lease: lease, Err: e}
		}
		defer failureTx.Rollback()
		if e = validateExecution(releaseCtx, failureTx, domain.WithExecutionLease(releaseCtx, lease), id); e != nil {
			return nil, e
		}
		var terminal *core.PermanentTaskError
		if errors.As(generateErr, &terminal) || errors.Is(generateErr, domain.ErrInvalidDiagnosis) || errors.Is(generateErr, domain.ErrInvestigationBudget) {
			old, e := find(releaseCtx, failureTx, "batch_id", id)
			if e != nil {
				return nil, e
			}
			if e = persistFailure(releaseCtx, failureTx, batchID, id, old, generateErr); e != nil {
				return nil, &domain.ExecutionFailure{Lease: lease, Err: e}
			}
			if _, e = failureTx.ExecContext(releaseCtx, `UPDATE investigation_executions SET owner=NULL,lease_until=NULL,updated_at=NOW() WHERE batch_id=$1`, batchID); e != nil {
				return nil, e
			}
			if e = failureTx.Commit(); e != nil {
				return nil, e
			}
		}
		return nil, &domain.ExecutionFailure{Lease: lease, Err: generateErr}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	tx, err = r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = validateExecution(ctx, tx, domain.WithExecutionLease(ctx, lease), id); err != nil {
		return nil, err
	}
	if result == nil || result.BatchID != batchID || result.Status != domain.DiagnosisStatusCompleted {
		return nil, fmt.Errorf("invalid generated diagnosis ownership/status")
	}
	if err = save(ctx, tx, result); err != nil {
		return nil, err
	}
	if err = corepg.EnqueueEvent(ctx, tx, completionEvent(result)); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE investigation_executions SET owner=NULL,lease_until=NULL,updated_at=NOW() WHERE batch_id=$1`, batchID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
func completionEvent(d *domain.Diagnosis) core.DiagnosisCompleted {
	return core.DiagnosisCompleted{Version: "v1.0", BatchID: d.BatchID, DiagnosisID: d.ID, DiagnosisSummary: d.DiagnosisSummary, OccurredAt: time.Now()}
}
func (r *PostgresDiagnosisRepository) heartbeat(ctx context.Context, cancel context.CancelFunc, lease domain.ExecutionLease, done chan struct{}) {
	defer close(done)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			callCtx, stop := context.WithTimeout(ctx, 3*time.Second)
			res, err := r.db.ExecContext(callCtx, `UPDATE investigation_executions SET lease_until=NOW()+INTERVAL '30 seconds',updated_at=NOW() WHERE batch_id=$1 AND owner=$2 AND generation=$3 AND lease_until>NOW()`, lease.BatchID, lease.Owner, lease.Generation)
			stop()
			if err != nil {
				cancel()
				return
			}
			count, _ := res.RowsAffected()
			if count != 1 {
				cancel()
				return
			}
		}
	}
}
func (r *PostgresDiagnosisRepository) releaseExecution(ctx context.Context, lease domain.ExecutionLease) {
	r.db.ExecContext(ctx, `UPDATE investigation_executions SET owner=NULL,lease_until=NULL,updated_at=NOW() WHERE batch_id=$1 AND owner=$2 AND generation=$3`, lease.BatchID, lease.Owner, lease.Generation)
}
func validateExecution(ctx context.Context, tx *sql.Tx, ownerCtx context.Context, id string) error {
	var owner uuid.NullUUID
	var generation int64
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT owner,generation,COALESCE(lease_until>NOW(),FALSE) FROM investigation_executions WHERE batch_id=$1 FOR UPDATE`, id).Scan(&owner, &generation, &active)
	lease, hasLease := domain.ExecutionLeaseFromContext(ownerCtx)
	if err == sql.ErrNoRows && !hasLease {
		return nil
	}
	if err != nil {
		return err
	}
	if hasLease {
		if lease.BatchID.String() != id || !owner.Valid || owner.UUID != lease.Owner || generation != lease.Generation || !active {
			return &core.DeferredTaskError{Reason: "execution lease lost"}
		}
	} else if owner.Valid && active {
		return &core.DeferredTaskError{Reason: "active execution must not be overwritten"}
	}
	return nil
}
func (r *PostgresDiagnosisRepository) GetOrCreate(ctx context.Context, id string, generate func() (*domain.Diagnosis, error)) (*domain.Diagnosis, error) {
	for {
		result, err := r.ExecuteOnce(ctx, id, func(context.Context) (*domain.Diagnosis, error) { return generate() })
		var deferred *core.DeferredTaskError
		if !errors.As(err, &deferred) {
			return result, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
