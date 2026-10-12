package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	sdk "github.com/meguminnnnnnnnn/go-openai"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPermanentAPIStatusClassification(t *testing.T) {
	for _, test := range []struct {
		status int
		want   bool
	}{{400, true}, {401, true}, {402, true}, {403, true}, {404, true}, {422, true}, {429, false}, {500, false}, {503, false}} {
		err := fmt.Errorf("wrapped: %w", &sdk.APIError{HTTPStatusCode: test.status})
		if got := IsPermanentError(err); got != test.want {
			t.Fatalf("status=%d got=%v", test.status, got)
		}
	}
}
func TestRealAdapterStatusClassification(t *testing.T) {
	for _, status := range []int{401, 429} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"fixture","type":"fixture","code":"fixture"}}`)
		}))
		policy := GovernorOptions{Concurrency: 1, Attempts: 3, Backoff: time.Millisecond}
		chat, e := NewGLM4ChatModel(&GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL, Governor: &policy})
		if e != nil {
			t.Fatal(e)
		}
		_, e = chat.Generate(context.Background(), nil)
		server.Close()
		if IsPermanentError(e) != (status == 401) {
			t.Fatalf("status=%d err=%v", status, e)
		}
		want := int64(1)
		if status == 429 {
			want = 3
		}
		if chat.(*GovernedModel).Snapshot().Attempts != want {
			t.Fatalf("status=%d retry not applied", status)
		}
	}
}
func TestAdapterForwardsToolChoice(t *testing.T) {
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"{}"}}]}`)
	}))
	defer server.Close()
	chat, e := NewGLM4ChatModel(&GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: server.URL + "/api.deepseek.com/"})
	if e != nil {
		t.Fatal(e)
	}
	e = chat.BindTools([]*schema.ToolInfo{{Name: "plan", Desc: "planning", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = chat.Generate(context.Background(), nil, model.WithTools([]*schema.ToolInfo{{Name: "plan", Desc: "planning", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}}), model.WithToolChoice(schema.ToolChoiceForced, "plan")); e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(body["tool_choice"])
	if !strings.Contains(string(encoded), `"name":"plan"`) {
		t.Fatalf("tool choice lost %s", encoded)
	}
	if _, e = chat.Generate(context.Background(), nil, model.WithTools([]*schema.ToolInfo{}), model.WithToolChoice(schema.ToolChoiceForbidden)); e != nil {
		t.Fatal(e)
	}
	if body["tool_choice"] != "none" {
		t.Fatalf("finalization override lost %v", body["tool_choice"])
	}
}
