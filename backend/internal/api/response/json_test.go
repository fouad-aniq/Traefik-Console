package response

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestResponseEncodingFailureReturnsStructured500(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, 200, make(chan int))
	var body Error
	if w.Code != 500 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != "ERR_INTERNAL" || body.OperationID == "" {
		t.Fatalf("encoding failed after success: %d %q", w.Code, w.Body.String())
	}
}

func TestResponseDoesNotMutateSharedError(t *testing.T) {
	original := ErrInternal("original", errors.New("synthetic-sensitive"))
	original.FieldErrors = map[string]string{"original": "message"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			w := httptest.NewRecorder()
			WriteError(w, original)
			if w.Code != 500 || strings.Contains(w.Body.String(), "synthetic-sensitive") || strings.Contains(w.Body.String(), "original") {
				t.Error("unsafe error response", w.Body.String())
			}
		})
	}
	wg.Wait()
	if original.OperationID != "" || original.Message != "original" || original.FieldErrors["original"] != "message" {
		t.Fatal("response mutated its caller's error")
	}
}
