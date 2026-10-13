package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/IBM/sarama"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	coreapp "github.com/xuewentao/argus-ota-platform/internal/application"
	core "github.com/xuewentao/argus-ota-platform/internal/domain"
	corekafka "github.com/xuewentao/argus-ota-platform/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/minio"
	corepg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
	"github.com/xuewentao/argus-ota-platform/internal/infrastructure/processing"
	coreredis "github.com/xuewentao/argus-ota-platform/internal/infrastructure/redis"
	"github.com/xuewentao/argus-ota-platform/internal/interfaces/http/handlers"
	"github.com/xuewentao/argus-ota-platform/internal/messaging"
	"github.com/xuewentao/argus-ota-platform/internal/testutil"
	app "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/application"
	aikafka "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/kafka"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	aipg "github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/postgres"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFullPipelineRealKafka(t *testing.T) { runFullPipeline(t, false) }
func TestFullPipelineActualCSVKafka(t *testing.T) {
	if os.Getenv("ARGUS_TEST_MINIO_ADDR") == "" {
		t.Skip("set ARGUS_TEST_MINIO_ADDR for real S3 storage")
	}
	runFullPipeline(t, true)
}
func runFullPipeline(t *testing.T, real bool) {
	brokers := os.Getenv("ARGUS_TEST_KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("set ARGUS_TEST_KAFKA_BROKERS for real Kafka workflow")
	}
	db := testutil.OpenDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	topic := "argus-test-" + uuid.NewString()
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	admin, err := sarama.NewClusterAdmin(strings.Split(brokers, ","), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err = admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.DeleteTopic(topic); admin.DeleteTopic(topic + "-dlq"); admin.Close() })
	publisher, err := corekafka.NewKafkaEventProducer(strings.Split(brokers, ","), topic, "")
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	repo := corepg.NewPostgresBatchRepository(db)
	orchestrator := coreapp.NewOrchestrateService(repo, nil, nil)
	var modelCalls, uploads atomic.Int32
	summary := "Connector loose"
	if real {
		summary = "Version-associated planner increase; causal attribution unproven"
	}
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := modelCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		message := map[string]interface{}{"role": "assistant", "content": `{"hypothesis_id":"H1","root_cause":"Connector loose","suggestions":["Inspect connector"],"severity":"high","confidence":0.85,"evidence_ids":["log:1"],"limitations":["Mock parser fixture"]}`}
		if real {
			message["content"] = fmt.Sprintf(`{"hypothesis_id":"H1","root_cause":%q,"suggestions":["Repeat identical replay and revert module change"],"severity":"high","confidence":0,"evidence_ids":["log:1"],"limitations":["Local scripted model; caller replay metadata; causality unproven"]}`, summary)
		}
		if call == 1 {
			message = map[string]interface{}{"role": "assistant", "content": "", "tool_calls": []interface{}{map[string]interface{}{"id": "stats", "type": "function", "function": map[string]string{"name": "error_statistics", "arguments": "{}"}}, map[string]interface{}{"id": "logs", "type": "function", "function": map[string]string{"name": "read_logs", "arguments": "{}"}}}}
		}
		if call == 1 {
			message["tool_calls"] = append(message["tool_calls"].([]interface{}), map[string]interface{}{"id": "plan", "type": "function", "function": map[string]string{"name": "update_investigation_plan", "arguments": `{"hypotheses":[{"id":"H1","description":"Connector loose","status":"supported","supports":["log:1"],"next_check":"Inspect connector"}]}`}})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"id": "fixture", "choices": []interface{}{map[string]interface{}{"index": 0, "message": message, "finish_reason": "stop"}}})
	}))
	defer modelServer.Close()
	diagnosisRepo := aipg.NewPostgresDiagnoseRepository(db)
	service, err := app.NewMultiAgentService(diagnosisRepo, nil, nil, &llm.GLM4Config{APIKey: "fixture", Model: "fixture", BaseURL: modelServer.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	ai := aikafka.NewConsumer(nil, service)
	subscribe := func(name string, handle messaging.MessageHandler) {
		t.Helper()
		consumer, err := corekafka.NewKafkaEventConsumer(strings.Split(brokers, ","), topic+name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { consumer.Close() })
		if err = consumer.Subscribe(ctx, []string{topic}, handle); err != nil {
			t.Fatal(err)
		}
	}
	subscribe("-orchestrator", orchestrator.HandleMessage)
	subscribe("-ai", ai.HandleMessage)
	stage := func(typ string, process func(context.Context, uuid.UUID) error) messaging.MessageHandler {
		return func(ctx context.Context, b []byte) error {
			var e struct {
				Type string `json:"event_type"`
				ID   string `json:"batch_id"`
			}
			if err := json.Unmarshal(b, &e); err != nil {
				return err
			}
			if e.Type != typ {
				return nil
			}
			id, err := uuid.Parse(e.ID)
			if err != nil {
				return err
			}
			return process(ctx, id)
		}
	}
	processor := &processing.Processor{DB: db, Python: processing.Python{Script: filepath.Join("..", "..", "..", "..", "workers", "python-aggregator", "regression.py"), Baseline: "v1", Candidate: "v2"}}
	if !real {
		subscribe("-parser", stage("BatchCreated", func(ctx context.Context, id uuid.UUID) error { return corepg.MockParseBatch(ctx, db, id) }))
		subscribe("-aggregator", stage("AllFilesScattered", func(ctx context.Context, id uuid.UUID) error { return corepg.MockAggregateBatch(ctx, db, id) }))
	} else {
		subscribe("-parser", stage("BatchCreated", processor.ParseBatch))
		subscribe("-aggregator", stage("AllFilesScattered", processor.AggregateBatch))
	}
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := corepg.DispatchOutbox(ctx, db, publisher, 100); err != nil && ctx.Err() == nil {
				t.Logf("relay: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	defer func() { cancel(); <-relayDone }()
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("location") {
			fmt.Fprint(w, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`)
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/fixture/") {
			b, _ := io.ReadAll(r.Body)
			if bytes.Contains(b, []byte("fixture rec bytes")) {
				uploads.Add(1)
			}
		}
		w.Header().Set("ETag", `"0123456789abcdef0123456789abcdef"`)
		w.WriteHeader(200)
	}))
	defer s3.Close()
	endpoint, bucket, access, secret := strings.TrimPrefix(s3.URL, "http://"), "fixture", "fixture", "fixture-secret"
	if real {
		endpoint = os.Getenv("ARGUS_TEST_MINIO_ADDR")
		bucket = "actual-" + uuid.NewString()
		access = "minioadmin"
		secret = "minioadmin"
	}
	storage, err := minio.NewMinIOClient(endpoint, bucket, access, secret, false)
	if err != nil {
		t.Fatal(err)
	}
	processor.Fetch = storage.GetObject
	gin.SetMode(gin.TestMode)
	router := gin.New()
	batchService := coreapp.NewBatchService(repo, corepg.NewPostgresFileRepository(db), nil)
	handlers.NewBatchHandler(batchService, storage).RegisterRoutes(router)
	var cache *coreredis.RedisClient
	if addr := os.Getenv("ARGUS_TEST_REDIS_ADDR"); addr != "" {
		cache, err = coreredis.NewRedisClient(ctx, addr, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer cache.Close()
	}
	query := coreapp.NewQueryService(repo, corepg.NewPostgresReportRepository(db), cache)
	qh := handlers.NewQueryHandler(query)
	router.GET("/api/v1/batches/:id/report", qh.GetReport)
	request := func(method, path, ctype string, body io.Reader) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, body).WithContext(ctx)
		r.Header.Set("Content-Type", ctype)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code >= 400 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	w := request("POST", "/api/v1/batches", "application/json", strings.NewReader(`{"vehicle_id":"fixture","vin":"VIN","expected_workers":1}`))
	var created struct {
		ID uuid.UUID `json:"batch_id"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	filename, content := "fixture.rec", "fixture rec bytes"
	if real {
		filename = "metrics.csv"
		content = "timestamp,version,vehicle_id,vehicle_class,module,replay_id,workload,error_code,cpu,memory_mb\n"
		for _, version := range []string{"v1", "v2"} {
			for i := 0; i < 10; i++ {
				cpu, errorCode := "20", ""
				if version == "v2" {
					cpu = "40"
					errorCode = "E42"
				}
				content += fmt.Sprintf("%d,%s,fixture,truck,planner,R1,AD,%s,%s,100\n", 1000+i, version, errorCode, cpu)
			}
		}
	}
	part, _ := form.CreateFormFile("file", filename)
	part.Write([]byte(content))
	form.Close()
	request("POST", "/api/v1/batches/"+created.ID.String()+"/files", form.FormDataContentType(), &body)
	request("POST", "/api/v1/batches/"+created.ID.String()+"/complete", "", nil)
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		b, e := repo.FindByID(ctx, created.ID)
		if e != nil {
			t.Fatal(e)
		}
		if b.Status == core.BatchStatusCompleted {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pipeline timeout at %s", b.Status)
		case <-tick.C:
		}
	}
	w = request("GET", "/api/v1/batches/"+created.ID.String()+"/report", "", nil)
	var report core.Report
	if err = json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.DiagnosisResult != summary || report.DiagnosisID == nil || report.Status != core.BatchStatusCompleted {
		t.Fatalf("report=%+v", report)
	}
	if (!real && uploads.Load() != 1) || modelCalls.Load() != 2 {
		t.Fatalf("uploads=%d model calls=%d", uploads.Load(), modelCalls.Load())
	}
	if err = service.DiagnoseBatch(ctx, created.ID.String()); err != nil {
		t.Fatal(err)
	}
	if modelCalls.Load() != 2 {
		t.Fatal("replayed diagnosis called model again")
	}
	if real {
		var regression struct {
			Status      string `json:"status"`
			SampleCount int    `json:"sample_count"`
			Comparisons []struct {
				CPU struct {
					Delta float64 `json:"delta"`
				} `json:"cpu"`
			} `json:"comparisons"`
		}
		if e := json.Unmarshal(report.VersionRegression, &regression); e != nil {
			t.Fatal(e)
		}
		if regression.Status != "comparable" || regression.SampleCount != 20 || len(regression.Comparisons) != 1 || regression.Comparisons[0].CPU.Delta != 20 {
			t.Fatalf("incorrect actual processing %s", report.VersionRegression)
		}
		t.Log("HTTP upload -> real MinIO -> real Kafka/PostgreSQL -> actual Python CSV parsing/comparison -> scripted model -> durable completed report; replay makes no extra calls")
		return
	}
	t.Log("real Kafka/PostgreSQL/Redis: HTTP upload -> mock parsing/aggregation -> model HTTP fixture -> durable diagnosis -> completed report; replay reused result")
}
