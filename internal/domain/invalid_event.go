package domain

// InvalidEventError identifies malformed messages that must be quarantined, not retried forever.
type InvalidEventError struct{ Err error }

func (e *InvalidEventError) Error() string { return e.Err.Error() }
func (e *InvalidEventError) Unwrap() error { return e.Err }

// PermanentTaskError stops automatic retries of an irrecoverable investigation.
type PermanentTaskError struct{ Err error }

func (e *PermanentTaskError) Error() string { return e.Err.Error() }
func (e *PermanentTaskError) Unwrap() error { return e.Err }
