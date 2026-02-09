package postgres

import (
	"strings"
	"testing"
)

func TestSummarizeLogs_ProducesStructuredSummary(t *testing.T) {
	raw := strings.Join([]string{
		"[2026-02-09 10:00:01] ERROR: CAN timeout dev=0x12",
		"[2026-02-09 10:00:02] ERROR: CAN timeout dev=0x13",
		"[2026-02-09 10:00:03] WARN: sensor jitter dev=0x20",
		"[2026-02-09 10:00:04] INFO: heartbeat ok",
		"[2026-02-09 10:00:05] ERROR: CAN timeout dev=0x14",
	}, "\n")

	out := summarizeLogs(raw, 240)

	if !strings.Contains(out, "total_lines=") {
		t.Fatalf("expected total_lines in summary, got: %s", out)
	}
	if !strings.Contains(out, "ERROR=") {
		t.Fatalf("expected ERROR count in summary, got: %s", out)
	}
	if len(out) > 240 {
		t.Fatalf("expected summary length <= 240, got %d", len(out))
	}
}

func TestSummarizeLogs_ShortInput(t *testing.T) {
	raw := "line1\nline2"
	out := summarizeLogs(raw, 1000)
	if out != raw {
		t.Fatalf("short logs should remain unchanged")
	}
}
