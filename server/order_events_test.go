package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func readOrderEvents(t *testing.T, s *orderService) []map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), "order-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var row map[string]interface{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestOrderFailureEventsAuthDurabilityAndDeduplication(t *testing.T) {
	s, _ := testOrderService(t)
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	token, err := s.grant(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	registerOrderHTTP(router, s, "")
	post := func(body, credential string) int {
		req := httptest.NewRequest("POST", "/order-viewer/v1/"+r.ID+"/failure", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	if got := post(`{"reason":"connection_failed"}`, "wrong"); got != 401 {
		t.Fatal(got)
	}
	if got := post(`{"reason":"arbitrary-secret-message"}`, token); got != 400 {
		t.Fatal(got)
	}
	if got := post(`{"reason":"connection_failed","token":"secret"}`, token); got != 400 {
		t.Fatal(got)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := post(`{"reason":"connection_failed"}`, token); got != 204 {
				t.Errorf("status=%d", got)
			}
		}()
	}
	wg.Wait()
	rows := readOrderEvents(t, s)
	count := 0
	for _, row := range rows {
		if row["event"] == "viewer_failure" {
			count++
			if row["order_id"] != r.businessID() || row["session_id"] != r.SessionID || row["device_id"] != r.Profile.DeviceID || row["reason"] != "connection_failed" {
				t.Fatal(row)
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicate failure events: %d", count)
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(s.path), "order-events.jsonl"))
	if strings.Contains(string(b), token) || strings.Contains(string(b), "arbitrary-secret-message") {
		t.Fatal("sensitive input logged")
	}
	info, _ := os.Stat(filepath.Join(filepath.Dir(s.path), "order-events.jsonl"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	// Reporting a failure does not extend the lease, return quota or free the device.
	current, _ := s.get(r.ID)
	if current.State != "active" || current.Remaining != r.Remaining {
		t.Fatal(current)
	}
}

func TestOrderFailureHeartbeatDistinguishesSilenceFromExpiry(t *testing.T) {
	s, _ := testOrderService(t)
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	r, _ = s.get(r.ID)
	now := time.Now()
	s.now = func() time.Time { return now }
	count := func() int {
		n := 0
		for _, row := range readOrderEvents(t, s) {
			if row["reason"] == "viewer_heartbeat_timeout" {
				n++
			}
		}
		return n
	}
	s.checkViewerHeartbeat(r)
	if count() != 0 {
		t.Fatal("unopened viewer marked failed")
	}
	s.viewerSeen(r)
	now = now.Add(29 * time.Second)
	s.checkViewerHeartbeat(r)
	if count() != 0 {
		t.Fatal("early timeout")
	}
	now = now.Add(time.Second)
	s.checkViewerHeartbeat(r)
	s.checkViewerHeartbeat(r)
	if count() != 1 {
		t.Fatal("timeout missing or repeated")
	}
	s.viewerSeen(r)
	now = now.Add(31 * time.Second)
	s.checkViewerHeartbeat(r)
	if count() != 2 {
		t.Fatal("new silence not detected")
	}
	s.viewerSeen(r)
	now = r.ExpiresAt
	s.checkViewerHeartbeat(r)
	if count() != 2 {
		t.Fatal("normal expiry mislabeled")
	}
	if err := s.change(r.ID, func(r *orderRecord) { r.State = "closing"; r.Reason = "expired" }); err != nil {
		t.Fatal(err)
	}
	last := readOrderEvents(t, s)
	if row := last[len(last)-1]; row["event"] != "session_state" || row["reason"] != "expired" {
		t.Fatal(row)
	}
}

func TestOrderFailureLogWriteErrorCanRetry(t *testing.T) {
	s, _ := testOrderService(t)
	r := createTestOrder(t, s)
	p := filepath.Join(filepath.Dir(s.path), "order-events.jsonl")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.viewerFailure(r, "signal_failed"); err == nil {
		t.Fatal("write failure ignored")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := s.viewerFailure(r, "signal_failed"); err != nil {
		t.Fatal(err)
	}
	if len(readOrderEvents(t, s)) != 1 {
		t.Fatal("failed write consumed dedup key")
	}
}
