package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"paqet/internal/diag"
)

func TestDiagDialRejectsBadPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/diag/dial", strings.NewReader(`{"host":"x","port":0}`))
	rec := httptest.NewRecorder()
	diagDialHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestDiagDialConcurrencyCap(t *testing.T) {
	for i := 0; i < 4; i++ {
		dialSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < 4; i++ {
			<-dialSem
		}
	}()
	req := httptest.NewRequest(http.MethodPost, "/api/diag/dial", strings.NewReader(`{"host":"x","port":80}`))
	rec := httptest.NewRecorder()
	diagDialHandler(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("429 must be JSON (dashboard parses it), got Content-Type %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("429 body must carry an error key, got %q", rec.Body.String())
	}
}

func TestDiagUDPRejectsBadPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/diag/udp", strings.NewReader(`{"host":"x","port":99999}`))
	rec := httptest.NewRecorder()
	diagUDPHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestDiagMTURejectsBadPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/diag/mtu", strings.NewReader(`{"host":"x","port":-1}`))
	rec := httptest.NewRecorder()
	diagMTUHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestDiagSpeedtestRejectsBadMode(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/diag/speedtest", strings.NewReader(`{"duration_sec":5,"mode":"sideways"}`))
	rec := httptest.NewRecorder()
	diagSpeedtestHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestSpeedtestHTTPStatus(t *testing.T) {	if got := speedtestHTTPStatus(diag.ErrBusy); got != http.StatusConflict {
		t.Fatalf("busy must map to 409, got %d", got)
	}
	if got := speedtestHTTPStatus(diag.ErrNoTunnel); got != http.StatusConflict {
		t.Fatalf("no-tunnel must map to 409, got %d", got)
	}
	if got := speedtestHTTPStatus(context.Canceled); got != http.StatusBadGateway {
		t.Fatalf("cancellation must map to 502, got %d", got)
	}
	if got := speedtestHTTPStatus(errors.New("boom")); got != http.StatusBadGateway {
		t.Fatalf("internal failure must map to 502, got %d", got)
	}
}

func TestDiagResultsListsEntries(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/diag/results", nil)
	rec := httptest.NewRecorder()
	diagResultsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"entries"`) {
		t.Fatal("missing entries key")
	}
}

func TestClampProbeSec(t *testing.T) {
	if got := clampProbeSec(99, probeDefaultSec, probeMaxSec); got != probeMaxSec {
		t.Fatalf("over-cap must clamp to %d, got %d", probeMaxSec, got)
	}
	if got := clampProbeSec(0, probeDefaultSec, probeMaxSec); got != probeDefaultSec {
		t.Fatalf("zero must use default %d, got %d", probeDefaultSec, got)
	}
	if got := clampProbeSec(-3, probeDefaultSec, probeMaxSec); got != probeDefaultSec {
		t.Fatalf("negative must use default %d, got %d", probeDefaultSec, got)
	}
	if got := clampProbeSec(5, probeDefaultSec, probeMaxSec); got != 5 {
		t.Fatalf("in-range must pass through, got %d", got)
	}
}

func TestDiagSpeedtestIdleReturns409(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/diag/speedtest", strings.NewReader(`{"duration_sec":5,"mode":"up"}`))
	rec := httptest.NewRecorder()
	diagSpeedtestHandler(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("idle tunnel must return 409, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "no active tunnel") {
		t.Fatalf("idle body must name the cause, got %q", body)
	}
	if strings.Contains(body, "retry_after_sec") {
		t.Fatalf("idle 409 must not suggest retry, got %q", body)
	}
}

func TestDiagSpeedtestBusyReturns409(t *testing.T) {
	IncConnections()
	defer DecConnections()
	defer speedtestMgr.SetActive(false)
	release := make(chan struct{})
	done := make(chan struct{})
	speedtestMgr.SetTransfer(func(ctx context.Context, mode string) (uint64, uint64, error) {
		select {
		case <-release:
			return 0, 0, nil
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
	})
	defer speedtestMgr.SetTransfer(func(ctx context.Context, mode string) (uint64, uint64, error) {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			return 0, 0, nil
		}
		return 0, 0, ctx.Err()
	})
	speedtestMgr.SetActive(true)
	go func() {
		defer close(done)
		_, _ = speedtestMgr.Run(context.Background(), 10, "up")
	}()
	time.Sleep(100 * time.Millisecond)
	req := httptest.NewRequest(http.MethodPost, "/api/diag/speedtest", strings.NewReader(`{"duration_sec":5,"mode":"up"}`))
	rec := httptest.NewRecorder()
	diagSpeedtestHandler(rec, req)
	close(release)
	<-done
	if rec.Code != http.StatusConflict {
		t.Fatalf("busy speedtest must return 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "retry_after_sec") {
		t.Fatalf("busy 409 must carry retry_after_sec, got %q", rec.Body.String())
	}
}
