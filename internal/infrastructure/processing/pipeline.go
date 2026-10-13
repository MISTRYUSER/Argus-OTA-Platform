package processing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xuewentao/argus-ota-platform/internal/domain"
	pg "github.com/xuewentao/argus-ota-platform/internal/infrastructure/postgres"
)

type Processor struct {
	DB     *sql.DB
	Python Python
	Fetch  func(context.Context, string) (io.ReadCloser, error)
}
type uploadedFile struct {
	ID        uuid.UUID
	Name, Key string
	Metadata  []byte
}

func (p *Processor) ParseBatch(ctx context.Context, id uuid.UUID) error {
	if p.DB == nil || p.Fetch == nil {
		return fmt.Errorf("processing requires database and object reader")
	}
	var status string
	if e := p.DB.QueryRowContext(ctx, `SELECT status FROM batches WHERE id=$1`, id).Scan(&status); e != nil {
		return e
	}
	if status != "uploaded" && status != "scattering" {
		return nil
	}
	rows, e := p.DB.QueryContext(ctx, `SELECT f.id,f.original_filename,f.minio_path,f.metadata FROM files f LEFT JOIN parsed_file_data p ON p.file_id=f.id WHERE f.batch_id=$1 AND p.file_id IS NULL ORDER BY f.id`, id)
	if e != nil {
		return e
	}
	var files []uploadedFile
	for rows.Next() {
		var f uploadedFile
		if e = rows.Scan(&f.ID, &f.Name, &f.Key, &f.Metadata); e != nil {
			rows.Close()
			return e
		}
		files = append(files, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, f := range files {
		if e = p.parseFile(ctx, id, f); e != nil {
			return e
		}
	}
	return nil
}
func (p *Processor) parseFile(ctx context.Context, id uuid.UUID, f uploadedFile) error {
	dir, e := os.MkdirTemp("", "argus-parse-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	// Only use basename; uploaded filename never selects an executable or escapes temp dir.
	name := filepath.Base(f.Name)
	if name == "." || name == string(filepath.Separator) {
		return &domain.PermanentTaskError{Err: fmt.Errorf("invalid input filename")}
	}
	path := filepath.Join(dir, name)
	dst, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	src, e := p.Fetch(ctx, f.Key)
	if e != nil {
		dst.Close()
		return e
	}
	n, copyErr := io.Copy(dst, io.LimitReader(src, MaxBytes+1))
	closeSrc := src.Close()
	closeDst := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeSrc != nil {
		return closeSrc
	}
	if closeDst != nil {
		return closeDst
	}
	if n > MaxBytes {
		return &domain.PermanentTaskError{Err: fmt.Errorf("input exceeds 32 MiB")}
	}
	python := p.Python
	if string(f.Metadata) != "{}" && len(f.Metadata) > 0 {
		python.RecordMetadata = string(f.Metadata)
	}
	parsed, e := python.Parse(ctx, path, "minio:"+f.Key)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &domain.PermanentTaskError{Err: e}
	}
	var observations []struct {
		Timestamp float64 `json:"timestamp"`
		ErrorCode string  `json:"error_code"`
	}
	if e = json.Unmarshal(parsed, &observations); e != nil || len(observations) == 0 {
		return &domain.PermanentTaskError{Err: fmt.Errorf("invalid parsed observations: %v", e)}
	}
	var logRows []json.RawMessage
	if e = json.Unmarshal(parsed, &logRows); e != nil {
		return e
	}
	tx, e := p.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var status string
	if e = tx.QueryRowContext(ctx, `SELECT status FROM batches WHERE id=$1 FOR UPDATE`, id).Scan(&status); e != nil {
		return e
	}
	if status != "uploaded" && status != "scattering" {
		return nil
	}
	res, e := tx.ExecContext(ctx, `INSERT INTO parsed_file_data(file_id,batch_id,observations,source) VALUES($1,$2,$3,$4) ON CONFLICT(file_id) DO NOTHING`, f.ID, id, []byte(parsed), "minio:"+f.Key)
	if e != nil {
		return e
	}
	affected, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if affected == 0 {
		return nil
	}
	// Legacy file error_code is explicitly a file-level representative code, not occurrence counts.
	code := ""
	for i, obs := range observations {
		if code == "" && obs.ErrorCode != "" {
			code = obs.ErrorCode
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO logs(batch_id,file_id,timestamp,log_content) VALUES($1,$2,$3,$4)`, id, f.ID, time.UnixMicro(int64(obs.Timestamp*1e6)).UTC(), string(logRows[i])); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE files SET processing_status='parsed',record_count=$2,error_code=$3,updated_at=NOW() WHERE id=$1`, f.ID, len(observations), code); e != nil {
		return e
	}
	if e = pg.EnqueueEvent(ctx, tx, domain.FileParsed{BatchID: id, FileID: f.ID, OccurredAt: time.Now()}); e != nil {
		return e
	}
	return tx.Commit()
}
func (p *Processor) AggregateBatch(ctx context.Context, id uuid.UUID) error {
	var status string
	var total, processed int
	if e := p.DB.QueryRowContext(ctx, `SELECT status,total_files,processed_files FROM batches WHERE id=$1`, id).Scan(&status, &total, &processed); e != nil {
		return e
	}
	if status == "diagnosing" || status == "completed" {
		return nil
	}
	if status != "gathering" || total < 1 || processed != total {
		return fmt.Errorf("batch not ready for real aggregation")
	}
	var old bool
	if e := p.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM version_regression_reports WHERE batch_id=$1)`, id).Scan(&old); e != nil {
		return e
	}
	if old {
		return nil
	}
	rows, e := p.DB.QueryContext(ctx, `SELECT observations FROM parsed_file_data WHERE batch_id=$1 ORDER BY file_id`, id)
	if e != nil {
		return e
	}
	var all []json.RawMessage
	files := 0
	size := 0
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		size += len(raw)
		if size > MaxBytes {
			rows.Close()
			return &domain.PermanentTaskError{Err: fmt.Errorf("aggregate exceeds 32 MiB")}
		}
		var one []json.RawMessage
		if e = json.Unmarshal(raw, &one); e != nil {
			rows.Close()
			return e
		}
		all = append(all, one...)
		files++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if files != total {
		return fmt.Errorf("missing real parse artifacts")
	}
	raw, e := json.Marshal(all)
	if e != nil {
		return e
	}
	report, e := p.Python.Analyze(ctx, raw)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &domain.PermanentTaskError{Err: e}
	}
	tx, e := p.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = tx.QueryRowContext(ctx, `SELECT status FROM batches WHERE id=$1 FOR UPDATE`, id).Scan(&status); e != nil {
		return e
	}
	if status != "gathering" {
		return nil
	}
	res, e := tx.ExecContext(ctx, `INSERT INTO version_regression_reports(batch_id,report) VALUES($1,$2) ON CONFLICT(batch_id) DO NOTHING`, id, []byte(report))
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return nil
	}
	if e = pg.EnqueueEvent(ctx, tx, domain.GatheringCompleted{Version: "v1.0", BatchID: id, TotalFiles: total, ChartFiles: []string{}, OccurredAt: time.Now()}); e != nil {
		return e
	}
	return tx.Commit()
}
func (p *Processor) HandleMessage(ctx context.Context, body []byte, stage string) error {
	var event struct {
		Type string `json:"event_type"`
		ID   string `json:"batch_id"`
	}
	if e := json.Unmarshal(body, &event); e != nil {
		return &domain.InvalidEventError{Err: e}
	}
	wanted := "BatchCreated"
	if stage == "aggregate" {
		wanted = "AllFilesScattered"
	} else if stage != "parse" {
		return fmt.Errorf("invalid processing stage")
	}
	if event.Type == "" {
		return &domain.InvalidEventError{Err: fmt.Errorf("missing event_type")}
	}
	if event.Type != wanted {
		return nil
	}
	id, e := uuid.Parse(strings.TrimSpace(event.ID))
	if e != nil {
		return &domain.InvalidEventError{Err: e}
	}
	if stage == "parse" {
		return p.ParseBatch(ctx, id)
	}
	return p.AggregateBatch(ctx, id)
}
