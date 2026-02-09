package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/segmentio/kafka-go"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	agentkafka "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	pgvectorimpl "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/pgvector"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
)

func main() {
	// 1. 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	// 2. 根据模式选择运行方式
	mode := os.Getenv("MODE")
	switch mode {
	case "kafka", "KAFKA":
		runKafkaConsumer()
	default:
		runOnce() // 默认单次运行模式
	}
}

// runOnce 单次运行模式（测试用）
func runOnce() {
	// 检查环境变量
	batchID := os.Getenv("BATCH_ID")
	if batchID == "" {
		batchID = "test-batch-001" // 默认测试批次
	}

	log.Println("========================================")
	log.Println("🚀 AI Agent Worker - Single Run Mode")
	log.Println("========================================")

	// 初始化依赖
	ctx, graph := initializeDependencies()

	// 运行诊断流程
	log.Printf("📋 Starting diagnosis for batch: %s", batchID)
	result, err := graph.Run(ctx, batchID)
	if err != nil {
		log.Fatalf("❌ Diagnosis failed: %v", err)
	}

	// 输出结果
	log.Println("========================================")
	log.Println("✅ Diagnosis completed successfully!")
	log.Println("========================================")
	log.Printf("📋 Root Cause: %s", result.RootCause)
	log.Printf("⚠️  Severity: %s", result.Severity)
	log.Printf("📈 Confidence: %.2f", result.Confidence)
	log.Println("\n💡 Suggestions:")
	for i, suggestion := range result.Suggestions {
		log.Printf("   %d. %s", i+1, suggestion)
	}
}

// runKafkaConsumer Kafka 消费模式（生产用）
func runKafkaConsumer() {
	log.Println("========================================")
	log.Println("🚀 AI Agent Worker - Kafka Consumer Mode")
	log.Println("========================================")

	// 初始化依赖
	ctx, graph := initializeDependencies()

	// 创建 Kafka Reader
	kafkaBroker := os.Getenv("KAFKA_BROKER")
	if kafkaBroker == "" {
		kafkaBroker = "localhost:9092"
	}

	kafkaGroup := os.Getenv("KAFKA_GROUP")
	if kafkaGroup == "" {
		kafkaGroup = "ai-agent-group"
	}

	inputTopic := os.Getenv("KAFKA_INPUT_TOPIC")
	if inputTopic == "" {
		inputTopic = "gathering-completed"
	}

	outputTopic := os.Getenv("KAFKA_OUTPUT_TOPIC")
	if outputTopic == "" {
		outputTopic = "diagnosis-completed"
	}

	log.Printf("📡 Kafka Broker: %s", kafkaBroker)
	log.Printf("📥 Input Topic: %s", inputTopic)
	log.Printf("📤 Output Topic: %s", outputTopic)

	// 创建 Reader
	//
	// P1-1: 设置 CommitInterval: 0 禁用自动提交，实现"失败不 commit 可重试"语义
	// 默认情况下 ReadMessage 会自动提交 offset，导致失败消息无法重试
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:       []string{kafkaBroker},
		GroupID:       kafkaGroup,
		Topic:         inputTopic,
		MinBytes:      10e3, // 10KB
		MaxBytes:      10e6, // 10MB
		CommitInterval: 0,   // 禁用自动提交，改为手动控制
	})
	defer reader.Close()

	// 创建 Writer（可选，用于发布完成事件）
	var writer *kafka.Writer
	if outputTopic != "" {
		writer = &kafka.Writer{
			Addr:     kafka.TCP(kafkaBroker),
			Topic:    outputTopic,
			Balancer: &kafka.LeastBytes{},
		}
		defer writer.Close()
	}

	// 创建 Consumer
	consumer := agentkafka.NewConsumer(reader, graph, writer, outputTopic)

	// 设置信号处理，优雅退出
	ctx, cancel := context.WithCancel(context.Background())
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	// 启动消费循环（在 goroutine 中）
	errChan := make(chan error, 1)
	go func() {
		errChan <- consumer.Consume(ctx)
	}()

	// 等待信号
	select {
	case <-sigchan:
		log.Println("\n🛑 Received shutdown signal, stopping consumer...")
		cancel()
		_ = consumer.Close()
	case err := <-errChan:
		log.Printf("❌ Consumer error: %v", err)
		cancel() // P2 修复：在错误路径上也调用 cancel，避免 context 泄漏
	}

	log.Println("👋 AI Agent Worker stopped")
}

// initializeDependencies 初始化所有依赖
func initializeDependencies() (context.Context, *application.DiagnosisGraph) {
	ctx := context.Background()

	// 1. 初始化数据库连接
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable"
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// 测试数据库连接
	if err := db.Ping(); err != nil {
		log.Printf("Warning: Database connection failed: %v", err)
		log.Println("P2: Note - Repository will still be created, but queries will fail at runtime.")
		log.Println("     Fix database connectivity or provide a valid DATABASE_URL.")
	}

	// 2. 初始化 Repository
	// P2: 注意：即使 Ping 失败也创建 Postgres Repository，失败会在运行时发生
	diagnosisRepo := postgres.NewPostgresDiagnoseRepository(db)

	// 3. 初始化 Embedding Model
	var vectorRetriever domain.VectorRetriever
	embeddingConfig := &llm.EmbeddingConfig{
		APIKey: os.Getenv("GLM_API_KEY"),
	}
	embedModel, err := llm.NewEmbeddingModel(embeddingConfig)
	if err != nil {
		log.Printf("Warning: Failed to create embedding model: %v", err)
		log.Println("Continuing without RAG capability...")
	} else {
		// 初始化 Vector Retriever
		vectorRetriever, err = pgvectorimpl.NewPgvectorRetriever(db, embedModel)
		if err != nil {
			log.Printf("Warning: Failed to create vector retriever: %v", err)
			log.Println("Continuing without RAG capability...")
			vectorRetriever = nil
		}
	}

	// 4. 初始化 LLM Config
	llmConfig := &llm.GLM4Config{
		APIKey: os.Getenv("GLM_API_KEY"),
		Model:  os.Getenv("GLM_MODEL"),
	}

	// 5. 创建 Diagnosis Graph
	graph, err := application.NewDiagnosisGraph(
		diagnosisRepo,
		vectorRetriever,
		llmConfig,
	)
	if err != nil {
		log.Fatalf("Failed to create diagnosis graph: %v", err)
	}

	return ctx, graph
}
