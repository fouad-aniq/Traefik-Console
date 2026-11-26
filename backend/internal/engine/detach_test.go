package engine

import (
	"context"
	"testing"
	"time"
)

// The browser's connection drops while Traefik restarts: the operation must keep running, still
// bounded by the request's deadline.
func TestOperationContextSurvivesTheClientButKeepsTheDeadline(t *testing.T) {
	request, cancelRequest := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelRequest()
	ctx, cancel := detachedContext(request, time.Minute)
	defer cancel()
	cancelRequest() // the client goes away
	select {
	case <-ctx.Done():
		t.Fatal("the operation was cancelled with the client")
	case <-time.After(20 * time.Millisecond):
	}
	want, _ := request.Deadline()
	if got, ok := ctx.Deadline(); !ok || !got.Equal(want) {
		t.Fatalf("deadline %v, want %v", got, want)
	}
	noDeadline, cancel2 := detachedContext(context.Background(), 30*time.Millisecond)
	defer cancel2()
	select {
	case <-noDeadline.Done():
	case <-time.After(time.Second):
		t.Fatal("the fallback timeout must bound an operation without a deadline")
	}
}
