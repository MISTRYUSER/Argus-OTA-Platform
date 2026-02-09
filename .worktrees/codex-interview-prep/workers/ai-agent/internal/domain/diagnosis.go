package domain

import (
	"time"

	"github.com/google/uuid"
)

// AggregatedData 聚合数据（从数据库获取）
type AggregatedData struct {
	BatchID        string
	ErrorCodeStats map[string]int
	RawLogs        string
	LogsSummary    string
}

// DiagnosisResult 诊断结果（由 LLM 生成）
type DiagnosisResult struct {
	RootCause    string
	Suggestions  []string
	Severity     string
	Confidence   float64
	RawLLMOutput string // 原始 LLM 输出（用于调试）
}

// DiagnosisStatus 诊断状态
type DiagnosisStatus string

const (
	DiagnosisStatusPending    DiagnosisStatus = "pending"
	DiagnosisStatusProcessing DiagnosisStatus = "processing"
	DiagnosisStatusCompleted  DiagnosisStatus = "completed"
	DiagnosisStatusFailed     DiagnosisStatus = "failed"
)

// Diagnosis AI诊断实体
type Diagnosis struct {
	ID               uuid.UUID
	BatchID          uuid.UUID
	Status           DiagnosisStatus
	AggregatedData   map[string]interface{}
	DiagnosisSummary string
	TopErrorCodes    []string
	Recommendations  []string
	Confidence       float64
	Embedding        []float32
	Model            string
	TokensUsed       int
	DiagnosedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// SimilarCase 相似案例（用于 RAG）
type SimilarCase struct {
	DiagnosisID   uuid.UUID
	BatchID       uuid.UUID
	Summary       string
	Confidence    float64
	Similarity    float64
	MatchedReason string
}

// StreamEvent 流式事件（用于 SSE）
type StreamEvent struct {
	EventType string                 `json:"event_type"`
	BatchID   string                 `json:"batch_id"`
	Status    string                 `json:"status"`
	Message   string                 `json:"message"`
	Progress  float64                `json:"progress"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// NewDiagnosis 创建新的诊断
func NewDiagnosis(batchID uuid.UUID) *Diagnosis {
	now := time.Now()
	return &Diagnosis{
		ID:        uuid.New(),
		BatchID:   batchID,
		Status:    DiagnosisStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// TransitionTo 转换状态
func (d *Diagnosis) TransitionTo(newStatus DiagnosisStatus) error {
	// 状态机验证
	if !d.isValidTransition(d.Status, newStatus) {
		return ErrInvalidStatusTransition
	}

	d.Status = newStatus
	d.UpdatedAt = time.Now()
	return nil
}

// isValidTransition 验证状态转换是否合法
func (d *Diagnosis) isValidTransition(current, new DiagnosisStatus) bool {
	transitions := map[DiagnosisStatus][]DiagnosisStatus{
		DiagnosisStatusPending:    {DiagnosisStatusProcessing},
		DiagnosisStatusProcessing: {DiagnosisStatusCompleted, DiagnosisStatusFailed},
	}

	allowed, exists := transitions[current]
	if !exists {
		return false
	}

	for _, status := range allowed {
		if status == new {
			return true
		}
	}
	return false
}

var (
	// ErrInvalidStatusTransition 非法的状态转换
	ErrInvalidStatusTransition = &DomainError{Code: "INVALID_STATUS_TRANSITION", Message: "invalid status transition"}
	// ErrDiagnosisNotFound 诊断不存在
	ErrDiagnosisNotFound = &DomainError{Code: "DIAGNOSIS_NOT_FOUND", Message: "diagnosis not found"}
)

// DomainError 领域错误
type DomainError struct {
	Code    string
	Message string
}

func (e *DomainError) Error() string {
	return e.Message
}
