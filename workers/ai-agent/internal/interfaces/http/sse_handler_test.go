package http

import (
	"bufio"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSSEForwardsRealRedisProgressAndCancels(t *testing.T) {
	addr := os.Getenv("ARGUS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set ARGUS_TEST_REDIS_ADDR")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewSSEHandler(client)
	router.GET("/batches/:id/stream", handler.StreamProgress)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id := uuid.NewString()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/batches/"+id+"/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(resp.Header)
	}
	payload := `{"event_type":"diagnosis_started","progress":0}`
	if err = client.Publish(ctx, "batch:"+id+":progress", payload).Err(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(line) != "data: "+payload {
		t.Fatal(line)
	}
	cancel()
	io.Copy(io.Discard, resp.Body)
}
