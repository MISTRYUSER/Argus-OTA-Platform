package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

type versionRegressionReader interface {
	GetVersionRegression(context.Context, string) (json.RawMessage, error)
}

func regressionTool(reader versionRegressionReader, id string) (tool.InvokableTool, error) {
	return utils.InferTool("read_version_regression", "Read code-computed baseline/candidate comparison scoped to this batch. Check comparability, matched replay/workload, sampling/window limitations and original file/line evidence before explaining module-associated candidates. Correlation is not causal proof; never recompute statistics with the language model.", func(ctx context.Context, _ *emptyInput) (*toolOutput, error) {
		raw, e := reader.GetVersionRegression(ctx, id)
		if e != nil {
			return nil, e
		}
		if len(raw) == 0 {
			return &toolOutput{Evidence: []domain.EvidenceItem{}, Unavailable: true, Message: "No version comparison available for this batch."}, nil
		}
		var report struct {
			Status      string            `json:"status"`
			Comparisons []json.RawMessage `json:"comparisons"`
			Limitations []string          `json:"limitations"`
		}
		if e = json.Unmarshal(raw, &report); e != nil {
			return nil, e
		}
		out := &toolOutput{Evidence: []domain.EvidenceItem{{ID: "regression:policy", Source: "batch:" + id + ":version_regression", Content: fmt.Sprintf("status=%s limitations=%v", report.Status, report.Limitations)}}}
		if len(out.Evidence[0].Content) > 2000 {
			out.Evidence[0].Content = out.Evidence[0].Content[:2000]
		}
		for i, comparison := range report.Comparisons {
			if i >= 20 {
				out.Message = "Only first20 groups returned; full report available through batch report API."
				break
			}
			if len(comparison) > 6000 {
				out.Message = "Oversized group omitted; inspect full report API."
				continue
			}
			out.Evidence = append(out.Evidence, domain.EvidenceItem{ID: fmt.Sprintf("regression:group:%d", i+1), Source: "batch:" + id + ":version_regression", Content: string(comparison)})
		}
		return out, nil
	})
}
