package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiagDialRejectsBadPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/diag/dial", strings.NewReader(`{"host":"x","port":0}`))
	rec := httptest.NewRecorder()
	diagDialHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestDiagDialConcurrencyCap(t *testing.T) {	for i := 0; i < 4; i++ {
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
