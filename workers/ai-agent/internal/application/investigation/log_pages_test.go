package investigation

import (
	"context"
	"encoding/json"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"strings"
	"testing"
)

func TestLogPaginationAndBounds(t *testing.T) {
	tools, _ := boundTools(reversalRepo{}, nil, "fixture")
	s, e := tools["read_logs"].InvokableRun(context.Background(), `{"offset":1,"limit":1}`)
	if e != nil {
		t.Fatal(e)
	}
	var o toolOutput
	json.Unmarshal([]byte(s), &o)
	if len(o.Evidence) != 1 || o.Evidence[0].ID != "log:2" {
		t.Fatalf("pagination not applied: %s", s)
	}
	if _, e = tools["read_logs"].InvokableRun(context.Background(), `{"offset":-1}`); e == nil {
		t.Fatal("negative offset accepted")
	}
}

type fragmentedLogs struct{ repositoryFixture }

func (fragmentedLogs) GetAggregatedData(context.Context, string) (*domain.AggregatedData, error) {
	lines := []string{"entity=payment namespace=business", "command invalid-command: executable not found", "container state=StartError"}
	for len(lines) < 60 {
		lines = append(lines, "ordinary status")
	}
	return &domain.AggregatedData{RawLogs: strings.Join(lines, "\n")}, nil
}
func TestTargetedLogReadIncludesAdjacentEntityIdentity(t *testing.T) {
	tools, _ := boundTools(fragmentedLogs{}, nil, "fixture")
	raw, err := tools["read_logs"].InvokableRun(context.Background(), `{"query":"invalid-command","limit":10}`)
	if err != nil {
		t.Fatal(err)
	}
	var out toolOutput
	json.Unmarshal([]byte(raw), &out)
	if len(out.Evidence) != 3 || out.Evidence[0].ID != "log:1" || !strings.Contains(out.Evidence[0].Content, "entity=payment") || out.NextOffset != 1 || out.TotalMatches != 1 {
		t.Fatalf("failure fragment separated from entity/cursor: %s", raw)
	}
}
func TestBroadLogReadSupportsFortyLinesWithinBound(t *testing.T) {
	tools, _ := boundTools(fragmentedLogs{}, nil, "fixture")
	raw, err := tools["read_logs"].InvokableRun(context.Background(), `{"limit":1000}`)
	if err != nil {
		t.Fatal(err)
	}
	var out toolOutput
	json.Unmarshal([]byte(raw), &out)
	if len(out.Evidence) != 40 || out.NextOffset != 40 || out.TotalMatches != 60 {
		t.Fatalf("broad page/cursor bound: %s", raw)
	}
}
func TestLogPageClampsLimitAndReportsCursor(t *testing.T) {
	tools, _ := boundTools(reversalRepo{}, nil, "fixture")
	raw, e := tools["read_logs"].InvokableRun(context.Background(), `{"limit":60}`)
	if e != nil {
		t.Fatal(e)
	}
	var o toolOutput
	json.Unmarshal([]byte(raw), &o)
	if len(o.Evidence) != 2 || o.NextOffset != 2 || o.TotalMatches != 2 {
		t.Fatalf("clamp/cursor incorrect: %s", raw)
	}
}
