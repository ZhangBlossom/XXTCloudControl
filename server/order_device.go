package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed order_assets/session.lua
var orderLua string

//go:embed order_assets/honor_cleanup.lua
var honorCleanupLua string

type orderReplyWaiter struct {
	conn *SafeConn
	ch   chan Message
}

var orderReplies = struct {
	sync.Mutex
	entries map[string]orderReplyWaiter
}{entries: map[string]orderReplyWaiter{}}

// Replies are bound to the exact device socket, not a user-supplied device ID.
func deliverOrderReply(conn *SafeConn, msg Message) bool {
	id := msg.RequestID
	if body, ok := msg.Body.(map[string]interface{}); ok {
		if v, ok := body["requestId"].(string); ok {
			id = v
		}
	}
	orderReplies.Lock()
	defer orderReplies.Unlock()
	w, ok := orderReplies.entries[id]
	if !ok || w.conn != conn {
		return false
	}
	select {
	case w.ch <- msg:
	default:
	}
	return true
}

type cloudOrderDevice struct{}

func (cloudOrderDevice) Online(id string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return deviceLinks[id] != nil
}
func (cloudOrderDevice) command(ctx context.Context, deviceID, kind string, body map[string]interface{}) (Message, error) {
	mu.RLock()
	conn := deviceLinks[deviceID]
	mu.RUnlock()
	if conn == nil {
		return Message{}, errors.New("device_offline")
	}
	id := randomOrderToken()
	ch := make(chan Message, 1)
	orderReplies.Lock()
	orderReplies.entries[id] = orderReplyWaiter{conn, ch}
	orderReplies.Unlock()
	defer func() { orderReplies.Lock(); delete(orderReplies.entries, id); orderReplies.Unlock() }()
	if kind == "http/request" {
		body["requestId"] = id
	}
	if err := sendOrderMessage(ctx, conn, Message{Type: kind, RequestID: id, Body: body}); err != nil {
		return Message{}, err
	}
	select {
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case msg := <-ch:
		if msg.Error != "" {
			return msg, fmt.Errorf("device command %s: %s", kind, msg.Error)
		}
		return msg, nil
	}
}

// Bound both writer contention and the socket write by the operation deadline.
func sendOrderMessage(ctx context.Context, conn *SafeConn, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := orderLockContext(ctx, &conn.mu); err != nil {
		return err
	}
	defer conn.mu.Unlock()
	if conn.writeMessageHook != nil {
		return conn.writeMessageHook(websocket.TextMessage, data)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(orderConnectTimeout)
	}
	if err := conn.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	defer conn.conn.SetWriteDeadline(time.Time{})
	return conn.conn.WriteMessage(websocket.TextMessage, data)
}

func (d cloudOrderDevice) http(ctx context.Context, r orderRecord, method, p string, body json.RawMessage, query map[string]interface{}) (json.RawMessage, error) {
	if query == nil {
		query = map[string]interface{}{}
	}
	msg, err := d.command(ctx, r.Profile.DeviceID, "http/request", map[string]interface{}{"method": method, "path": p, "query": query, "headers": map[string]string{"Content-Type": "application/json"}, "body": base64.StdEncoding.EncodeToString(body), "timeoutMs": 25000})
	if err != nil {
		return nil, err
	}
	b, ok := msg.Body.(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid_http_reply")
	}
	status, _ := toInt(b["statusCode"])
	if status < 200 || status >= 300 || (b["error"] != nil && b["error"] != "") {
		return nil, errors.New("device_http_failed")
	}
	text, _ := b["body"].(string)
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, errors.New("invalid_device_json")
	}
	return raw, nil
}

func (d cloudOrderDevice) Prepare(ctx context.Context, r orderRecord) error {
	// Query the native API, because the stock script.running field maps query failures to false.
	body, err := d.http(ctx, r, "POST", "/is_running", nil, nil)
	if err != nil {
		return err
	}
	var status struct {
		Code *int `json:"code"`
	}
	if json.Unmarshal(body, &status) != nil || status.Code == nil || *status.Code != 0 {
		return errors.New("script_busy_or_unknown")
	}
	if !r.Profile.ManualTest && r.Request.BundleID == "com.levelinfinite.sgameGlobal" &&
		r.Profile.CleanupScript == path.Join(r.Profile.ScriptsDir, "honor-cleanup.lua") {
		_, err = d.command(ctx, r.Profile.DeviceID, "script/put", map[string]interface{}{
			"name": "honor-cleanup.lua", "data": base64.StdEncoding.EncodeToString([]byte(honorCleanupLua)),
		})
		if err != nil {
			return err
		}
	}
	price := fmt.Sprintf("%d.%02d", r.Request.PriceMinor/100, r.Request.PriceMinor%100)
	price = strings.TrimRight(strings.TrimRight(price, "0"), ".")
	base := "xxt-order-" + r.SessionID
	cfg := map[string]interface{}{"session_id": r.SessionID, "bundle_id": r.Request.BundleID, "price": price, "quantity": r.Request.Quantity, "policy_path": r.Profile.PolicyPath, "report_path": path.Join(r.Profile.ScriptsDir, base+".json"), "stop_path": path.Join(r.Profile.ScriptsDir, base+".stop"), "cleanup_script": r.Profile.CleanupScript, "duration_seconds": int(orderUseTime / time.Second)}
	cfg["manual_test"] = r.Profile.ManualTest
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	script := "local json = require('cjson.safe')\nlocal config = assert(json.decode(('" + base64.StdEncoding.EncodeToString(encoded) + "'):base64_decode()))\n" + orderLua
	_, err = d.command(ctx, r.Profile.DeviceID, "script/put", map[string]interface{}{"name": base + ".lua", "data": base64.StdEncoding.EncodeToString([]byte(script))})
	if err != nil {
		return err
	}
	_, err = d.command(ctx, r.Profile.DeviceID, "script/run", map[string]interface{}{"name": base + ".lua"})
	return err
}

func (d cloudOrderDevice) Report(ctx context.Context, r orderRecord) (orderDeviceReport, error) {
	msg, err := d.command(ctx, r.Profile.DeviceID, "script/get", map[string]interface{}{"name": "xxt-order-" + r.SessionID + ".json"})
	if err != nil {
		return orderDeviceReport{}, err
	}
	text, ok := msg.Body.(string)
	if !ok {
		return orderDeviceReport{}, errors.New("invalid_report")
	}
	b, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return orderDeviceReport{}, err
	}
	var report orderDeviceReport
	err = json.Unmarshal(b, &report)
	if err == nil && report.Phase == "closed" {
		// XXTouch can keep the interpreter busy briefly after the final report.
		// Do not offer this device to another order until the native runner is idle.
		body, idleErr := d.http(ctx, r, "POST", "/is_running", nil, nil)
		if idleErr != nil {
			return orderDeviceReport{}, idleErr
		}
		var status struct {
			Code *int `json:"code"`
		}
		if json.Unmarshal(body, &status) != nil || status.Code == nil {
			return orderDeviceReport{}, errors.New("script_idle_unconfirmed")
		}
		switch *status.Code {
		case 0:
		case 3:
			report.Phase = "cleaning"
		default:
			return orderDeviceReport{}, errors.New("script_idle_unconfirmed")
		}
	}
	return report, err
}
func (d cloudOrderDevice) RequestStop(ctx context.Context, r orderRecord) error {
	_, err := d.command(ctx, r.Profile.DeviceID, "script/put", map[string]interface{}{"name": "xxt-order-" + r.SessionID + ".stop", "data": base64.StdEncoding.EncodeToString([]byte(r.SessionID))})
	return err
}
func (d cloudOrderDevice) StopViewer(ctx context.Context, r orderRecord) error {
	_, err := d.http(ctx, r, "POST", "/api/webrtc/stop", nil, nil)
	return err
}
func (d cloudOrderDevice) Signal(ctx context.Context, r orderRecord, action string, body json.RawMessage) (json.RawMessage, error) {
	method := "POST"
	query := map[string]interface{}{}
	switch action {
	case "start":
		body, _ = json.Marshal(map[string]interface{}{"iceServers": GetTURNICEServers()})
	case "answer", "ice":
		if len(body) == 0 || !json.Valid(body) {
			return nil, errors.New("invalid_signal")
		}
	case "poll":
		method = "GET"
		query["timeout"] = 1
	default:
		return nil, errors.New("invalid_signal")
	}
	return d.http(ctx, r, method, "/api/webrtc/"+action, body, query)
}
