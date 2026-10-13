package processing

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	_ "github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	pg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
)

func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func RunWorker(stage string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	db, e := sql.Open("postgres", env("DATABASE_URL", "postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable"))
	if e != nil {
		return e
	}
	defer db.Close()
	if e = db.PingContext(ctx); e != nil {
		return e
	}
	p := Processor{DB: db, Python: Python{Executable: env("ARGUS_PYTHON", "python3"), Script: env("ARGUS_ANALYSIS_SCRIPT", "workers/python-aggregator/regression.py"), Baseline: os.Getenv("ARGUS_BASELINE_VERSION"), Candidate: os.Getenv("ARGUS_CANDIDATE_VERSION"), RecordMetadata: os.Getenv("ARGUS_RECORD_METADATA_JSON")}}
	if stage == "parse" {
		secure, e := strconv.ParseBool(env("MINIO_USE_SSL", "false"))
		if e != nil {
			return fmt.Errorf("invalid MINIO_USE_SSL")
		}
		client, e := minio.New(env("MINIO_ENDPOINT", "localhost:9000"), &minio.Options{Creds: credentials.NewStaticV4(env("MINIO_ACCESS_KEY", "minioadmin"), env("MINIO_SECRET_KEY", "minioadmin"), ""), Secure: secure})
		if e != nil {
			return e
		}
		p.Fetch = func(ctx context.Context, key string) (io.ReadCloser, error) {
			return client.GetObject(ctx, env("MINIO_BUCKET", "argus-logs"), key, minio.GetObjectOptions{})
		}
	}
	failure := application.NewOrchestrateService(pg.NewPostgresBatchRepository(db), nil, nil).HandleFailure
	consumer, e := kafka.NewKafkaEventConsumer(strings.Split(env("KAFKA_BROKERS", "localhost:9092"), ","), "real-"+stage+"-worker-v1", kafka.WithFailureHandler(failure), kafka.WithDeadLetterStore(pg.DeadLetterStore(db)))
	if e != nil {
		return e
	}
	defer consumer.Close()
	if e = consumer.Subscribe(ctx, []string{env("KAFKA_TOPIC", "batch-events")}, func(ctx context.Context, data []byte) error { return p.HandleMessage(ctx, data, stage) }); e != nil {
		return e
	}
	<-ctx.Done()
	return nil
}
