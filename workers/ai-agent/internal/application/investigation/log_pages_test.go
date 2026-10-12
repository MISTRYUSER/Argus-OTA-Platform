package investigation

import (
	"context"
	"encoding/json"
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
