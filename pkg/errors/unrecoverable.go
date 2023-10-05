package errors

// UnrecoverableError represents an error that can not be recovered
// from without user intervention. When this error is returned
// the request should not be requeued.
type UnrecoverableError struct {
	error
}

func NewUnrecoverableError(err error) *UnrecoverableError {
	return &UnrecoverableError{err}
}
