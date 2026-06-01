package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/astra-go/astra"
	"github.com/astra-go/game-backend/internal/services"
	"github.com/astra-go/game-backend/pkg/payment"
	"go.uber.org/zap"
)

// PaymentAPI 支付API
type PaymentAPI struct {
	shopService   *services.ShopService
	paymentConfig *payment.Config
	logger        *zap.Logger
}

// NewPaymentAPI 创建支付API
func NewPaymentAPI(shopService *services.ShopService, cfg *payment.Config, logger *zap.Logger) *PaymentAPI {
	return &PaymentAPI{
		shopService:   shopService,
		paymentConfig: cfg,
		logger:        logger,
	}
}

// RegisterRoutes 注册路由
func (api *PaymentAPI) RegisterRoutes(app *astra.App, authMiddleware astra.HandlerFunc) {
	// 支付
	app.POST("/api/v1/payment/create", authMiddleware, api.CreatePayment)
	app.GET("/api/v1/payment/query", authMiddleware, api.QueryPayment)
	app.GET("/api/v1/payment/channels", authMiddleware, api.ListChannels)

	// 支付宝回调（无需鉴权）
	app.POST("/api/v1/payment/alipay/notify", api.AlipayNotify)
	app.GET("/api/v1/payment/alipay/return", api.AlipayReturn)

	// 微信支付回调（无需鉴权）
	app.POST("/api/v1/payment/wechat/notify", api.WechatNotify)

	// 退款（管理员权限）
	app.POST("/api/v1/payment/refund", authMiddleware, api.Refund)
}

// ========== 支付 ==========

type CreatePaymentReq struct {
	OrderID  string `json:"order_id"`  // 商城订单ID
	Channel  string `json:"channel"`  // 支付通道 alipay/wechat
	ClientIP string `json:"client_ip"` // 客户端IP
}

// CreatePayment 创建支付订单
func (api *PaymentAPI) CreatePayment(c *astra.Ctx) error {
	var req CreatePaymentReq
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	if req.OrderID == "" {
		return ResponseError(c, http.StatusBadRequest, "order_id 不能为空")
	}

	clientIP := req.ClientIP
	if clientIP == "" {
		clientIP = c.Request().RemoteAddr
	}

	channel := api.validateChannel(req.Channel)
	if channel == "" {
		return ResponseError(c, http.StatusBadRequest, "不支持的支付通道")
	}

	ctx := c.Request().Context()
	resp, err := api.shopService.CreatePayment(ctx, req.OrderID, channel, clientIP)
	if err != nil {
		api.logger.Error("创建支付失败", zap.String("order_id", req.OrderID), zap.Error(err))
		return ResponseError(c, http.StatusInternalServerError, "创建支付失败: "+err.Error())
	}

	result := map[string]any{
		"order_no":         resp.OrderNo,
		"channel_order_no": resp.ChannelOrderNo,
		"expires_at":       resp.ExpiresAt,
	}

	switch channel {
	case payment.ChannelAlipay:
		result["qr_code"] = resp.QRCodeData
		result["pay_url"] = resp.PayURL
	case payment.ChannelWechat:
		result["qr_code"] = resp.QRCodeData
		result["pay_url"] = resp.PayURL
	}

	return ResponseOK(c, result)
}

type QueryPaymentReq struct {
	OrderID string `json:"order_id" query:"order_id"`
	OrderNo string `json:"order_no" query:"order_no"`
}

// QueryPayment 查询支付状态
func (api *PaymentAPI) QueryPayment(c *astra.Ctx) error {
	var req QueryPaymentReq
	c.BindQuery(&req)

	if req.OrderID == "" && req.OrderNo == "" {
		return ResponseError(c, http.StatusBadRequest, "order_id 或 order_no 必须提供一个")
	}

	ctx := c.Request().Context()
	order, err := api.shopService.GetOrder(ctx, req.OrderID)
	if err == nil && string(order.Status) == "paid" {
		return ResponseOK(c, map[string]any{
			"status":   "paid",
			"order_id": req.OrderID,
			"paid_at":  order.PayTime,
		})
	}

	result := map[string]any{"status": ""}
	if order != nil {
		result["status"] = string(order.Status)
		result["payment_order_no"] = order.PaymentOrderNo
	}

	return ResponseOK(c, result)
}

// ListChannels 获取可用支付通道
func (api *PaymentAPI) ListChannels(c *astra.Ctx) error {
	channels := api.shopService.ListEnabledPaymentChannels()

	result := make([]map[string]string, 0, len(channels))
	for _, ch := range channels {
		item := map[string]string{"channel": string(ch)}
		switch ch {
		case payment.ChannelAlipay:
			item["name"] = "支付宝"
			item["icon"] = "/static/icons/alipay.png"
		case payment.ChannelWechat:
			item["name"] = "微信支付"
			item["icon"] = "/static/icons/wechat.png"
		case payment.ChannelAppleIAP:
			item["name"] = "Apple Pay"
			item["icon"] = "/static/icons/apple.png"
		case payment.ChannelGoogleIAP:
			item["name"] = "Google Pay"
			item["icon"] = "/static/icons/google.png"
		}
		result = append(result, item)
	}

	return ResponseOK(c, result)
}

// ========== 支付宝回调 ==========

// AlipayNotify 支付宝异步通知
func (api *PaymentAPI) AlipayNotify(c *astra.Ctx) error {
	params := make(map[string]string)
	for _, key := range []string{
		"out_trade_no", "trade_no", "trade_status", "total_amount",
		"buyer_logon_id", "notify_time", "notify_type", "sign",
	} {
		params[key] = c.PostForm(key)
	}

	body, _ := io.ReadAll(c.Request().Body)
	ctx := c.Request().Context()

	result := api.shopService.HandlePaymentNotify(ctx, payment.ChannelAlipay, params, body)

	c.SetHeader("Content-Type", "text/plain")
	return c.String(http.StatusOK, "%s", result.Reply)
}

// AlipayReturn 支付宝同步返回
func (api *PaymentAPI) AlipayReturn(c *astra.Ctx) error {
	outTradeNo := c.Query("out_trade_no")
	tradeStatus := c.Query("trade_status")

	redirectURL := "/payment/success?order_no=" + outTradeNo
	if tradeStatus != "TRADE_SUCCESS" && tradeStatus != "TRADE_FINISHED" {
		redirectURL = "/payment/failed?order_no=" + outTradeNo
	}

	return c.Redirect(http.StatusFound, redirectURL)
}

// ========== 微信支付回调 ==========

// WechatNotify 微信支付异步通知
func (api *PaymentAPI) WechatNotify(c *astra.Ctx) error {
	body, _ := io.ReadAll(c.Request().Body)
	params := api.parseWechatNotify(string(body))
	ctx := c.Request().Context()

	result := api.shopService.HandlePaymentNotify(ctx, payment.ChannelWechat, params, body)

	c.SetHeader("Content-Type", "application/xml")
	return c.String(http.StatusOK, "%s", result.Reply)
}

func (api *PaymentAPI) parseWechatNotify(body string) map[string]string {
	params := make(map[string]string)
	parts := strings.Split(body, "<![CDATA[")
	for i := 1; i < len(parts); i++ {
		s := parts[i]
		end := strings.Index(s, "]]>")
		if end > 0 {
			value := s[:end]
			rest := s[end+3:]
			nextStart := strings.Index(rest, "<![CDATA[")
			if nextStart > 0 {
				key := rest[:nextStart]
				key = strings.TrimPrefix(key, "<")
				key = strings.TrimSuffix(key, ">")
				params[key] = value
			}
		}
	}
	return params
}

// ========== 退款 ==========

type RefundReq struct {
	OrderID    string `json:"order_id"`
	Reason     string `json:"reason"`
	OperatorID string `json:"operator_id"`
}

// Refund 退款
func (api *PaymentAPI) Refund(c *astra.Ctx) error {
	var req RefundReq
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	if req.OrderID == "" {
		return ResponseError(c, http.StatusBadRequest, "order_id 不能为空")
	}

	if req.Reason == "" {
		req.Reason = "用户申请退款"
	}

	ctx := c.Request().Context()
	resp, err := api.shopService.RefundPayment(ctx, req.OrderID, req.Reason, req.OperatorID)
	if err != nil {
		api.logger.Error("退款失败", zap.String("order_id", req.OrderID), zap.Error(err))
		return ResponseError(c, http.StatusInternalServerError, "退款失败: "+err.Error())
	}

	return ResponseOK(c, map[string]any{
		"refund_id":         resp.RefundID,
		"channel_refund_no": resp.ChannelRefundNo,
		"amount":           resp.Amount,
		"status":           resp.Status,
	})
}

// ========== 辅助方法 ==========

func (api *PaymentAPI) validateChannel(channel string) payment.Channel {
	switch strings.ToLower(channel) {
	case "alipay", "zfb", "支付宝":
		return payment.ChannelAlipay
	case "wechat", "wx", "微信":
		return payment.ChannelWechat
	case "apple", "appleiap", "apple_iap":
		return payment.ChannelAppleIAP
	case "google", "googleiap", "google_iap":
		return payment.ChannelGoogleIAP
	default:
		return ""
	}
}

// InitPaymentProviders 初始化支付通道
func InitPaymentProviders(cfg *payment.Config, svc *payment.Service) {
	if cfg.Alipay.Enabled {
		alipayProvider := payment.NewAlipayProvider(*cfg)
		svc.RegisterProvider(alipayProvider)
	}

	if cfg.WechatPay.Enabled {
		wechatProvider := payment.NewWechatPayProvider(*cfg)
		svc.RegisterProvider(wechatProvider)
	}
}

// ========== 支付通知处理集成 ==========

// SetupPaymentCallbacks 设置支付回调处理
func SetupPaymentCallbacks(shopSvc *services.ShopService, paymentSvc *payment.Service) {
	_ = shopSvc
	_ = paymentSvc
}
