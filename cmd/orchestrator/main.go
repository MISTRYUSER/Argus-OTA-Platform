package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	redisinfra "github.com/xuewentao/argus-ota-platform/internal/infrastructure/redis"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
	_ "github.com/lib/pq"
)

func main() {
	ctx := context.Background()

	// 1. 初始化 PostgreSQL
	db := initDB()

	// 2. 初始化 Redis
	redisClient := initRedis(ctx)

	// 3. 初始化 Kafka Producer（发布事件）
	kafkaProducer := initKafkaProducer()

	// 4. 初始化 Kafka Consumer（消费事件）
	kafkaConsumer, err := kafka.NewKafkaEventConsumer(
		[]string{"localhost:9092"},
		"orchestrator-group", // Consumer Group ID
	)
	if err != nil {
		log.Fatalf("Failed to create Kafka consumer: %v", err)
	}

	// 5. 初始化 Repository
	batchRepo := postgres.NewPostgresBatchRepository(db)

	// 6. 初始化 OrchestrateService
	orchestrateService := application.NewOrchestrateService(
		batchRepo,
		redisClient,
		kafkaProducer,
	)

	// 7. 启动 Kafka Consumer
	topics := []string{"batch-events"}

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

	producer, err := kafka.NewKafkaEventProducer(brokers, topic)
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
