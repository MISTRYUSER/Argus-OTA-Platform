package domain

import (
)

type BatchStatus string

const (
	BatchStatusPending    BatchStatus = "pending"
	BatchStatusUploaded   BatchStatus = "uploaded"
	BatchStatusScattering BatchStatus = "scattering"
	BatchStatusScattered  BatchStatus = "scattered"
	BatchStatusGathering  BatchStatus = "gathering"
	BatchStatusGathered   BatchStatus = "gathered"
	BatchStatusDiagnosing BatchStatus = "diagnosing"
	BatchStatusCompleted  BatchStatus = "completed"
	BatchStatusFailed     BatchStatus = "failed"
)

func (s BatchStatus) String() string {
	return string(s)
}
func (s BatchStatus) IsValid() bool {
	switch s {
	case
		BatchStatusPending,
		BatchStatusUploaded,
		BatchStatusScattering,
		BatchStatusScattered,
		BatchStatusGathering,
		BatchStatusGathered,
		BatchStatusDiagnosing,
		BatchStatusCompleted,
		BatchStatusFailed:
		return true
	default:
		return false
	}
}
func (s BatchStatus) CanTransitionTo(newStatus BatchStatus) bool {
	var batchStatusTransitions = map[BatchStatus][]BatchStatus{
		BatchStatusPending: {
			BatchStatusUploaded,
		},
		BatchStatusUploaded: {
			BatchStatusScattering,
		},
		BatchStatusScattering: {
			BatchStatusScattered,
			BatchStatusFailed,
		},
		BatchStatusScattered: {
			BatchStatusGathering,
		},
		BatchStatusGathering: {
			BatchStatusGathered,
			BatchStatusFailed,
		},
		BatchStatusGathered: {
			BatchStatusDiagnosing,
		},
		BatchStatusDiagnosing: {
			BatchStatusCompleted,
			BatchStatusFailed,
		},
		BatchStatusFailed: {
			BatchStatusFailed,
		},
		BatchStatusCompleted: {
			BatchStatusPending,
		},
	}
	if !s.IsValid() || !newStatus.IsValid() {
		return false
	}
	nextStatuses, ok := batchStatusTransitions[s]
	if !ok {
		return false
	}
	for _, status := range nextStatuses {
		if status == newStatus {
			return true
		}
	}
	return false
}