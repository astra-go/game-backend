package payment

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"
)

// ========== 错误 ==========
var (
	ErrInvalidSign        = fmt.Errorf("签名验证失败")
	ErrOrderNotFound      = fmt.Errorf("订单不存在")
	ErrOrderAlreadyPaid   = fmt.Errorf("订单已支付")
	ErrAmountMismatch     = fmt.Errorf("金额不匹配")
	ErrChannelUnavailable = fmt.Errorf("支付通道不可用")
)

// ========== 类型 ==========
type Channel string
type Status string
type NotifyType string

const (
	ChannelAlipay    Channel = "alipay"
	ChannelWechat    Channel = "wechat"
	ChannelAppleIAP  Channel = "apple_iap"
	ChannelGoogleIAP Channel = "google_iap"
)

const (
	StatusPending   Status = "pending"
	StatusSuccess   Status = "success"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusRefunded  Status = "refunded"
)

const (
	NotifyTypePayment NotifyType = "payment"
	NotifyTypeRefund  NotifyType = "refund"
	NotifyTypeSync   NotifyType = "sync"
)

// ========== 请求/响应结构 ==========

type ChargeRequest struct {
	OrderID   string `json:"order_id"`
	OrderNo   string `json:"order_no"`
	Amount    int64  `json:"amount"`    // 分
	Currency  string `json:"currency"`  // CNY/USD
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	Channel   Channel `json:"channel"`
	PlayerID  string `json:"player_id"`
	ProductID string `json:"product_id"`
	NotifyURL string `json:"notify_url"`
	ReturnURL string `json:"return_url"`
	ClientIP  string `json:"client_ip"`
	Metadata  string `json:"metadata"`  // JSON string
	Extras    map[string]string `json:"extras"`
}

type ChargeResponse struct {
	OrderNo        string    `json:"order_no"`
	PayURL         string    `json:"pay_url"`
	QRCodeData     string    `json:"qr_code_data"`
	FormHTML       string    `json:"form_html"`
	ExpiresAt      time.Time `json:"expires_at"`
	ChannelOrderNo string    `json:"channel_order_no"`
	Metadata       string    `json:"metadata"`
}

type RefundRequest struct {
	OrderID        string `json:"order_id"`
	ChannelOrderNo string `json:"channel_order_no"`
	RefundID       string `json:"refund_id"`
	Amount         int64  `json:"amount"` // 分
	Reason         string `json:"reason"`
	OperatorID     string `json:"operator_id"`
}

type RefundResponse struct {
	RefundID        string    `json:"refund_id"`
	ChannelRefundNo string    `json:"channel_refund_no"`
	Status          Status    `json:"status"`
	Amount          int64     `json:"amount"`
	ProcessedAt     time.Time `json:"processed_at"`
}

type OrderQuery struct {
	OrderNo        string `json:"order_no"`
	ChannelOrderNo string `json:"channel_order_no"`
}

type OrderResponse struct {
	OrderNo        string    `json:"order_no"`
	ChannelOrderNo string    `json:"channel_order_no"`
	Channel        Channel   `json:"channel"`
	Amount         int64     `json:"amount"`
	Currency       string    `json:"currency"`
	Status         Status    `json:"status"`
	PaidAt         time.Time `json:"paid_at,omitempty"`
	FailReason     string    `json:"fail_reason,omitempty"`
}

type NotifyPayload struct {
	Type           NotifyType `json:"type"`
	Channel        Channel    `json:"channel"`
	OrderNo        string     `json:"order_no"`
	ChannelOrderNo string     `json:"channel_order_no"`
	Status         Status     `json:"status"`
	Amount         int64      `json:"amount"`
	Currency       string     `json:"currency"`
	PaidAt         time.Time  `json:"paid_at,omitempty"`
	RefundAmount   int64      `json:"refund_amount,omitempty"`
	RefundAt       time.Time  `json:"refund_at,omitempty"`
	RawData        string     `json:"raw_data"`
	Signature      string     `json:"signature"`
}

type NotifyResult struct {
	Handled bool   `json:"handled"`
	Reply   string `json:"reply"`
	Message string `json:"message"`
}

// ========== 支付通道接口 ==========
type PaymentProvider interface {
	Name() Channel
	IsAvailable() bool
	Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error)
	Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error)
	Query(ctx context.Context, query *OrderQuery) (*OrderResponse, error)
	ParseNotify(ctx context.Context, notifyType NotifyType, params map[string]string, body []byte) (*NotifyPayload, error)
	VerifySign(orderNo string, params map[string]string, body []byte, sign string) bool
	BuildNotifyReply(result *NotifyResult) string
}

// ========== 配置 ==========
type Config struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	NotifyURL string `json:"notify_url"`
	ReturnURL string `json:"return_url"`
	Timeout   int    `json:"timeout"`

	Alipay struct {
		Enabled      bool   `json:"enabled"`
		AppID        string `json:"app_id"`
		PrivateKey   string `json:"private_key"`
		AlipayPubKey string `json:"alipay_pub_key"`
		ServerURL    string `json:"server_url"`
	} `json:"alipay"`

	WechatPay struct {
		Enabled        bool   `json:"enabled"`
		AppID          string `json:"app_id"`
		MchID          string `json:"mch_id"`
		APIKey         string `json:"api_key"`
		APIv3Key       string `json:"api_v3_key"`
		CertPath       string `json:"cert_path"`
		KeyPath        string `json:"key_path"`
		NotifyURL      string `json:"notify_url"`
		SandboxEnabled bool   `json:"sandbox_enabled"`
	} `json:"wechat_pay"`

	AppleIAP struct {
		Enabled      bool   `json:"enabled"`
		BundleID     string `json:"bundle_id"`
		Environment  string `json:"environment"`
		SharedSecret string `json:"shared_secret"`
	} `json:"apple_iap"`

	GoogleIAP struct {
		Enabled     bool   `json:"enabled"`
		PackageName string `json:"package_name"`
		Credentials string `json:"credentials"`
	} `json:"google_iap"`
}

// ========== 工具 ==========
func GenNonceStr(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

func NormalizeAmount(yuan float64) int64  { return int64(yuan * 100) }
func DenormalizeAmount(fen int64) float64 { return float64(fen) / 100 }

func FormatAmount(fen int64, currency string) string {
	return fmt.Sprintf("%.2f %s", DenormalizeAmount(fen), currency)
}

func JSONMarshal(v any) ([]byte, error) { return json.Marshal(v) }
func JSONUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
