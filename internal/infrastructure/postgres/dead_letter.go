package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

func DeadLetterStore(db *sql.DB) messaging.DeadLetterStore {
	return func(ctx context.Context, topic string, partition int32, offset int64, payload []byte, reason string) (string, error) {
		hash := fmt.Sprintf("%x", sha256.Sum256(payload))
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("%s:%d:%d:%s", topic, partition, offset, hash)))
		_, err := db.ExecContext(ctx, `INSERT INTO dead_letter_messages(id,topic,partition_id,message_offset,payload,payload_sha256,reason) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO UPDATE SET reason=EXCLUDED.reason`, id, topic, partition, offset, payload, hash, domain.SafeText(reason, 1500))
		if err != nil {
			return "", err
		}
		return "postgres:dead_letter_messages:" + id.String(), nil
	}
}
