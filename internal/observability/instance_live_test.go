package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

// Transport/storage fixture only: independent SDKs export counters3 and7.
func TestRealCollectorSeparatesInstanceCounters(t *testing.T) {
	output := os.Getenv("ARGUS_LIVE_INSTANCE_PROOF")
	if output == "" {
		t.Skip("real Collector/Prometheus not configured")
	}
	ids := []string{uuid.NewString(), uuid.NewString()}
	for i, count := range []int{3, 7} {
		shutdown, err := Setup(context.Background(), Config{Endpoint: "http://127.0.0.1:4318", Service: "argus-instance-proof", Instance: ids[i], MetricInterval: time.Hour})
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
	query := fmt.Sprintf(`argus_model_tokens_total{service_name="argus-instance-proof",exported_instance=~"%s|%s"}`, ids[0], ids[1])
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://127.0.0.1:9090/api/v1/query?query=" + url.QueryEscape(query))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Status string `json:"status"`
			Data   struct {
				Result []struct {
					Metric map[string]string `json:"metric"`
					Value  []json.RawMessage `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]float64{}
		for _, series := range result.Data.Result {
			if len(series.Value) != 2 {
				t.Fatal("invalid native metric sample")
			}
			var value string
			if err = json.Unmarshal(series.Value[1], &value); err != nil {
				t.Fatal(err)
			}
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatal(err)
			}
			counts[series.Metric["exported_instance"]] = n
		}
		if result.Status == "success" && len(counts) == 2 && counts[ids[0]] == 3 && counts[ids[1]] == 7 {
			proof := map[string]interface{}{"synthetic_fixture": true, "service": "argus-instance-proof", "instances": counts, "series_count": 2, "sum": 10, "query": query, "prometheus_response": result, "ok": true}
			b, err := json.MarshalIndent(proof, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(output, b, 0644); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("two SDK counters collided or were not scraped")
}
