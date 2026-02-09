package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

// GuardAction 定义 Guard 失败后的策略。
type GuardAction string

const (
	GuardActionReject   GuardAction = "reject"   // 拒绝后续执行
	GuardActionNotify   GuardAction = "notify"   // 记录告警但允许继续
	GuardActionSchedule GuardAction = "schedule" // 可重试（用于临时性错误）
)

type GuardRule struct {
	Name   string
	Check  func(ctx context.Context, state *domain.DiagnosisContext) (bool, error)
	OnFail GuardAction
	Reason string
}

type GuardResult struct {
	Passed  bool
	Blocked *BlockedInfo
}

type BlockedInfo struct {
	RuleName string
	Action   GuardAction
	Reason   string
}

func checkContextActive(ctx context.Context, _ *domain.DiagnosisContext) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
		return true, nil
	}
}

func checkBatchIDPresent(_ context.Context, state *domain.DiagnosisContext) (bool, error) {
	if state == nil {
		return false, fmt.Errorf("diagnosis context is nil")
	}
	return state.BatchID != "", nil
}

func checkBatchIDFormat(_ context.Context, state *domain.DiagnosisContext) (bool, error) {
	if state == nil || state.BatchID == "" {
		return false, nil
	}
	_, err := uuid.Parse(state.BatchID)
	if err != nil {
		return false, nil
	}
	return true, nil
}

var hardGuardRules = []GuardRule{
	{
		Name:   "context_active",
		Check:  checkContextActive,
		OnFail: GuardActionSchedule,
		Reason: "上下文已取消，需稍后重试",
	},
	{
		Name:   "batch_id_present",
		Check:  checkBatchIDPresent,
		OnFail: GuardActionReject,
		Reason: "BatchID 为空，无法进行诊断",
	},
	{
		Name:   "batch_id_uuid_format",
		Check:  checkBatchIDFormat,
		OnFail: GuardActionReject,
		Reason: "BatchID 非法（不是 UUID）",
	},
}

func RunGuards(ctx context.Context, state *domain.DiagnosisContext) GuardResult {
	for _, guard := range hardGuardRules {
		passed, err := guard.Check(ctx, state)
		if err != nil {
			return GuardResult{
				Passed: false,
				Blocked: &BlockedInfo{
					RuleName: guard.Name,
					Action:   GuardActionSchedule,
					Reason:   fmt.Sprintf("guard check failed: %v", err),
				},
			}
		}
		if !passed {
			return GuardResult{
				Passed: false,
				Blocked: &BlockedInfo{
					RuleName: guard.Name,
					Action:   guard.OnFail,
					Reason:   guard.Reason,
				},
			}
		}
	}

	return GuardResult{Passed: true}
}
