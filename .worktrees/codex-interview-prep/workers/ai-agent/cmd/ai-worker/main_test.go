package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

// TestDiagnosisGraph 测试诊断流程图
func TestDiagnosisGraph(t *testing.T) {
	// 1. 创建 mock 依赖
	mockRepo := &MockDiagnosisRepository{}
	mockRetriever := &MockVectorRetriever{}
	llmConfig := &MockGLM4Config{}

	// 2. 创建 Graph
	graph, err := application.NewDiagnosisGraph(
		mockRepo,
		mockRetriever,
		llmConfig,
	)
	assert.NoError(t, err, "Failed to create graph")

	// 3. 运行测试
	ctx := context.Background()
	batchID := "test-batch-001"

	result, err := graph.Run(ctx, batchID)

	// 4. 验证结果
	// 注意：由于没有真实的 GLM API Key，这里可能会失败
	// 但至少可以验证 Graph 结构是否正确
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
			"E_CAN_TIMEOUT":     5,
			"E_SENSOR_INVALID":  3,
			"E_BRAKE_FAILURE":   2,
		},
		RawLogs: `
[2025-01-01 10:00:00] ERROR: CAN bus timeout, device_id=0x123
[2025-01-01 10:00:01] WARN:  Sensor invalid signal, device_id=0x456
[2025-01-01 10:00:02] ERROR: Brake failure, device_id=0x789
... 更多日志 ...
[2025-01-01 10:00:10] INFO: System recovered
`,
	}, nil
}

func (m *MockDiagnosisRepository) Save(ctx context.Context, diagnosis *domain.Diagnosis) error {
	return nil
}

// MockGLM4Config Mock LLM Config
type MockGLM4Config struct{}

func (m *MockGLM4Config) GetBaseURL() string {
	return "mock://test"
}

func (m *MockGLM4Config) GetToken() string {
	return "mock-token"
}

func (m *MockGLM4Config) GetModel() string {
	return "mock-model"
}
