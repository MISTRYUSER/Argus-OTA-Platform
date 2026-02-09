package domain

import "time"

// DiagnosisContext 是在 Eino Graph 中流转的状态对象
// 📌 Supervisor 模式：Supervisor Agent 持有并更新此 State
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
	NextStep         string // 📌 Supervisor 决策的下一步

	// ============================================================
	// 📌 Supervisor 模式专用字段 (v2.0)
	// ============================================================

	// 6. Supervisor 决策状态
	CurrentWorker    string        // 当前正在执行的 Worker
	LastWorker       string        // 上一个执行的 Worker
	WorkerHistory    []string      // 执行历史（用于防止无限循环）
	MaxIterations    int           // 最大迭代次数（防止死循环）
	CurrentIteration int           // 当前迭代次数

	// 7. Supervisor 指令
	Instruction      string        // Supervisor 给 Worker 的指令
	WorkerResult     string        // Worker 返回给 Supervisor 的结果
	FeedbackRequired bool          // 是否需要 Supervisor 反馈

	// 8. 性能监控
	StartTime        time.Time     // 流程开始时间
	WorkerTimings    map[string]time.Duration // 每个 Worker 的执行时间
}

// NewDiagnosisContext 创建初始上下文
func NewDiagnosisContext(batchID string) *DiagnosisContext {
	return &DiagnosisContext{
		BatchID:          batchID,
		ProcessingStatus: StatusPending,
		NextStep:         "supervisor",
		CurrentWorker:    "",
		WorkerHistory:    []string{},
		MaxIterations:    10, // 最多 10 次迭代
		CurrentIteration: 0,
		Instruction:      "",
		StartTime:        time.Now(),
		WorkerTimings:    make(map[string]time.Duration),
	}
}

// ShouldTerminate 判断是否应该终止（防止无限循环）
func (c *DiagnosisContext) ShouldTerminate() bool {
	// 达到最大迭代次数
	if c.CurrentIteration >= c.MaxIterations {
		c.ErrorMessage = "Max iterations reached"
		return true
	}

	// 检测循环（同一个 Worker 连续执行 3 次）
	if len(c.WorkerHistory) >= 6 {
		last6 := c.WorkerHistory[len(c.WorkerHistory)-6:]
		// 检查是否在两个 Worker 之间循环
		if last6[0] == last6[2] && last6[2] == last6[4] &&
			last6[1] == last6[3] && last6[3] == last6[5] &&
			last6[0] != last6[1] {
			c.ErrorMessage = "Detected worker loop"
			return true
		}
	}

	return false
}

// RecordWorker 记录 Worker 执行
func (c *DiagnosisContext) RecordWorker(workerName string) {
	c.LastWorker = c.CurrentWorker
	c.CurrentWorker = workerName
	c.WorkerHistory = append(c.WorkerHistory, workerName)
	c.CurrentIteration++
}

// StatusEnum 状态枚举
type StatusEnum string

const (
	StatusPending    StatusEnum = "PENDING"
	StatusProcessing StatusEnum = "PROCESSING"
	StatusSuccess    StatusEnum = "SUCCESS"
	StatusFailed     StatusEnum = "FAILED"
)
