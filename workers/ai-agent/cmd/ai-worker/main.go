package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/joho/godotenv"
	redisclient "github.com/redis/go-redis/v9"
	corekafka "github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	obs "github.com/xuewentao/argus-ota-platform/internal/observability"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/config"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	agentkafka "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/pgvector"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
	progress "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/redis"
	query "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/telemetryquery"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	_ = godotenv.Load(".env.local", ".env")
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	shutdownTelemetry, e := obs.SetupFromEnv(ctx, "argus-ai-agent")
	if e != nil {
		return e
	}
	defer func() {
		flushCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = shutdownTelemetry(flushCtx)
	}()
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.Concurrency + 5)
	if err = db.PingContext(ctx); err != nil {
		return err
	}
	repo := postgres.NewPostgresDiagnoseRepository(db)
	var retriever domain.VectorRetriever = pgvector.NewTextRetriever(db)
	if cfg.Embedding.APIKey != "" {
		embed, e := llm.NewEmbeddingModel(cfg.Embedding)
		if e != nil {
			return e
		}
		retriever, e = pgvector.NewPgvectorRetriever(db, embed)
		if e != nil {
			return e
		}
	}
	var publisher domain.EventPublisher
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		client := redisclient.NewClient(&redisclient.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD")})
		defer client.Close()
		if err = client.Ping(ctx).Err(); err != nil {
			return err
		}
		publisher = progress.NewEventPublisher(client)
	}
	options := []application.ServiceOption{application.WithConcurrency(cfg.Concurrency)}
	if os.Getenv("TELEMETRY_PROMETHEUS_URL") != "" {
		metrics := strings.Split(os.Getenv("TELEMETRY_ALLOWED_METRICS"), ",")
		reader, e := query.NewClient(query.Config{PrometheusURL: os.Getenv("TELEMETRY_PROMETHEUS_URL"), LokiURL: os.Getenv("TELEMETRY_LOKI_URL"), TempoURL: os.Getenv("TELEMETRY_TEMPO_URL"), AllowedMetrics: metrics}, query.NewPostgresScopeResolver(db))
		if e != nil {
			return e
		}
		policy := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join([]string{os.Getenv("TELEMETRY_PROMETHEUS_URL"), os.Getenv("TELEMETRY_LOKI_URL"), os.Getenv("TELEMETRY_TEMPO_URL"), strings.Join(metrics, ",")}, "|"))))
		options = append(options, application.WithTelemetryReader(reader, policy))
	}
	service, err := application.NewMultiAgentService(repo, retriever, publisher, cfg.LLM, options...)
	if err != nil {
		return err
	}
	if cfg.Mode == "once" {
		taskCtx, stop := context.WithTimeout(ctx, cfg.TaskTimeout)
		defer stop()
		if err = service.DiagnoseBatch(taskCtx, cfg.BatchID); err != nil {
			return err
		}
		log.Printf("diagnosis saved for batch %s", cfg.BatchID)
		return nil
	}
	handler := agentkafka.NewConsumer(nil, service)
	consumer, err := corekafka.NewKafkaEventConsumer(cfg.Brokers, cfg.Group, corekafka.WithFailureHandler(handler.HandleFailure), corekafka.WithDeadLetterStore(corepg.DeadLetterStore(db)))
	if err != nil {
		return err
	}
	defer consumer.Close()
	if err = consumer.Subscribe(ctx, []string{cfg.Topic}, func(ctx context.Context, body []byte) error {
		taskCtx, stop := context.WithTimeout(ctx, cfg.TaskTimeout)
		defer stop()
		return handler.HandleMessage(taskCtx, body)
	}); err != nil {
		return err
	}
	log.Printf("AI investigation worker started: model=%s topic=%s concurrency=%d", cfg.LLM.Model, cfg.Topic, cfg.Concurrency)
	<-ctx.Done()
	return nil
}
