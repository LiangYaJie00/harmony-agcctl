// Package apperr defines the classified errors and process exit codes used by agcctl.
package apperr

import "fmt"

// ExitCode is part of the external contract consumed by the release scheduler.
type ExitCode int

const (
	ExitSuccess           ExitCode = 0
	ExitSafeRetry         ExitCode = 10
	ExitNonRetryable      ExitCode = 20
	ExitReconcileFirst    ExitCode = 30
	ExitLocalPrecondition ExitCode = 40
	ExitToolInternal      ExitCode = 50
)

// RetryClass returns the stable retry class written to the response.
func RetryClass(code ExitCode) string {
	switch code {
	case ExitSuccess:
		return "NONE"
	case ExitSafeRetry:
		return "SAFE_RETRY"
	case ExitNonRetryable:
		return "NON_RETRYABLE"
	case ExitReconcileFirst:
		return "RECONCILE_FIRST"
	case ExitLocalPrecondition:
		return "LOCAL_PRECONDITION_FAILED"
	default:
		return "TOOL_INTERNAL_ERROR"
	}
}

// Error is a failure that has already been classified for the caller.
type Error struct {
	ExitCode ExitCode
	Code     string
	Message  string
	Detail   *string
}

func (e *Error) Error() string { return e.Message }

// New creates a classified error without a detail.
func New(code ExitCode, errorCode, message string) *Error {
	return &Error{ExitCode: code, Code: errorCode, Message: message}
}

// NewDetail creates a classified error with a redacted-detail candidate.
func NewDetail(code ExitCode, errorCode, message, detail string) *Error {
	return &Error{ExitCode: code, Code: errorCode, Message: message, Detail: &detail}
}

// RequestInvalid reports malformed request values using the common contract code.
func RequestInvalid(format string, args ...any) *Error {
	return New(ExitLocalPrecondition, "REQUEST_INVALID", fmt.Sprintf(format, args...))
}

func severity(code ExitCode) int {
	switch code {
	case ExitSuccess:
		return 0
	case ExitSafeRetry:
		return 1
	case ExitReconcileFirst:
		return 2
	case ExitNonRetryable:
		return 3
	case ExitLocalPrecondition:
		return 4
	default:
		return 5
	}
}

// WorstExit returns the most severe exit code, matching doctor aggregation.
func WorstExit(codes []ExitCode) ExitCode {
	worst := ExitSuccess
	for _, code := range codes {
		if severity(code) > severity(worst) {
			worst = code
		}
	}
	return worst
}
