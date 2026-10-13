package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application/investigation"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
)

func TestPreflightCountsActualEligibleAndDoesNotUseConfidence(t *testing.T) {
	c := inputCase{ID: "1001", Lines: []string{`{"error_type":"OTA_CHECKSUM_MISMATCH","expected_checksum":"a","actual_checksum":"b"}`}, ErrorCodeStats: map[string]int{"OTA_CHECKSUM_MISMATCH": 1}, LogsSummary: fixtureCoverage}
	other := c
	other.ID = "1002"
	other.LogsSummary = "{}"
	got := preflightSuite(suite{Cases: []inputCase{c, other}})
	if got.Fixed != 1 || got.Agent != 1 || got.Cases[0].Reason != "OTA_CHECKSUM_MISMATCH" {
		t.Fatalf("%+v", got)
	}
}

func TestOTABriefIsDifferentFromSREAndRetainsRootCauseField(t *testing.T) {
	b, err := taskBrief("ota")
	if err != nil || !strings.Contains(b, "cause_code") || strings.Contains(b, "Kubernetes") || strings.Contains(b, "injection") {
		t.Fatal(b, err)
	}
	if _, err = taskBrief("unsupported"); err == nil {
		t.Fatal("unknown task accepted")
	}
}

type baselineCapture struct {
	model.ChatModel
	messages []*schema.Message
}

func (m *baselineCapture) Generate(_ context.Context, msg []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.messages = msg
	return &schema.Message{Content: `{"hypothesis_id":"H1","root_cause":"candidate","cause_code":"manifest_mismatch","suggestions":[],"severity":"high","confidence":0.5,"evidence_ids":["log:1","stats:OTA_CHECKSUM_MISMATCH"],"limitations":[]}`}, nil
}

func TestOTAFixedBaselineGetsSameEvidenceAndEnforcesReferences(t *testing.T) {
	c := inputCase{Lines: []string{`{"error_type":"OTA_CHECKSUM_MISMATCH"}`}, ErrorCodeStats: map[string]int{"OTA_CHECKSUM_MISMATCH": 1}}
	capture := &baselineCapture{}
	observer := &briefModel{ChatModel: capture}
	result, err := otaBaseline(context.Background(), observer, c, 1000000, "test")
	if err != nil || result == nil || len(result.Evidence) != 2 || len(result.EvidenceIDs) != 2 {
		t.Fatal(result, err)
	}
	msg := capture.messages[1].Content
	if !strings.Contains(msg, "log:1") || !strings.Contains(msg, "stats:OTA_CHECKSUM_MISMATCH") {
		t.Fatal(msg)
	}
	if _, err = otaBaseline(context.Background(), observer, c, 1, "test"); err == nil {
		t.Fatal("budget ignored")
	}
}

func TestActualHTTPUsageIncludesCacheAndProviderModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"model": "pinned-provider-v1", "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15, "prompt_cache_hit_tokens": 4, "prompt_cache_miss_tokens": 6}})
	}))
	defer server.Close()
	tr := &requestTrace{}
	req, _ := http.NewRequest("POST", server.URL, strings.NewReader(`{}`))
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	p := priceTable{Model: "configured-v1", AsOf: "2026-10-13", Source: "test tariff", InputCached: 1, InputUncached: 2, Output: 3}
	cost, known := measuredCost(tr.rows, &p)
	if !known || cost == nil || *cost != 0.000031 {
		t.Fatal(cost, known)
	}
	if tr.rows[0]["provider_model"] != "pinned-provider-v1" {
		t.Fatal(tr.rows)
	}
	tr.rows = append(tr.rows, map[string]any{"http_status": 429})
	if cost, known = measuredCost(tr.rows, &p); known || cost != nil {
		t.Fatal("unreported failed attempt priced as zero")
	}
	if cost, known = measuredCost(nil, &p); !known || cost == nil || *cost != 0 {
		t.Fatal("no attempted call should be zero")
	}
	if cost, known = measuredCost(tr.rows, nil); known || cost != nil {
		t.Fatal("missing tariff")
	}
}

func TestValidateCasesRejectsDuplicateIDsAndOverlongOTARecords(t *testing.T) {
	c := inputCase{ID: "1001", Lines: []string{"a"}, SHA: fmt.Sprintf("%x", sha256.Sum256([]byte("a")))}
	if err := validateCases(suite{Task: "ota", Cases: []inputCase{c, c}}); err == nil {
		t.Fatal("duplicate task would overwrite output")
	}
	c.Lines = []string{strings.Repeat("a", 401)}
	c.SHA = fmt.Sprintf("%x", sha256.Sum256([]byte(c.Lines[0])))
	if err := validateCases(suite{Task: "ota", Cases: []inputCase{c}}); err == nil {
		t.Fatal("production truncation would hide OTA evidence")
	}
}

func TestPriceTableRejectsOmittedRatesRatherThanClaimingFree(t *testing.T) {
	var p priceTable
	if err := json.Unmarshal([]byte(`{"model":"m","as_of":"2026-10-13","source":"tariff"}`), &p); err == nil {
		t.Fatal("missing tariff treated as zero fee")
	}
}

func TestRequestExposureRejectsIDOnlyAndTruncatedContent(t *testing.T) {
	line := `{"event":"decisive service delay","duration":500}`
	evidence := map[string]string{"log:1": line}
	body := map[string]interface{}{"messages": []interface{}{map[string]interface{}{"role": "tool", "content": `{"evidence":[{"id":"log:1","content":"truncated"}]}`}}}
	if got := requestEvidence(body, evidence); len(got) != 0 {
		t.Fatal(got)
	}
	payload, _ := json.Marshal(map[string]interface{}{"evidence": []interface{}{map[string]interface{}{"id": "log:1", "content": line}}})
	body["messages"] = []interface{}{map[string]interface{}{"role": "tool", "content": string(payload)}}
	if got := requestEvidence(body, evidence); len(got) != 1 {
		t.Fatal(got)
	}
	body["messages"] = []interface{}{map[string]interface{}{"role": "assistant", "content": string(payload)}}
	if got := requestEvidence(body, evidence); len(got) != 0 {
		t.Fatal("model fabrication counted as tool evidence", got)
	}
}

func TestOTADigestRejectsChangedStatisticsWithUnchangedLines(t *testing.T) {
	c := inputCase{ID: "1", Lines: []string{"a"}, ErrorCodeStats: map[string]int{"E": 1}, LogsSummary: "{}", Sources: []map[string]interface{}{}}
	c.SHA = fmt.Sprintf("%x", sha256.Sum256([]byte("a")))
	s := suite{Task: "ota", Adapter: "ota-fault-pack-v1", Benchmark: "test", Partition: "dev", InputSchema: "ota-input-v2", Cases: []inputCase{c}}
	s.Cases[0].FullInputSHA = fullCaseDigest(s.Cases[0], s)
	s.Revision = suiteDigest(s)
	if err := validateCases(s); err != nil {
		t.Fatal(err)
	}
	s.Cases[0].ErrorCodeStats["E"] = 2
	if err := validateCases(s); err == nil {
		t.Fatal("changed routing/model-visible statistics still accepted")
	}
}

func TestCompactedToolGroupTracksRetrievalSeparatelyFromExposure(t *testing.T) {
	lines := []string{}
	facts := map[string]string{}
	for i := 1; i <= 40; i++ {
		line := fmt.Sprintf(`{"row":%d,"detail":"%s"}`, i, strings.Repeat("x", 280))
		lines = append(lines, line)
		facts[fmt.Sprintf("log:%d", i)] = line
	}
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		msg := map[string]interface{}{"role": "assistant"}
		if count == 1 {
			calls := []interface{}{}
			for _, item := range []struct{ name, args string }{{"read_logs", `{"limit":40}`}, {"error_statistics", `{}`}, {"update_investigation_plan", `{"hypotheses":[{"id":"H1","description":"observed symptom","status":"supported","supports":["log:1"],"next_check":"inspect"}]}`}} {
				calls = append(calls, map[string]interface{}{"id": item.name, "type": "function", "function": map[string]string{"name": item.name, "arguments": item.args}})
			}
			msg["tool_calls"] = calls
			msg["content"] = ""
		} else {
			msg["content"] = `{"hypothesis_id":"H1","root_cause":"unknown","cause_code":"unknown","suggestions":[],"severity":"info","confidence":0,"evidence_ids":["log:1"],"limitations":["scripted test"]}`
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": msg, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	tr := &requestTrace{evidence: facts}
	chat, err := llm.NewGLM4ChatModel(&llm.GLM4Config{APIKey: "fixture", BaseURL: server.URL, Model: "fixture", HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		t.Fatal(err)
	}
	store := &repo{lines: lines, errorCodeStats: map[string]int{"E": 1}}
	a, err := investigation.NewAgent(store, nil, chat, investigation.Options{MaxContextChars: 6000, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Run(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextCompactions < 1 || len(tr.rows) != 2 {
		t.Fatal("test did not exercise compaction", result)
	}
	var cp struct {
		Evidence map[string]domain.EvidenceItem
	}
	json.Unmarshal(store.checkpoint, &cp)
	if _, ok := cp.Evidence["log:40"]; !ok {
		t.Fatal("decisive record was not retrieved")
	}
	for _, id := range tr.rows[1]["supplied_evidence_ids"].([]string) {
		if id == "log:40" {
			t.Fatal("omitted record counted as supplied fact")
		}
	}
}
