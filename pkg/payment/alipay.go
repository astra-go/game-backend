package payment

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AlipayProvider 支付宝支付通道
type AlipayProvider struct {
	cfg    Config
	client *http.Client
}

// NewAlipayProvider 创建支付宝支付通道
func NewAlipayProvider(cfg Config) *AlipayProvider {
	return &AlipayProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *AlipayProvider) Name() Channel { return ChannelAlipay }

func (p *AlipayProvider) IsAvailable() bool {
	return p.cfg.Alipay.Enabled && p.cfg.Alipay.AppID != "" && p.cfg.Alipay.PrivateKey != ""
}

func (p *AlipayProvider) Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error) {
	bizContent := map[string]any{
		"out_trade_no":    req.OrderNo,
		"total_amount":    fmt.Sprintf("%.2f", DenormalizeAmount(req.Amount)),
		"subject":         req.Subject,
		"body":            req.Body,
		"timeout_express": "2h",
	}
	bizJSON, _ := JSONMarshal(bizContent)

	params := map[string]string{
		"app_id":      p.cfg.Alipay.AppID,
		"method":      "alipay.trade.precreate",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"notify_url":  req.NotifyURL,
		"biz_content": string(bizJSON),
	}

	sign, err := p.signParams(params)
	if err != nil {
		return nil, fmt.Errorf("sign failed: %w", err)
	}
	params["sign"] = sign

	respBody, err := p.postForm(ctx, "https://openapi.alipay.com/gateway.do", params)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	var result alipayPrecreateResp
	if err := JSONUnmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}

	if result.AlipayTradePrecreateResponse.Code != "10000" {
		return nil, fmt.Errorf("alipay error: %s - %s",
			result.AlipayTradePrecreateResponse.Code, result.AlipayTradePrecreateResponse.Msg)
	}

	return &ChargeResponse{
		OrderNo:        result.AlipayTradePrecreateResponse.OutTradeNo,
		QRCodeData:     result.AlipayTradePrecreateResponse.QRCode,
		ExpiresAt:      time.Now().Add(2 * time.Hour),
		ChannelOrderNo: result.AlipayTradePrecreateResponse.OutTradeNo,
	}, nil
}

func (p *AlipayProvider) Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error) {
	bizContent := map[string]any{
		"trade_no":      req.ChannelOrderNo,
		"out_request_no": req.RefundID,
		"refund_amount": fmt.Sprintf("%.2f", DenormalizeAmount(req.Amount)),
		"refund_reason": req.Reason,
	}
	bizJSON, _ := JSONMarshal(bizContent)

	params := map[string]string{
		"app_id":      p.cfg.Alipay.AppID,
		"method":      "alipay.trade.refund",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"biz_content": string(bizJSON),
	}
	sign, _ := p.signParams(params)
	params["sign"] = sign

	respBody, err := p.postForm(ctx, "https://openapi.alipay.com/gateway.do", params)
	if err != nil {
		return nil, err
	}

	var result alipayRefundResp
	JSONUnmarshal(respBody, &result)

	if result.AlipayTradeRefundResponse.Code != "10000" {
		return nil, fmt.Errorf("refund failed: %s", result.AlipayTradeRefundResponse.Msg)
	}

	now := time.Now()
	return &RefundResponse{
		RefundID:        req.RefundID,
		ChannelRefundNo: result.AlipayTradeRefundResponse.TradeNo,
		Status:          StatusSuccess,
		ProcessedAt:     now,
	}, nil
}

func (p *AlipayProvider) Query(ctx context.Context, query *OrderQuery) (*OrderResponse, error) {
	orderNo := query.OrderNo
	if query.ChannelOrderNo != "" {
		orderNo = query.ChannelOrderNo
	}

	bizContent := map[string]any{"out_trade_no": orderNo}
	bizJSON, _ := JSONMarshal(bizContent)

	params := map[string]string{
		"app_id":      p.cfg.Alipay.AppID,
		"method":      "alipay.trade.query",
		"format":      "JSON",
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"biz_content": string(bizJSON),
	}
	sign, _ := p.signParams(params)
	params["sign"] = sign

	respBody, err := p.postForm(ctx, "https://openapi.alipay.com/gateway.do", params)
	if err != nil {
		return nil, err
	}

	var result alipayQueryResp
	JSONUnmarshal(respBody, &result)

	if result.AlipayTradeQueryResponse.Code != "10000" && result.AlipayTradeQueryResponse.Code != "40004" {
		return nil, ErrOrderNotFound
	}

	if result.AlipayTradeQueryResponse.Code == "40004" {
		return &OrderResponse{OrderNo: query.OrderNo, Status: StatusPending}, nil
	}

	amountFen := parseFen(result.AlipayTradeQueryResponse.TotalAmount)

	return &OrderResponse{
		OrderNo:        result.AlipayTradeQueryResponse.OutTradeNo,
		ChannelOrderNo: result.AlipayTradeQueryResponse.TradeNo,
		Channel:        ChannelAlipay,
		Amount:         amountFen,
		Currency:       "CNY",
		Status:         p.parseTradeStatus(result.AlipayTradeQueryResponse.TradeStatus),
	}, nil
}

func (p *AlipayProvider) ParseNotify(ctx context.Context, notifyType NotifyType, params map[string]string, body []byte) (*NotifyPayload, error) {
	amount := parseFen(params["total_amount"])
	return &NotifyPayload{
		Type:            NotifyTypePayment,
		Channel:         ChannelAlipay,
		OrderNo:         params["out_trade_no"],
		ChannelOrderNo: params["trade_no"],
		Amount:         amount,
		Currency:       params["currency"],
		Status:         p.parseTradeStatus(params["trade_status"]),
		RawData:        string(body),
		Signature:      params["sign"],
	}, nil
}

func (p *AlipayProvider) VerifySign(orderNo string, params map[string]string, body []byte, sign string) bool {
	signParams := make(map[string]string)
	for k, v := range params {
		if k != "sign" && k != "sign_type" {
			signParams[k] = v
		}
	}
	signedStr := concatParams(signParams)
	return p.verifySign(signedStr, sign)
}

func (p *AlipayProvider) BuildNotifyReply(result *NotifyResult) string { return result.Reply }

// ========== 内部方法 ==========

func (p *AlipayProvider) signParams(params map[string]string) (string, error) {
	keys := sortedKeys(params)
	var parts []string
	for _, k := range keys {
		if params[k] != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", k, url.QueryEscape(params[k])))
		}
	}
	signedStr := strings.Join(parts, "&")

	privateKey, err := p.parsePrivateKey(p.cfg.Alipay.PrivateKey)
	if err != nil {
		return "", err
	}

	h := sha256.New()
	h.Write([]byte(signedStr))
	digest := h.Sum(nil)

	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func (p *AlipayProvider) verifySign(data, signStr string) bool {
	pubKey, err := p.parsePublicKey(p.cfg.Alipay.AlipayPubKey)
	if err != nil {
		return false
	}
	sigBytes, _ := base64.StdEncoding.DecodeString(signStr)
	h := sha256.New()
	h.Write([]byte(data))
	return rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, h.Sum(nil), sigBytes) == nil
}

func (p *AlipayProvider) parsePrivateKey(keyStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(keyStr))
	if block == nil {
		return nil, fmt.Errorf("invalid private key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func (p *AlipayProvider) parsePublicKey(keyStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(keyStr))
	if block == nil {
		return nil, fmt.Errorf("invalid public key")
	}
	pubInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		pubInterface, err = x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
	}
	pub, ok := pubInterface.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not RSA key")
	}
	return pub, nil
}

func (p *AlipayProvider) postForm(ctx context.Context, urlStr string, params map[string]string) ([]byte, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", urlStr, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (p *AlipayProvider) parseTradeStatus(status string) Status {
	switch status {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		return StatusSuccess
	case "TRADE_CLOSED":
		return StatusCancelled
	default:
		return StatusPending
	}
}

// ========== 内部结构 ==========

type alipayRespBase struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

type alipayPrecreateResp struct {
	AlipayTradePrecreateResponse struct {
		OutTradeNo string `json:"out_trade_no"`
		QRCode     string `json:"qr_code"`
		Code       string `json:"code"`
		Msg        string `json:"msg"`
	} `json:"alipay_trade_precreate_response"`
}

type alipayRefundResp struct {
	AlipayTradeRefundResponse struct {
		TradeNo   string `json:"trade_no"`
		RefundFee string `json:"refund_fee"`
		Code      string `json:"code"`
		Msg       string `json:"msg"`
	} `json:"alipay_trade_refund_response"`
}

type alipayQueryResp struct {
	AlipayTradeQueryResponse struct {
		TradeNo     string `json:"trade_no"`
		OutTradeNo  string `json:"out_trade_no"`
		TotalAmount string `json:"total_amount"`
		TradeStatus string `json:"trade_status"`
		Code        string `json:"code"`
	} `json:"alipay_trade_query_response"`
}

// ========== 工具函数 ==========

func sortedKeys(params map[string]string) []string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func concatParams(params map[string]string) string {
	keys := sortedKeys(params)
	var parts []string
	for _, k := range keys {
		if params[k] != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", k, params[k]))
		}
	}
	return strings.Join(parts, "&")
}

func parseFen(s string) int64 {
	if s == "" {
		return 0
	}
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return int64(f * 100)
}

// AlipayXMLNotify 支付宝XML回调解析（兼容旧版）
type AlipayXMLNotify struct {
	XMLName   xml.Name `xml:"alipay"`
	Sign      string   `xml:"sign"`
	OutTradeNo string   `xml:"out_trade_no"`
	TradeNo    string   `xml:"trade_no"`
	TradeStatus string  `xml:"trade_status"`
	TotalAmount string  `xml:"total_amount"`
	BuyerLogonID string `xml:"buyer_logon_id"`
	GmtPayment string   `xml:"gmt_payment"`
}

func ParseAlipayXML(body []byte) (*NotifyPayload, error) {
	var notify AlipayXMLNotify
	if err := xml.Unmarshal(body, &notify); err != nil {
		return nil, err
	}
	return &NotifyPayload{
		Type:            NotifyTypePayment,
		Channel:         ChannelAlipay,
		OrderNo:         notify.OutTradeNo,
		ChannelOrderNo: notify.TradeNo,
		Amount:         parseFen(notify.TotalAmount),
		Currency:       "CNY",
		Status:         parseAlipayStatus(notify.TradeStatus),
		RawData:        string(body),
		Signature:      notify.Sign,
	}, nil
}

func parseAlipayStatus(s string) Status {
	switch s {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		return StatusSuccess
	case "TRADE_CLOSED":
		return StatusCancelled
	default:
		return StatusPending
	}
}

// GenAlipayOrderNo 生成支付宝风格订单号
func GenAlipayOrderNo() string {
	return fmt.Sprintf("A%s%04d",
		time.Now().Format("20060102150405"),
		time.Now().Nanosecond()/100000%10000)
}

