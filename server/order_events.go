package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const orderViewerSilence = 30 * time.Second

type orderViewerWatch struct {
	lastSeen time.Time
	lost     bool
	reported map[string]bool
}

type orderEventLog struct {
	mu      sync.Mutex
	viewers map[string]*orderViewerWatch
}

// Only bounded event codes and server-owned identifiers are logged. Never log
// tokens, viewer URLs, account data, SDP or arbitrary browser error messages.
func (s *orderService) appendEventLocked(r orderRecord, event, reason string) error {
	value := struct {
		Time      time.Time `json:"time"`
		OrderID   string    `json:"order_id"`
		SessionID string    `json:"session_id"`
		DeviceID  string    `json:"device_id"`
		Event     string    `json:"event"`
		State     string    `json:"state"`
		Reason    string    `json:"reason,omitempty"`
	}{s.now().UTC(), r.businessID(), r.SessionID, r.Profile.DeviceID, event, r.State, reason}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(s.path), "order-events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *orderService) recordStateEvent(r orderRecord) {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	if err := s.appendEventLocked(r, "session_state", r.Reason); err != nil {
		// The order journal still contains the state/reason when event logging fails.
		log.Printf("Order event log write failed for session %s: %v", r.SessionID, err)
	}
}

func (s *orderService) viewerSeen(r orderRecord) {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	if s.events.viewers == nil {
		s.events.viewers = make(map[string]*orderViewerWatch)
	}
	w := s.events.viewers[r.SessionID]
	if w == nil {
		w = &orderViewerWatch{reported: make(map[string]bool)}
		s.events.viewers[r.SessionID] = w
	}
	w.lastSeen = s.now()
	w.lost = false
}

func (s *orderService) viewerFailure(r orderRecord, reason string) error {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	if s.events.viewers == nil {
		s.events.viewers = make(map[string]*orderViewerWatch)
	}
	w := s.events.viewers[r.SessionID]
	if w == nil {
		w = &orderViewerWatch{reported: make(map[string]bool)}
		s.events.viewers[r.SessionID] = w
	}
	if w.reported[reason] {
		return nil
	}
	if err := s.appendEventLocked(r, "viewer_failure", reason); err != nil {
		return err
	}
	w.reported[reason] = true
	return nil
}

func (s *orderService) checkViewerHeartbeat(r orderRecord) {
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	w := s.events.viewers[r.SessionID]
	if r.State != "active" || !s.now().Before(r.ExpiresAt) || w == nil ||
		w.lastSeen.IsZero() || w.lost || s.now().Sub(w.lastSeen) < orderViewerSilence {
		return
	}
	// Absence is observable; the cause could be a closed/background page or a
	// network problem. It must not be mislabeled as confirmed network failure.
	if err := s.appendEventLocked(r, "viewer_failure", "viewer_heartbeat_timeout"); err != nil {
		log.Printf("Order heartbeat log write failed for session %s: %v", r.SessionID, err)
		return
	}
	w.lost = true
}
