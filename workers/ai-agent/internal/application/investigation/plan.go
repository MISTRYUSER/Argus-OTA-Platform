package investigation

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"strings"
)

type planInput struct {
	Hypotheses []domain.Hypothesis `json:"hypotheses"`
}

func planTool(s *agentState) (tool.InvokableTool, error) {
	return utils.InferTool("update_investigation_plan", "Record or revise at most five competing fault hypotheses. Status is open, supported or refuted. Supported/refuted hypotheses must cite gathered evidence IDs. This changes only the investigation plan, never a vehicle.", func(ctx context.Context, in *planInput) (*planInput, error) {
		if in == nil || len(in.Hypotheses) == 0 || len(in.Hypotheses) > 5 {
			return nil, fmt.Errorf("plan requires 1..5 hypotheses")
		}
		seen := map[string]bool{}
		for _, h := range in.Hypotheses {
			if h.ID == "" || len(h.ID) > 64 || seen[h.ID] || strings.TrimSpace(h.Description) == "" || len([]rune(h.Description)) > 600 || len([]rune(h.NextCheck)) > 600 {
				return nil, fmt.Errorf("invalid hypothesis")
			}
			seen[h.ID] = true
			if h.Status != "open" && h.Status != "supported" && h.Status != "refuted" {
				return nil, fmt.Errorf("invalid hypothesis status")
			}
			if h.Status == "supported" && len(h.Supports) == 0 || h.Status == "refuted" && len(h.Refutes) == 0 {
				return nil, fmt.Errorf("hypothesis status requires evidence")
			}
			for _, id := range append(append([]string{}, h.Supports...), h.Refutes...) {
				if _, ok := s.evidence[id]; !ok {
					return nil, fmt.Errorf("unknown evidence reference: %s", id)
				}
			}
		}
		s.hypotheses = append([]domain.Hypothesis(nil), in.Hypotheses...)
		return in, nil
	})
}
