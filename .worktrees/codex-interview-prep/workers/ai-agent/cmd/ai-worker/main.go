package main

import (
	"context"
	"database/sql"
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
)

func main() {
	// 1. 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	// 2. 检查环境变量
	batchID := os.Getenv("BATCH_ID")
	if batchID == "" {
		batchID = "test-batch-001" // 默认测试批次
	}

	// 3. 初始化依赖
	ctx := context.Background()

	// 3.1 初始化数据库连接
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable"
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// 测试数据库连接
	if err := db.Ping(); err != nil {
		log.Printf("Warning: Database connection failed: %v", err)
		log.Println("Continuing with mock repository for testing...")
	}

	// 3.2 初始化 Repository
	diagnosisRepo := postgres.NewPostgresDiagnoseRepository(db)

	// 3.3 初始化 Vector Retriever（暂时使用 mock）
	// TODO: 替换为真实的 pgvector retriever
	vectorRetriever := &MockVectorRetriever{}

	// 3.4 初始化 LLM Config
	llmConfig := &llm.GLM4Config{
		APIKey: os.Getenv("GLM_API_KEY"),
		Model:  os.Getenv("GLM_MODEL"),
	}

	// 4. 创建 Diagnosis Graph
	graph, err := application.NewDiagnosisGraph(
		diagnosisRepo,
		vectorRetriever,
		llmConfig,
	)
	if err != nil {
		log.Fatalf("Failed to create diagnosis graph: %v", err)
	}

	// 5. 运行诊断流程
	log.Printf("Starting diagnosis for batch: %s", batchID)
	result, err := graph.Run(ctx, batchID)
	if err != nil {
		log.Fatalf("Diagnosis failed: %v", err)
	}

	// 6. 输出结果
	log.Printf("Diagnosis completed successfully!")
	log.Printf("Root Cause: %s", result.RootCause)
	log.Printf("Severity: %s", result.Severity)
	log.Printf("Confidence: %.2f", result.Confidence)
	log.Printf("Suggestions: %v", result.Suggestions)
}

// MockVectorRetriever 用于测试（暂时）
type MockVectorRetriever struct{}

func (m *MockVectorRetriever) Search(ctx context.Context, params domain.SearchParams) ([]domain.SimilarCase, error) {
	// 返回模拟的相似案例
	return []domain.SimilarCase{
		{
			Summary:     "类似的 CAN 总线超时问题，通常是由于线路干扰导致",
			Confidence:  0.92,
		},
		{
			Summary:     "传感器信号异常，可能是供电不足",
			Confidence:  0.85,
		},
	}, nil
}

func (m *MockVectorRetriever) Retrieve(ctx context.Context, query string, topK int) ([]domain.SimilarCase, error) {
	return m.Search(ctx, domain.SearchParams{
		EmbeddingText: query,
		TopK:          topK,
	})
}

func (m *MockVectorRetriever) Index(ctx context.Context, diagnosis *domain.Diagnosis) error {
	// Mock implementation
	return nil
}
