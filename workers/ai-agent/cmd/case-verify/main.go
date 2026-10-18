// case-verify publishes an independently reviewed historical case to L2 recall.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/llm"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/pgvector"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	id := flag.String("diagnosis-id", "", "completed diagnosis UUID")
	cause := flag.String("cause", "", "independently verified root cause ID")
	module := flag.String("module", "", "exact module name")
	exception := flag.String("exception", "", "exact exception type, empty when absent")
	reviewer := flag.String("reviewer", "", "operator identity for audit")
	flag.Parse()
	diagnosis, err := uuid.Parse(*id)
	if err != nil {
		return fmt.Errorf("valid diagnosis-id required")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	embed, err := llm.NewEmbeddingModel(&llm.EmbeddingConfig{APIKey: os.Getenv("EMBEDDING_API_KEY"), BaseURL: os.Getenv("EMBEDDING_BASE_URL"), Model: os.Getenv("EMBEDDING_MODEL")})
	if err != nil {
		return err
	}
	retriever, err := pgvector.NewPgvectorRetriever(db, embed)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return retriever.PromoteCase(ctx, diagnosis, *cause, *module, *exception, *reviewer)
}
