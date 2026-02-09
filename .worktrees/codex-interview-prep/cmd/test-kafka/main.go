package main

import (
	"context"
	"database/sql"
	"log"
	"time"

	_ "github.com/lib/pq" // PostgreSQL 驱动
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/application"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
)

func main() {
	log.Println("=== Argus OTA Platform - Kafka Integration Test ===")

	// 1. 连接 PostgreSQL
	db, err := sql.Open("postgres", "host=localhost port=5432 user=argus password=argus_password dbname=argus_ota sslmode=disable")
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	log.Println("✅ Database connected successfully")

	// 2. 创建 Kafka Producer
	kafkaProducer, err := kafka.NewKafkaEventProducer(
		[]string{"localhost:9092"}, // Kafka brokers
		"batch-events",             // Topic
	)
	if err != nil {
		log.Fatalf("Failed to create Kafka producer: %v", err)
	}
	defer kafkaProducer.Close()
	log.Println("✅ Kafka producer created successfully")

	// 3. 创建 Repository
	batchRepo := postgres.NewPostgresBatchRepository(db)

	// 4. 创建 BatchService
	batchService := application.NewBatchService(batchRepo, kafkaProducer)

	// 5. 测试：创建 Batch
	log.Println("\n--- Test 1: Create Batch ---")
	batch, err := batchService.CreateBatch(
		ctx,
		"vehicle-001",
		"VIN123456789",
		5, // expected workers
	)
	if err != nil {
		log.Fatalf("Failed to create batch: %v", err)
	}
	log.Printf("✅ Batch created: ID=%s, Status=%s, VehicleID=%s",
		batch.ID, batch.Status, batch.VehicleID)

	// 等待一下，让 Kafka 消息发送完成
	time.Sleep(1 * time.Second)

	// 6. 测试：添加文件（在 pending 状态）
	log.Println("\n--- Test 2: Add Files (在 pending 状态) ---")
	fileID1 := uuid.New()
	fileID2 := uuid.New()

	err = batchService.AddFile(ctx, batch.ID, fileID1)
	if err != nil {
		log.Fatalf("Failed to add file 1: %v", err)
	}
	log.Printf("✅ File 1 added: %s", fileID1)

	err = batchService.AddFile(ctx, batch.ID, fileID2)
	if err != nil {
		log.Fatalf("Failed to add file 2: %v", err)
	}
	log.Printf("✅ File 2 added: %s", fileID2)

	// 7. 测试：转换状态
	log.Println("\n--- Test 3: Transition Status ---")
	err = batchService.TransitionBatchStatus(ctx, batch.ID, domain.BatchStatusUploaded)
	if err != nil {
		log.Fatalf("Failed to transition status: %v", err)
	}
	log.Printf("✅ Status transitioned: %s → %s", domain.BatchStatusPending, domain.BatchStatusUploaded)

	time.Sleep(1 * time.Second)

	err = batchService.TransitionBatchStatus(ctx, batch.ID, domain.BatchStatusScattering)
	if err != nil {
		log.Fatalf("Failed to transition status: %v", err)
	}
	log.Printf("✅ Status transitioned: %s → %s", domain.BatchStatusUploaded, domain.BatchStatusScattering)

	time.Sleep(1 * time.Second)

	// 8. 查询 Batch 验证
	log.Println("\n--- Test 4: Query Batch ---")
	updatedBatch, err := batchRepo.FindByID(ctx, batch.ID)
	if err != nil {
		log.Fatalf("Failed to query batch: %v", err)
	}
	log.Printf("✅ Batch queried: ID=%s, Status=%s, TotalFiles=%d",
		updatedBatch.ID, updatedBatch.Status, updatedBatch.TotalFiles)

	log.Println("\n=== All tests completed successfully! ===")
	log.Println("Check your Kafka topic 'batch-events' to see the published events.")
	log.Println("You can use kafkacat or kafka-console-consumer to read the events:")
	log.Println("  kafkacat -C -b localhost:9092 -t batch-events -f '%T: %s\n'")
}
