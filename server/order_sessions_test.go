package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type orderFakeDevice struct {
	stopErr error
	report  orderDeviceReport
	stopped atomic.Int32
}

func (*orderFakeDevice) Online(string) bool                         { return true }
func (*orderFakeDevice) Prepare(context.Context, orderRecord) error { return nil }
func (d *orderFakeDevice) Report(context.Context, orderRecord) (orderDeviceReport, error) {
	return d.report, nil
}
func (*orderFakeDevice) RequestStop(context.Context, orderRecord) error { return nil }
func (d *orderFakeDevice) StopViewer(context.Context, orderRecord) error {
	d.stopped.Add(1)
	return d.stopErr
}
func (*orderFakeDevice) Signal(context.Context, orderRecord, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func testOrderService(t *testing.T) (*orderService, *orderFakeDevice) {
	t.Helper()
	d := &orderFakeDevice{}
	s, err := newOrderService(filepath.Join(t.TempDir(), "orders.json"), []orderProfile{{DeviceID: "phone-1", BundleID: "test.game", Currency: "CNY", Verified: true}}, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.cancel(); s.wg.Wait() })
	return s, d
}
func testOrderRequest() orderRequest {
	return orderRequest{BundleID: "test.game", Currency: "CNY", ProductName: "648元商品", PriceMinor: 64800, Quantity: 5}
}
func createTestOrder(t *testing.T, s *orderService) orderRecord {
	t.Helper()
	r, _, err := s.create("order-1", testOrderRequest())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func activateTestOrder(t *testing.T, s *orderService, r orderRecord) {
	t.Helper()
	if err := s.change(r.ID, func(r *orderRecord) { r.State = "active"; r.ExpiresAt = s.now().Add(orderUseTime) }); err != nil {
		t.Fatal(err)
	}
}

func TestOrderConcurrentAllocationAndIdempotence(t *testing.T) {
	s, _ := testOrderService(t)
	var wg sync.WaitGroup
	var created atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, ok, err := s.create(fmt.Sprint(i), testOrderRequest())
			if err == nil && ok {
				created.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("allocated %d orders on one phone", created.Load())
	}
	r := s.list()[0]
	again, newOrder, err := s.create(r.ID, r.Request)
	if err != nil || newOrder || again.SessionID != r.SessionID {
		t.Fatal("retry reset the session", err)
	}
	req := r.Request
	req.Quantity++
	if _, _, err = s.create(r.ID, req); err == nil || err.Error() != "order_conflict" {
		t.Fatal("changed retry accepted")
	}
}
func TestOrderUnverifiedAndBadInputs(t *testing.T) {
	s, _ := testOrderService(t)
	s.profiles[0].Verified = false
	if _, _, err := s.create("one", testOrderRequest()); err == nil || err.Error() != "no_capacity" {
		t.Fatal("unverified device allocated")
	}
	for _, mutate := range []func(*orderRequest){func(r *orderRequest) { r.PriceMinor = 0 }, func(r *orderRequest) { r.Quantity = 0 }, func(r *orderRequest) { r.Currency = "usd" }, func(r *orderRequest) { r.BundleID = "';os.exit()" }} {
		r := testOrderRequest()
		mutate(&r)
		if validOrderRequest("one", r) {
			t.Fatalf("accepted invalid input %+v", r)
		}
	}
}
func TestOrderViewerRotationExpiryAndIsolation(t *testing.T) {
	s, d := testOrderService(t)
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	a, err := s.grant(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.grant(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.authorizeViewer(r.ID, a); err == nil {
		t.Fatal("old viewer accepted")
	}
	if _, err = s.authorizeViewer("different-order", b); err == nil {
		t.Fatal("cross order viewer accepted")
	}
	if _, err = s.authorizeViewer(r.ID, b); err != nil {
		t.Fatal(err)
	}
	if d.stopped.Load() != 2 {
		t.Fatal("rotation did not revoke media")
	}
	s.change(r.ID, func(r *orderRecord) { r.ExpiresAt = s.now() })
	if _, err = s.authorizeViewer(r.ID, b); err == nil {
		t.Fatal("expired viewer accepted")
	}
}
func TestOrderCleanupAndRestartFailClosed(t *testing.T) {
	s, d := testOrderService(t)
	r := createTestOrder(t, s)
	d.stopErr = errors.New("offline")
	s.cleanup(r.ID)
	got, _ := s.get(r.ID)
	if got.State != "quarantined" {
		t.Fatal(got.State)
	}
	if _, _, err := s.create("next", r.Request); err == nil {
		t.Fatal("unclean device reused")
	}
	d.stopErr = nil
	d.report = orderDeviceReport{SessionID: r.SessionID, Phase: "closed", Remaining: 0, Stopped: true}
	s.cleanup(r.ID)
	got, _ = s.get(r.ID)
	if got.State != "completed" || got.Remaining != 0 {
		t.Fatal(got)
	}
	next, _, err := s.create("next", r.Request)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := newOrderService(s.path, s.profiles, d)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.cancel()
	got, _ = reopened.get(next.ID)
	if got.State != "quarantined" || got.ViewerHash != "" {
		t.Fatal("restart resumed unsafe session")
	}
	if _, _, err = reopened.create("third", r.Request); err == nil {
		t.Fatal("restart released occupied phone")
	}
}
func TestOrderInvalidQuotaReportQuarantines(t *testing.T) {
	s, d := testOrderService(t)
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	d.report = orderDeviceReport{SessionID: r.SessionID, Phase: "ready", Remaining: 6}
	d.stopErr = errors.New("offline")
	s.run(r.ID, false)
	got, _ := s.get(r.ID)
	if got.State != "quarantined" {
		t.Fatal("increased quota accepted", got)
	}
}
func TestOrderSixMinuteExpiryCloses(t *testing.T) {
	s, d := testOrderService(t)
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	if orderUseTime != 360*time.Second {
		t.Fatal(orderUseTime)
	}
	s.change(r.ID, func(r *orderRecord) { r.ExpiresAt = s.now().Add(-time.Second) })
	d.report = orderDeviceReport{SessionID: r.SessionID, Phase: "closed", Remaining: 0, Stopped: true}
	s.run(r.ID, false)
	got, _ := s.get(r.ID)
	if got.State != "completed" || d.stopped.Load() != 1 {
		t.Fatal(got)
	}
}
func TestOrderHTTPAuthAndStrictInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, _ := testOrderService(t)
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	token, err := s.grant(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.publicURL = "https://cloud.example.test"
	router := gin.New()
	router.Use(apiAuthMiddleware())
	registerOrderHTTP(router, s, "partner-test-key")
	for _, tc := range []struct {
		method, path, auth, body string
		want                     int
	}{
		{"POST", "/orders/v1/connect", "", `{}`, 400},
		{"GET", "/api/order-sessions", "Bearer " + token, "", 401},
		{"POST", "/orders/v1/connect", "Bearer " + token, `{}`, 400},

		{"GET", "/order-viewer/v1/order-1/status", "Bearer partner-test-key", "", 401},
		{"GET", "/order-viewer/v1/order-1/status", "Bearer " + token, "", 200},
		{"POST", "/order-viewer/v1/order-1/signal/script", "Bearer " + token, `{}`, 400},
		{"POST", "/orders/v1/connect", "Bearer partner-test-key", `{"unknown":true}`, 400},
		{"POST", "/orders/v1/connect", "Bearer partner-test-key", `{} {}`, 400},
		{"POST", "/orders/v1/connect", "Bearer partner-test-key", `{"order_id":"bad","bundle_id":"test.game","price":648,"quantity":5}`, 400},
		{"POST", "/orders/v1/connect", "Bearer partner-test-key", `{"order_id":"bad","bundle_id":"test.game","product_name":"game","quantity":5}`, 400},
		{"POST", "/orders/v1/connect", "Bearer partner-test-key", `{"order_id":"bad","bundle_id":"test.game","product_name":"game","price_minor":64800,"quantity":5}`, 400},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s %s got %d want %d: %s", tc.method, tc.path, w.Code, tc.want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "viewer_hash") || strings.Contains(w.Body.String(), "policy_path") {
			t.Fatal("internal profile leaked")
		}
	}
}
func TestOrderReplyBoundToDeviceConnection(t *testing.T) {
	a, b := &SafeConn{}, &SafeConn{}
	ch := make(chan Message, 1)
	orderReplies.Lock()
	orderReplies.entries["test-order-reply"] = orderReplyWaiter{a, ch}
	orderReplies.Unlock()
	defer func() { orderReplies.Lock(); delete(orderReplies.entries, "test-order-reply"); orderReplies.Unlock() }()
	msg := Message{RequestID: "test-order-reply", Type: "script/put"}
	if deliverOrderReply(b, msg) {
		t.Fatal("other device spoofed reply")
	}
	if !deliverOrderReply(a, msg) {
		t.Fatal("correct device reply missing")
	}
}

func TestOrderRealAdapterProtocol(t *testing.T) {
	d := cloudOrderDevice{}
	r := orderRecord{SessionID: "protocol-session", Request: testOrderRequest(), Profile: orderProfile{DeviceID: "protocol-phone", ScriptsDir: "/scripts", PolicyPath: "/policy", CleanupScript: "/cleanup"}}
	var conn *SafeConn
	var uploaded string
	var commands []string
	conn = &SafeConn{writeMessageHook: func(_ int, data []byte) error {
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		commands = append(commands, msg.Type)
		body, _ := msg.Body.(map[string]interface{})
		switch msg.Type {
		case "http/request":
			if _, ok := body["query"].(map[string]interface{}); !ok {
				t.Fatal("native Lua requires a query table, not JSON null")
			}
			if body["path"] != "/is_running" {
				t.Fatalf("unexpected path %v", body["path"])
			}
			msg.Type = "http/response"
			msg.Body = map[string]interface{}{"requestId": msg.RequestID, "statusCode": 200, "body": base64.StdEncoding.EncodeToString([]byte(`{"code":0}`))}
			msg.RequestID = "" // The native HTTP path replies with body.requestId.
		case "script/put":
			raw, err := base64.StdEncoding.DecodeString(body["data"].(string))
			if err != nil {
				return err
			}
			uploaded = string(raw)
		case "script/run":
			if body["name"] != "xxt-order-protocol-session.lua" {
				t.Fatal(body)
			}
		case "script/get":
			report, _ := json.Marshal(orderDeviceReport{SessionID: r.SessionID, Phase: "ready", Remaining: 4})
			msg.Body = base64.StdEncoding.EncodeToString(report)
		default:
			t.Fatalf("unexpected command %s", msg.Type)
		}
		if !deliverOrderReply(conn, msg) {
			t.Fatal("reply was not correlated")
		}
		return nil
	}}
	mu.Lock()
	deviceLinks[r.Profile.DeviceID] = conn
	mu.Unlock()
	defer func() { mu.Lock(); delete(deviceLinks, r.Profile.DeviceID); mu.Unlock() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Prepare(ctx, r); err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`json.decode\(\('([^']+)'`).FindStringSubmatch(uploaded)
	if len(match) != 2 {
		t.Fatal("missing encoded config")
	}
	raw, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["price"] != "648" || cfg["quantity"] != float64(5) || cfg["duration_seconds"] != float64(360) || cfg["policy_path"] != "/policy" {
		t.Fatal(cfg)
	}
	if !strings.HasSuffix(uploaded, orderLua) {
		t.Fatal("different script deployed")
	}
	report, err := d.Report(ctx, r)
	if err != nil || report.Remaining != 4 || report.SessionID != r.SessionID {
		t.Fatal(report, err)
	}
	if strings.Join(commands, ",") != "http/request,script/put,script/run,script/get" {
		t.Fatal(commands)
	}
}

func TestOrderSingleConnectAndRetry(t *testing.T) {
	s, d := testOrderService(t)
	s.publicURL = "https://cloud.example.test"
	r := createTestOrder(t, s)
	router := gin.New()
	registerOrderHTTP(router, s, "partner-key")
	payload := `{"order_id":"order-1","bundle_id":"test.game","product_name":"648元商品","price":648,"quantity":5}`
	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/orders/v1/connect", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer partner-key")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- call(payload) }()
	select {
	case <-result:
		t.Fatal("returned before the phone was ready")
	case <-time.After(30 * time.Millisecond):
	}
	activateTestOrder(t, s, r)
	first := <-result
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var data struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		URL     string `json:"viewer_url"`
		Expires string `json:"expires_at"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Code != 200 || data.Message != "成功" {
		t.Fatal("invalid success response", first.Body.String())
	}
	u, err := url.Parse(data.URL)
	if err != nil || u.Host != "cloud.example.test" || u.Path != "/order-viewer" {
		t.Fatal(data.URL, err)
	}
	fragment, _ := url.ParseQuery(u.Fragment)
	if _, err = s.authorizeViewer(r.ID, fragment.Get("token")); err != nil {
		t.Fatal("returned unusable token", err)
	}
	second := call(strings.Replace(strings.TrimSuffix(payload, "}"), `"price":648`, `"price":648.00`, 1) + `,"currency":"CNY"}`)
	if second.Code != 200 || second.Body.String() != first.Body.String() || d.stopped.Load() != 1 {
		t.Fatal("retry changed URL/expiry or interrupted media", second.Body.String(), d.stopped.Load())
	}
	conflict := call(strings.Replace(payload, "\"price\":648", "\"price\":328", 1))
	if conflict.Code != 409 {
		t.Fatal(conflict.Code)
	}
	s.quarantine(r.ID, "test")
	unavailable := call(payload)
	if unavailable.Code != 503 || strings.Contains(unavailable.Body.String(), "viewer_url") {
		t.Fatal("failed phone delivered", unavailable.Body.String())
	}
}
func TestOrderReadinessTimeoutDoesNotDeliver(t *testing.T) {
	s, _ := testOrderService(t)
	r := createTestOrder(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := s.waitReady(ctx, r.ID); err == nil || err.Error() != "prepare_timeout" {
		t.Fatal(err)
	}
	got, _ := s.get(r.ID)
	if got.State != "preparing" || got.ViewerHash != "" {
		t.Fatal("waiting reset or handed out a pending session")
	}
}

func TestOrderDecimalPriceAndRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		number string
		minor  orderPrice
		valid  bool
	}{
		{"648", 64800, true}, {"648.0", 64800, true}, {"648.00", 64800, true}, {"6.48", 648, true}, {"0.01", 1, true}, {"0.29", 29, true}, {"1000000", 100000000, true},
		{"0", 0, false}, {"-1", 0, false}, {"1.001", 0, false}, {"1000000.01", 0, false}, {`"648"`, 0, false}, {"null", 0, false}, {"1e2", 0, false}, {"1e999999", 0, false},
	} {
		var p orderPrice
		err := json.Unmarshal([]byte(tc.number), &p)
		if (err == nil) != tc.valid {
			t.Errorf("%s valid=%v error=%v", tc.number, tc.valid, err)
			continue
		}
		if tc.valid {
			if p != tc.minor {
				t.Errorf("%s became %d", tc.number, p)
			}
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var again orderPrice
			if err = json.Unmarshal(data, &again); err != nil || again != p {
				t.Fatalf("round trip %s: %v", data, err)
			}
		}
	}
	for _, name := range []string{"", " ", "\t\n"} {
		r := testOrderRequest()
		r.ProductName = name
		if validOrderRequest("one", r) {
			t.Fatal("blank product accepted")
		}
	}
	s, _ := testOrderService(t)
	req := testOrderRequest()
	req.Currency = ""
	r, created, err := s.create("default-currency", req)
	if err != nil || !created || r.Request.Currency != "CNY" {
		t.Fatal(r, err)
	}
	req.Currency = "CNY"
	again, created, err := s.create(r.ID, req)
	if err != nil || created || again.SessionID != r.SessionID {
		t.Fatal("default and explicit CNY are not idempotent", err)
	}
}

func TestOrderErrorResponseEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		reason string
		status int
	}{
		{"request_in_progress", 409}, {"previous_session_unavailable", 503}, {"internal_error", 500}, {"invalid_order", 400}, {"unauthorized", 401}, {"order_conflict", 409}, {"no_capacity", 409}, {"not_active", 409}, {"device_unavailable", 503}, {"storage_unavailable", 503}, {"prepare_timeout", 504},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		orderHTTPError(c, tc.reason)
		var got struct {
			Code      int    `json:"code"`
			Message   string `json:"message"`
			ErrorCode string `json:"error_code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || got.Code != w.Code || got.Message == "" || got.Message == "成功" || got.ErrorCode != tc.reason {
			t.Fatalf("unexpected error response: %s", w.Body.String())
		}
		if strings.Contains(w.Body.String(), "viewer_url") {
			t.Fatal("failed response contains viewer URL")
		}
	}
}

// Simulates lifecycle acknowledgements, not actual iOS cleanup or StoreKit behavior.
type orderRetryDevice struct {
	orderFakeDevice
	mu       sync.Mutex
	closed   map[string]bool
	prepares atomic.Int32
}

func (d *orderRetryDevice) Prepare(context.Context, orderRecord) error { d.prepares.Add(1); return nil }
func (d *orderRetryDevice) Report(_ context.Context, r orderRecord) (orderDeviceReport, error) {
	d.mu.Lock()
	closed := d.closed[r.SessionID]
	d.mu.Unlock()
	phase := "ready"
	if closed {
		phase = "closed"
	}
	remaining := r.Request.Quantity
	if closed {
		remaining = 0
	}
	return orderDeviceReport{SessionID: r.SessionID, Phase: phase, Remaining: remaining, Stopped: closed}, nil
}
func (d *orderRetryDevice) RequestStop(_ context.Context, r orderRecord) error {
	d.mu.Lock()
	d.closed[r.SessionID] = true
	d.mu.Unlock()
	return nil
}
func TestOrderExplicitRetryStartsNewAttempt(t *testing.T) {
	s, _ := testOrderService(t)
	first := createTestOrder(t, s)
	s.change(first.ID, func(r *orderRecord) { r.State = "completed"; r.Remaining = 1 })
	req := testOrderRequest()
	req.PriceMinor = 198
	req.Quantity = 3
	next, created, err := s.allocate(first.ID, req, true)
	if err != nil || !created || next.ID == first.ID || next.SessionID == first.SessionID || next.Remaining != 3 || next.Attempt != 1 || next.businessID() != first.ID {
		t.Fatal(next, created, err)
	}
	old, _ := s.get(first.ID)
	if old.Remaining != 1 || old.State != "completed" {
		t.Fatal("retry overwrote history", old)
	}
	repeated, created, err := s.create(first.ID, req)
	if err != nil || created || repeated.ID != next.ID {
		t.Fatal("ordinary repeat did not select latest attempt", err)
	}
	// Each new true flag means another attempt, not an idempotent redelivery.
	s.change(next.ID, func(r *orderRecord) { r.State = "completed" })
	third, created, err := s.allocate(first.ID, req, true)
	if err != nil || !created || third.ID == next.ID || third.Attempt != 2 {
		t.Fatal(third, err)
	}
	restored, err := newOrderService(s.path, s.profiles, s.io)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.cancel()
	restored.mu.Lock()
	latest, ok := restored.latestLocked(first.ID)
	restored.mu.Unlock()
	if !ok || latest.ID != third.ID {
		t.Fatal("restart lost latest retry", latest)
	}
}
func TestOrderRetryEndpointRevokesOldViewer(t *testing.T) {
	d := &orderRetryDevice{closed: map[string]bool{}}
	s, err := newOrderService(filepath.Join(t.TempDir(), "orders.json"), []orderProfile{{DeviceID: "phone", BundleID: "test.game", Currency: "CNY", Verified: true}}, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.cancel(); s.wg.Wait() })
	s.publicURL = "https://cloud.example.test"
	router := gin.New()
	registerOrderHTTP(router, s, "test-key")
	payload := `{"order_id":"retry-order","bundle_id":"test.game","product_name":"game","price":1.98,"quantity":5}`
	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/orders/v1/connect", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-key")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first := call(payload)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var a, b struct {
		OrderID string `json:"order_id"`
		URL     string `json:"viewer_url"`
	}
	json.Unmarshal(first.Body.Bytes(), &a)
	u, _ := url.Parse(a.URL)
	oldParams, _ := url.ParseQuery(u.Fragment)
	retry := call(strings.TrimSuffix(payload, "}") + `,"retry":true}`)
	if retry.Code != 200 {
		t.Fatal(retry.Code, retry.Body.String())
	}
	json.Unmarshal(retry.Body.Bytes(), &b)
	if a.OrderID != b.OrderID || a.URL == b.URL || d.prepares.Load() != 2 {
		t.Fatal("explicit retry did not create a new usable attempt", a, b, d.prepares.Load())
	}
	if _, err = s.authorizeViewer(oldParams.Get("order"), oldParams.Get("token")); err == nil {
		t.Fatal("old viewer remains authorized")
	}
	u, _ = url.Parse(b.URL)
	currentParams, _ := url.ParseQuery(u.Fragment)
	if _, err = s.authorizeViewer(currentParams.Get("order"), currentParams.Get("token")); err != nil {
		t.Fatal(err)
	}
	normal := call(payload)
	if normal.Body.String() != retry.Body.String() || d.prepares.Load() != 2 {
		t.Fatal("normal repeat allocated again")
	}
}
func TestOrderRetryCannotOverlapUnconfirmedSession(t *testing.T) {
	s, d := testOrderService(t)
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	d.stopErr = errors.New("offline")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.connectRequest(ctx, r.ID, r.Request, true)
	if err == nil || err.Error() != "previous_session_unavailable" || len(s.list()) != 1 {
		t.Fatal("retry handed out another phone before revocation", err)
	}
}

func TestOrderConnectDeadline(t *testing.T) {
	if orderConnectTimeout != 30*time.Second {
		t.Fatal("device fallback deadline must be 30 seconds")
	}
	for _, stage := range []string{"preparing", "viewer_busy"} {
		t.Run(stage, func(t *testing.T) {
			s, _ := testOrderService(t)
			s.publicURL = "https://cloud.example.test"
			r := createTestOrder(t, s)
			if stage == "viewer_busy" {
				activateTestOrder(t, s, r)
				lock := s.viewerLock(r.ID)
				lock.Lock()
				defer lock.Unlock()
			}
			router := gin.New()
			registerOrderHTTP(router, s, "partner-key")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			req := httptest.NewRequest("POST", "/orders/v1/connect", strings.NewReader(`{"order_id":"order-1","bundle_id":"test.game","product_name":"648元商品","price":648,"quantity":5}`)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer partner-key")
			out := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { router.ServeHTTP(out, req); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("handler ignored deadline")
			}
			if out.Code != 504 || !strings.Contains(out.Body.String(), `"error_code":"prepare_timeout"`) || strings.Contains(out.Body.String(), "viewer_url") {
				t.Fatal(out.Code, out.Body.String())
			}
		})
	}
}

func TestOrderDeviceWriteDeadline(t *testing.T) {
	conn := &SafeConn{}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sendOrderMessage(ctx, conn, Message{Type: "test"}) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("device writer lock ignored deadline")
	}
}

func TestOrderManualTestIsolation(t *testing.T) {
	s, d := testOrderService(t)
	s.profiles[0].Verified = false
	s.profiles[0].ManualTest = true
	if _, _, err := s.create("manual-test", testOrderRequest()); err == nil {
		t.Fatal("manual profile allocated outside local test mode")
	}
	s.localTest = true
	r, _, err := s.create("manual-test", testOrderRequest())
	if err != nil {
		t.Fatal(err)
	}
	d.report = orderDeviceReport{SessionID: r.SessionID, Phase: "closed", Remaining: 0, Stopped: true}
	s.cleanup(r.ID)
	after, _ := s.get(r.ID)
	if after.State != "pending_cleanup" || after.Reason != "manual_cleanup_required" {
		t.Fatal(after)
	}
	if _, _, err := s.create("next-test", testOrderRequest()); err == nil {
		t.Fatal("manual device automatically reused")
	}
}

func TestOrderLocalTestRejectsNonLoopback(t *testing.T) {
	s, _ := testOrderService(t)
	s.localTest = true
	s.publicURL = "http://127.0.0.1:46980"
	router := gin.New()
	registerOrderHTTP(router, s, "test-key")
	for _, addr := range []string{"192.168.1.9:5000", "127.0.0.1:5000"} {
		req := httptest.NewRequest("POST", "/orders/v1/connect", strings.NewReader(`{}`))
		req.RemoteAddr = addr
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		want := 401
		if strings.HasPrefix(addr, "127.") {
			want = 400
		}
		if out.Code != want {
			t.Fatalf("%s: %d %s", addr, out.Code, out.Body.String())
		}
	}
}

func TestOrderAvailabilityAndWaiting(t *testing.T) {
	s, _ := testOrderService(t)
	req := testOrderRequest()
	req.Currency = "USD"
	r, _, err := s.create("busy", req)
	if err != nil {
		t.Fatal(err)
	}
	activateTestOrder(t, s, r)
	status, err := s.availability(req.BundleID, "USD")
	if err != nil || status.AvailableCount != 0 || status.EstimatedWaitSeconds == nil || *status.EstimatedWaitSeconds < 359 {
		t.Fatal(status, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err = s.connectRequest(ctx, "waiting", req, false); err == nil || err.Error() != "prepare_timeout" {
		t.Fatal(err)
	}
	if len(s.list()) != 1 {
		t.Fatal("cancelled waiter allocated a phone")
	}
	s.change(r.ID, func(r *orderRecord) { r.State = "pending_cleanup" })
	status, err = s.availability(req.BundleID, "USD")
	if err != nil || status.AvailableCount != 0 || status.NextAvailableAt != nil {
		t.Fatal(status, err)
	}
	s.change(r.ID, func(r *orderRecord) { r.State = "completed" })
	status, err = s.availability(req.BundleID, "USD")
	if err != nil || status.AvailableCount != 1 || status.EstimatedWaitSeconds == nil || *status.EstimatedWaitSeconds != 0 {
		t.Fatal(status, err)
	}
}

func TestOrderWaiterGetsReleasedDevice(t *testing.T) {
	d := &orderRetryDevice{closed: map[string]bool{}}
	s, err := newOrderService(filepath.Join(t.TempDir(), "orders.json"), []orderProfile{{DeviceID: "one", BundleID: "test.game", Currency: "CNY", Verified: true}}, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.cancel(); s.wg.Wait() })
	r, _, _ := s.create("busy", testOrderRequest())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := s.connectRequest(ctx, "waiter", testOrderRequest(), false); result <- err }()
	select {
	case err := <-result:
		t.Fatal("returned before release", err)
	case <-time.After(30 * time.Millisecond):
	}
	s.change(r.ID, func(r *orderRecord) { r.State = "completed" })
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestOrderPendingCleanupAllowsRetryOnOtherPhone(t *testing.T) {
	s, d := testOrderService(t)
	s.localTest = true
	s.profiles[0].Verified = false
	s.profiles[0].ManualTest = true
	r := createTestOrder(t, s)
	d.report = orderDeviceReport{SessionID: r.SessionID, Phase: "failed", Error: "manual_cleanup_required", Stopped: true, Remaining: 0}
	s.cleanup(r.ID)
	s.profiles = append(s.profiles, orderProfile{DeviceID: "phone-2", BundleID: "test.game", Currency: "CNY", Verified: true})
	next, created, err := s.allocate(r.ID, r.Request, true)
	if err != nil || !created || next.Profile.DeviceID != "phone-2" {
		t.Fatal(next, err)
	}
	old, _ := s.get(r.ID)
	if old.State != "pending_cleanup" {
		t.Fatal(old)
	}
}

func TestOrderCountryNormalization(t *testing.T) {
	s, _ := testOrderService(t)
	r := createTestOrder(t, s)
	if r.Request.CountryCode != "CN" {
		t.Fatal(r.Request)
	}
	req := testOrderRequest()
	req.CountryCode = "CN"
	if _, created, err := s.create(r.ID, req); err != nil || created {
		t.Fatal(err)
	}
	req.CountryCode = "US"
	if _, _, err := s.create(r.ID, req); err == nil || err.Error() != "order_conflict" {
		t.Fatal(err)
	}
	req.CountryCode = "usa"
	if validOrderRequest("x", req) {
		t.Fatal("invalid country accepted")
	}
}

func TestOrderManualCleanupConfirmation(t *testing.T) {
	s, _ := testOrderService(t)
	r := createTestOrder(t, s)
	for _, state := range []string{"preparing", "active", "quarantined"} {
		s.change(r.ID, func(r *orderRecord) { r.State = state })
		if err := s.confirmClean(r.ID); err == nil || err.Error() != "cleanup_not_confirmable" {
			t.Fatal(state, err)
		}
	}
	s.change(r.ID, func(r *orderRecord) { r.State = "pending_cleanup" })
	if err := s.confirmClean(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.create("next", testOrderRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestOrderUnconfirmedStopCannotBecomePendingCleanup(t *testing.T) {
	s, d := testOrderService(t)
	s.localTest = true
	s.profiles[0].Verified = false
	s.profiles[0].ManualTest = true
	r := createTestOrder(t, s)
	d.report = orderDeviceReport{SessionID: r.SessionID, Phase: "failed", Error: "manual_cleanup_required", Remaining: 0}
	s.cleanup(r.ID)
	after, _ := s.get(r.ID)
	if after.State != "quarantined" {
		t.Fatal(after)
	}
	if err := s.confirmClean(r.ID); err == nil {
		t.Fatal("unconfirmed stop released")
	}
}

type orderBlockingViewerDevice struct {
	orderFakeDevice
	deadline time.Time
}

func (d *orderBlockingViewerDevice) StopViewer(ctx context.Context, _ orderRecord) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("missing operation deadline")
	}
	d.deadline = deadline
	<-ctx.Done()
	return ctx.Err()
}
func TestOrderGrantBoundsStopAndReleasesLock(t *testing.T) {
	for _, stage := range []string{"stop", "lock"} {
		t.Run(stage, func(t *testing.T) {
			s, _ := testOrderService(t)
			d := &orderBlockingViewerDevice{}
			s.io = d
			r := createTestOrder(t, s)
			expiry := time.Now().Add(40 * time.Millisecond)
			s.change(r.ID, func(r *orderRecord) { r.State = "active"; r.ExpiresAt = expiry })
			lock := s.viewerLock(r.ID)
			if stage == "lock" {
				lock.Lock()
			}
			done := make(chan error, 1)
			go func() { _, err := s.grantToken(context.Background(), r.ID, "test-token"); done <- err }()
			select {
			case err := <-done:
				if err == nil || err.Error() != "prepare_timeout" {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("grant ignored expiry")
			}
			if stage == "lock" {
				lock.Unlock()
			} else {
				if d.deadline.IsZero() || d.deadline.Sub(expiry) > 5*time.Millisecond {
					t.Fatal("stop deadline exceeded lease", d.deadline)
				}
			}
			if !lock.TryLock() {
				t.Fatal("grant retained viewer lock")
			}
			lock.Unlock()
		})
	}
}

func TestOrderCleanupRequiresConfirmedStopAndZeroQuota(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stopped   bool
		remaining int
	}{
		{"missing stop confirmation", false, 0},
		{"quota remains", true, 1},
		{"negative quota", true, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, d := testOrderService(t)
			r := createTestOrder(t, s)
			d.report = orderDeviceReport{SessionID: r.SessionID, Phase: "closed", Stopped: tc.stopped, Remaining: tc.remaining}
			s.cleanup(r.ID)
			got, _ := s.get(r.ID)
			if got.State != "quarantined" || got.Reason != "stop_unconfirmed" {
				t.Fatal(got)
			}
			if _, _, err := s.create("next", r.Request); err == nil {
				t.Fatal("unsafe device reused")
			}
		})
	}
}

type orderDelayedCleanupDevice struct {
	orderFakeDevice
	now         *time.Time
	elapsed     time.Duration
	closed      bool
	reportCalls int
}

func (d *orderDelayedCleanupDevice) Report(ctx context.Context, r orderRecord) (orderDeviceReport, error) {
	d.reportCalls++
	*d.now = d.now.Add(d.elapsed)
	if d.closed && d.reportCalls > 1 {
		return orderDeviceReport{SessionID: r.SessionID, Phase: "closed", Stopped: true, Remaining: 0}, nil
	}
	return orderDeviceReport{}, errors.New("still cleaning")
}
func TestOrderCleanupIndependentDeadline(t *testing.T) {
	if orderCleanupTimeout != 120*time.Second {
		t.Fatal(orderCleanupTimeout)
	}
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		closed  bool
		state   string
	}{
		{"cleanup exceeds old 30 second limit", 45 * time.Second, true, "completed"},
		{"cleanup deadline isolates device", 120 * time.Second, false, "quarantined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testOrderService(t)
			now := time.Now()
			s.now = func() time.Time { return now }
			d := &orderDelayedCleanupDevice{now: &now, elapsed: tc.elapsed, closed: tc.closed}
			s.io = d
			r := createTestOrder(t, s)
			s.cleanup(r.ID)
			got, _ := s.get(r.ID)
			wantCalls := 1
			if tc.closed {
				wantCalls = 2
			}
			if got.State != tc.state || d.reportCalls != wantCalls {
				t.Fatal(got, d.reportCalls)
			}
			if !tc.closed && got.Reason != "cleanup_unconfirmed" {
				t.Fatal(got)
			}
		})
	}
}

type orderCleaningReportDevice struct {
	orderFakeDevice
	service         *orderService
	reports         int
	closingObserved bool
}

func (d *orderCleaningReportDevice) Report(_ context.Context, r orderRecord) (orderDeviceReport, error) {
	d.reports++
	if d.reports == 1 {
		return orderDeviceReport{SessionID: r.SessionID, Phase: "cleaning", Remaining: 0, Stopped: true}, nil
	}
	current, _ := d.service.get(r.ID)
	d.closingObserved = current.State == "closing" && current.ViewerHash == ""
	return orderDeviceReport{SessionID: r.SessionID, Phase: "closed", Remaining: 0, Stopped: true}, nil
}
func TestOrderCleaningReportRevokesViewerAndWaitsForCleanup(t *testing.T) {
	s, _ := testOrderService(t)
	d := &orderCleaningReportDevice{service: s}
	s.io = d
	r := createTestOrder(t, s)
	activateTestOrder(t, s, r)
	s.change(r.ID, func(r *orderRecord) { r.ViewerHash = orderTokenHash("token") })
	s.run(r.ID, false)
	after, _ := s.get(r.ID)
	if !d.closingObserved || d.reports != 2 || after.State != "completed" || after.Reason == "invalid_device_report" {
		t.Fatal(after, d.closingObserved, d.reports)
	}
	if _, err := s.authorizeViewer(r.ID, "token"); err == nil {
		t.Fatal("cleaning viewer stayed valid")
	}
}
