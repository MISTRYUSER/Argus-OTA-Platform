package application
import (
	"context"
	"fmt"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)
//定义 guard failure Action
type GuardAction string

const (
	GuardActionReject GuardAction = "reject" // 拒绝执行后续操作 
	GuardActionNotify GuardAction = "notify" // 发送通知，但继续执行后续操作
	GuardActionSchedule  GuardAction = "schedule"  // 重试当前操作（适用于临时性错误）
)
type GuardRule struct {
	Name 	  string
	Check 	  func(ctx context.Context,state *domain.DiagnosisContext) (bool,error)
	OnFail 	  GuardAction
	Reason	  string
}
type GuardResult struct {
	Passed bool
	Blocked *BlockedInfo
}
type BlockedInfo struct {
	RuleName string
	Action   GuardAction
	Reason   string
}
// checkBatchIDValid 检查 BatchID 是否有效（前置条件）
func checkBatchIDValid(ctx context.Context, state *domain.DiagnosisContext) (bool, error) {
	return state.BatchID != "", nil
}

func checkVehicleOnline(ctx context.Context, state *domain.DiagnosisContext) (bool, error) {
	// TODO: 实现真实的车辆在线状态检查（调用外部服务或数据库查询）
	return true, nil
}

var hardGuardRules = []GuardRule{
	{
		Name:  "batch_id_valid",
		Check: checkBatchIDValid,
		OnFail: GuardActionReject,
		Reason: "BatchID 为空，无法进行诊断",
	},
	{
		Name:  "vehicle_online",
		Check: checkVehicleOnline,
		OnFail: GuardActionNotify,
		Reason: "车辆离线，可能无法获取实时数据",
	},
}	

func RunGuards(ctx context.Context,state *domain.DiagnosisContext) GuardResult {
	for _,guard := range hardGuardRules {
		passed,err := guard.Check(ctx,state)
		if err != nil {
			return GuardResult{
				Passed: false,
				Blocked: &BlockedInfo{
					RuleName: guard.Name,
					Action:   GuardActionNotify,
					Reason:  fmt.Sprintf("Guard检查失败: %v", err),
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
	return GuardResult{
		Passed:  true,
		Blocked: nil,
	}
}