package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	redisinfra "github.com/xuewentao/argus-ota-platform/internal/infrastructure/redis"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 1. 初始化 PostgreSQL
	db := initDB()

	// 2. 初始化 Redis
	redisClient := initRedis(ctx)

	// 3. 初始化 Kafka Producer（发布事件）
	kafkaProducer := initKafkaProducer()

	// 5. 初始化 Repository
	batchRepo := postgres.NewPostgresBatchRepository(db)

	// 6. 初始化 OrchestrateService
	orchestrateService := application.NewOrchestrateService(
		batchRepo,
		redisClient,
		kafkaProducer,
	)

	// 4. 初始化 Kafka Consumer（消费事件）
	kafkaConsumer, err := kafka.NewKafkaEventConsumer(
		strings.Split(getEnv("KAFKA_BROKERS", "localhost:9092"), ","),
		"orchestrator-group", kafka.WithFailureHandler(orchestrateService.HandleFailure), kafka.WithDeadLetterStore(postgres.DeadLetterStore(db)), // Consumer Group ID
	)
	if err != nil {
		log.Fatalf("Failed to create Kafka consumer: %v", err)
	}

	// 7. 启动 Kafka Consumer
	topics := []string{getEnv("KAFKA_TOPIC", "batch-events")}

	log.Println("========================================")
	log.Println("🚀 Orchestrator started successfully!")
	log.Printf("📡 Consuming topic: %s", topics[0])
	log.Printf("📦 Consumer Group: orchestrator-group")
	log.Println("========================================")

	// 8. 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 在后台 goroutine 中消费消息
	go func() {
		if err := kafkaConsumer.Subscribe(ctx, topics, orchestrateService.HandleMessage); err != nil {
			log.Printf("Consumer error: %v", err)
		}
	}()

	// 启动补偿任务（后台 goroutine）
	go compensationJob(ctx, orchestrateService, batchRepo)

	// 等待系统信号
	<-sigCh
	log.Println("\n🛑 Shutting down Orchestrator...")

	// 关闭 Kafka Consumer
	if err := kafkaConsumer.Close(); err != nil {
		log.Printf("Failed to close Kafka consumer: %v", err)
	}

	// 关闭 Kafka Producer
	if err := kafkaProducer.Close(); err != nil {
		log.Printf("Failed to close Kafka producer: %v", err)
	}

	// 关闭 Redis
	if err := redisClient.Close(); err != nil {
		log.Printf("Failed to close Redis: %v", err)
	}

	// 关闭 PostgreSQL
	if err := db.Close(); err != nil {
		log.Printf("Failed to close PostgreSQL: %v", err)
	}

	log.Println("✅ Orchestrator stopped gracefully")
}

// initDB 初始化 PostgreSQL 连接
func initDB() *sql.DB {
	// 从环境变量读取配置
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "argus")
	dbPassword := getEnv("DB_PASSWORD", "argus_password")
	dbName := getEnv("DB_NAME", "argus_ota")

	// 构建 DSN
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName)

	// 连接数据库
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		dsn = databaseURL
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// 配置连接池
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Ping 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	log.Printf("[PostgreSQL] Connected to %s:%s/%s", dbHost, dbPort, dbName)
	return db
}

// initRedis 初始化 Redis 连接
func initRedis(ctx context.Context) *redisinfra.RedisClient {
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisPassword := getEnv("REDIS_PASSWORD", "")

	redisClient, err := redisinfra.NewRedisClient(ctx, redisAddr, redisPassword, 0)
	if err != nil {
		log.Fatalf("Failed to create Redis client: %v", err)
	}

	return redisClient
}

// initKafkaProducer 初始化 Kafka Producer
func initKafkaProducer() messaging.KafkaEventPublisher {
	brokers := []string{getEnv("KAFKA_BROKERS", "localhost:9092")}
	topic := getEnv("KAFKA_TOPIC", "batch-events")
	dlqTopic := getEnv("KAFKA_DLQ_TOPIC", "batch-events-dlq")

	producer, err := kafka.NewKafkaEventProducer(brokers, topic, dlqTopic)
	if err != nil {
		log.Fatalf("Failed to create Kafka producer: %v", err)
	}

	return producer
}

// getEnv 读取环境变量，提供默认值
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// compensationJob 补偿任务：定期检查并处理卡住的批次
func compensationJob(ctx context.Context, s *application.OrchestrateService, repo domain.BatchRepository) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		log.Printf("[Compensation] Checking for stuck batches...")

		// 1. 查询状态卡住的 Batch（通过 Repository）
		stuckBatches, err := repo.FindStuckBatches(ctx)
		if err != nil {
			log.Printf("[Compensation] Failed to find stuck batches: %v", err)
			continue
		}

		if len(stuckBatches) == 0 {
			log.Printf("[Compensation] No stuck batches found")
			continue
		}

		log.Printf("[Compensation] Found %d stuck batches", len(stuckBatches))

		// 2. 处理每个卡住的 Batch（通过 Service）
		for _, batch := range stuckBatches {
			if err := s.HandleStuckBatch(ctx, batch); err != nil {
				log.Printf("[Compensation] Failed to handle stuck batch %s: %v", batch.ID, err)
				// 继续处理下一个，不中断整个循环
			}
		}
	}
}
