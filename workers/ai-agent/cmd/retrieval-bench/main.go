// retrieval-bench runs the production text retriever over an isolated BEIR corpus.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/domain"
	"github.com/xuewentao/argus-ota-platform/workers/ai-agent/internal/infrastructure/pgvector"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type document struct {
	ID    string `json:"_id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}
type query struct {
	ID   string `json:"_id"`
	Text string `json:"text"`
}
type hit struct {
	CorpusID    string  `json:"corpus_id"`
	DiagnosisID string  `json:"diagnosis_id"`
	Rank        int     `json:"rank"`
	Similarity  float64 `json:"production_similarity"`
	EvalScore   float64 `json:"rank_derived_eval_score"`
}
type result struct {
	QueryID   string  `json:"query_id"`
	Query     string  `json:"query"`
	Hits      []hit   `json:"hits"`
	LatencyMS float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
}

func readJSONLines(path string, each func([]byte) error) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		if e = each(scanner.Bytes()); e != nil {
			return e
		}
	}
	return scanner.Err()
}
func loadCorpus(path string) ([]document, error) {
	docs := []document{}
	seen := map[string]bool{}
	e := readJSONLines(path, func(line []byte) error {
		var d document
		if e := json.Unmarshal(line, &d); e != nil {
			return e
		}
		if d.ID == "" || seen[d.ID] {
			return errors.New("empty or duplicate corpus ID")
		}
		seen[d.ID] = true
		docs = append(docs, d)
		return nil
	})
	return docs, e
}
func loadQueries(queryPath, qrelsPath string) ([]query, error) {
	f, e := os.Open(qrelsPath)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.Comma = '\t'
	header, e := reader.Read()
	if e != nil || strings.Join(header, "\t") != "query-id\tcorpus-id\tscore" {
		return nil, errors.New("invalid BEIR qrels header")
	}
	ids := map[string]bool{}
	for {
		row, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if len(row) != 3 {
			return nil, errors.New("invalid qrels row")
		}
		ids[row[0]] = true
	}
	selected := map[string]query{}
	e = readJSONLines(queryPath, func(line []byte) error {
		var q query
		if e := json.Unmarshal(line, &q); e != nil {
			return e
		}
		if ids[q.ID] {
			if _, exists := selected[q.ID]; exists {
				return errors.New("duplicate query ID")
			}
			selected[q.ID] = q
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if len(selected) != len(ids) {
		return nil, errors.New("missing test queries")
	}
	queries := make([]query, 0, len(ids))
	for _, q := range selected {
		queries = append(queries, q)
	}
	sort.Slice(queries, func(i, j int) bool { return queries[i].ID < queries[j].ID })
	return queries, nil
}

// The session search_path is configured in the connector DSN, so a reconnect
// also fails closed rather than accidentally falling through to business tables.
func openIsolatedDB(ctx context.Context, dsn string) (*sql.DB, error) {
	u, e := url.Parse(dsn)
	if e != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, errors.New("benchmark requires PostgreSQL URL")
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return nil, errors.New("benchmark PostgreSQL must be local")
	}
	values := u.Query()
	values.Set("search_path", "pg_temp")
	u.RawQuery = values.Encode()
	db, e := sql.Open("postgres", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
func seedCorpus(ctx context.Context, db *sql.DB, docs []document) (map[uuid.UUID]string, error) {
	if _, e := db.ExecContext(ctx, `CREATE TEMP TABLE knowledge_base(id BIGINT PRIMARY KEY,error_code TEXT,symptom_text TEXT NOT NULL,solution_text TEXT NOT NULL) ON COMMIT PRESERVE ROWS`); e != nil {
		return nil, e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	stmt, e := tx.PrepareContext(ctx, pq.CopyIn("knowledge_base", "id", "error_code", "symptom_text", "solution_text"))
	if e != nil {
		return nil, e
	}
	defer stmt.Close()
	mapping := map[uuid.UUID]string{}
	for i, d := range docs {
		id := int64(i + 1)
		if _, e = stmt.ExecContext(ctx, id, "BEIR_SCIFACT", d.Title, d.Text); e != nil {
			return nil, e
		}
		mapping[uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("knowledge-base:%d", id)))] = d.ID
	}
	if _, e = stmt.ExecContext(ctx); e != nil {
		return nil, e
	}
	if e = stmt.Close(); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return mapping, nil
}
func runQueries(ctx context.Context, db *sql.DB, queries []query, mapping map[uuid.UUID]string) ([]result, error) {
	retriever := pgvector.NewTextRetriever(db)
	results := make([]result, 0, len(queries))
	for _, q := range queries {
		queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		start := time.Now()
		cases, e := retriever.Search(queryCtx, domain.SearchParams{EmbeddingText: q.Text, TopK: 10})
		cancel()
		r := result{QueryID: q.ID, Query: q.Text, Hits: []hit{}, LatencyMS: float64(time.Since(start).Microseconds()) / 1000}
		if e != nil {
			r.Error = e.Error()
		} else {
			for i, c := range cases {
				corpus, ok := mapping[c.DiagnosisID]
				if !ok {
					return nil, fmt.Errorf("unknown returned diagnosis identity for query %s", q.ID)
				}
				r.Hits = append(r.Hits, hit{CorpusID: corpus, DiagnosisID: c.DiagnosisID.String(), Rank: i + 1, Similarity: c.Similarity, EvalScore: float64(10 - i)})
			}
		}
		results = append(results, r)
	}
	return results, nil
}

// readTextSearchSQL extracts the exact production SQL constant without executing Go code.
func readTextSearchSQL(path string) (string, error) {
	file, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if e != nil {
		return "", e
	}
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if name.Name != "textSearchSQL" || i >= len(value.Values) {
					continue
				}
				literal, ok := value.Values[i].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return "", errors.New("production SQL must be a string constant")
				}
				return strconv.Unquote(literal.Value)
			}
		}
	}
	return "", errors.New("production textSearchSQL constant not found")
}
func writeSQLPlan(ctx context.Context, db *sql.DB, source, out string, q query) error {
	statement, e := readTextSearchSQL(source)
	if e != nil {
		return e
	}
	pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q.Text) + "%"
	var plan json.RawMessage
	if e = db.QueryRowContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, VERBOSE, FORMAT JSON) "+statement, nil, q.Text, 10, pattern).Scan(&plan); e != nil {
		return e
	}
	envelope := map[string]any{"query_id": q.ID, "query": q.Text, "production_sql": statement, "sql_source": source, "plan": plan, "scope": "session temp SciFact corpus; EXPLAIN ANALYZE executes only the production read query"}
	data, e := json.MarshalIndent(envelope, "", "  ")
	if e != nil {
		return e
	}
	file, err := createOutput(out)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// Exclusive creation protects historical runs even if another process creates the path.
func createOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
func run() error {
	data := flag.String("data", "../benchmarks/beir-scifact/scifact", "BEIR extracted dataset directory")
	defaultOut := filepath.Join("..", "benchmarks", "beir-scifact", "runs", time.Now().UTC().Format("20060102T150405.000000000Z"), "production.jsonl")
	out := flag.String("out", defaultOut, "per-query output path")
	dsn := flag.String("postgres", "postgres://argus@127.0.0.1:15432/argus_ota_test?sslmode=disable", "local isolated benchmark PostgreSQL URL")
	planOnly := flag.Bool("plan-only", false, "capture SQL plan without rerunning all retrieval queries")
	planOut := flag.String("plan-out", "", "optional production EXPLAIN ANALYZE JSON path")
	planSource := flag.String("plan-source", "workers/ai-agent/internal/infrastructure/pgvector/text_retriever.go", "production source containing SQL constant")
	flag.Parse()
	if !*planOnly {
		if _, e := os.Lstat(*out); e == nil {
			return fmt.Errorf("benchmark output exists; choose a fresh path: %s", *out)
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	docs, e := loadCorpus(filepath.Join(*data, "corpus.jsonl"))
	if e != nil {
		return e
	}
	queries, e := loadQueries(filepath.Join(*data, "queries.jsonl"), filepath.Join(*data, "qrels", "test.tsv"))
	if e != nil {
		return e
	}
	if len(docs) != 5183 || len(queries) != 300 {
		return fmt.Errorf("SciFact full test expected 5183 corpus/300 test queries, got %d/%d", len(docs), len(queries))
	}
	ctx := context.Background()
	db, e := openIsolatedDB(ctx, *dsn)
	if e != nil {
		return e
	}
	defer db.Close()
	mapping, e := seedCorpus(ctx, db, docs)
	if e != nil {
		return e
	}
	if *planOut != "" {
		if e = os.MkdirAll(filepath.Dir(*planOut), 0755); e != nil {
			return e
		}
		if e = writeSQLPlan(ctx, db, *planSource, *planOut, queries[0]); e != nil {
			return e
		}
	}
	if *planOnly {
		if *planOut == "" {
			return errors.New("plan-only requires plan-out")
		}
		return nil
	}
	results, e := runQueries(ctx, db, queries, mapping)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(*out), 0755); e != nil {
		return e
	}
	f, e := createOutput(*out)
	if e != nil {
		return e
	}
	encoder := json.NewEncoder(f)
	failures := 0
	returned := 0
	for _, r := range results {
		if r.Error != "" {
			failures++
		}
		returned += len(r.Hits)
		if e = encoder.Encode(r); e != nil {
			f.Close()
			return e
		}
	}
	if e = f.Close(); e != nil {
		return e
	}
	fmt.Printf("queries=%d corpus=%d failures=%d returned=%d output=%s\n", len(results), len(docs), failures, returned, *out)
	if failures > 0 {
		return fmt.Errorf("%d query failures retained in output", failures)
	}
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
