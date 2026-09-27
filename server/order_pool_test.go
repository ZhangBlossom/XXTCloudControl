package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Simulated acknowledgements: this does not exercise two physical iPhones.
func testTwoPhonePool(t *testing.T) *orderService {
	t.Helper()
	d := &orderRetryDevice{closed: map[string]bool{}}
	profiles := []orderProfile{
		{DeviceID: "pool-a", BundleID: "test.game", Currency: "CNY", ManualTest: true},
		{DeviceID: "pool-b", BundleID: "test.game", Currency: "CNY", ManualTest: true},
	}
	s, err := newOrderService(filepath.Join(t.TempDir(), "orders.json"), profiles, d)
	if err != nil {
		t.Fatal(err)
	}
	s.localTest = true
	t.Cleanup(func() { s.cancel(); s.wg.Wait() })
	return s
}

func TestOrderTwoPhonePoolWaitsForCleanup(t *testing.T) {
	s := testTwoPhonePool(t)
	a, _, err := s.create("pool-order-a", testOrderRequest())
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.create("pool-order-b", testOrderRequest())
	if err != nil {
		t.Fatal(err)
	}
	if a.Profile.DeviceID == b.Profile.DeviceID {
		t.Fatal("A and B share a phone")
	}
	activateTestOrder(t, s, a)
	activateTestOrder(t, s, b)
	token, err := s.grant(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		r   orderRecord
		err error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		r, err := s.connectRequest(ctx, "pool-order-c", testOrderRequest(), false)
		done <- result{r, err}
	}()
	assertWaiting := func(stage string) {
		t.Helper()
		select {
		case result := <-done:
			t.Fatalf("C stopped waiting during %s: %+v", stage, result)
		case <-time.After(250 * time.Millisecond):
		}
	}
	assertWaiting("both phones active")
	if err := s.stop(a.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, _ := s.get(a.ID)
		if current.State == "pending_cleanup" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("A did not reach pending_cleanup: %+v", current)
		case <-ticker.C:
		}
	}
	if _, err := s.authorizeViewer(a.ID, token); err == nil {
		t.Fatal("A old token remains valid")
	}
	assertWaiting("A pending cleanup")
	available, err := s.availability("test.game", "CNY")
	if err != nil || available.AvailableCount != 0 {
		t.Fatal(available, err)
	}
	if err := s.confirmClean(a.ID); err != nil {
		t.Fatal(err)
	}
	var c orderRecord
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		c = result.r
	case <-ctx.Done():
		t.Fatal("C did not receive released phone")
	}
	if c.Profile.DeviceID != a.Profile.DeviceID || c.Profile.DeviceID == b.Profile.DeviceID {
		t.Fatal("C received wrong device", c.Profile.DeviceID)
	}
	if _, err := s.authorizeViewer(a.ID, token); err == nil {
		t.Fatal("A old token became valid after reuse")
	}
	if _, err := s.waitReady(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	currentB, _ := s.get(b.ID)
	if currentB.State != "active" {
		t.Fatal("B was interrupted", currentB)
	}
}

func TestOrderTwoPhonePoolConcurrentRequests(t *testing.T) {
	s := testTwoPhonePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	type result struct {
		r   orderRecord
		err error
	}
	results := make(chan result, 12)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r, err := s.connectRequest(ctx, fmt.Sprintf("pool-concurrent-%d", i), testOrderRequest(), false)
			results <- result{r, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	devices := map[string]bool{}
	allocated, waiting := 0, 0
	for result := range results {
		if result.err != nil {
			if result.err.Error() != "prepare_timeout" {
				t.Fatal(result.err)
			}
			waiting++
			continue
		}
		allocated++
		if devices[result.r.Profile.DeviceID] {
			t.Fatal("concurrent requests received same phone")
		}
		devices[result.r.Profile.DeviceID] = true
	}
	if allocated != 2 || waiting != 10 || len(s.list()) != 2 {
		t.Fatalf("allocated=%d waiting=%d records=%d", allocated, waiting, len(s.list()))
	}
}
