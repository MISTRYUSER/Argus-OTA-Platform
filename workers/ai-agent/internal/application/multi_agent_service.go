package application

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/components/model"
	"github.com/google/uuid"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/investigation"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/supervisor"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"os"
	"sort"
	"time"
)

type MultiAgentService struct {
	diagnosisRepo  domain.DiagnosisRepository
	runner         domain.DiagnosisRunner
	eventPublisher domain.EventPublisher
	semaphore      chan struct{}
}

func NewMultiAgentService(repo domain.DiagnosisRepository, retriever domain.VectorRetriever, publisher domain.EventPublisher, cfg *llm.GLM4Config, options ...ServiceOption) (*MultiAgentService, error) {
	var runner domain.DiagnosisRunner
	var err error
	switch os.Getenv("AI_RUNNER") {
	case "conditional":
		runner, err = NewDiagnosisGraph(repo, retriever, cfg)
	case "supervisor":
		runner, err = supervisor.NewSupervisorGraph(repo, retriever, cfg)
	case "", "investigation":
		var chat model.ChatModel
		chat, err = llm.NewGLM4ChatModel(cfg)
		if err == nil {
			runner, err = investigation.NewAgent(repo, retriever, chat, investigation.Options{MaxSteps: 8, ModelName: cfg.Model})
		}
	default:
		return nil, fmt.Errorf("unsupported AI_RUNNER")
	}
	if err != nil {
		return nil, err
	}
	concurrency := 20
	for _, option := range options {
		option(&concurrency)
	}
	return NewServiceWithRunner(repo, runner, publisher, concurrency), nil
}

func NewServiceWithRunner(repo domain.DiagnosisRepository, runner domain.DiagnosisRunner, publisher domain.EventPublisher, concurrency int) *MultiAgentService {
	if concurrency < 1 {
		concurrency = 1
	}
	return &MultiAgentService{repo, runner, publisher, make(chan struct{}, concurrency)}
}
func (s *MultiAgentService) publish(ctx context.Context, id string, typ string, progress float64, message string) {
	if s.eventPublisher != nil {
		_ = s.eventPublisher.PublishProgress(ctx, id, domain.StreamEvent{BatchID: id, EventType: typ, Progress: progress, Message: message, Timestamp: time.Now()})
	}
}
func (s *MultiAgentService) DiagnoseBatch(ctx context.Context, id string) error {
	batchID, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid batch id: %w", err)
	}
	id = batchID.String()
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-ctx.Done():
		return ctx.Err()
	}
	generate := func(ctx context.Context) (*domain.Diagnosis, error) {
		s.publish(ctx, id, "diagnosis_started", 0, "AI诊断开始")
		result, err := s.runner.Run(ctx, id)
		if err != nil {
			return nil, classifyDiagnosisError(err)
		}
		if result == nil {
			return nil, fmt.Errorf("empty diagnosis result")
		}
		data, err := s.diagnosisRepo.GetAggregatedData(ctx, id)
		if err != nil {
			return nil, err
		}
		if data == nil {
			return nil, fmt.Errorf("aggregated data missing")
		}
		now := time.Now()
		codes := make([]string, 0, len(data.ErrorCodeStats))
		for code := range data.ErrorCodeStats {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		if result.Model == "" {
			result.Model = "configured-model"
		}
		return &domain.Diagnosis{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("argus-diagnosis:"+id)), BatchID: batchID, Status: domain.DiagnosisStatusCompleted, DiagnosisSummary: result.RootCause, AggregatedData: map[string]interface{}{"context_compactions": result.ContextCompactions, "hypotheses": result.Hypotheses, "hypothesis_id": result.HypothesisID, "tool_trace": result.ToolTrace, "evidence": result.Evidence, "limitations": result.Limitations}, TokensUsed: result.TokensUsed, TopErrorCodes: codes, Recommendations: result.Suggestions, Confidence: result.Confidence, Model: result.Model, CreatedAt: now, UpdatedAt: now, DiagnosedAt: &now}, nil
	}
	if leased, ok := s.diagnosisRepo.(domain.LeasedDiagnosisRepository); ok {
		_, err = leased.ExecuteOnce(ctx, id, generate)
	} else if atomic, ok := s.diagnosisRepo.(domain.IdempotentDiagnosisRepository); ok {
		_, err = atomic.GetOrCreate(ctx, id, func() (*domain.Diagnosis, error) { return generate(ctx) })
	} else {
		var old *domain.Diagnosis
		old, err = s.diagnosisRepo.FindByBatchID(ctx, id)
		if errors.Is(err, domain.ErrDiagnosisNotFound) {
			err = nil
		}
		if err == nil && (old == nil || old.Status != domain.DiagnosisStatusCompleted) {
			var d *domain.Diagnosis
			d, err = generate(ctx)
			if err == nil {
				err = s.diagnosisRepo.Save(ctx, d)
			}
		}
	}
	if err != nil {
		var deferred *core.DeferredTaskError
		if errors.As(err, &deferred) {
			return err
		}
		s.publish(ctx, id, "diagnosis_failed", 0, err.Error())
		if errors.Is(err, domain.ErrInvalidDiagnosis) || errors.Is(err, domain.ErrInvestigationBudget) || llm.IsPermanentError(err) {
			return &core.PermanentTaskError{Err: err}
		}
		return err
	}
	s.publish(ctx, id, "diagnosis_completed", 1, "AI诊断已保存")
	return nil
}

type ServiceOption func(*int)

func WithConcurrency(n int) ServiceOption { return func(value *int) { *value = n } }

func (s *MultiAgentService) FailBatch(ctx context.Context, id string, cause error) error {
	if recorder, ok := s.diagnosisRepo.(interface {
		RecordFailure(context.Context, string, error) error
	}); ok {
		return recorder.RecordFailure(ctx, id, cause)
	}
	return fmt.Errorf("durable failure recorder required")
}

func classifyDiagnosisError(err error) error {
	if errors.Is(err, domain.ErrInvalidDiagnosis) || errors.Is(err, domain.ErrInvestigationBudget) || llm.IsPermanentError(err) {
		return &core.PermanentTaskError{Err: err}
	}
	return err
}
