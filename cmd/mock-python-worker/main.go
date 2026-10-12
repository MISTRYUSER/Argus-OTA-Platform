// Mock aggregation worker; actual C++/Python processing remains a separate integration.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func getEnv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	db, err := sql.Open("postgres", getEnv("DATABASE_URL", "postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	consumer, err := kafka.NewKafkaEventConsumer(strings.Split(getEnv("KAFKA_BROKERS", "localhost:9092"), ","), "mock-python-worker-group", kafka.WithFailureHandler(application.NewOrchestrateService(postgres.NewPostgresBatchRepository(db), nil, nil).HandleFailure), kafka.WithDeadLetterStore(postgres.DeadLetterStore(db)))
	if err != nil {
		log.Fatal(err)
	}
	defer consumer.Close()
	err = consumer.Subscribe(ctx, []string{getEnv("KAFKA_TOPIC", "batch-events")}, func(ctx context.Context, data []byte) error {
		var e struct {
			Type string `json:"event_type"`
			ID   string `json:"batch_id"`
		}
		if err := json.Unmarshal(data, &e); err != nil {
			return err
		}
		if e.Type != "AllFilesScattered" {
			return nil
		}
		id, err := uuid.Parse(e.ID)
		if err != nil {
			return err
		}
		return postgres.MockAggregateBatch(ctx, db, id)
	})
	if err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
	<-ctx.Done()
}
