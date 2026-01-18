package domain

import (
	"context"

	"github.com/google/uuid"
)
type ListOptions struct {
	//page 
	Limit 		int
	Offset 		int

	SortBy		string
	SortOrder	string

	vehicleID	*string
	VIN			*string
	Status 		*string
}
type BatchRepository interface {
	Save(ctx context.Context, batch *Batch) error
	FindByID(ctx context.Context, id uuid.UUID) (*Batch, error)
	FindByVIN(ctx context.Context, vin string) ([]*Batch, error)
	FindByStatus(ctx context.Context, status BatchStatus) ([]*Batch, error)
	List(ctx context.Context, opts ListOptions) ([]*Batch, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type FileRepository interface {
	Save(ctx context.Context, file *File) error
	FindByID(ctx context.Context, id uuid.UUID) (*File, error)
	FindByBatchID(ctx context.Context, batchID uuid.UUID) ([]*File, error)
	UpdateProcessingStatus(ctx context.Context, id uuid.UUID, status ProcessingStatus) error
}
