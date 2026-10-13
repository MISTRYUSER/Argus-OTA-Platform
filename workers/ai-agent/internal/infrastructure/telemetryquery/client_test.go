package telemetryquery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
)

func fixtureScope(context.Context, string) (domain.IncidentScope, error) {
	return domain.IncidentScope{Namespace: "fleet-prod", Service: "ota", Start: time.Unix(1700000000, 0), End: time.Unix(1700000060, 0)}, nil
}
func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c, err := NewClient(Config{PrometheusURL: s.URL, LokiURL: s.URL, TempoURL: s.URL, AllowedMetrics: []string{"argus_errors_total"}, Timeout: time.Second}, fixtureScope)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestProtocolsUseTrustedScopeAndStableEvidence(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/v1/query_range":
			if q.Get("query") != `argus_errors_total{namespace="fleet-prod",service_name="ota"}` || q.Get("start") != "1700000000" || q.Get("end") != "1700000060" {
				t.Errorf("unscoped request: %s", r.URL)
			}
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"namespace":"fleet-prod","service_name":"ota"},"values":[[1700000000,"3"],[1700000060,"4"]]}]}}`)
		case "/loki/api/v1/query_range":
			if q.Get("query") != `{namespace="fleet-prod",service_name="ota"} |= "failed\"} | {namespace=\"other"` || q.Get("start") != "1700000000000000000" {
				t.Errorf("unsafe logs: %s", r.URL)
			}
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"namespace":"fleet-prod","service_name":"ota"},"values":[["1700000000000000000","failed"]]}]}}`)
		case "/api/search":
			if q.Get("q") != `{ resource.k8s.namespace.name = "fleet-prod" && resource.service.name = "ota" && status = error } | select(resource.k8s.namespace.name, resource.service.name)` || q.Get("start") != "1700000000" {
				t.Errorf("unsafe traces: %s", r.URL)
			}
			fmt.Fprint(w, `{"traces":[{"traceID":"abcdef","rootServiceName":"ota","startTimeUnixNano":"1700000000000000000","durationMs":3,"spanSets":[{"spans":[{"spanID":"1234","name":"deploy","startTimeUnixNano":"1700000000000000000","durationNanos":"3000000","attributes":[{"key":"resource.k8s.namespace.name","value":{"stringValue":"fleet-prod"}},{"key":"resource.service.name","value":{"stringValue":"ota"}}]}]}]}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	m, err := c.QueryMetrics(context.Background(), "batch-1", domain.MetricQuery{Metric: "argus_errors_total", Limit: 1})
	if err != nil || len(m) != 1 {
		t.Fatalf("metrics %v %v", m, err)
	}
	m2, _ := c.QueryMetrics(context.Background(), "batch-1", domain.MetricQuery{Metric: "argus_errors_total", Limit: 1})
	if m[0].ID != m2[0].ID || !strings.Contains(m[0].Source, "prometheus") || !strings.Contains(m[0].Source, "fleet-prod") {
		t.Fatal("missing stable provenance", m)
	}
	l, err := c.QueryLogs(context.Background(), "batch-1", domain.LogQuery{Contains: `failed"} | {namespace="other`, Limit: 1})
	if err != nil || len(l) != 1 {
		t.Fatalf("logs %v %v", l, err)
	}
	tr, err := c.QueryTraces(context.Background(), "batch-1", domain.TraceQuery{Status: "error", Limit: 1})
	if err != nil || len(tr) != 1 {
		t.Fatalf("traces %v %v", tr, err)
	}
}
func TestRejectUntrustedQueriesBeforeNetwork(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid query reached server") })
	for _, m := range []string{"up", "argus_errors_total{namespace=\"other\"}", "argus_errors_total or up"} {
		if _, e := c.QueryMetrics(context.Background(), "b", domain.MetricQuery{Metric: m}); e == nil {
			t.Error("accepted", m)
		}
	}
	if _, e := c.QueryTraces(context.Background(), "b", domain.TraceQuery{Status: `error } || { true`}); e == nil {
		t.Fatal("accepted trace injection")
	}
}
func TestRejectMalformedOversizedAndCrossScopeResponses(t *testing.T) {
	for _, body := range []string{`{`, strings.Repeat("x", (1<<20)+1), `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"namespace":"other","service_name":"ota"},"values":[[1700000000,"1"]]}]}}`, `{"status":"error","error":"unavailable"}`, `{"status":"success","data":{"resultType":"vector","result":[]}}`} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			if _, err := c.QueryMetrics(context.Background(), "b", domain.MetricQuery{Metric: "argus_errors_total"}); err == nil {
				t.Fatal("accepted unsafe response")
			}
		})
	}
}
func TestCancellationAndScopeFailures(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.QueryLogs(ctx, "b", domain.LogQuery{}); err == nil {
		t.Fatal("ignored cancellation")
	}
	c.resolve = func(context.Context, string) (domain.IncidentScope, error) {
		s, _ := fixtureScope(context.Background(), "")
		s.End = s.Start.Add(2 * time.Hour)
		return s, nil
	}
	if _, err := c.QueryLogs(context.Background(), "b", domain.LogQuery{}); err == nil {
		t.Fatal("accepted unbounded scope")
	}
}

func TestRejectTraceFromOtherNamespace(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"traces":[{"traceID":"abcdef","startTimeUnixNano":"1700000000000000000","spanSets":[{"spans":[{"spanID":"1234","startTimeUnixNano":"1700000000000000000","attributes":[{"key":"resource.k8s.namespace.name","value":{"stringValue":"other"}},{"key":"resource.service.name","value":{"stringValue":"ota"}}]}]}]}]}`)
	})
	if _, err := c.QueryTraces(context.Background(), "b", domain.TraceQuery{}); err == nil {
		t.Fatal("accepted trace spans from other namespace")
	}
}

func TestBoundEvidenceResults(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"namespace":"fleet-prod","service_name":"ota"},"values":[["1700000000000000000","first"],["1700000001000000000","second"]]}]}}`)
	})
	items, e := c.QueryLogs(context.Background(), "b", domain.LogQuery{Limit: 1})
	if e != nil || len(items) != 1 || !strings.Contains(items[0].Content, "first") {
		t.Fatalf("bounded evidence %v %v", items, e)
	}
	for _, n := range []int{-1, 101} {
		if _, e := c.QueryLogs(context.Background(), "b", domain.LogQuery{Limit: n}); e == nil {
			t.Fatal("accepted invalid limit", n)
		}
	}
}
func TestCancellationInFlightAndTimeout(t *testing.T) {
	started := make(chan struct{}, 2)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.QueryLogs(ctx, "b", domain.LogQuery{}); done <- e }()
	<-started
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("ignored canceled in-flight query")
		}
	case <-time.After(time.Second):
		t.Fatal("query failed to stop")
	}
	c.http.Timeout = 20 * time.Millisecond
	if _, e := c.QueryLogs(context.Background(), "b", domain.LogQuery{}); e == nil {
		t.Fatal("ignored endpoint timeout")
	}
}
func TestRejectLogOutsideScopeAndTime(t *testing.T) {
	for _, body := range []string{`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"namespace":"fleet-prod","service_name":"other"},"values":[["1700000000000000000","private"]]}]}}`, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"namespace":"fleet-prod","service_name":"ota"},"values":[["1699999999000000000","old"]]}]}}`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, e := c.QueryLogs(context.Background(), "b", domain.LogQuery{}); e == nil {
			t.Fatal("accepted unsafe log response")
		}
	}
}
func TestRejectRedirectAndOversizedConfig(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/", http.StatusFound)
	})
	if _, e := c.QueryLogs(context.Background(), "b", domain.LogQuery{}); e == nil {
		t.Fatal("followed redirect")
	}
	if _, e := NewClient(Config{PrometheusURL: "http://localhost", LokiURL: "http://localhost", TempoURL: "http://localhost", MaxResponseBytes: 2 << 20}, fixtureScope); e == nil {
		t.Fatal("accepted excess byte cap")
	}
}

func TestMetricPreservesFractionalScopeAndRejectsEarlierSample(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") != "1700000000.9" || r.URL.Query().Get("end") != "1700000060.9" {
			t.Error("fractional scope lost", r.URL)
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"namespace":"fleet-prod","service_name":"ota"},"values":[[1700000000,"3"]]}]}}`)
	})
	c.resolve = func(context.Context, string) (domain.IncidentScope, error) {
		s, _ := fixtureScope(context.Background(), "")
		s.Start = s.Start.Add(900 * time.Millisecond)
		s.End = s.End.Add(900 * time.Millisecond)
		return s, nil
	}
	if _, e := c.QueryMetrics(context.Background(), "b", domain.MetricQuery{Metric: "argus_errors_total"}); e == nil {
		t.Fatal("accepted metric before precise incident start")
	}
}
func TestTempoWireAttributeAliasesAndConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			attrs := `{"key":"k8s.namespace.name","value":{"stringValue":"fleet-prod"}},{"key":"service.name","value":{"stringValue":"ota"}}`
			if conflict {
				attrs += `,{"key":"resource.k8s.namespace.name","value":{"stringValue":"other"}}`
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"traces":[{"traceID":"abc","spanSets":[{"spans":[{"spanID":"def","name":"deploy","startTimeUnixNano":"1700000000000000000","durationNanos":"5","attributes":[%s]}]}]}]}`, attrs)
			})
			items, e := c.QueryTraces(context.Background(), "b", domain.TraceQuery{})
			if conflict {
				if e == nil {
					t.Fatal("accepted conflicting aliases")
				}
			} else if e != nil || len(items) != 1 {
				t.Fatalf("actual Tempo wire rejected %v %v", items, e)
			}
		})
	}
}
func TestLargeLogsNormalizeUnderCumulativeCheckpointBudget(t *testing.T) {
	line := strings.Repeat("界", 110000)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		values := make([][]string, 100)
		for i := range values {
			values[i] = []string{"1700000000000000000", strings.Repeat("<", 1400)}
		}
		if r.URL.Query().Get("limit") == "1" {
			values = [][]string{{"1700000000000000000", line}}
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "streams", "result": []any{map[string]any{"stream": map[string]string{"namespace": "fleet-prod", "service_name": "ota", "extra": strings.Repeat("x", 1000)}, "values": values}}}})
	})
	var cumulative []json.RawMessage
	for i := 0; i < 6; i++ {
		items, e := c.QueryLogs(context.Background(), "b", domain.LogQuery{Limit: 100})
		if e != nil || len(items) == 0 {
			t.Fatal(items, e)
		}
		raw, _ := json.Marshal(items)
		if len(raw) > 16*1024 {
			t.Fatalf("normalized query JSON too large: %d", len(raw))
		}
		if !strings.Contains(items[len(items)-1].Source, "result_truncated=true") {
			t.Fatal("missing output-budget truncation provenance")
		}
		for _, item := range items {
			if utf8.RuneCountInString(item.Content) > 1000 {
				t.Fatal("oversized content")
			}
			if !strings.Contains(item.Source, "original_sha256=") || !strings.Contains(item.Content, "truncated") {
				t.Fatal("missing explicit content truncation provenance")
			}
		}
		cumulative = append(cumulative, raw)
	}
	// Model the checkpoint's three copies (evidence, tool trace, conversation) across its six telemetry calls.
	checkpoint, _ := json.Marshal(map[string]any{"evidence": cumulative, "tool_trace": cumulative, "messages": cumulative, "result": map[string]any{"evidence": cumulative, "tool_trace": cumulative, "messages": cumulative}})
	if len(checkpoint) > 768*1024 {
		t.Fatalf("telemetry copies exhaust checkpoint budget: %d", len(checkpoint))
	}
	items, e := c.QueryLogs(context.Background(), "b", domain.LogQuery{Limit: 1})
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	raw, _ := json.Marshal(items)
	if len(raw) > 16*1024 {
		t.Fatal("single giant log exceeded budget")
	}
	firstID := items[0].ID
	line = strings.Repeat("界", 109999) + "异"
	other, _ := c.QueryLogs(context.Background(), "b", domain.LogQuery{Limit: 1})
	if other[0].ID == firstID {
		t.Fatal("ID ignored original truncated tail")
	}
}

func TestMetricNanosecondBoundaryAndSafeNormalization(t *testing.T) {
	for _, tc := range []struct {
		stamp, value string
		bad          bool
	}{{"1700000000.899999999", "3", true}, {"1700000000.9", "3", false}, {"1700000060.900000001", "3", true}, {"1700000060.9", strings.Repeat("1", 65), true}, {"1700000001", "invalid", true}} {
		t.Run(tc.stamp+tc.value[:1], func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"namespace":"fleet-prod","service_name":"ota","untrusted":"%s"},"values":[[%s,%q]]}]}}`, strings.Repeat("x", 350000), tc.stamp, tc.value)
			})
			c.resolve = func(context.Context, string) (domain.IncidentScope, error) {
				s, _ := fixtureScope(context.Background(), "")
				s.Start = s.Start.Add(900 * time.Millisecond)
				s.End = s.End.Add(900 * time.Millisecond)
				return s, nil
			}
			items, e := c.QueryMetrics(context.Background(), "b", domain.MetricQuery{Metric: "argus_errors_total"})
			if tc.bad {
				if e == nil {
					t.Fatal("accepted invalid precise metric")
				}
			} else {
				if e != nil || len(items) != 1 {
					t.Fatal(items, e)
				}
				if strings.Contains(items[0].Content, "untrusted") || len(items[0].Content) > 1000 || !strings.Contains(items[0].Source, "original_sha256=") {
					t.Fatal("metric carried untrusted giant labels", items)
				}
			}
		})
	}
}
func TestTraceNameIsBoundedWithOriginalHash(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"traces":[{"traceID":"abc","spanSets":[{"spans":[{"spanID":"def","name":%q,"startTimeUnixNano":"1700000000000000000","durationNanos":"5","attributes":[{"key":"k8s.namespace.name","value":{"stringValue":"fleet-prod"}},{"key":"service.name","value":{"stringValue":"ota"}}]}]}]}]}`, strings.Repeat("界", 100000))
	})
	items, e := c.QueryTraces(context.Background(), "b", domain.TraceQuery{})
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	if utf8.RuneCountInString(items[0].Content) > 1000 || !strings.Contains(items[0].Content, "truncated original_sha256=") {
		t.Fatal("unbounded trace name")
	}
}

func TestDistinctTracesWithIdenticalSpansHaveDistinctEvidenceIDs(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		span := `{"spanID":"same","name":"deploy","startTimeUnixNano":"1700000000000000000","durationNanos":"5","attributes":[{"key":"k8s.namespace.name","value":{"stringValue":"fleet-prod"}},{"key":"service.name","value":{"stringValue":"ota"}}]}`
		fmt.Fprintf(w, `{"traces":[{"traceID":"first","spanSets":[{"spans":[%s]}]},{"traceID":"second","spanSets":[{"spans":[%s]}]}]}`, span, span)
	})
	items, e := c.QueryTraces(context.Background(), "b", domain.TraceQuery{})
	if e != nil || len(items) != 2 {
		t.Fatal(items, e)
	}
	if items[0].ID == items[1].ID {
		t.Fatal("distinct trace IDs collide on original span evidence")
	}
}

func TestPrometheusRequestRoundsInwardToMillis(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") != "1700000000.901" || r.URL.Query().Get("end") != "1700000060.9" {
			t.Error("request not aligned inward to Prometheus milliseconds", r.URL)
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"namespace":"fleet-prod","service_name":"ota"},"values":[[1700000000.901,"3"]]}]}}`)
	})
	c.resolve = func(context.Context, string) (domain.IncidentScope, error) {
		s, _ := fixtureScope(context.Background(), "")
		s.Start = s.Start.Add(900001 * time.Microsecond)
		s.End = s.End.Add(900001 * time.Microsecond)
		return s, nil
	}
	items, e := c.QueryMetrics(context.Background(), "b", domain.MetricQuery{Metric: "argus_errors_total"})
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
}
func TestPrometheusWindowWithoutRepresentableMillisReturnsEmpty(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("queried Prometheus with no in-scope millisecond timestamp")
	})
	c.resolve = func(context.Context, string) (domain.IncidentScope, error) {
		s, _ := fixtureScope(context.Background(), "")
		s.Start = s.Start.Add(1 * time.Nanosecond)
		s.End = s.Start.Add(500 * time.Microsecond)
		return s, nil
	}
	items, e := c.QueryMetrics(context.Background(), "b", domain.MetricQuery{Metric: "argus_errors_total"})
	if e != nil || items == nil || len(items) != 0 {
		t.Fatal("expected explicit empty evidence array", items, e)
	}
}
