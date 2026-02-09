package domain

// DiagnosisContext 是在 Eino Graph 中流转的状态对象
type DiagnosisContext struct {
	// 1. 基础元数据
	TaskID  string
	BatchID string

	// 2. 输入数据 (由 Repository 获取)
	AggregatedData *AggregatedData

	// 3. 中间产物 (由 RAG 组件填充)
	RAGCases       []SimilarCase
	RAGUnavailable bool // 📌 P1 改进：标记 RAG 是否不可用（降级时通知 LLM）

	// 4. 最终结果 (由 LLM 生成)
	DiagnosisResult *DiagnosisResult

	// 5. 流程控制 (用于 Graph 分支判断)
	ProcessingStatus StatusEnum
	ErrorMessage     string
	Confidence       float64 // 📌 P1 改进：用于动态决策（v2.0）
}

// StatusEnum 状态枚举
type StatusEnum string

const (
	StatusPending    StatusEnum = "PENDING"
	StatusProcessing StatusEnum = "PROCESSING"
	StatusSuccess    StatusEnum = "SUCCESS"
	StatusFailed     StatusEnum = "FAILED"
)
