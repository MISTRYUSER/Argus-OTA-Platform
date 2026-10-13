package observability

import (
	"context"
	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestMetricSDKsKeepIndependentInstanceIdentity(t *testing.T) {
	var mu sync.Mutex
	values := map[string]int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/metrics" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			request := &metricspb.ExportMetricsServiceRequest{}
			if err = proto.Unmarshal(body, request); err != nil {
				t.Error(err)
				return
			}
			for _, resource := range request.ResourceMetrics {
				instance := ""
				for _, attr := range resource.Resource.Attributes {
					if attr.Key == "service.instance.id" {
						instance = attr.Value.GetStringValue()
					}
				}
				for _, scope := range resource.ScopeMetrics {
					for _, metric := range scope.Metrics {
						if metric.Name == "argus_model_tokens_total" {
							for _, point := range metric.GetSum().DataPoints {
								mu.Lock()
								values[instance] = point.GetAsInt()
								mu.Unlock()
							}
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for instance, count := range map[string]int{"opaque-worker-a": 3, "opaque-worker-b": 7} {
		shutdown, err := Setup(context.Background(), Config{Endpoint: server.URL, Service: "same-service", Instance: instance, MetricInterval: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		AddTokens(context.Background(), count)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = shutdown(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(values) != 2 || values["opaque-worker-a"] != 3 || values["opaque-worker-b"] != 7 {
		t.Fatalf("same-service counters collided: %v", values)
	}
}

func TestDefaultInstanceIsOpaqueAndStableForProcess(t *testing.T) {
	first := instanceIdentity("")
	second := instanceIdentity("")
	if first == "" || first != second {
		t.Fatal("process instance identity missing or unstable")
	}
	if instanceIdentity("configured-pod-instance") != "configured-pod-instance" {
		t.Fatal("explicit deployment identity ignored")
	}
}

func TestDurationHistogramUsesSecondScaleBuckets(t *testing.T) {
	var bounds []float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/metrics" {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			request := &metricspb.ExportMetricsServiceRequest{}
			if err = proto.Unmarshal(b, request); err != nil {
				t.Error(err)
				return
			}
			for _, res := range request.ResourceMetrics {
				for _, scope := range res.ScopeMetrics {
					for _, metric := range scope.Metrics {
						if metric.Name == "argus_operation_duration_seconds" {
							bounds = metric.GetHistogram().DataPoints[0].ExplicitBounds
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	shutdown, err := Setup(context.Background(), Config{Endpoint: server.URL, Service: "duration-fixture", MetricInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	_, end := Operation(context.Background(), "tool")
	end(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	submillisecond := false
	hasLongTask := false
	for _, b := range bounds {
		if b > 0 && b <= 0.001 {
			submillisecond = true
		}
		if b >= 180 {
			hasLongTask = true
		}
	}
	if !submillisecond || !hasLongTask {
		t.Fatalf("seconds histogram cannot resolve fast tools and long model tasks: %v", bounds)
	}
}
