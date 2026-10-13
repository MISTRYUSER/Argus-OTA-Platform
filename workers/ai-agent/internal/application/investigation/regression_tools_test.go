package investigation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type regressionRepoFixture struct{ repositoryFixture }

func (regressionRepoFixture) GetVersionRegression(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"status":"comparable","comparisons":[{"group":{"module":"planner"},"cpu":{"delta":20},"evidence_ids":["source:minio:file:2"]}],"limitations":["causality unproven"]}`), nil
}
func TestVersionRegressionToolUsesComputedDataAndBoundedSources(t *testing.T) {
	tools, e := boundTools(regressionRepoFixture{}, nil, "batch-fixture")
	if e != nil {
		t.Fatal(e)
	}
	tool := tools["read_version_regression"]
	if tool == nil {
		t.Fatal("computed regression report tool missing")
	}
	raw, e := tool.InvokableRun(context.Background(), `{}`)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(raw, "planner") || !strings.Contains(raw, "delta") || !strings.Contains(raw, "source:minio:file:2") || !strings.Contains(raw, "causality unproven") {
		t.Fatalf("lost numerical or source evidence %s", raw)
	}
}
