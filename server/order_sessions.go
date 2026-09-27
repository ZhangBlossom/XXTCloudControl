package main

// Order sessions are intentionally a single-process extension of XXTCloud.
// Device readiness and cleanup are evidence, never inferred from script.running=false.
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const orderUseTime = 6 * time.Minute
const orderCleanupTimeout = 120 * time.Second

type orderProfile struct {
	DeviceID      string `json:"device_id"`
	BundleID      string `json:"bundle_id"`
	Currency      string `json:"currency"`
	PolicyPath    string `json:"policy_path"`
	ScriptsDir    string `json:"scripts_dir"`
	CleanupScript string `json:"cleanup_script"`
	Verified      bool   `json:"verified"`
	ManualTest    bool   `json:"manual_test,omitempty"`
}

// orderPrice stores minor units internally but accepts/emits ordinary JSON decimal prices.
// No binary floating-point arithmetic is used to decide purchase allowances.
type orderPrice int64

func (p *orderPrice) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if len(text) == 0 || len(text) > 16 {
		return errors.New("invalid price")
	}
	whole, fraction, decimal := strings.Cut(text, ".")
	if whole == "" || (decimal && (len(fraction) == 0 || len(fraction) > 2)) {
		return errors.New("invalid price")
	}
	for _, c := range whole + fraction {
		if c < '0' || c > '9' {
			return errors.New("invalid price")
		}
	}
	major, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || major > 1000000 {
		return errors.New("invalid price")
	}
	minor := int64(0)
	if decimal {
		minor, err = strconv.ParseInt(fraction+strings.Repeat("0", 2-len(fraction)), 10, 64)
	}
	total := major*100 + minor
	if err != nil || total <= 0 || total > 100000000 {
		return errors.New("invalid price")
	}
	*p = orderPrice(total)
	return nil
}

func (p orderPrice) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%d.%02d", p/100, p%100)), nil
}

type orderRequest struct {
	BundleID    string     `json:"bundle_id"`
	ProductName string     `json:"product_name"`
	PriceMinor  orderPrice `json:"price"`
	Currency    string     `json:"currency"`
	CountryCode string     `json:"country_code"`
	Quantity    int        `json:"quantity"`
}

type orderRecord struct {
	BusinessOrderID string       `json:"business_order_id,omitempty"`
	Attempt         int          `json:"attempt,omitempty"`
	ID              string       `json:"order_id"`
	SessionID       string       `json:"session_id"`
	Request         orderRequest `json:"request"`
	Profile         orderProfile `json:"profile"`
	State           string       `json:"state"`
	CreatedAt       time.Time    `json:"created_at"`
	ExpiresAt       time.Time    `json:"expires_at"`
	Remaining       int          `json:"remaining"`
	Reason          string       `json:"reason,omitempty"`
	ViewerHash      string       `json:"viewer_hash,omitempty"`
}

type orderDeviceReport struct {
	Stopped   bool   `json:"stopped"`
	SessionID string `json:"session_id"`
	Phase     string `json:"phase"`
	Remaining int    `json:"remaining"`
	Error     string `json:"error,omitempty"`
}

type orderDeviceIO interface {
	Online(string) bool
	Prepare(context.Context, orderRecord) error
	Report(context.Context, orderRecord) (orderDeviceReport, error)
	RequestStop(context.Context, orderRecord) error
	StopViewer(context.Context, orderRecord) error
	Signal(context.Context, orderRecord, string, json.RawMessage) (json.RawMessage, error)
}

type orderService struct {
	mu           sync.Mutex
	records      map[string]orderRecord
	profiles     []orderProfile
	path         string
	io           orderDeviceIO
	now          func() time.Time
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	workers      map[string]bool
	viewerLocks  sync.Map
	requestLocks sync.Map
	publicURL    string
	storageErr   error
	localTest    bool
	events       orderEventLog
}

func newOrderService(path string, profiles []orderProfile, device orderDeviceIO) (*orderService, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &orderService{records: map[string]orderRecord{}, profiles: profiles, path: path, io: device, now: time.Now, ctx: ctx, cancel: cancel, workers: map[string]bool{}}
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &s.records)
	}
	if err != nil && !os.IsNotExist(err) {
		cancel()
		return nil, err
	}
	if s.records == nil {
		cancel()
		return nil, errors.New("invalid order journal")
	}
	for id, r := range s.records {
		r.Request = normalizeOrderRequest(r.Request)
		if !validOrderRequest(id, r.Request) {
			cancel()
			return nil, errors.New("invalid order journal: request schema requires price and product_name")
		}
		if r.State != "completed" {
			r.State = "quarantined"
			r.Reason = "server_restart_requires_cleanup"
		}
		r.ViewerHash = ""
		s.records[id] = r
	}
	if err = s.persistLocked(); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

func (s *orderService) persistLocked() (err error) {
	defer func() {
		if err != nil {
			s.storageErr = err
		}
	}()
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".orders-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, s.path)
	}
	if err == nil {
		var d *os.File
		d, err = os.Open(filepath.Dir(s.path))
		if err == nil {
			err = d.Sync()
			d.Close()
		}
	}
	if err != nil {
		s.storageErr = err
	}
	return err
}

func randomOrderToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func orderTokenHash(t string) string { b := sha256.Sum256([]byte(t)); return hex.EncodeToString(b[:]) }

func validOrderRequest(id string, r orderRequest) bool {
	if id == "" || len(id) > 128 || strings.ContainsAny(id, "/\\\x00") || strings.TrimSpace(r.ProductName) == "" || len(r.ProductName) > 200 {
		return false
	}
	if r.BundleID == "" || len(r.BundleID) > 255 || !upperCode(r.Currency, 3) || (r.CountryCode != "" && !upperCode(r.CountryCode, 2)) || r.PriceMinor <= 0 || r.PriceMinor > 100000000 || r.Quantity < 1 || r.Quantity > 100 {
		return false
	}
	for _, c := range r.BundleID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

func upperCode(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func normalizeOrderRequest(req orderRequest) orderRequest {
	if req.Currency == "" {
		req.Currency = "CNY"
	}
	if req.CountryCode == "" {
		req.CountryCode = "CN"
	}
	return req
}

func (s *orderService) create(id string, req orderRequest) (orderRecord, bool, error) {
	return s.allocate(id, req, false)
}

func (s *orderService) allocate(id string, req orderRequest, retry bool) (orderRecord, bool, error) {
	req = normalizeOrderRequest(req)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validOrderRequest(id, req) {
		return orderRecord{}, false, errors.New("invalid_order")
	}
	previous, exists := s.latestLocked(id)
	if exists && !retry {
		if previous.Request != req {
			return previous, false, errors.New("order_conflict")
		}
		return previous, false, nil
	}
	if exists && previous.State != "completed" && previous.State != "pending_cleanup" {
		return previous, false, errors.New("previous_session_unavailable")
	}
	if s.storageErr != nil {
		return orderRecord{}, false, errors.New("storage_unavailable")
	}
	used := map[string]bool{}
	for _, r := range s.records {
		if r.State != "completed" {
			used[r.Profile.DeviceID] = true
		}
	}
	var p orderProfile
	found := false
	for _, candidate := range s.profiles {
		if (candidate.Verified || (s.localTest && candidate.ManualTest)) && candidate.BundleID == req.BundleID && !used[candidate.DeviceID] && s.io.Online(candidate.DeviceID) {
			p = candidate
			found = true
			break
		}
	}
	if !found {
		return orderRecord{}, false, errors.New("no_capacity")
	}
	recordID, attempt := id, 0
	if exists {
		recordID, attempt = "retry-"+randomOrderToken(), previous.Attempt+1
	}
	if _, collision := s.records[recordID]; collision {
		return orderRecord{}, false, errors.New("order_conflict")
	}
	r := orderRecord{ID: recordID, BusinessOrderID: id, Attempt: attempt, SessionID: randomOrderToken(), Request: req, Profile: p, State: "preparing", CreatedAt: s.now().UTC(), Remaining: req.Quantity}
	s.records[recordID] = r
	if err := s.persistLocked(); err != nil {
		return orderRecord{}, false, errors.New("storage_unavailable")
	}
	return r, true, nil
}

func (s *orderService) get(id string) (orderRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	return r, ok
}
func (s *orderService) list() []orderRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]orderRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *orderService) change(id string, fn func(*orderRecord)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return errors.New("not_found")
	}
	previousState, previousReason := r.State, r.Reason
	fn(&r)
	s.records[id] = r
	if err := s.persistLocked(); err != nil {
		return err
	}
	if r.State != previousState || r.Reason != previousReason {
		s.recordStateEvent(r)
	}
	return nil
}

func (s *orderService) start(id string, prepare bool) {
	s.mu.Lock()
	if s.workers[id] {
		s.mu.Unlock()
		return
	}
	s.workers[id] = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.workers, id)
			retry := s.records[id].State == "closing" && s.ctx.Err() == nil
			s.mu.Unlock()
			if retry {
				s.start(id, false)
			}
		}()
		s.run(id, prepare)
	}()
}

func (s *orderService) quarantine(id, reason string) {
	_ = s.change(id, func(r *orderRecord) { r.State = "quarantined"; r.ViewerHash = ""; r.Reason = reason })
}

func (s *orderService) run(id string, prepare bool) {
	r, _ := s.get(id)
	if prepare {
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		err := s.io.Prepare(ctx, r)
		cancel()
		if err != nil {
			log.Printf("Order device preparation failed: %v", err)
			s.quarantine(id, "prepare_failed")
			s.cleanup(id)
			return
		}
	}
	deadline := s.now().Add(45 * time.Second)
	prepareDeadline := deadline
	for {
		r, _ = s.get(id)
		s.checkViewerHeartbeat(r)
		if r.State == "completed" || r.State == "pending_cleanup" {
			return
		}
		if r.State == "preparing" && !s.now().Before(prepareDeadline) {
			s.quarantine(id, "prepare_timeout")
			s.cleanup(id)
			return
		}
		if r.State == "closing" || r.State == "quarantined" {
			s.cleanup(id)
			return
		}
		if r.State == "active" && !s.now().Before(r.ExpiresAt) {
			_ = s.change(id, func(r *orderRecord) { r.State = "closing"; r.ViewerHash = ""; r.Reason = "expired" })
			s.cleanup(id)
			return
		}
		reportWait := 5 * time.Second
		if r.State == "active" && r.ExpiresAt.Sub(s.now()) < reportWait {
			reportWait = r.ExpiresAt.Sub(s.now())
		}
		ctx, cancel := context.WithTimeout(s.ctx, reportWait)
		report, err := s.io.Report(ctx, r)
		cancel()
		if err != nil {
			if !s.now().Before(deadline) {
				s.quarantine(id, "device_report_timeout")
				s.cleanup(id)
				return
			}
		} else {
			deadline = s.now().Add(15 * time.Second)
			if report.SessionID != r.SessionID || report.Remaining < 0 || report.Remaining > r.Remaining || (report.Phase != "preparing" && report.Phase != "ready" && report.Phase != "closed" && report.Phase != "failed" && report.Phase != "cleaning") {
				s.quarantine(id, "invalid_device_report")
				s.cleanup(id)
				return
			}
			if report.Phase == "ready" && r.State == "preparing" {
				err = s.change(id, func(current *orderRecord) {
					if current.State == "preparing" {
						current.State = "active"
						current.ExpiresAt = s.now().UTC().Add(orderUseTime)
						current.Remaining = report.Remaining
					}
				})
			} else if report.Phase == "ready" && r.State == "active" && report.Remaining != r.Remaining {
				err = s.change(id, func(current *orderRecord) { current.Remaining = report.Remaining })
			} else if report.Phase == "closed" || report.Phase == "failed" || report.Phase == "cleaning" {
				_ = s.change(id, func(r *orderRecord) { r.State = "closing"; r.ViewerHash = "" })
				s.cleanup(id)
				return
			}
			if err != nil {
				s.quarantine(id, "storage_unavailable")
				s.cleanup(id)
				return
			}
		}
		select {
		case <-s.ctx.Done():
			s.quarantine(id, "server_stopping")
			s.cleanup(id)
			return
		case <-time.After(s.pollDelay(id)):
		}
	}
}

func (s *orderService) pollDelay(id string) time.Duration {
	r, _ := s.get(id)
	if r.State == "active" {
		if remaining := r.ExpiresAt.Sub(s.now()); remaining < time.Second {
			if remaining < 0 {
				return 0
			}
			return remaining
		}
	}
	return time.Second
}

func (s *orderService) cleanup(id string) {
	l := s.viewerLock(id)
	l.Lock()
	defer l.Unlock()
	r, _ := s.get(id)
	// Stop media independently of the Lua script: a crashed Lua must not keep a viewer alive.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	viewErr := s.io.StopViewer(ctx, r)
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	stopErr := s.io.RequestStop(ctx, r)
	cancel()
	if viewErr != nil || stopErr != nil {
		s.quarantine(id, "stop_unconfirmed")
		return
	}
	deadline := s.now().Add(orderCleanupTimeout)
	for s.now().Before(deadline) {
		ctx, cancel = context.WithTimeout(context.Background(), min(5*time.Second, deadline.Sub(s.now())))
		report, err := s.io.Report(ctx, r)
		cancel()
		if !s.now().Before(deadline) {
			break
		}
		if err == nil && report.SessionID == r.SessionID && report.Phase == "closed" {
			if !report.Stopped || report.Remaining != 0 {
				s.quarantine(id, "stop_unconfirmed")
				return
			}
			if r.Profile.ManualTest {
				_ = s.change(id, func(current *orderRecord) {
					current.State = "pending_cleanup"
					current.ViewerHash = ""
					current.Remaining = 0
					current.Reason = "manual_cleanup_required"
				})
				return
			}
			// closed is emitted only after the configured game's cleanup AND verification pass.
			if err = s.change(id, func(r *orderRecord) { r.State = "completed"; r.ViewerHash = ""; r.Remaining = report.Remaining }); err != nil {
				s.quarantine(id, "storage_unavailable")
			}
			return
		}
		if err == nil && report.SessionID == r.SessionID && report.Phase == "failed" {
			if r.Profile.ManualTest && report.Error == "manual_cleanup_required" && report.Stopped && report.Remaining == 0 {
				_ = s.change(id, func(current *orderRecord) {
					current.State = "pending_cleanup"
					current.ViewerHash = ""
					current.Remaining = 0
					current.Reason = "manual_cleanup_required"
				})
				return
			}
			s.quarantine(id, "cleanup_failed")
			return
		}
		select {
		case <-s.ctx.Done():
			s.quarantine(id, "cleanup_interrupted")
			return
		case <-time.After(min(time.Second, deadline.Sub(s.now()))):
		}
	}
	s.quarantine(id, "cleanup_unconfirmed")
}

func (s *orderService) stop(id string) error {
	r, ok := s.get(id)
	if !ok {
		return errors.New("not_found")
	}
	if r.State == "completed" || r.State == "pending_cleanup" {
		return nil
	}
	if err := s.change(id, func(r *orderRecord) {
		if r.State != "completed" {
			r.State = "closing"
			r.ViewerHash = ""
			r.Reason = "released"
		}
	}); err != nil {
		return errors.New("storage_unavailable")
	}
	s.start(id, false)
	return nil
}

func (s *orderService) viewerLock(id string) *sync.Mutex {
	v, _ := s.viewerLocks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func (s *orderService) grant(ctx context.Context, id string) (string, error) {
	return s.grantToken(ctx, id, randomOrderToken())
}

// Wait for shared device operations without extending the caller's deadline.
func orderLockContext(ctx context.Context, mu *sync.Mutex) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *orderService) grantToken(ctx context.Context, id, token string) (string, error) {
	r, ok := s.get(id)
	if !ok {
		return "", errors.New("not_found")
	}
	if r.State != "active" || !s.now().Before(r.ExpiresAt) {
		return "", errors.New("not_active")
	}
	operationTime := 10 * time.Second
	if remaining := r.ExpiresAt.Sub(s.now()); remaining < operationTime {
		operationTime = remaining
	}
	ctx, cancel := context.WithTimeout(ctx, operationTime)
	defer cancel()
	l := s.viewerLock(id)
	if err := orderLockContext(ctx, l); err != nil {
		return "", errors.New("prepare_timeout")
	}
	defer l.Unlock()
	r, ok = s.get(id)
	if !ok {
		return "", errors.New("not_found")
	}
	if r.State != "active" || !s.now().Before(r.ExpiresAt) {
		return "", errors.New("not_active")
	}
	if r.ViewerHash == orderTokenHash(token) {
		return token, nil
	}
	if err := s.io.StopViewer(ctx, r); err != nil {
		s.quarantine(id, "viewer_stop_unconfirmed")
		if ctx.Err() != nil {
			return "", errors.New("prepare_timeout")
		}
		return "", errors.New("device_unavailable")
	}
	if ctx.Err() != nil {
		return "", errors.New("prepare_timeout")
	}
	err := s.change(id, func(r *orderRecord) {
		if r.State == "active" {
			r.ViewerHash = orderTokenHash(token)
		}
	})
	if err != nil {
		return "", errors.New("storage_unavailable")
	}
	if _, err := s.authorizeViewer(id, token); err != nil {
		return "", errors.New("not_active")
	}
	return token, nil
}

func (s *orderService) authorizeViewer(id, token string) (orderRecord, error) {
	r, ok := s.get(id)
	if !ok || r.State != "active" || !s.now().Before(r.ExpiresAt) || r.ViewerHash == "" || subtle.ConstantTimeCompare([]byte(r.ViewerHash), []byte(orderTokenHash(token))) != 1 {
		return orderRecord{}, errors.New("viewer_expired")
	}
	return r, nil
}

func validateOrderProfiles(profiles []orderProfile) error {
	seen := map[string]bool{}
	for _, p := range profiles {
		if p.DeviceID == "" || p.BundleID == "" || !upperCode(p.Currency, 3) || seen[p.DeviceID+"/"+p.BundleID] {
			return errors.New("invalid or duplicate order profile")
		}
		seen[p.DeviceID+"/"+p.BundleID] = true
		paths := []string{p.PolicyPath, p.ScriptsDir}
		if !p.ManualTest {
			paths = append(paths, p.CleanupScript)
		}
		if p.ManualTest && p.Verified {
			return errors.New("manual test profile cannot be verified")
		}
		for _, path := range paths {
			if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
				return fmt.Errorf("order profile paths must be device absolute paths")
			}
		}
	}
	return nil
}

// waitReady never returns a preparing device or hands polling work to the partner.
func (s *orderService) waitReady(ctx context.Context, id string) (orderRecord, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return orderRecord{}, errors.New("prepare_timeout")
		}
		r, ok := s.get(id)
		if !ok {
			return r, errors.New("not_found")
		}
		switch r.State {
		case "active":
			if !s.now().Before(r.ExpiresAt) {
				return r, errors.New("not_active")
			}
			return r, nil
		case "preparing":
		case "quarantined":
			return r, errors.New("device_unavailable")
		default:
			return r, errors.New("not_active")
		}
		select {
		case <-ctx.Done():
			return orderRecord{}, errors.New("prepare_timeout")
		case <-s.ctx.Done():
			return orderRecord{}, errors.New("device_unavailable")
		case <-ticker.C:
		}
	}
}

func (r orderRecord) businessID() string {
	if r.BusinessOrderID != "" {
		return r.BusinessOrderID
	}
	return r.ID
}

func (s *orderService) latestLocked(orderID string) (orderRecord, bool) {
	var result orderRecord
	found := false
	for _, r := range s.records {
		if r.businessID() == orderID && (!found || r.Attempt > result.Attempt) {
			result, found = r, true
		}
	}
	return result, found
}

// Explicit retry starts a fresh attempt, while retaining history and old worker identities.
// Only the partner decides to retry. Old viewers are revoked before a new phone is handed out.
func (s *orderService) connectRequest(ctx context.Context, id string, req orderRequest, retry bool) (orderRecord, error) {
	req = normalizeOrderRequest(req)
	if !validOrderRequest(id, req) {
		return orderRecord{}, errors.New("invalid_order")
	}
	gateValue, _ := s.requestLocks.LoadOrStore(id, make(chan struct{}, 1))
	gate := gateValue.(chan struct{})
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	default:
		return orderRecord{}, errors.New("request_in_progress")
	}
	if retry {
		s.mu.Lock()
		previous, exists := s.latestLocked(id)
		storageFailed := s.storageErr != nil
		s.mu.Unlock()
		if storageFailed {
			return orderRecord{}, errors.New("storage_unavailable")
		}
		if exists && previous.State != "completed" && previous.State != "pending_cleanup" {
			if err := s.stop(previous.ID); err != nil {
				return orderRecord{}, err
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				latest, _ := s.get(previous.ID)
				if latest.State == "completed" || latest.State == "pending_cleanup" {
					break
				}
				if latest.State == "quarantined" {
					return orderRecord{}, errors.New("previous_session_unavailable")
				}
				select {
				case <-ctx.Done():
					return orderRecord{}, errors.New("prepare_timeout")
				case <-s.ctx.Done():
					return orderRecord{}, errors.New("device_unavailable")
				case <-ticker.C:
				}
			}
		}
	}
	if ctx.Err() != nil {
		return orderRecord{}, errors.New("prepare_timeout")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return orderRecord{}, errors.New("prepare_timeout")
		}
		if s.ctx.Err() != nil {
			return orderRecord{}, errors.New("device_unavailable")
		}
		r, created, err := s.allocate(id, req, retry)
		if err == nil {
			if created {
				s.start(r.ID, true)
			}
			return r, nil
		}
		if err.Error() != "no_capacity" {
			return r, err
		}
		select {
		case <-ctx.Done():
			return orderRecord{}, errors.New("prepare_timeout")
		case <-s.ctx.Done():
			return orderRecord{}, errors.New("device_unavailable")
		case <-ticker.C:
		}
	}
}

// Availability is a snapshot, not a reservation or a queue-position promise.
type orderAvailability struct {
	AvailableCount       int        `json:"available_count"`
	NextAvailableAt      *time.Time `json:"next_available_at"`
	EstimatedWaitSeconds *int64     `json:"estimated_wait_seconds"`
}

func (s *orderService) availability(bundleID, currency string) (orderAvailability, error) {
	req := normalizeOrderRequest(orderRequest{BundleID: bundleID, Currency: currency, ProductName: "availability", PriceMinor: 1, Quantity: 1})
	if !validOrderRequest("availability", req) {
		return orderAvailability{}, errors.New("invalid_order")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageErr != nil {
		return orderAvailability{}, errors.New("storage_unavailable")
	}
	result := orderAvailability{}
	used := map[string]orderRecord{}
	for _, r := range s.records {
		if r.State != "completed" {
			used[r.Profile.DeviceID] = r
		}
	}
	seen := map[string]bool{}
	now := s.now().UTC()
	for _, p := range s.profiles {
		if seen[p.DeviceID] || p.BundleID != bundleID || !(p.Verified || (s.localTest && p.ManualTest)) || !s.io.Online(p.DeviceID) {
			continue
		}
		seen[p.DeviceID] = true
		r, busy := used[p.DeviceID]
		if !busy {
			result.AvailableCount++
			continue
		}
		if r.State == "active" && r.ExpiresAt.After(now) && (result.NextAvailableAt == nil || r.ExpiresAt.Before(*result.NextAvailableAt)) {
			expiry := r.ExpiresAt
			result.NextAvailableAt = &expiry
		}
	}
	if result.AvailableCount > 0 {
		result.NextAvailableAt = &now
	}
	if result.NextAvailableAt != nil {
		seconds := int64((result.NextAvailableAt.Sub(now) + time.Second - 1) / time.Second)
		result.EstimatedWaitSeconds = &seconds
	}
	return result, nil
}

// confirmClean records an administrator's completed cleanup, it does not clean the phone.
func (s *orderService) confirmClean(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return errors.New("not_found")
	}
	if r.State != "pending_cleanup" {
		return errors.New("cleanup_not_confirmable")
	}
	if s.storageErr != nil {
		return errors.New("storage_unavailable")
	}
	r.State = "completed"
	r.Reason = "manual_cleanup_confirmed"
	r.ViewerHash = ""
	s.records[id] = r
	return s.persistLocked()
}
