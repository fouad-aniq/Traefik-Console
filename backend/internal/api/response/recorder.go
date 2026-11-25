package response

import (
	"crypto/rand"
	"net/http"
)

type Recorder struct {
	http.ResponseWriter
	status      int
	operationID string
}

func (w *Recorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *Recorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *Recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func OperationID(w http.ResponseWriter) string {
	if response, ok := w.(*Recorder); ok && response.operationID != "" {
		return response.operationID
	}
	return "op-" + rand.Text()
}

// NewRecorder records the final status and one operation identity for a request.
func NewRecorder(w http.ResponseWriter) *Recorder {
	return &Recorder{ResponseWriter: w, operationID: "op-" + rand.Text()}
}
func (w *Recorder) Status() int { return w.status }
