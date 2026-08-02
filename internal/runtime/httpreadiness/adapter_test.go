package httpreadiness

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeUntilOKIgnoresNon200Responses(t *testing.T) {
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		switch requests.Add(1) {
		case 1:
			return nil, errors.New("connection refused")
		case 2:
			return response(http.StatusInternalServerError), nil
		case 3:
			return response(http.StatusNotFound), nil
		case 4:
			return nil, errors.New("connection reset")
		default:
			return response(http.StatusOK), nil
		}
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := make(chan time.Time, 1)
	ready := make(chan probeResult, 1)
	startedAt := time.Now()
	start <- startedAt
	go probeUntilOK(ctx, client, "http://127.0.0.1:8080/", start, ready)

	select {
	case result := <-ready:
		if result.attempts != 5 {
			t.Fatalf("attempts = %d, want 5", result.attempts)
		}
		if result.readyAt.Before(startedAt) {
			t.Fatalf("ready time %s is before start time %s", result.readyAt, startedAt)
		}
	case <-ctx.Done():
		t.Fatal("probe did not report HTTP 200")
	}
}

func TestProbeUntilOKStopsWhenContextIsCancelled(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusBadRequest), nil
	})}

	ctx, cancel := context.WithCancel(context.Background())
	start := make(chan time.Time, 1)
	ready := make(chan probeResult, 1)
	done := make(chan struct{})
	start <- time.Now()
	go func() {
		probeUntilOK(ctx, client, "http://127.0.0.1:8080/", start, ready)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("probe did not stop after context cancellation")
	}

	select {
	case result := <-ready:
		t.Fatalf("unexpected readiness result: %+v", result)
	default:
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func response(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("response")),
		Header:     make(http.Header),
	}
}
