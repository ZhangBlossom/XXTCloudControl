package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestOrderClosedReportWaitsForNativeIdle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		native    string
		status    int
		wantPhase string
		wantError bool
	}{
		{"native idle", `{"code":0}`, 200, "closed", false},
		{"native still running", `{"code":3}`, 200, "cleaning", false},
		{"missing code", `{}`, 200, "", true},
		{"null code", `{"code":null}`, 200, "", true},
		{"unknown code", `{"code":4}`, 200, "", true},
		{"string code", `{"code":"0"}`, 200, "", true},
		{"invalid json", `invalid`, 200, "", true},
		{"native HTTP failure", `{"code":0}`, 500, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := orderRecord{SessionID: "cleanup-session", Profile: orderProfile{DeviceID: "cleanup-test-phone"}}
			httpCalls := 0
			var conn *SafeConn
			conn = &SafeConn{writeMessageHook: func(_ int, data []byte) error {
				var msg Message
				if err := json.Unmarshal(data, &msg); err != nil {
					return err
				}
				switch msg.Type {
				case "script/get":
					raw, _ := json.Marshal(orderDeviceReport{SessionID: r.SessionID, Phase: "closed", Stopped: true, Remaining: 0})
					msg.Body = base64.StdEncoding.EncodeToString(raw)
				case "http/request":
					httpCalls++
					body, _ := msg.Body.(map[string]interface{})
					if body["path"] != "/is_running" {
						t.Fatalf("unexpected native path: %v", body["path"])
					}
					if _, ok := body["query"].(map[string]interface{}); !ok {
						t.Fatal("native query must be a table")
					}
					msg.Type = "http/response"
					msg.Body = map[string]interface{}{"requestId": msg.RequestID, "statusCode": tc.status, "body": base64.StdEncoding.EncodeToString([]byte(tc.native))}
					msg.RequestID = ""
				default:
					t.Fatalf("unexpected command: %s", msg.Type)
				}
				if !deliverOrderReply(conn, msg) {
					t.Fatal("reply not correlated")
				}
				return nil
			}}
			mu.Lock()
			deviceLinks[r.Profile.DeviceID] = conn
			mu.Unlock()
			defer func() { mu.Lock(); delete(deviceLinks, r.Profile.DeviceID); mu.Unlock() }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			report, err := (cloudOrderDevice{}).Report(ctx, r)
			if httpCalls != 1 {
				t.Errorf("closed report returned before checking native idle: HTTP calls=%d", httpCalls)
			}
			if (err != nil) != tc.wantError {
				t.Fatalf("report=%+v error=%v wantError=%v", report, err, tc.wantError)
			}
			if err == nil && report.Phase != tc.wantPhase {
				t.Fatalf("phase=%s want=%s", report.Phase, tc.wantPhase)
			}
		})
	}
}
