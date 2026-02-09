package domain

import (
	"time"

	"github.com/google/uuid"
)
type File struct {
	ID               uuid.UUID
	BatchID          uuid.UUID
	Filename         string
	OriginalFilename string
	FileSize         int64
	FileType         string
	UploadTime       time.Time
	MinIOPath        string
	MinIOETag        string
	ProcessingStatus ProcessingStatus
	ParseDurationMs  int
	RecordCount      int
	ErrorMessage     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ProcessingStatus string 
const (
	FileStatusPending 		ProcessingStatus = "pending"
	FileStatusParsing 		ProcessingStatus = "parsing"
	FileStatusParsed 		ProcessingStatus = "parsed"
	FileStatusAggregating	ProcessingStatus = "aggregating"
	FileStatusCompleted  	ProcessingStatus = "completed"
	FileStatusFailed		ProcessingStatus = "failed"
)
func (s ProcessingStatus) String() string {
	return string(s)
}
func (s ProcessingStatus) IsValid() bool {
	switch s {
		case
			FileStatusPending,
			FileStatusParsing,
			FileStatusParsed,
			FileStatusAggregating,
			FileStatusCompleted,
			FileStatusFailed:
			return true
	default:
		return false
	}
}
func (s ProcessingStatus) CanTransitionTo(newStatus ProcessingStatus) bool {
	// 定义文件处理的完整状态转换路径
	// 面试重点：为什么要在每个中间状态都允许 Failed？
	// 答：任何一个步骤都可能失败（C++ 崩溃、数据异常、网络错误）
	var fileStatusTransitions = map[ProcessingStatus][]ProcessingStatus{
		FileStatusPending: {
			FileStatusParsing, // 准备中 → 解析中
			FileStatusFailed,  // 准备阶段检查失败（如文件损坏）
		},
		FileStatusParsing: {
			FileStatusParsed, // 解析中 → 解析完成
			FileStatusFailed, // C++ 解析失败
		},
		FileStatusParsed: {
			FileStatusAggregating, // 解析完成 → 聚合中
			FileStatusFailed,      // 后处理失败
		},
		FileStatusAggregating: {
			FileStatusCompleted, // 聚合完成 → 流程结束
			FileStatusFailed,    // 聚合失败
		},
		// 终态：不允许转换
		FileStatusCompleted: {},
		FileStatusFailed:    {},
	}

	if !s.IsValid() || !newStatus.IsValid() {
		return false
	}

	nextStatuses, ok := fileStatusTransitions[s]
	if !ok {
		return false
	}

	for _, status := range nextStatuses {
		if status == newStatus {
			return true
		}
	}
	return false
}
