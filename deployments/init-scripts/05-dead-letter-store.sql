-- Full failed payloads are retained here; Kafka DLQ records carry a bounded reference.
CREATE TABLE IF NOT EXISTS dead_letter_messages (
 id UUID PRIMARY KEY, topic TEXT NOT NULL, partition_id INTEGER NOT NULL, message_offset BIGINT NOT NULL,
 payload BYTEA NOT NULL, payload_sha256 TEXT NOT NULL, reason TEXT NOT NULL,
 created_at TIMESTAMP NOT NULL DEFAULT NOW(),
 UNIQUE(topic,partition_id,message_offset,payload_sha256)
);
