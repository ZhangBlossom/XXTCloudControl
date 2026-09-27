package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type deadlineViewerDevice struct{ *orderFakeDevice }

func (d deadlineViewerDevice) Signal(ctx context.Context, _ orderRecord, _ string, _ json.RawMessage) (json.RawMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// Expiry must bound both queued and executing signals, otherwise teardown waits
// behind the signal lock while the existing WebRTC data channel stays usable.
func TestViewerExpiryBoundsSignalAndLockWait(t *testing.T) {
	for _, busyLock := range []bool{false, true} {
		t.Run(map[bool]string{false: "executing_signal", true: "queued_signal"}[busyLock], func(t *testing.T) {
			s, d := testOrderService(t)
			s.io = deadlineViewerDevice{d}
			r := createTestOrder(t, s)
			activateTestOrder(t, s, r)
			token, err := s.grant(context.Background(), r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.change(r.ID, func(r *orderRecord) { r.ExpiresAt = time.Now().Add(100 * time.Millisecond) }); err != nil {
				t.Fatal(err)
			}
			if busyLock {
				l := s.viewerLock(r.ID)
				l.Lock()
				defer l.Unlock()
			}
			router := gin.New()
			registerOrderHTTP(router, s, "")
			req := httptest.NewRequest("POST", "/order-viewer/v1/"+r.ID+"/signal/poll", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			out := httptest.NewRecorder()
			started := time.Now()
			router.ServeHTTP(out, req)
			if time.Since(started) > time.Second {
				t.Fatal("expired signal delayed teardown")
			}
			if out.Code != 401 {
				t.Fatalf("expired signal status %d: %s", out.Code, out.Body.String())
			}
			status := httptest.NewRecorder()
			req = httptest.NewRequest("GET", "/order-viewer/v1/"+r.ID+"/status", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			router.ServeHTTP(status, req)
			if status.Code != 401 {
				t.Fatal("expired token remained authorized")
			}
		})
	}
}

func TestAvailabilityDoesNotRequirePartnerSecret(t *testing.T) {
	s, _ := testOrderService(t)
	router := gin.New()
	registerOrderHTTP(router, s, "")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, httptest.NewRequest("GET", "/orders/v1/availability?bundle_id=test.game", nil))
	if out.Code != 200 {
		t.Fatalf("availability status %d: %s", out.Code, out.Body.String())
	}
	var body struct {
		Code      int `json:"code"`
		Available int `json:"available_count"`
	}
	if json.Unmarshal(out.Body.Bytes(), &body) != nil || body.Code != 200 || body.Available != 1 {
		t.Fatalf("unexpected availability %s", out.Body.String())
	}
}

func TestConfirmCleanHTTPRejectsActiveSession(t *testing.T) {
	s, _ := testOrderService(t)
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	router := gin.New()
	registerOrderHTTP(router, s, "")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, httptest.NewRequest("POST", "/api/order-sessions/"+r.ID+"/confirm-clean", nil))
	if out.Code != 409 || !strings.Contains(out.Body.String(), "cleanup_not_confirmable") {
		t.Fatalf("unexpected response %d: %s", out.Code, out.Body.String())
	}
}

type deadlineRecordingWriter struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecordingWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}
func TestConnectClearsOnlyItsWriteDeadlineThroughGin(t *testing.T) {
	s, _ := testOrderService(t)
	router := gin.New()
	registerOrderHTTP(router, s, "")
	for _, route := range []string{"/orders/v1/connect", "/orders/v1/availability?bundle_id=test.game"} {
		method := "GET"
		if route == "/orders/v1/connect" {
			method = "POST"
		}
		out := &deadlineRecordingWriter{ResponseRecorder: httptest.NewRecorder()}
		router.ServeHTTP(out, httptest.NewRequest(method, route, strings.NewReader(`{}`)))
		if method == "POST" {
			if len(out.deadlines) != 1 || !out.deadlines[0].IsZero() {
				t.Fatalf("connect did not clear deadline through Gin: %v", out.deadlines)
			}
		} else if len(out.deadlines) != 0 {
			t.Fatal("ordinary endpoint changed write deadline")
		}
	}
}
