package main

import (
	"context"
	"database/sql"
	_ "github.com/lib/pq"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	db, err := sql.Open("postgres", env("DATABASE_URL", "postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	publisher, err := kafka.NewKafkaEventProducer(strings.Split(env("KAFKA_BROKERS", "localhost:9092"), ","), env("KAFKA_TOPIC", "batch-events"), "")
	if err != nil {
		log.Fatal(err)
	}
	defer publisher.Close()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := postgres.DispatchOutbox(ctx, db, publisher, 100); err != nil && ctx.Err() == nil {
			log.Printf("outbox retry: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
