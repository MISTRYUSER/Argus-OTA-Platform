package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
	"time"
)

func EnqueueEvent(ctx context.Context, tx *sql.Tx, event domain.DomainEvent) error {
	payload, err := domain.MarshalEvent(event)
	if err != nil {
		return err
	}
	query := `INSERT INTO event_outbox(id,event_key,batch_id,event_type,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_key) DO NOTHING`
	if e, ok := event.(domain.WireEvent); ok && e.Data["retry"] == true {
		query = `INSERT INTO event_outbox(id,event_key,batch_id,event_type,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_key) DO UPDATE SET published_at=NULL,created_at=NOW(),payload=EXCLUDED.payload WHERE event_outbox.published_at IS NOT NULL`
	}
	_, err = tx.ExecContext(ctx, query, uuid.New(), domain.EventKey(event), event.AggregateID(), event.EventType(), payload)
	return err
}

// DispatchOutbox marks a record only after publication succeeds. A crash can replay, never lose it.
func DispatchOutbox(ctx context.Context, db *sql.DB, publisher messaging.KafkaEventPublisher, limit int) (int, error) {
	if publisher == nil {
		return 0, fmt.Errorf("outbox publisher is nil")
	}
	if limit <= 0 {
		limit = 100
	}
	sent := 0
	for sent < limit {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return sent, err
		}
		var id, batchID uuid.UUID
		var typ string
		var body json.RawMessage
		var created time.Time
		err = tx.QueryRowContext(ctx, `SELECT id,batch_id,event_type,payload,created_at FROM event_outbox WHERE published_at IS NULL ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &batchID, &typ, &body, &created)
		if err == sql.ErrNoRows {
			tx.Rollback()
			return sent, nil
		}
		if err != nil {
			tx.Rollback()
			return sent, err
		}
		event := domain.WireEvent{Type: typ, BatchID: batchID, Timestamp: created, Raw: body}
		if err = publisher.PublishEvents(ctx, []domain.DomainEvent{event}); err != nil {
			tx.Rollback()
			return sent, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE event_outbox SET published_at=NOW() WHERE id=$1`, id); err != nil {
			tx.Rollback()
			return sent, err
		}
		if err = tx.Commit(); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}
