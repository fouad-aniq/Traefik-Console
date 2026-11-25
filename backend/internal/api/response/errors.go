package response

import (
	"fmt"
	"net/http"
)

// Error matches the exact JSON contract your Svelte frontend expects
type Error struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	FieldErrors map[string]string `json:"field_errors,omitempty"`
	OperationID string            `json:"operation_id"`
	Retryable   bool              `json:"retryable"`
	Status      int               `json:"-"` // HTTP status code
	InternalErr error             `json:"-"` // Logged server-side on 500s
}

func (e *Error) Error() string {
	if e.InternalErr != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.InternalErr)
	}

	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// --- Error Constructors used by DB, FS, Core, and Engine ---

func ErrValidation(msg string, fields map[string]string) *Error {
	return &Error{
		Status:      http.StatusUnprocessableEntity, // 422
		Code:        "ERR_VALIDATION_FAILED",
		Message:     msg,
		FieldErrors: fields,
		Retryable:   false,
	}
}

func ErrConflict(code, msg string) *Error {
	return &Error{
		Status:    http.StatusConflict, // 409
		Code:      code,
		Message:   msg,
		Retryable: false,
	}
}

func ErrExternal(code, msg string, retryable bool, err error) *Error {
	return &Error{
		Status:      http.StatusBadGateway, // 502 (e.g., Docker socket or CrowdSec down)
		Code:        code,
		Message:     msg,
		Retryable:   retryable,
		InternalErr: err,
	}
}

func ErrInternal(msg string, err error) *Error {
	return &Error{
		Status:      http.StatusInternalServerError, // 500
		Code:        "ERR_INTERNAL",
		Message:     msg,
		Retryable:   true,
		InternalErr: err,
	}
}
