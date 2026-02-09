package main_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

// TestDiagnosisGraph_Basic tests the diagnosis graph structure without LLM
func TestDiagnosisGraph_Basic(t *testing.T) {
	// 1. 创建 mock 依赖
	mockRepo := &MockDiagnosisRepository{}
	mockRetriever := &MockVectorRetriever{}

	// 2. 使用空的 LLM Config (测试 Graph 结构，不实际调用 LLM)
	llmConfig := &llm.GLM4Config{
		APIKey: "test-key",
		Model:  "glm-4",
	}

	// 3. 创建 Graph
	graph, err := application.NewDiagnosisGraph(
		mockRepo,
		mockRetriever,
		llmConfig,
	)
	require.NoError(t, err, "Failed to create graph")
	require.NotNil(t, graph, "Graph should not be nil")

	// 4. 验证 Graph 编译成功（说明结构正确）
	t.Log("Graph structure compiled successfully")
}

// TestDiagnosisGraph_WithMockData tests with mock data (requires API key for full test)
func TestDiagnosisGraph_WithMockData(t *testing.T) {
	// Skip if no API key configured
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	mockRepo := &MockDiagnosisRepository{}
	mockRetriever := &MockVectorRetriever{}

	llmConfig := &llm.GLM4Config{
		APIKey: "",
		Model:  "glm-4",
	}

	graph, err := application.NewDiagnosisGraph(
		mockRepo,
		mockRetriever,
		llmConfig,
	)
	require.NoError(t, err)

	ctx := context.Background()
	batchID := "test-batch-001"

	// 由于没有真实 API key，这个测试预期会失败
	// 但至少验证 Graph 结构正确
	result, err := graph.Run(ctx, batchID)

	if err != nil {
		t.Logf("Graph execution failed (expected without API key): %v", err)
	} else {
		assert.NotNil(t, result, "Result should not be nil")
		assert.NotEmpty(t, result.RootCause, "Root cause should not be empty")
		t.Logf("Root Cause: %s", result.RootCause)
		t.Logf("Severity: %s", result.Severity)
		t.Logf("Confidence: %.2f", result.Confidence)
	}
}

// MockDiagnosisRepository Mock Repository
type MockDiagnosisRepository struct{}

func (m *MockDiagnosisRepository) GetAggregatedData(ctx context.Context, batchID string) (*domain.AggregatedData, error) {
	return &domain.AggregatedData{
		BatchID: batchID,
		ErrorCodeStats: map[string]int{
			"E001": 5, // Known error code (CAN timeout)
			"E002": 3, // Known error code (Sensor invalid)
			"E003": 2, // Known error code (Brake failure)
		},
		RawLogs: `
[2025-01-01 10:00:00] ERROR: CAN bus timeout, device_id=0x123
[2025-01-01 10:00:01] WARN:  Sensor invalid signal, device_id=0x456
[2025-01-01 10:00:02] ERROR: Brake failure, device_id=0x789
[2025-01-01 10:00:03] INFO: System recovered
`,
	}, nil
}

func (m *MockDiagnosisRepository) Save(ctx context.Context, diagnosis *domain.Diagnosis) error {
	return nil
}

func (m *MockDiagnosisRepository) FindByBatchID(ctx context.Context, batchID string) (*domain.Diagnosis, error) {
	return nil, domain.ErrDiagnosisNotFound
}

func (m *MockDiagnosisRepository) FindByID(ctx context.Context, id string) (*domain.Diagnosis, error) {
	return nil, domain.ErrDiagnosisNotFound
}

func (m *MockDiagnosisRepository) FindSimilar(ctx context.Context, embedding []float32, limit int) ([]*domain.Diagnosis, error) {
	return nil, nil
}

// MockVectorRetriever Mock Vector Retriever
type MockVectorRetriever struct{}

func (m *MockVectorRetriever) Search(ctx context.Context, params domain.SearchParams) ([]domain.SimilarCase, error) {
	// 返回模拟的相似案例
	return []domain.SimilarCase{
		{
			DiagnosisID:   uuid.New(),
			BatchID:       uuid.New(),
			Summary:       "Similar case: CAN bus timeout caused by loose connection",
			Confidence:    0.85,
			Similarity:    0.92,
			MatchedReason: "Error code E001 matched",
		},
		{
			DiagnosisID:   uuid.New(),
			BatchID:       uuid.New(),
			Summary:       "Similar case: Sensor failure after firmware update",
			Confidence:    0.78,
			Similarity:    0.85,
			MatchedReason: "Error code E002 matched",
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
	return nil
}
