package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
	"strings"
)

// Worker - Mock C++ Worker 结构体
type Worker struct {
	kafka     messaging.KafkaEventPublisher
	batchRepo domain.BatchRepository
	db        *sql.DB
}
type Config struct {
	Database DatabaseConfig
}
type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

// NewWorker 创建 Worker
func NewWorker(kafka messaging.KafkaEventPublisher, batchRepo domain.BatchRepository, db *sql.DB) *Worker {
	return &Worker{
		kafka:     kafka,
		batchRepo: batchRepo,
		db:        db,
	}
}

// HandleMessage 处理 Kafka 消息
func (w *Worker) HandleMessage(ctx context.Context, data []byte) error {
	// 1. 解析 JSON
	var event map[string]interface{}
	if err := json.Unmarshal(data, &event); err != nil {
		log.Printf("Failed to unmarshal event: %v", err)
		return err
	}

	eventType, ok := event["event_type"].(string)
	if !ok {
		log.Printf("Missing event_type in event")
		return fmt.Errorf("missing event_type")
	}

	// 2. 事件路由
	switch eventType {
	case "BatchCreated":
		return w.handleBatchCreated(ctx, event)

	case "StatusChanged":
		// Worker 不关心 StatusChanged 事件
		return nil

	default:
		log.Printf("[Worker] Unknown event type: %s", eventType)
	}

	return nil
}
func mustAtoi(s string, field string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		log.Fatalf("invalid %s: %s", field, s)
	}
	return i
}

// handleBatchCreated 处理 BatchCreated 事件
// 模拟 C++ Worker 解析 rec 文件
func (w *Worker) handleBatchCreated(ctx context.Context, event map[string]interface{}) error {
	batchIDStr, ok := event["batch_id"].(string)
	if !ok {
		log.Printf("[Worker] Missing batch_id in event")
		return fmt.Errorf("missing batch_id")
	}

	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		log.Printf("[Worker] Invalid batch_id: %v", err)
		return fmt.Errorf("invalid batch_id: %w", err)
	}
	return postgres.MockParseBatch(ctx, w.db, batchID)
}

func initDB(cfg *Config) *sql.DB {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.DBName,
	)
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		dsn = databaseURL
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal("Failed to open database : ", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5) // Idle 应该小于 Open
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Fatal("Failed to ping database:", err)
	}
	log.Println("[DB] Database connected successfully")
	return db
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 1. 初始化 Kafka Producer（发布事件）
	kafkaProducer := initKafkaProducer()

	//初始化 DB
	cfg := &Config{
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     mustAtoi(getEnv("DB_PORT", "5432"), "DB_PORT"),
			User:     getEnv("DB_USER", "argus"),
			Password: getEnv("DB_PASSWORD", "argus_password"),
			DBName:   getEnv("DB_NAME", "argus_ota"),
		},
	}
	db := initDB(cfg)
	batchRepo := postgres.NewPostgresBatchRepository(db)
	// 3. 创建 Worker
	worker := NewWorker(kafkaProducer, batchRepo, db)

	// 2. 初始化 Kafka Consumer（消费事件）
	kafkaConsumer, err := kafka.NewKafkaEventConsumer(
		strings.Split(getEnv("KAFKA_BROKERS", "localhost:9092"), ","),
		"cpp-worker-group-v2", kafka.WithFailureHandler(application.NewOrchestrateService(batchRepo, nil, nil).HandleFailure), kafka.WithDeadLetterStore(postgres.DeadLetterStore(db)), // Consumer Group ID (new for testing)
	)
	if err != nil {
		log.Fatalf("Failed to create Kafka consumer: %v", err)
	}
	// 4. 启动 Kafka Consumer
	topics := []string{"batch-events"}

	log.Println("========================================")
	log.Println("🚀 Mock C++ Worker started successfully!")
	log.Printf("📡 Consuming topic: %s", topics[0])
	log.Printf("📦 Consumer Group: cpp-worker-group-v2")
	log.Println("========================================")

	// 5. 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 在后台 goroutine 中消费消息
	go func() {
		if err := kafkaConsumer.Subscribe(ctx, topics, worker.HandleMessage); err != nil {
			log.Printf("Consumer error: %v", err)
		}
	}()

	// 等待系统信号
	<-sigCh
	log.Println("\n🛑 Shutting down Worker...")
	if worker.db != nil {
		if err := worker.db.Close(); err != nil {
			log.Printf("Failed to close database: %v", err)
		}
	}
	// 关闭 Kafka Consumer
	if err := kafkaConsumer.Close(); err != nil {
		log.Printf("Failed to close Kafka consumer: %v", err)
	}

	// 关闭 Kafka Producer
	if err := kafkaProducer.Close(); err != nil {
		log.Printf("Failed to close Kafka producer: %v", err)
	}

	log.Println("✅ Worker stopped gracefully")
}

// initKafkaProducer 初始化 Kafka Producer
func initKafkaProducer() messaging.KafkaEventPublisher {
	brokers := []string{getEnv("KAFKA_BROKERS", "localhost:9092")}
	topic := getEnv("KAFKA_TOPIC", "batch-events")

	producer, err := kafka.NewKafkaEventProducer(brokers, topic, "") // P0 fix: 新增 dlqTopic 参数
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
