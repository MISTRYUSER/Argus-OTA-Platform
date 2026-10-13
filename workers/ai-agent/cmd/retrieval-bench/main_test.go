package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCorpusKeepsStringIdentityAndRejectsDuplicates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "corpus.jsonl")
	os.WriteFile(p, []byte("{\"_id\":\"001\",\"title\":\"Title\",\"text\":\"Body\"}\n{\"_id\":\"9\",\"text\":\"Other\"}\n"), 0600)
	docs, e := loadCorpus(p)
	if e != nil || len(docs) != 2 || docs[0].ID != "001" {
		t.Fatal(docs, e)
	}
	os.WriteFile(p, []byte("{\"_id\":\"x\"}\n{\"_id\":\"x\"}\n"), 0600)
	if _, e = loadCorpus(p); e == nil {
		t.Fatal("accepted duplicate corpus IDs")
	}
}
func TestSelectedQueriesAreExactlyQrelsTestIDs(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "queries.jsonl"), []byte("{\"_id\":\"train\",\"text\":\"never evaluate\"}\n{\"_id\":\"test\",\"text\":\"claim\"}\n"), 0600)
	os.WriteFile(filepath.Join(d, "test.tsv"), []byte("query-id\tcorpus-id\tscore\ntest\tdoc\t1\n"), 0600)
	q, e := loadQueries(filepath.Join(d, "queries.jsonl"), filepath.Join(d, "test.tsv"))
	if e != nil || len(q) != 1 || q[0].ID != "test" {
		t.Fatal(q, e)
	}
	os.WriteFile(filepath.Join(d, "test.tsv"), []byte("query-id\tcorpus-id\tscore\nmissing\tdoc\t1\n"), 0600)
	if _, e = loadQueries(filepath.Join(d, "queries.jsonl"), filepath.Join(d, "test.tsv")); e == nil {
		t.Fatal("silent missing test query")
	}
}
func TestBenchmarkCallsProductionRetrieverAgainstTemporaryCorpus(t *testing.T) {
	dsn := os.Getenv("RETRIEVAL_BENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("requires explicitly configured local benchmark PostgreSQL")
	}
	ctx := context.Background()
	db, e := openIsolatedDB(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var path string
	if e = db.QueryRow("SHOW search_path").Scan(&path); e != nil || path != "pg_temp" {
		t.Fatal("unsafe search path", path, e)
	}
	docs := []document{{ID: "gold", Title: "Specific", Text: "a scientific claim"}, {ID: "other", Title: "Unrelated", Text: "unrelated"}}
	mapping, e := seedCorpus(ctx, db, docs)
	if e != nil {
		t.Fatal(e)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if e = writeSQLPlan(ctx, db, filepath.Join("..", "..", "internal", "infrastructure", "pgvector", "text_retriever.go"), planPath, query{ID: "plan-fixture", Text: "scientific claim"}); e != nil {
		t.Fatal(e)
	}
	plan, e := os.ReadFile(planPath)
	if e != nil || !strings.Contains(string(plan), "pg_temp") {
		t.Fatal("plan failed to use temporary corpus", string(plan), e)
	}
	rows, e := runQueries(ctx, db, []query{{ID: "hit", Text: "scientific claim"}, {ID: "empty", Text: "no document contains this full phrase"}}, mapping)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 || len(rows[0].Hits) != 1 || rows[0].Hits[0].CorpusID != "gold" || rows[0].Hits[0].Similarity != 0 || len(rows[1].Hits) != 0 {
		t.Fatal(rows)
	}
	if rows[0].Hits[0].EvalScore != 10 {
		t.Fatal("ranking score did not preserve rank")
	}
	encoded, _ := json.Marshal(rows)
	if !strings.Contains(string(encoded), "latency_ms") {
		t.Fatal("missing latency")
	}
	db.Close()
	db, e = openIsolatedDB(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var count int
	e = db.QueryRow("SELECT count(*) FROM knowledge_base").Scan(&count)
	if e == nil || !strings.Contains(e.Error(), "does not exist") {
		t.Fatal("temporary data leaked into next session", count, e)
	}
}

func TestSQLPlanReadsProductionConstantRatherThanCommentOrOtherSQL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "retriever.go")
	os.WriteFile(p, []byte("package fixture\n// const textSearchSQL = `DROP TABLE business`\nconst ignored = `SELECT 0`\nconst textSearchSQL = `SELECT $2`\n"), 0600)
	q, e := readTextSearchSQL(p)
	if e != nil || q != "SELECT $2" {
		t.Fatal(q, e)
	}
	os.WriteFile(p, []byte("package fixture\nconst ignored = `SELECT 0`"), 0600)
	if _, e = readTextSearchSQL(p); e == nil {
		t.Fatal("invented absent production SQL")
	}
}

func TestRawOutputRefusesExistingFileWithoutChangingBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "production.jsonl")
	original := []byte("historical baseline bytes\n")
	if e := os.WriteFile(p, original, 0600); e != nil {
		t.Fatal(e)
	}
	f, e := createOutput(p)
	if e == nil {
		f.Close()
		t.Fatal("overwrote existing benchmark output")
	}
	// Exercise the CLI boundary too: it must refuse before reading a missing dataset.
	savedFlags, savedArgs := flag.CommandLine, os.Args
	flag.CommandLine = flag.NewFlagSet("retrieval-bench-test", flag.ContinueOnError)
	os.Args = []string{"retrieval-bench", "-out", p, "-data", filepath.Join(t.TempDir(), "missing-dataset")}
	defer func() { flag.CommandLine = savedFlags; os.Args = savedArgs }()
	if err := run(); err == nil || !strings.Contains(err.Error(), "output exists") {
		t.Fatal("CLI did not refuse historical output before work", err)
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(original) {
		t.Fatal("historical output changed")
	}
}
