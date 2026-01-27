package domain

import (
	"context"
    "time"
    "github.com/google/uuid"
)

type Report struct {
	ID 				uuid.UUID
	BatchID 		uuid.UUID
	VehicleID		string
	VIN				string
	Status      	BatchStatus
	TotalFiles		int
	ProcessedFiles	int

	//statics FIles
	
	CPUStats        *CPUStats
	RAMStats        *RAMStats


	DiagnosisResult	string
	DiagnosisID		*uuid.UUID

	CreatedAt		time.Time
	UpdatedAt		time.Time
	
}
type CPUStats struct {
	AvgUtilization float64
	P95Utilization float64
	P99Utilization float64
	MaxUtilization float64
}

type RAMStats struct {
	AvgUsageMB     float64
	P95UsageMB     float64
	P99UsageMB     float64
	MaxUsageMB     float64
}


func NewReport(batch *Batch) *Report {
	now := time.Now()
	return &Report{
		ID:             uuid.New(),
		BatchID:        batch.ID,
		VehicleID:      batch.VehicleID,
		VIN:            batch.VIN,
		Status:         batch.Status,
		TotalFiles:     batch.TotalFiles,
		ProcessedFiles: batch.ProcessedFiles,
		CPUStats:       nil, // 后续填充
		RAMStats:       nil, // 后续填充
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}
type ReportRepository interface {
	Save(ctx context.Context, report *Report) error
	FindByID(ctx context.Context, id uuid.UUID) (*Report, error)
	FindByBatchID(ctx context.Context, batchID uuid.UUID) (*Report, error)
}

