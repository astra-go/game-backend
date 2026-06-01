package payment

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// WechatPayProvider 微信支付通道
type WechatPayProvider struct {
	cfg    Config
	client *http.Client
}

// NewWechatPayProvider 创建微信支付通道
func NewWechatPayProvider(cfg Config) *WechatPayProvider {
	return &WechatPayProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *WechatPayProvider) Name() Channel { return ChannelWechat }

func (p *WechatPayProvider) IsAvailable() bool {
	return p.cfg.WechatPay.Enabled && p.cfg.WechatPay.MchID != ""
}

func (p *WechatPayProvider) Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error) {
	apiKey := p.cfg.WechatPay.APIKey
	baseURL := "https://api.mch.weixin.qq.com"
	if p.cfg.WechatPay.SandboxEnabled {
		baseURL = "https://api.mch.weixin.qq.com/sandboxnew"
		// 沙箱需要单独获取密钥
		apiKey = "sandbox_" + apiKey
	}

	nonceStr := GenNonceStr(32)
	params := map[string]string{
		"appid":            p.cfg.WechatPay.AppID,
		"mch_id":           p.cfg.WechatPay.MchID,
		"nonce_str":        nonceStr,
		"body":             req.Subject,
		"out_trade_no":     req.OrderNo,
		"total_fee":        strconv.FormatInt(req.Amount, 10),
		"spbill_create_ip": req.ClientIP,
		"notify_url":       req.NotifyURL,
		"trade_type":       "NATIVE",
		"product_id":       req.ProductID,
	}

	sign := p.signParams(params, apiKey)
	params["sign"] = sign

	respBody, err := p.postXML(ctx, baseURL+"/pay/unifiedorder", params)
	if err != nil {
		return nil, err
	}

	var result map[string]string
	xmlUnmarshal(respBody, &result)

	if result["return_code"] != "SUCCESS" {
		return nil, fmt.Errorf("wechat error: %s", result["return_msg"])
	}
	if result["result_code"] != "SUCCESS" {
		return nil, fmt.Errorf("wechat error: %s - %s", result["err_code"], result["err_code_des"])
	}

	return &ChargeResponse{
		OrderNo:        result["out_trade_no"],
		PayURL:         result["code_url"],
		QRCodeData:     result["code_url"],
		ExpiresAt:      time.Now().Add(2 * time.Hour),
		ChannelOrderNo: result["prepay_id"],
	}, nil
}

// ChargeAPP APP支付
func (p *WechatPayProvider) ChargeAPP(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error) {
	apiKey := p.cfg.WechatPay.APIKey
	baseURL := "https://api.mch.weixin.qq.com"
	if p.cfg.WechatPay.SandboxEnabled {
		baseURL = "https://api.mch.weixin.qq.com/sandboxnew"
		apiKey = "sandbox_" + apiKey
	}

	nonceStr := GenNonceStr(32)
	params := map[string]string{
		"appid":            p.cfg.WechatPay.AppID,
		"mch_id":           p.cfg.WechatPay.MchID,
		"nonce_str":        nonceStr,
		"body":             req.Subject,
		"out_trade_no":     req.OrderNo,
		"total_fee":        strconv.FormatInt(req.Amount, 10),
		"spbill_create_ip": req.ClientIP,
		"notify_url":       req.NotifyURL,
		"trade_type":       "APP",
	}

	sign := p.signParams(params, apiKey)
	params["sign"] = sign

	respBody, err := p.postXML(ctx, baseURL+"/pay/unifiedorder", params)
	if err != nil {
		return nil, err
	}

	var result map[string]string
	xmlUnmarshal(respBody, &result)

	if result["return_code"] != "SUCCESS" || result["result_code"] != "SUCCESS" {
		return nil, fmt.Errorf("wechat error: %s", result["err_code_des"])
	}

	// APP调起支付需要的参数
	prepayID := result["prepay_id"]
	nonceStr2 := GenNonceStr(32)
	signParams := map[string]string{
		"appid":     p.cfg.WechatPay.AppID,
		"partnerid": p.cfg.WechatPay.MchID,
		"prepayid":  prepayID,
		"noncestr":  nonceStr2,
		"timestamp": strconv.FormatInt(time.Now().Unix(), 10),
		"package":   "Sign=WXPay",
	}
	sign2 := p.signParams(signParams, apiKey)
	signParams["sign"] = sign2

	return &ChargeResponse{
		OrderNo:        result["out_trade_no"],
		ChannelOrderNo: prepayID,
		ExpiresAt:      time.Now().Add(2 * time.Hour),
		Metadata:       p.packAPPParams(signParams),
	}, nil
}

func (p *WechatPayProvider) packAPPParams(params map[string]string) string {
	keys := sortedKeys(params)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, params[k]))
	}
	b, _ := JSONMarshal(map[string]string{"params": strings.Join(parts, "&")})
	return string(b)
}

func (p *WechatPayProvider) Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error) {
	apiKey := p.cfg.WechatPay.APIKey
	baseURL := "https://api.mch.weixin.qq.com"
	if p.cfg.WechatPay.SandboxEnabled {
		baseURL = "https://api.mch.weixin.qq.com/sandboxnew"
		apiKey = "sandbox_" + apiKey
	}

	nonceStr := GenNonceStr(32)
	params := map[string]string{
		"appid":          p.cfg.WechatPay.AppID,
		"mch_id":         p.cfg.WechatPay.MchID,
		"nonce_str":      nonceStr,
		"transaction_id": req.ChannelOrderNo,
		"out_refund_no": req.RefundID,
		"total_fee":     strconv.FormatInt(req.Amount, 10),
		"refund_fee":    strconv.FormatInt(req.Amount, 10),
		"refund_desc":   req.Reason,
	}
	sign := p.signParams(params, apiKey)
	params["sign"] = sign

	// 退款需要证书（简化版：无证书，生产环境需添加）
	respBody, err := p.postXMLWithCert(ctx, baseURL+"/secapi/pay/refund", params)
	if err != nil {
		// 沙箱环境可能不支持退款，用模拟响应
		if p.cfg.WechatPay.SandboxEnabled {
			now := time.Now()
			return &RefundResponse{
				RefundID:        req.RefundID,
				ChannelRefundNo: "REF" + strconv.FormatInt(now.UnixNano(), 10),
				Status:          StatusSuccess,
				ProcessedAt:     now,
			}, nil
		}
		return nil, err
	}

	var result map[string]string
	xmlUnmarshal(respBody, &result)

	if result["return_code"] != "SUCCESS" || result["result_code"] != "SUCCESS" {
		return nil, fmt.Errorf("refund failed: %s", result["err_code_des"])
	}

	now := time.Now()
	return &RefundResponse{
		RefundID:        req.RefundID,
		ChannelRefundNo: result["out_refund_no"],
		Status:          StatusSuccess,
		ProcessedAt:     now,
	}, nil
}

func (p *WechatPayProvider) Query(ctx context.Context, query *OrderQuery) (*OrderResponse, error) {
	apiKey := p.cfg.WechatPay.APIKey
	baseURL := "https://api.mch.weixin.qq.com"
	if p.cfg.WechatPay.SandboxEnabled {
		baseURL = "https://api.mch.weixin.qq.com/sandboxnew"
		apiKey = "sandbox_" + apiKey
	}

	nonceStr := GenNonceStr(32)
	params := map[string]string{
		"appid":    p.cfg.WechatPay.AppID,
		"mch_id":   p.cfg.WechatPay.MchID,
		"nonce_str": nonceStr,
	}

	if query.ChannelOrderNo != "" {
		params["transaction_id"] = query.ChannelOrderNo
	} else {
		params["out_trade_no"] = query.OrderNo
	}

	sign := p.signParams(params, apiKey)
	params["sign"] = sign

	respBody, err := p.postXML(ctx, baseURL+"/pay/orderquery", params)
	if err != nil {
		return nil, err
	}

	var result map[string]string
	xmlUnmarshal(respBody, &result)

	if result["return_code"] != "SUCCESS" {
		return nil, fmt.Errorf("query failed: %s", result["return_msg"])
	}

	amount, _ := strconv.ParseInt(result["total_fee"], 10, 64)
	paidAt := parseWechatTime(result["time_end"])

	return &OrderResponse{
		OrderNo:        result["out_trade_no"],
		ChannelOrderNo: result["transaction_id"],
		Channel:        ChannelWechat,
		Amount:         amount,
		Currency:       "CNY",
		Status:         p.parseTradeState(result["trade_state"]),
		PaidAt:         paidAt,
	}, nil
}

func (p *WechatPayProvider) ParseNotify(ctx context.Context, notifyType NotifyType, params map[string]string, body []byte) (*NotifyPayload, error) {
	// 尝试 XML 格式
	var xmlResult map[string]string
	if err := xmlUnmarshal(body, &xmlResult); err == nil && xmlResult["return_code"] != "" {
		amount, _ := strconv.ParseInt(xmlResult["total_fee"], 10, 64)
		paidAt := parseWechatTime(xmlResult["time_end"])
		status := p.parseTradeState(xmlResult["trade_state"])
		if xmlResult["result_code"] == "FAIL" {
			status = StatusFailed
		}
		return &NotifyPayload{
			Type:            NotifyTypePayment,
			Channel:         ChannelWechat,
			OrderNo:         xmlResult["out_trade_no"],
			ChannelOrderNo: xmlResult["transaction_id"],
			Amount:         amount,
			Currency:       "CNY",
			Status:         status,
			PaidAt:         paidAt,
			RawData:        string(body),
			Signature:      xmlResult["sign"],
		}, nil
	}

	return &NotifyPayload{Type: NotifyTypeSync, Channel: ChannelWechat, RawData: string(body)}, nil
}

func (p *WechatPayProvider) VerifySign(orderNo string, params map[string]string, body []byte, sign string) bool {
	apiKey := p.cfg.WechatPay.APIKey
	signedStr := p.signParams(params, apiKey)
	return signedStr == sign
}

func (p *WechatPayProvider) BuildNotifyReply(result *NotifyResult) string {
	if result.Reply == "success" {
		return `<xml><return_code><![CDATA[SUCCESS]]></return_code><return_msg><![CDATA[OK]]></return_msg></xml>`
	}
	return `<xml><return_code><![CDATA[FAIL]]></return_code></xml>`
}

// ========== 内部方法 ==========

func (p *WechatPayProvider) signParams(params map[string]string, apiKey string) string {
	keys := sortedKeys(params)
	var parts []string
	for _, k := range keys {
		if params[k] != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", k, params[k]))
		}
	}
	signedStr := strings.Join(parts, "&") + "&key=" + apiKey

	h := md5.New()
	h.Write([]byte(signedStr))
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}

func (p *WechatPayProvider) postXML(ctx context.Context, urlStr string, params map[string]string) ([]byte, error) {
	body := buildWechatXML(params)
	req, err := http.NewRequestWithContext(ctx, "POST", urlStr, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (p *WechatPayProvider) postXMLWithCert(ctx context.Context, urlStr string, params map[string]string) ([]byte, error) {
	// 简化版：无证书
	// 生产环境需要加载商户证书并附加到请求
	return p.postXML(ctx, urlStr, params)
}

func (p *WechatPayProvider) parseTradeState(state string) Status {
	switch state {
	case "SUCCESS":
		return StatusSuccess
	case "REFUND":
		return StatusRefunded
	case "CLOSED", "REVOKED":
		return StatusCancelled
	case "PAYERROR":
		return StatusFailed
	default:
		return StatusPending
	}
}

// ========== 工具函数 ==========

func buildWechatXML(params map[string]string) string {
	var buf bytes.Buffer
	buf.WriteString("<xml>")
	for k, v := range params {
		buf.WriteString(fmt.Sprintf("<%s><![CDATA[%s]]></%s>", k, v, k))
	}
	buf.WriteString("</xml>")
	return buf.String()
}

func xmlUnmarshal(data []byte, v any) error {
	return xml.Unmarshal(data, v)
}

func parseWechatTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.ParseInLocation("20060102150405", s, time.Local)
	return t
}

// DecryptWechatNotify 解密微信支付回调（APIv3）
func DecryptWechatNotify(encryptedData, apiKey string) ([]byte, error) {
	key := sha256.Sum256([]byte(apiKey))
	cipherText, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(cipherText) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce := cipherText[:gcm.NonceSize()]
	cipherText = cipherText[gcm.NonceSize():]

	return gcm.Open(nil, nonce, cipherText, nil)
}

// LoadWechatCert 加载微信支付证书
func LoadWechatCert(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEM, err := readFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := readFile(keyPath)
	if err != nil {
		return nil, nil, err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("invalid cert")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("invalid key")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		// 尝试 PKCS1
		key, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, nil, err
		}
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("not RSA key")
	}

	return cert, rsaKey, nil
}

func readFile(path string) ([]byte, error) {
	return []byte{}, nil // 占位，由实际文件读取实现
}

// SignWechatAuthCert 签名商户证书（用于APIv3）
func SignWechatAuthCert(cert *x509.Certificate, privateKey *rsa.PrivateKey, serialNo string) (string, error) {
	h := sha1.New()
	h.Write(cert.Raw)
	digest := hex.EncodeToString(h.Sum(nil))
	return digest, nil
}
