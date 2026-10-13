package pgvector

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"net/url"
	"os"
	"strings"
	"testing"
)

func lexicalFixture(t *testing.T) *PgvectorRetriever {
	t.Helper()
	dsn := os.Getenv("RETRIEVAL_BENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("requires explicit local benchmark PostgreSQL")
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" {
		t.Fatal("fixture database must be local")
	}
	q := u.Query()
	q.Set("search_path", "pg_temp")
	u.RawQuery = q.Encode()
	db, e := sql.Open("postgres", u.String())
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })
	if _, e = db.Exec(`CREATE TEMP TABLE knowledge_base(id BIGINT PRIMARY KEY,error_code TEXT,symptom_text TEXT NOT NULL,solution_text TEXT NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	for i, row := range [][3]string{{"E_FIRMWARE", "firmware unexpectedly fails", "inspect update log"}, {"E_FIRMWARE", "firmware update", "restore"}, {"E_OTHER", "thermal alarm", "inspect cooling"}, {"E_SYMBOL", "gain 50% and sensor_x path C:\\logs", "literal percent and underscore"}, {"E_SYMBOL", "gain 500 and sensorAx path C:logs", "avoid wildcard"}} {
		if _, e = db.Exec(`INSERT INTO knowledge_base VALUES($1,$2,$3,$4)`, i+1, row[0], row[1], row[2]); e != nil {
			t.Fatal(e)
		}
	}
	return NewTextRetriever(db)
}
func TestNaturalQueryRecallsDispersedLexemesWithoutWholePhrase(t *testing.T) {
	r := lexicalFixture(t)
	got, e := r.Search(context.Background(), domain.SearchParams{EmbeddingText: "firmware update failure", TopK: 10})
	if e != nil || len(got) != 2 {
		t.Fatalf("distributed lexical terms should recall only relevant docs: %v %v", got, e)
	}
	for _, item := range got {
		if !strings.Contains(item.Summary, "firmware") {
			t.Fatal("unrelated fallback", item)
		}
	}
}
func TestLiteralPhraseRanksBeforeDispersedTerms(t *testing.T) {
	r := lexicalFixture(t)
	got, e := r.Search(context.Background(), domain.SearchParams{EmbeddingText: "firmware update", TopK: 10})
	if e != nil || len(got) != 2 || !strings.HasPrefix(got[0].Summary, "firmware update;") {
		t.Fatal("phrase ranking or lexical recall wrong", got, e)
	}
}
func TestStructuredErrorFilterRemainsExactAndIndependentOfText(t *testing.T) {
	r := lexicalFixture(t)
	got, e := r.Search(context.Background(), domain.SearchParams{EmbeddingText: "firmware", ErrorCodes: []string{"E_OTHER"}, TopK: 10})
	if e != nil || len(got) != 1 || !strings.Contains(got[0].Summary, "thermal") {
		t.Fatal("structured filter lost or crossed", got, e)
	}
	got, e = r.Search(context.Background(), domain.SearchParams{EmbeddingText: "firmware", ErrorCodes: []string{"E_%' OR true --"}, TopK: 10})
	if e != nil || len(got) != 0 {
		t.Fatal("unbound error-code filter", got, e)
	}
}
func TestLiteralPercentUnderscoreDoNotBecomePhraseWildcards(t *testing.T) {
	r := lexicalFixture(t)
	for _, text := range []string{"gain 50%", "sensor_x", "path C:\\logs"} {
		got, e := r.Search(context.Background(), domain.SearchParams{EmbeddingText: text, TopK: 10})
		if e != nil || len(got) == 0 || !strings.Contains(got[0].Summary, "50%") {
			t.Fatal("literal phrase rank lost", text, got, e)
		}
	}
}
func TestEmptyPunctuationOrUnknownQueriesDoNotReturnUnrelatedCorpus(t *testing.T) {
	r := lexicalFixture(t)
	for _, text := range []string{"", "  ", "%%", "___", "!@#$?", "zzzznohits"} {
		got, e := r.Search(context.Background(), domain.SearchParams{EmbeddingText: text, TopK: 10})
		if len(got) != 0 {
			t.Fatal("empty/punctuation query returned corpus", fmt.Sprintf("%q", text), got)
		}
		if e != nil && text != "" {
			t.Fatal("parameterized plain query returned error", text, e)
		}
	}
}
func TestTextTopKRemainsCappedAtTen(t *testing.T) {
	r := lexicalFixture(t)
	for i := 6; i < 22; i++ {
		if _, e := r.db.Exec(`INSERT INTO knowledge_base VALUES($1,'E_FIRMWARE','firmware recall','inspect')`, i); e != nil {
			t.Fatal(e)
		}
	}
	got, e := r.Search(context.Background(), domain.SearchParams{EmbeddingText: "firmware", TopK: 100})
	if e != nil || len(got) != 10 {
		t.Fatal("topK cap violated", len(got), e)
	}
}
