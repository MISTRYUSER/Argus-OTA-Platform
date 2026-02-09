package application

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

// MultiAgentService AI多智能体服务（Application层）
// 职责：编排AI Agent的诊断流程，协调各个组件
type MultiAgentService struct {
	diagnosisRepo   domain.DiagnosisRepository
	vectorRetriever domain.VectorRetriever
	eventPublisher  domain.EventPublisher
	diagnosisGraph  *DiagnosisGraph
}

// NewMultiAgentService 创建MultiAgentService
func NewMultiAgentService(
	diagnosisRepo domain.DiagnosisRepository,
	vectorRetriever domain.VectorRetriever,
	eventPublisher domain.EventPublisher,
	llmConfig *llm.GLM4Config,
) (*MultiAgentService, error) {
	// 创建 Diagnosis Graph
	graph, err := NewDiagnosisGraph(diagnosisRepo, vectorRetriever, llmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create diagnosis graph: %w", err)
	}

	return &MultiAgentService{
		diagnosisRepo:   diagnosisRepo,
		vectorRetriever: vectorRetriever,
		eventPublisher:  eventPublisher,
		diagnosisGraph:  graph,
	}, nil
}

// DiagnoseBatch 诊断一批文件（供 Kafka Consumer 调用）
func (s *MultiAgentService) DiagnoseBatch(ctx context.Context, batchIDStr string) error {
	return s.handleAllFilesScattered(ctx, batchIDStr)
}

// handleAllFilesScattered 处理所有文件解析完成事件
func (s *MultiAgentService) handleAllFilesScattered(ctx context.Context, batchIDStr string) error {
	// 1. 发布进度事件
	_ = s.eventPublisher.PublishProgress(ctx, batchIDStr, domain.StreamEvent{
		EventType: "diagnosis_started",
		BatchID:   batchIDStr,
		Status:    string(domain.DiagnosisStatusProcessing),
		Message:   "AI诊断开始",
		Progress:  0.0,
		Timestamp: time.Now(),
	})

	// 2. 运行诊断流程（使用 DiagnosisGraph）
	result, err := s.diagnosisGraph.Run(ctx, batchIDStr)
	if err != nil {
		// 发布失败事件
		_ = s.eventPublisher.PublishProgress(ctx, batchIDStr, domain.StreamEvent{
			EventType: "diagnosis_failed",
			BatchID:   batchIDStr,
			Status:    string(domain.DiagnosisStatusFailed),
			Message:   fmt.Sprintf("AI诊断失败: %v", err),
			Progress:  0.0,
			Timestamp: time.Now(),
		})
		return fmt.Errorf("diagnosis graph execution failed: %w", err)
	}

	// 3. 保存诊断结果到数据库
	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		return fmt.Errorf("invalid batch id: %w", err)
	}
	now := time.Now()
	diagnosis := &domain.Diagnosis{
		ID:               uuid.New(),
		BatchID:          batchID,
		Status:           domain.DiagnosisStatusCompleted,
		DiagnosisSummary: result.RootCause,
		Recommendations:  result.Suggestions,
		Confidence:       result.Confidence,
		DiagnosedAt:      &now,
		Model:            "glm-4",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.diagnosisRepo.Save(ctx, diagnosis); err != nil {
		_ = s.eventPublisher.PublishProgress(ctx, batchIDStr, domain.StreamEvent{
			EventType: "diagnosis_failed",
			BatchID:   batchIDStr,
			Status:    string(domain.DiagnosisStatusFailed),
			Message:   fmt.Sprintf("AI诊断回写失败: %v", err),
			Progress:  0.95,
			Timestamp: time.Now(),
		})
		return fmt.Errorf("save diagnosis failed: %w", err)
	}

	_ = s.eventPublisher.PublishProgress(ctx, batchIDStr, domain.StreamEvent{
		EventType: "diagnosis_completed",
		BatchID:   batchIDStr,
		Status:    string(domain.DiagnosisStatusCompleted),
		Message:   fmt.Sprintf("AI诊断完成，置信度: %.2f", result.Confidence),
		Progress:  1.0,
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"diagnosis_id": diagnosis.ID.String(),
			"batch_id":     batchIDStr,
			"confidence":   result.Confidence,
		},
	})

	return nil
}
