package response

import (
	"net/http/httptest"
	"testing"
)

func TestStatusWriterTracksOnlyFinalCommit(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := NewRecorder(underlying)
	w.WriteHeader(201)
	w.WriteHeader(500)
	w.Write([]byte("ok"))
	if w.status != 201 || underlying.Code != 201 || underlying.Body.String() != "ok" || w.Unwrap() != underlying {
		t.Fatal("commit state lost")
	}
}
