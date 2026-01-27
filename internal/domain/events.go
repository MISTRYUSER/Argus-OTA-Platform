package domain
import (
	"github.com/google/uuid"
	"time"
)

type DomainEvent interface {
	OccurredOn() time.Time
	AggregateID() uuid.UUID
	EventType() string
}

type BatchCreated struct {
	BatchID     uuid.UUID
	VehicleID   string
	VIN         string
	OccurredAt  time.Time
}

type BatchStatusChanged struct {
	BatchID     uuid.UUID
	OldStatus   BatchStatus
	NewStatus   BatchStatus
	OccurredAt  time.Time
}

// FileParsed - 文件解析完成事件（C++ Worker 发布）
type FileParsed struct {
	BatchID     uuid.UUID
	FileID      uuid.UUID
	OccurredAt  time.Time
}

// ErrorCodeSummary - Top-K 异常码摘要
type ErrorCodeSummary struct {
	Code     string `json:"code"`
	Count    int    `json:"count"`
	Severity string `json:"severity"` // "high", "medium", "low"
}

// TokenUsageInfo - Token 使用统计
type TokenUsageInfo struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCost    float64 `json:"estimated_cost"` // USD
}

// GatheringCompleted - 数据聚合完成事件（Python Worker 发布）
// 注意：假设每个 Batch 平均 < 50 个文件，ChartFiles 总大小 < 1MB
// 如果文件数过多，考虑分批发送事件
type GatheringCompleted struct {
	Version     string    // 事件版本 "v1.0"
	BatchID     uuid.UUID
	TotalFiles  int
	ChartFiles  []string // MinIO object paths (PNG/JPG)
	OccurredAt  time.Time
}

// DiagnosisCompleted - AI 诊断完成事件（AI Agent Worker 发布）
type DiagnosisCompleted struct {
	Version           string             // 事件版本 "v1.0"
	BatchID           uuid.UUID
	DiagnosisID       uuid.UUID
	DiagnosisSummary  string             // 诊断摘要（可能很长）
	TopErrorCodes     []ErrorCodeSummary // ✅ Top-K 异常码
	TokenUsage        TokenUsageInfo     // ✅ Token 使用统计
	OccurredAt        time.Time
}

// ============================================================================
// DomainEvent Interface Implementation
// ============================================================================

// FileParsed implements DomainEvent interface
func (e FileParsed) OccurredOn() time.Time {
	return e.OccurredAt
}

func (e FileParsed) AggregateID() uuid.UUID {
	return e.BatchID
}

func (e FileParsed) EventType() string {
	return "FileParsed"
}

// GatheringCompleted implements DomainEvent interface
func (e GatheringCompleted) OccurredOn() time.Time {
	return e.OccurredAt
}

func (e GatheringCompleted) AggregateID() uuid.UUID {
	return e.BatchID
}

func (e GatheringCompleted) EventType() string {
	return "GatheringCompleted"
}

// DiagnosisCompleted implements DomainEvent interface
func (e DiagnosisCompleted) OccurredOn() time.Time {
	return e.OccurredAt
}

func (e DiagnosisCompleted) AggregateID() uuid.UUID {
	return e.BatchID
}

func (e DiagnosisCompleted) EventType() string {
	return "DiagnosisCompleted"
}

// BatchCreated implements DomainEvent interface
func (e BatchCreated) OccurredOn() time.Time {
	return e.OccurredAt
}

func (e BatchCreated) AggregateID() uuid.UUID {
	return e.BatchID
}

func (e BatchCreated) EventType() string {
	return "BatchCreated"
}

// BatchStatusChanged implements DomainEvent interface
func (e BatchStatusChanged) OccurredOn() time.Time {
	return e.OccurredAt
}

func (e BatchStatusChanged) AggregateID() uuid.UUID {
	return e.BatchID
}

func (e BatchStatusChanged) EventType() string {
	return "StatusChanged"
}
