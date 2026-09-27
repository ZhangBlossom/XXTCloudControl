package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Bounds individual socket writes, not the partner allocation wait.
const orderConnectTimeout = 30 * time.Second

//go:embed order_assets/viewer.html
var orderViewerHTML string

func orderPublic(r orderRecord) gin.H {
	return gin.H{"order_id": r.businessID(), "session_reference": r.ID, "attempt": r.Attempt, "session_id": r.SessionID, "state": r.State, "request": r.Request, "remaining": r.Remaining, "created_at": r.CreatedAt, "expires_at": r.ExpiresAt, "reason": r.Reason}
}
func orderHTTPError(c *gin.Context, code string) {
	status, message := http.StatusInternalServerError, "平台服务异常，请稍后使用原订单重试"
	switch code {
	case "invalid_order":
		status, message = 400, "订单参数不合法，请检查必填字段、金额、币种、数量和请求格式"
	case "invalid_signal":
		status, message = 400, "远控连接参数不合法"
	case "unauthorized":
		status, message = 401, "本机测试接口仅允许本机访问"
	case "viewer_expired":
		status, message = 401, "远控入口已失效或使用时间已到"
	case "not_found":
		status, message = 404, "订单不存在"
	case "order_conflict":
		status, message = 409, "订单号已存在，但订单参数与原订单不一致"
	case "request_in_progress":
		status, message = 409, "该订单有请求正在处理，请勿并发提交"
	case "previous_session_unavailable":
		status, message = 503, "原会话暂时无法结束，本次未分配新手机，请联系平台处理"
	case "no_capacity":
		status, message = 409, "暂无符合订单要求的可用手机，请稍后重试"
	case "cleanup_not_confirmable":
		status, message = 409, "设备不处于待清理状态，不能确认回收"
	case "not_active":
		status, message = 409, "订单会话已结束、到期或正在回收，无法连接"
	case "device_unavailable":
		status, message = 503, "手机准备或连接交付失败，请联系平台排查"
	case "storage_unavailable":
		status, message = 503, "平台订单存储或配置暂不可用，请稍后使用原订单重试"
	case "prepare_timeout":
		status, message = 504, "手机准备超时，请使用相同订单信息重试"
	default:
		code = "internal_error"
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"code": status, "message": message, "error_code": code})
}
func orderDecode(c *gin.Context, out interface{}) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		orderHTTPError(c, "invalid_order")
		return false
	}
	var extra interface{}
	if d.Decode(&extra) != io.EOF {
		orderHTTPError(c, "invalid_order")
		return false
	}
	return true
}

// Disabled by default. No profile is auto-enabled from a mock or an online heartbeat.
func initOrderHTTP(r *gin.Engine) (func(), error) {
	localTest := os.Getenv("XXTCC_LOCAL_ORDER_TEST") == "1"
	if os.Getenv("XXTCC_PUBLIC_URL") == "" {
		return func() {}, nil
	}
	// Internal viewer tokens remain unpredictable without a partner API secret.
	key := randomOrderToken()
	publicURL, err := url.Parse(os.Getenv("XXTCC_PUBLIC_URL"))
	if err != nil || publicURL.Host == "" || (publicURL.Scheme != "https" && publicURL.Scheme != "http") || publicURL.User != nil || publicURL.RawQuery != "" || publicURL.Fragment != "" || (publicURL.Path != "" && publicURL.Path != "/") {
		return nil, fmtOrderConfigError("XXTCC_PUBLIC_URL must be an absolute public HTTP(S) origin")
	}
	data, err := os.ReadFile(filepath.Join(serverConfig.DataDir, "order-profiles.json"))
	if err != nil {
		return nil, err
	}
	var profiles []orderProfile
	if err = json.Unmarshal(data, &profiles); err != nil {
		return nil, err
	}
	if err = validateOrderProfiles(profiles); err != nil {
		return nil, err
	}
	for _, p := range profiles {
		if p.ManualTest && !localTest {
			return nil, fmtOrderConfigError("manual_test requires XXTCC_LOCAL_ORDER_TEST=1")
		}
	}
	if localTest && (publicURL.Hostname() != "127.0.0.1" || len(profiles) != 1 || !(profiles[0].ManualTest || profiles[0].Verified)) {
		return nil, fmtOrderConfigError("local test requires loopback public URL and exactly one manual_test or verified profile")
	}
	s, err := newOrderService(filepath.Join(serverConfig.DataDir, "order-sessions.json"), profiles, cloudOrderDevice{})
	if err != nil {
		return nil, err
	}
	s.publicURL = strings.TrimRight(publicURL.String(), "/")
	s.localTest = localTest
	registerOrderHTTP(r, s, key)
	return func() { s.cancel(); s.wg.Wait() }, nil
}

type orderConfigError string

func (e orderConfigError) Error() string { return string(e) }
func fmtOrderConfigError(s string) error { return orderConfigError(s) }

func registerOrderHTTP(router *gin.Engine, s *orderService, key string) {
	if key == "" {
		key = randomOrderToken()
	}
	partner := router.Group("/orders/v1")
	partner.Use(func(c *gin.Context) {
		host, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		if s.localTest && !net.ParseIP(host).IsLoopback() {
			c.Abort()
			orderHTTPError(c, "unauthorized")
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	partner.GET("/availability", func(c *gin.Context) {
		availability, err := s.availability(c.Query("bundle_id"), c.Query("currency"))
		if err != nil {
			orderHTTPError(c, err.Error())
			return
		}
		c.JSON(200, gin.H{"code": 200, "message": "成功", "available_count": availability.AvailableCount, "next_available_at": availability.NextAvailableAt, "estimated_wait_seconds": availability.EstimatedWaitSeconds})
	})
	// The partner makes one call. Device preparation and readiness polling stay here.
	partner.POST("/connect", func(c *gin.Context) {
		// Allocation may wait longer than the server's normal response timeout.
		// Gin exposes Unwrap, so ResponseController reaches the net/http writer.
		if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			orderHTTPError(c, "internal_error")
			return
		}
		var req struct {
			OrderID string `json:"order_id"`
			Retry   bool   `json:"retry"`
			orderRequest
		}
		if !orderDecode(c, &req) {
			return
		}
		if s.publicURL == "" {
			orderHTTPError(c, "storage_unavailable")
			return
		}
		ctx := c.Request.Context()
		r, err := s.connectRequest(ctx, req.OrderID, req.orderRequest, req.Retry)
		if err != nil {
			orderHTTPError(c, err.Error())
			return
		}
		r, err = s.waitReady(ctx, r.ID)
		if err != nil {
			orderHTTPError(c, err.Error())
			return
		}
		// Derive a recoverable token: identical retries must not disconnect the user.
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte("order-viewer-v1:" + r.SessionID))
		token, err := s.grantToken(ctx, r.ID, hex.EncodeToString(mac.Sum(nil)))
		if err != nil {
			orderHTTPError(c, err.Error())
			return
		}
		if ctx.Err() != nil {
			orderHTTPError(c, "prepare_timeout")
			return
		}
		fragment := url.Values{"order": []string{r.ID}, "token": []string{token}}.Encode()
		c.JSON(200, gin.H{"code": 200, "message": "成功", "order_id": req.OrderID, "viewer_url": s.publicURL + "/order-viewer#" + fragment, "expires_at": r.ExpiresAt})
	})
	// Existing XXT administrator authentication applies to /api/*.
	router.GET("/api/order-sessions", func(c *gin.Context) {
		list := s.list()
		out := make([]gin.H, 0, len(list))
		for _, r := range list {
			v := orderPublic(r)
			v["device_id"] = r.Profile.DeviceID
			out = append(out, v)
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(200, out)
	})
	router.POST("/api/order-sessions/:id/release", func(c *gin.Context) {
		if err := s.stop(c.Param("id")); err != nil {
			orderHTTPError(c, err.Error())
			return
		}
		c.Status(202)
	})
	router.POST("/api/order-sessions/:id/confirm-clean", func(c *gin.Context) {
		if err := s.confirmClean(c.Param("id")); err != nil {
			orderHTTPError(c, err.Error())
			return
		}
		c.JSON(200, gin.H{"code": 200, "message": "成功"})
	})
	router.GET("/order-viewer", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; media-src blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		c.Data(200, "text/html; charset=utf-8", []byte(orderViewerHTML))
	})
	viewer := router.Group("/order-viewer/v1/:id")
	viewer.Use(func(c *gin.Context) {
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if _, err := s.authorizeViewer(c.Param("id"), token); err != nil {
			c.Abort()
			orderHTTPError(c, "viewer_expired")
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	viewer.GET("/status", func(c *gin.Context) {
		r, _ := s.get(c.Param("id"))
		s.viewerSeen(r)
		c.JSON(200, orderPublic(r))
	})
	viewer.POST("/failure", func(c *gin.Context) {
		var body struct {
			Reason string `json:"reason"`
		}
		if !orderDecode(c, &body) {
			return
		}
		switch body.Reason {
		case "connection_failed", "signal_failed", "remote_disconnected", "browser_offline":
		default:
			orderHTTPError(c, "invalid_order")
			return
		}
		r, _ := s.get(c.Param("id"))
		if err := s.viewerFailure(r, body.Reason); err != nil {
			orderHTTPError(c, "storage_unavailable")
			return
		}
		c.Status(204)
	})
	viewer.POST("/release", func(c *gin.Context) {
		if err := s.stop(c.Param("id")); err != nil {
			orderHTTPError(c, err.Error())
			return
		}
		c.Status(202)
	})
	viewer.POST("/signal/:action", func(c *gin.Context) {
		action := c.Param("action")
		if action != "start" && action != "answer" && action != "ice" && action != "poll" {
			orderHTTPError(c, "invalid_signal")
			return
		}
		var body json.RawMessage
		if !orderDecode(c, &body) {
			return
		}
		// Drain in-flight signaling before releasing a device to its next order.
		r, err := s.authorizeViewer(c.Param("id"), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if err != nil {
			orderHTTPError(c, "viewer_expired")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), min(28*time.Second, r.ExpiresAt.Sub(s.now())))
		defer cancel()
		l := s.viewerLock(c.Param("id"))
		if err := orderLockContext(ctx, l); err != nil {
			orderHTTPError(c, "viewer_expired")
			return
		}
		defer l.Unlock()
		r, err = s.authorizeViewer(c.Param("id"), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if err != nil {
			orderHTTPError(c, "viewer_expired")
			return
		}
		result, err := s.io.Signal(ctx, r, action, body)
		if err != nil {
			if _, authErr := s.authorizeViewer(r.ID, strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")); authErr != nil {
				orderHTTPError(c, "viewer_expired")
			} else {
				if logErr := s.viewerFailure(r, "device_signal_failed"); logErr != nil {
					orderHTTPError(c, "storage_unavailable")
					return
				}
				orderHTTPError(c, "device_unavailable")
			}
			return
		}
		// If expiry happened while signaling, tear down rather than return a late offer.
		if _, err = s.authorizeViewer(r.ID, strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")); err != nil {
			if action == "start" {
				stopCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
				_ = s.io.StopViewer(stopCtx, r)
				done()
			}
			orderHTTPError(c, "viewer_expired")
			return
		}
		c.Data(200, "application/json", result)
	})
}
