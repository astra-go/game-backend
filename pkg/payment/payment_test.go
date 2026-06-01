package payment

import (
	"testing"
)

// TestChargeRequest 充值请求测试
func TestChargeRequest(t *testing.T) {
	req := &ChargeRequest{
		OrderID:   "ord_123",
		OrderNo:   "PAY456",
		Amount:    600, // 6元,单位分
		Currency:  "CNY",
		Subject:   "测试钻石包",
		Body:      "600钻石",
		Channel:   "alipay",
		PlayerID:  "player_001",
		ProductID: "diamond_600",
		NotifyURL: "https://example.com/payment/notify",
		ClientIP:  "192.168.1.1",
	}

	if req.Amount != 600 {
		t.Errorf("金额应为600,实际%d", req.Amount)
	}

	t.Logf("充值请求测试通过: %+v", req)
}

// TestOrderStatus 订单状态测试
func TestOrderStatus(t *testing.T) {
	statuses := []Status{
		StatusPending,
		StatusSuccess,
		StatusFailed,
		StatusCancelled,
		StatusRefunded,
	}

	expected := []string{"pending", "success", "failed", "cancelled", "refunded"}

	for i, status := range statuses {
		if string(status) != expected[i] {
			t.Errorf("期望状态%s,实际%s", expected[i], status)
		}
	}

	t.Logf("订单状态测试通过")
}

// TestPaymentConfig 支付配置测试
func TestPaymentConfig(t *testing.T) {
	cfg := Config{}
	cfg.Alipay.Enabled = true
	cfg.Alipay.AppID = "2021001234567890"
	cfg.Alipay.PrivateKey = "test_key"
	cfg.Alipay.AlipayPubKey = "test_pub_key"
	cfg.Alipay.ServerURL = "https://openapi.alipay.com/gateway.do"

	cfg.WechatPay.Enabled = true
	cfg.WechatPay.AppID = "wx1234567890abcdef"
	cfg.WechatPay.MchID = "1234567890"
	cfg.WechatPay.APIKey = "test_api_key_32chars"
	cfg.WechatPay.SandboxEnabled = true

	// 验证支付宝配置
	alipayProvider := NewAlipayProvider(cfg)
	if !alipayProvider.IsAvailable() {
		t.Error("支付宝通道应该可用")
	}

	// 验证微信支付配置
	wechatProvider := NewWechatPayProvider(cfg)
	if !wechatProvider.IsAvailable() {
		t.Error("微信支付通道应该可用")
	}

	t.Logf("支付配置测试通过")
}

// TestBuildWechatXML 微信XML构建测试
func TestBuildWechatXML(t *testing.T) {
	params := map[string]string{
		"appid":    "wx1234567890",
		"mch_id":   "1234567890",
		"nonce_str": "random123",
		"sign":     "ABCD1234",
	}

	xml := buildWechatXML(params)

	// 验证XML包含必要标签
	expectedTags := []string{"<appid>", "</appid>", "<mch_id>", "</mch_id>", "<nonce_str>", "</nonce_str>", "<sign>", "</sign>"}
	for _, tag := range expectedTags {
		if !contains(xml, tag) {
			t.Errorf("XML应包含标签%s,实际XML: %s", tag, xml)
		}
	}

	t.Logf("微信XML构建测试通过: %s", xml)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestChannelEnum 支付通道枚举测试
func TestChannelEnum(t *testing.T) {
	channels := []Channel{
		ChannelAlipay,
		ChannelWechat,
		ChannelAppleIAP,
		ChannelGoogleIAP,
	}

	expected := []string{"alipay", "wechat", "apple_iap", "google_iap"}

	for i, ch := range channels {
		if string(ch) != expected[i] {
			t.Errorf("期望通道%s,实际%s", expected[i], ch)
		}
	}

	t.Logf("支付通道枚举测试通过")
}

// TestNotifyPayload 支付回调载荷测试
func TestNotifyPayload(t *testing.T) {
	payload := &NotifyPayload{
		Type:            NotifyTypePayment,
		Channel:         ChannelWechat,
		OrderNo:         "ORD123456",
		ChannelOrderNo: "WX123456789",
		Amount:         100,
		Currency:       "CNY",
		Status:         StatusSuccess,
	}

	if payload.Type != NotifyTypePayment {
		t.Errorf("期望类型Payment,实际%s", payload.Type)
	}
	if payload.Status != StatusSuccess {
		t.Errorf("期望状态Success,实际%s", payload.Status)
	}

	t.Logf("支付回调载荷测试通过: %+v", payload)
}

// TestRefundRequest 退款请求测试
func TestRefundRequest(t *testing.T) {
	req := &RefundRequest{
		OrderID:        "ord_123",
		ChannelOrderNo: "PAY123456",
		RefundID:       "REF789",
		Amount:         100,
		Reason:         "用户申请退款",
		OperatorID:     "admin_001",
	}

	if req.Amount != 100 {
		t.Errorf("期望退款金额100,实际%d", req.Amount)
	}

	t.Logf("退款请求测试通过: %+v", req)
}

// TestWechatSignParams 微信签名参数测试
func TestWechatSignParams(t *testing.T) {
	cfg := Config{}
	cfg.WechatPay.AppID = "wx1234567890abcdef"
	cfg.WechatPay.MchID = "1234567890"
	cfg.WechatPay.APIKey = "test_api_key_32chars_abcdefg"

	provider := NewWechatPayProvider(cfg)

	params := map[string]string{
		"appid":      "wx1234567890abcdef",
		"mch_id":     "1234567890",
		"nonce_str":  "random_nonce_str",
		"body":       "测试商品",
		"out_trade_no": "ORDER123456",
		"total_fee":  "100",
	}

	sign := provider.signParams(params, cfg.WechatPay.APIKey)
	if sign == "" {
		t.Error("签名不应为空")
	}

	// 验证签名格式(MD5签名应为32字符大写)
	if len(sign) != 32 {
		t.Errorf("MD5签名长度应为32字符,实际%d", len(sign))
	}

	t.Logf("微信签名参数测试通过,签名: %s", sign)
}

// TestAlipayProviderCreation 支付宝通道创建测试
func TestAlipayProviderCreation(t *testing.T) {
	cfg := Config{}
	cfg.Alipay.Enabled = true
	cfg.Alipay.AppID = "2021001234567890"
	cfg.Alipay.PrivateKey = "test_private_key"
	cfg.Alipay.ServerURL = "https://openapi.alipay.com/gateway.do"

	provider := NewAlipayProvider(cfg)

	if provider.Name() != ChannelAlipay {
		t.Errorf("期望通道名为alipay,实际%s", provider.Name())
	}

	if !provider.IsAvailable() {
		t.Error("支付宝通道应该可用")
	}

	t.Logf("支付宝通道创建测试通过")
}

// TestWechatProviderCreation 微信支付通道创建测试
func TestWechatProviderCreation(t *testing.T) {
	cfg := Config{}
	cfg.WechatPay.Enabled = true
	cfg.WechatPay.AppID = "wx1234567890abcdef"
	cfg.WechatPay.MchID = "1234567890"
	cfg.WechatPay.APIKey = "test_api_key_32chars"

	provider := NewWechatPayProvider(cfg)

	if provider.Name() != ChannelWechat {
		t.Errorf("期望通道名为wechat,实际%s", provider.Name())
	}

	if !provider.IsAvailable() {
		t.Error("微信支付通道应该可用")
	}

	t.Logf("微信支付通道创建测试通过")
}

// TestServiceCreation 服务创建测试
func TestServiceCreation(t *testing.T) {
	cfg := &Config{}
	cfg.Alipay.Enabled = true
	cfg.Alipay.AppID = "2021001234567890"
	cfg.Alipay.PrivateKey = "test_key"
	cfg.WechatPay.Enabled = true
	cfg.WechatPay.AppID = "wx1234567890"
	cfg.WechatPay.MchID = "1234567890"
	cfg.WechatPay.APIKey = "test_api_key_32chars"

	svc := NewService(cfg, nil, nil)

	if svc == nil {
		t.Error("服务创建失败")
	}

	// 注册通道
	svc.RegisterProvider(NewAlipayProvider(*cfg))
	svc.RegisterProvider(NewWechatPayProvider(*cfg))

	channels := svc.ListEnabledChannels()
	if len(channels) != 2 {
		t.Errorf("期望2个通道，实际%d", len(channels))
	}

	t.Logf("服务创建测试通过，可用通道: %v", channels)
}

// TestNotifyResult 通知结果测试
func TestNotifyResult(t *testing.T) {
	result := &NotifyResult{
		Handled: true,
		Reply:   "success",
		Message: "处理成功",
	}

	if !result.Handled {
		t.Error("期望已处理")
	}

	if result.Reply != "success" {
		t.Errorf("期望回复success,实际%s", result.Reply)
	}

	t.Logf("通知结果测试通过: %+v", result)
}
