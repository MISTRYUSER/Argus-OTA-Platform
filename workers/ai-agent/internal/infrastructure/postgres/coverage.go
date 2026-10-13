package postgres

import (
	"encoding/json"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"strings"
)

// ApplyParsedCoverage advertises fixed-workflow evidence only when durable parse
// records and every returned log are completely covered. Routing still verifies
// the explicit error type, observed checks and conflicts itself.
func ApplyParsedCoverage(d *domain.AggregatedData, total int) {
	d.LogsSummary = truncate(d.RawLogs, 2400)
	if total < 1 || total > 100 || len(d.RawLogs) > 16000 {
		return
	}
	lines := strings.Split(strings.TrimSpace(d.RawLogs), "\n")
	if len(lines) != total {
		return
	}
	for _, line := range lines {
		var v struct {
			ErrorType string `json:"error_type"`
		}
		if json.Unmarshal([]byte(line), &v) != nil || v.ErrorType == "" {
			return
		}
	}
	d.LogsSummary = `{"hybrid_evidence_schema":"v1","stats_grain":"log_occurrence","complete":true}`
}
