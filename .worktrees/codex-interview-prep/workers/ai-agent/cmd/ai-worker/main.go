package main

import (
	"context"
	"database/sql"
	"log"

	"github.com/joho/godotenv"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	agentconfig "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/config"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	pgvectorimpl "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/pgvector"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
)

func main() {
	// 1. 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}
	cfg := agentconfig.Load()

	// 2. 检查环境变量
	batchID := cfg.BatchID

	// 3. 初始化依赖
	ctx := context.Background()

	// 3.1 初始化数据库连接
	databaseURL := cfg.DatabaseURL

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

	// 3.3 初始化 Vector Retriever（优先使用 pgvector，失败则降级无 RAG）
	var vectorRetriever domain.VectorRetriever
	embeddingConfig := &llm.EmbeddingConfig{
		APIKey: cfg.GLMAPIKey,
	}
	embedModel, err := llm.NewEmbeddingModel(embeddingConfig)
	if err != nil {
		log.Printf("Warning: failed to create embedding model, RAG disabled: %v", err)
		vectorRetriever = nil
	} else {
		vectorRetriever, err = pgvectorimpl.NewPgvectorRetriever(db, embedModel)
		if err != nil {
			log.Printf("Warning: failed to create pgvector retriever, RAG disabled: %v", err)
			vectorRetriever = nil
		}
	}

	// 3.4 初始化 LLM Config
	llmConfig := &llm.GLM4Config{
		APIKey: cfg.GLMAPIKey,
		Model:  cfg.GLMModel,
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
