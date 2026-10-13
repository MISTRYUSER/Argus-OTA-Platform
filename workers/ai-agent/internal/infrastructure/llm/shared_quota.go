package llm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"github.com/google/uuid"
	"time"
)

// SharedAdmission grants a permit for one physical Generate attempt. Release must
// run only once the model has returned, including cancellation cleanup.
type SharedAdmission interface {
	Acquire(context.Context) (func(), error)
}
type PostgresQuota struct {
	db             *sql.DB
	scope          string
	capacity       int
	request, grace time.Duration
}

func NewPostgresQuota(ctx context.Context, db *sql.DB, scope string, capacity int, request, grace time.Duration) (*PostgresQuota, error) {
	if db == nil || scope == "" || capacity < 1 || capacity > 128 || request < time.Millisecond || grace < time.Millisecond {
		return nil, fmt.Errorf("invalid shared model quota")
	}
	q := &PostgresQuota{db: db, scope: scope, capacity: capacity, request: request, grace: grace}
	_, err := db.ExecContext(ctx, `INSERT INTO model_quota_scopes(scope,capacity,request_ms,grace_ms) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, scope, capacity, request.Milliseconds(), grace.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("initialize shared model quota: %w", err)
	}
	var c int
	var r, g int64
	err = db.QueryRowContext(ctx, `SELECT capacity,request_ms,grace_ms FROM model_quota_scopes WHERE scope=$1`, scope).Scan(&c, &r, &g)
	if err != nil {
		return nil, err
	}
	if c != capacity || r != request.Milliseconds() || g != grace.Milliseconds() {
		return nil, fmt.Errorf("shared model quota scope policy mismatch")
	}
	return q, nil
}
func (q *PostgresQuota) Acquire(ctx context.Context) (func(), error) {
	conn, err := q.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: shared quota unavailable", ErrModelQueueFull)
	}
	// Bound detection of a vanished TCP peer; unlike an idle-session timeout,
	// keepalives do not evict a healthy session running a slow request. Persisted
	// expiry still guards a database restart that drops all advisory locks.
	if _, err = conn.ExecContext(ctx, `SET tcp_keepalives_idle=10; SET tcp_keepalives_interval=5; SET tcp_keepalives_count=3; SET tcp_user_timeout=25000`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: shared quota unavailable", ErrModelQueueFull)
	}
	id := uuid.New()
	var lock int64
	if err = conn.QueryRowContext(ctx, `SELECT hashtextextended($1,981735)`, id.String()).Scan(&lock); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: shared quota unavailable", ErrModelQueueFull)
	}
	// A live session lock prevents expiration from releasing an in-flight call,
	// even if its context is canceled or it takes longer than its deadline.
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lock).Scan(&locked); err != nil || !locked {
		conn.Close()
		return nil, fmt.Errorf("%w: shared quota unavailable", ErrModelQueueFull)
	}
	cleanup := func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, delErr := conn.ExecContext(releaseCtx, `DELETE FROM model_quota_permits WHERE id=$1`, id)
		var unlocked bool
		unlockErr := conn.QueryRowContext(releaseCtx, `SELECT pg_advisory_unlock($1)`, lock).Scan(&unlocked)
		if delErr != nil || unlockErr != nil || !unlocked {
			_ = conn.Raw(func(interface{}) error { return driver.ErrBadConn })
		}
		conn.Close()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("%w: shared quota unavailable", ErrModelQueueFull)
	}
	defer tx.Rollback()
	var capacity int
	var request, grace int64
	err = tx.QueryRowContext(ctx, `SELECT capacity,request_ms,grace_ms FROM model_quota_scopes WHERE scope=$1 FOR UPDATE`, q.scope).Scan(&capacity, &request, &grace)
	if err == nil && (capacity != q.capacity || request != q.request.Milliseconds() || grace != q.grace.Milliseconds()) {
		err = fmt.Errorf("scope policy changed")
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM model_quota_permits WHERE scope=$1 AND expires_at<=clock_timestamp() AND pg_try_advisory_xact_lock(lock_id)`, q.scope)
	}
	var count int
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM model_quota_permits WHERE scope=$1`, q.scope).Scan(&count)
	}
	if err == nil && count >= q.capacity {
		err = ErrModelQueueFull
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO model_quota_permits(id,scope,lock_id,expires_at) VALUES($1,$2,$3,clock_timestamp()+$4*INTERVAL '1 millisecond')`, id, q.scope, lock, (q.request + q.grace).Milliseconds())
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		tx.Rollback()
		cleanup()
		return nil, fmt.Errorf("%w: shared quota admission failed", ErrModelQueueFull)
	}
	return cleanup, nil
}
