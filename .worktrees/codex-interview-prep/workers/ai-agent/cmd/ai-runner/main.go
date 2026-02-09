package main

import (
	"context"
	"log"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

func main() {
	log.Println("🚀 AI Agent Worker - Eino Multi-Agent Test")
	log.Println("==========================================")

	// 1. 创建 mock 依赖
	mockRepo := &MockDiagnosisRepository{
		data: &domain.AggregatedData{
			BatchID: "test-batch-001",
			ErrorCodeStats: map[string]int{
				"E_CAN_TIMEOUT":    5,
				"E_SENSOR_INVALID": 3,
				"E_BRAKE_FAILURE":  2,
			},
			RawLogs: `
[2025-01-01 10:00:00] ERROR: CAN bus timeout, device_id=0x123
[2025-01-01 10:00:01] WARN:  Sensor invalid signal, device_id=0x456
[2025-01-01 10:00:02] ERROR: Brake failure, device_id=0x789
... 更多日志 ...
[2025-01-01 10:00:10] INFO: System recovered
`,
		},
	}

	mockRetriever := &MockVectorRetriever{
		cases: []domain.SimilarCase{
			{
				Summary:    "类似的 CAN 总线超时问题，通常是由于线路干扰导致",
				Confidence: 0.92,
			},
		},
	}

	llmConfig := &llm.GLM4Config{
		APIKey:  "mock-api-key",
		BaseURL: "mock://test",
		Model:   "mock-model",
	}

	// 2. 创建 Diagnosis Graph
	graph, err := application.NewDiagnosisGraph(
		mockRepo,
		mockRetriever,
		llmConfig,
	)
	if err != nil {
		log.Fatalf("Failed to create diagnosis graph: %v", err)
	}

	// 3. 运行测试
	log.Println("\n📊 Running diagnosis flow...")
	log.Println("--------------------------------")

	ctx := context.Background()
	batchID := "test-batch-001"

	result, err := graph.Run(ctx, batchID)
	if err != nil {
		log.Fatalf("❌ Diagnosis failed: %v", err)
	}

	// 4. 输出结果
	log.Println("\n✅ Diagnosis completed successfully!")
	log.Println("========================================")
	log.Printf("📋 Root Cause: %s\n", result.RootCause)
	log.Printf("⚠️  Severity: %s\n", result.Severity)
	log.Printf("📈 Confidence: %.2f\n", result.Confidence)
	log.Println("\n💡 Suggestions:")
	for i, suggestion := range result.Suggestions {
		log.Printf("   %d. %s\n", i+1, suggestion)
	}

	log.Println("\n🎉 All tests passed!")
	log.Println("========================================")
}

// ============ Mock 实现 ============

// MockDiagnosisRepository Mock Repository
type MockDiagnosisRepository struct {
	data *domain.AggregatedData
}

func (m *MockDiagnosisRepository) GetAggregatedData(ctx context.Context, batchID string) (*domain.AggregatedData, error) {
	return m.data, nil
}

func (m *MockDiagnosisRepository) Save(ctx context.Context, diagnose *domain.Diagnosis) error {
	return nil
}

func (m *MockDiagnosisRepository) FindByBatchID(ctx context.Context, batchID string) (*domain.Diagnosis, error) {
	return &domain.Diagnosis{}, nil
}

func (m *MockDiagnosisRepository) FindByID(ctx context.Context, id string) (*domain.Diagnosis, error) {
	return &domain.Diagnosis{}, nil
}

func (m *MockDiagnosisRepository) FindSimilar(ctx context.Context, embedding []float32, limit int) ([]*domain.Diagnosis, error) {
	return []*domain.Diagnosis{}, nil
}

// MockVectorRetriever Mock Vector Retriever
type MockVectorRetriever struct {
	cases []domain.SimilarCase
}

func (m *MockVectorRetriever) Search(ctx context.Context, params domain.SearchParams) ([]domain.SimilarCase, error) {
	return m.cases, nil
}

func (m *MockVectorRetriever) Retrieve(ctx context.Context, query string, topK int) ([]domain.SimilarCase, error) {
	return m.Search(ctx, domain.SearchParams{
		EmbeddingText: query,
		TopK:          topK,
	})
}

func (m *MockVectorRetriever) Index(ctx context.Context, diagnosis *domain.Diagnosis) error {
	return nil
}
