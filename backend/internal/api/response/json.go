package response

import (
	"encoding/json"
	"log"
	"maps"
	"net/http"

	"github.com/traefik-console/internal/redaction"
)

// WriteJSON finishes redaction/encoding before committing a successful response.
func WriteJSON(w http.ResponseWriter, status int, data any) {
	raw, err := redaction.JSON(data)
	if err != nil {
		WriteError(w, ErrInternal("Response encoding failed", nil))
		return
	}
	writeEncoded(w, status, raw)
}

// WriteError builds a private projection; it never changes the caller's error value.
func WriteError(w http.ResponseWriter, err error) {
	mapped := MapDomainError(err)
	if mapped == nil || mapped.Status < 400 || mapped.Status > 599 {
		mapped = ErrInternal("Invalid error response", nil)
	}
	projected := *mapped
	projected.FieldErrors = maps.Clone(mapped.FieldErrors)
	projected.OperationID = OperationID(w)
	if projected.Status == http.StatusInternalServerError {
		projected.Message = "An internal configuration or system error occurred."
		projected.FieldErrors = nil
		log.Printf("ERROR operation_id=%s code=%s", projected.OperationID, projected.Code)
	} else if projected.InternalErr != nil {
		log.Printf("WARN operation_id=%s code=%s", projected.OperationID, projected.Code)
	}
	// All serialized fields are bounded API strings/maps; internal errors are excluded.
	raw, _ := json.Marshal(projected)
	writeEncoded(w, projected.Status, raw)
}

func WriteCustomError(w http.ResponseWriter, status int, code, message string, fields map[string]string, retryable bool) {
	WriteError(w, &Error{Status: status, Code: code, Message: message, FieldErrors: fields, Retryable: retryable})
}

func writeEncoded(w http.ResponseWriter, status int, raw []byte) {
	if response, ok := w.(*Recorder); ok && response.status != 0 {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(append(raw, '\n')); err != nil {
		log.Printf("WARN operation_id=%s code=ERR_RESPONSE_WRITE", OperationID(w))
	}
}
