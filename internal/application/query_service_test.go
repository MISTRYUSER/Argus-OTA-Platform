package application

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xuewentao/argus-ota-platform/internal/domain"
)

type mockBatchRepo struct {
	findByIDBatch *domain.Batch
	findByIDErr   error
}

func (m *mockBatchRepo) Save(ctx context.Context, batch *domain.Batch) error {
	return nil
}

func (m *mockBatchRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Batch, error) {
	return m.findByIDBatch, m.findByIDErr
}

func (m *mockBatchRepo) FindByVIN(ctx context.Context, vin string) ([]*domain.Batch, error) {
	return nil, nil
}

func (m *mockBatchRepo) FindByStatus(ctx context.Context, status domain.BatchStatus) ([]*domain.Batch, error) {
	return nil, nil
}

func (m *mockBatchRepo) List(ctx context.Context, opts domain.ListOptions) ([]*domain.Batch, error) {
	return nil, nil
}

func (m *mockBatchRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (m *mockBatchRepo) FindStuckBatches(ctx context.Context) ([]*domain.Batch, error) {
	return nil, nil
}

type mockReportRepo struct {
	findByBatchReport *domain.Report
	findByBatchErr    error
}

func (m *mockReportRepo) Save(ctx context.Context, report *domain.Report) error {
	return nil
}

func (m *mockReportRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Report, error) {
	return nil, nil
}

func (m *mockReportRepo) FindByBatchID(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
	return m.findByBatchReport, m.findByBatchErr
}

func TestQueryService_GetReport_BatchNotFoundReturnsError(t *testing.T) {
	service := NewQueryService(
		&mockBatchRepo{findByIDBatch: nil, findByIDErr: nil},
		&mockReportRepo{findByBatchReport: nil, findByBatchErr: nil},
		nil,
	)

	_, err := service.GetReport(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("expected error when batch is not found")
	}
}

func TestQueryService_GetReport_ReportRepoErrorShouldReturnError(t *testing.T) {
	service := NewQueryService(
		&mockBatchRepo{
			findByIDBatch: &domain.Batch{ID: uuid.New(), TotalFiles: 1, ProcessedFiles: 1, Status: domain.BatchStatusCompleted},
			findByIDErr:   nil,
		},
		&mockReportRepo{findByBatchErr: errors.New("db unavailable")},
		nil,
	)

	_, err := service.GetReport(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("expected error from report repository")
	}
}

func TestQueryService_GetProgress_ZeroTotalFilesNoNaN(t *testing.T) {
	batchID := uuid.New()
	service := NewQueryService(
		&mockBatchRepo{
			findByIDBatch: &domain.Batch{
				ID:             batchID,
				Status:         domain.BatchStatusUploaded,
				TotalFiles:     0,
				ProcessedFiles: 0,
			},
		},
		&mockReportRepo{},
		nil,
	)

	progress, err := service.GetProgress(context.Background(), batchID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, ok := progress["progress_percent"].(float64)
	if !ok {
		t.Fatalf("progress_percent must be float64")
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		t.Fatalf("progress_percent should be finite, got: %v", v)
	}
}

type ctxAwareBatchRepo struct {
	delay time.Duration
	batch *domain.Batch
}

func (m *ctxAwareBatchRepo) Save(ctx context.Context, batch *domain.Batch) error {
	return nil
}

func (m *ctxAwareBatchRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Batch, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(m.delay):
		return m.batch, nil
	}
}

func (m *ctxAwareBatchRepo) FindByVIN(ctx context.Context, vin string) ([]*domain.Batch, error) {
	return nil, nil
}

func (m *ctxAwareBatchRepo) FindByStatus(ctx context.Context, status domain.BatchStatus) ([]*domain.Batch, error) {
	return nil, nil
}

func (m *ctxAwareBatchRepo) List(ctx context.Context, opts domain.ListOptions) ([]*domain.Batch, error) {
	return nil, nil
}

func (m *ctxAwareBatchRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (m *ctxAwareBatchRepo) FindStuckBatches(ctx context.Context) ([]*domain.Batch, error) {
	return nil, nil
}

type nilReportRepo struct{}

func (m *nilReportRepo) Save(ctx context.Context, report *domain.Report) error {
	return nil
}

func (m *nilReportRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Report, error) {
	return nil, nil
}

func (m *nilReportRepo) FindByBatchID(ctx context.Context, batchID uuid.UUID) (*domain.Report, error) {
	return nil, nil
}

func TestQueryService_GetReport_CanceledLeaderContextDoesNotPoisonFollowers(t *testing.T) {
	batchID := uuid.New()
	service := NewQueryService(
		&ctxAwareBatchRepo{
			delay: 80 * time.Millisecond,
			batch: &domain.Batch{
				ID:         batchID,
				Status:     domain.BatchStatusCompleted,
				TotalFiles: 1,
			},
		},
		&nilReportRepo{},
		nil,
	)

	leaderCtx, cancel := context.WithCancel(context.Background())
	cancel()

	leaderErrCh := make(chan error, 1)
	go func() {
		_, err := service.GetReport(leaderCtx, batchID)
		leaderErrCh <- err
	}()

	time.Sleep(10 * time.Millisecond)

	report, followerErr := service.GetReport(context.Background(), batchID)
	if followerErr != nil {
		t.Fatalf("follower should still succeed, got error: %v", followerErr)
	}
	if report == nil || report.BatchID != batchID {
		t.Fatalf("unexpected report result: %+v", report)
	}

	leaderErr := <-leaderErrCh
	if leaderErr == nil {
		t.Fatalf("leader should return canceled error")
	}
}
