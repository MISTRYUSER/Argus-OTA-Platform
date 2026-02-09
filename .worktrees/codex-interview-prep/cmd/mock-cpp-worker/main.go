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

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
)

// Worker - Mock C++ Worker 结构体
type Worker struct {
	kafka 		messaging.KafkaEventPublisher
	batchRepo  	domain.BatchRepository
	db			*sql.DB
}
type Config struct {
	Database DatabaseConfig
}
type DatabaseConfig struct {
	Host     string
	Port 	 int
	User     string
	Password string
	DBName   string
}
// NewWorker 创建 Worker
func NewWorker(kafka messaging.KafkaEventPublisher,batchRepo domain.BatchRepository,db *sql.DB) *Worker {
	return &Worker{
		kafka: 		kafka,
		batchRepo:  batchRepo,
		db: 		db,
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
func mustAtoi(s string, field string) int  {
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
	batch, err := w.batchRepo.FindByID(ctx, batchID)
	if err != nil {
		return fmt.Errorf("failed to find batch: %w",err)
	}
	if batch == nil {  // ← 添加这个检查
		return fmt.Errorf("batch not found: %s", batchID)
	}
  
  
	log.Printf("[Worker] Received BatchCreated: batch=%s", batchID)

	// 模拟解析 rec 文件（sleep 2 秒）
	log.Printf("[Worker] 🔄 Simulating rec file parsing for batch %s...", batchID)
	time.Sleep(2 * time.Second)

	log.Printf("[Worker] ✅ Parsing completed for batch %s", batchID)
	fileParsedEvents := make([]domain.FileParsed,0,batch.TotalFiles)
	for i := 0;i < batch.TotalFiles;i ++{
		fileParsedEvents = append(fileParsedEvents,domain.FileParsed{
			BatchID:   batchID,
			FileID:	   uuid.New(),
			OccurredAt:time.Now(),
		})
	}
	// 转换为 DomainEvent 接口类型
	events := make([]domain.DomainEvent, len(fileParsedEvents))
	for i, e := range fileParsedEvents {
		events[i] = e
	}

	log.Printf("[Worker] Publishing %d FileParsed events...", len(events))
	if err := w.kafka.PublishEvents(ctx, events); err != nil {
		log.Printf("[Worker] Failed to publish FileParsed events: %v", err)
		return fmt.Errorf("failed to publish FileParsed events: %w", err)
	}

	log.Printf("[Worker] ✅ Successfully published %d FileParsed events for batch %s", len(events), batchID)
	return nil
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
	db,err := sql.Open("postgres",dsn)
	if err != nil {
		log.Fatal("Failed to open database : ",err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)  // Idle 应该小于 Open
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping();err != nil {
		log.Fatal("Failed to ping database:", err)
	}
	log.Println("[DB] Database connected successfully")
    return db
}
func main() {
	ctx := context.Background()

	// 1. 初始化 Kafka Producer（发布事件）
	kafkaProducer := initKafkaProducer()

	// 2. 初始化 Kafka Consumer（消费事件）
	kafkaConsumer, err := kafka.NewKafkaEventConsumer(
		[]string{"localhost:9092"},
		"cpp-worker-group-v2", // Consumer Group ID (new for testing)
	)
	if err != nil {
		log.Fatalf("Failed to create Kafka consumer: %v", err)
	}
	//初始化 DB
	cfg := &Config {
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     mustAtoi(getEnv("DB_PORT","5432"), "DB_PORT"),
			User:     getEnv("DB_USER", "argus"),
			Password: getEnv("DB_PASSWORD", "argus123"),
			DBName:   getEnv("DB_NAME", "argus_ota"),
		},
	}
	db := initDB(cfg)
	batchRepo := postgres.NewPostgresBatchRepository(db)
	// 3. 创建 Worker
	worker := NewWorker(kafkaProducer,batchRepo,db)

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
