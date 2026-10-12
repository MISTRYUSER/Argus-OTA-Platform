package domain

import (
	"context"
	"github.com/google/uuid"
)

type ExecutionLease struct {
	BatchID, Owner uuid.UUID
	Generation     int64
}
type leaseContextKey struct{}

func WithExecutionLease(ctx context.Context, lease ExecutionLease) context.Context {
	return context.WithValue(ctx, leaseContextKey{}, lease)
}
func ExecutionLeaseFromContext(ctx context.Context) (ExecutionLease, bool) {
	lease, ok := ctx.Value(leaseContextKey{}).(ExecutionLease)
	return lease, ok
}

type LeasedDiagnosisRepository interface {
	ExecuteOnce(context.Context, string, func(context.Context) (*Diagnosis, error)) (*Diagnosis, error)
}

// ExecutionFailure retains the originating generation for later quarantine callbacks.
type ExecutionFailure struct {
	Lease ExecutionLease
	Err   error
}

func (e *ExecutionFailure) Error() string { return e.Err.Error() }
func (e *ExecutionFailure) Unwrap() error { return e.Err }
