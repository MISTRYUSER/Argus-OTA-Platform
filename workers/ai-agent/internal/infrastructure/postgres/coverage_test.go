package postgres

import (
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/hybrid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"testing"
)

func TestParsedCoverageContractCanRouteActualOTAObservation(t *testing.T) {
	d := &domain.AggregatedData{RawLogs: `{"error_type":"OTA_CHECKSUM_MISMATCH","expected_checksum":"a","actual_checksum":"b"}`, ErrorCodeStats: map[string]int{"OTA_CHECKSUM_MISMATCH": 1}}
	ApplyParsedCoverage(d, 1)
	if ok, _ := hybrid.Eligible(d); !ok {
		t.Fatal("real parsed observation not eligible")
	}
	ApplyParsedCoverage(d, 2)
	if ok, _ := hybrid.Eligible(d); ok {
		t.Fatal("incomplete logs eligible")
	}
}
