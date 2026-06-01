package payment

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Service 支付服务（管理所有支付通道）
type Service struct {
	cfg     *Config
	db      *gorm.DB
	redis   *redis.Client
	providers map[Channel]PaymentProvider
	mu       sync.RWMutex

	onPaymentSuccess func(ctx context.Context, payload *NotifyPayload) error
	onRefundSuccess  func(ctx context.Context, payload *NotifyPayload) error
}

// PaymentOrder 支付订单记录
type PaymentOrder struct {
	ID             string    `json:"id" gorm:"primaryKey;size:64"`
	OrderID        string    `json:"order_id" gorm:"uniqueIndex;size:64;not null"`
	OrderNo        string    `json:"order_no" gorm:"uniqueIndex;size:64;not null"`
	Channel        Channel   `json:"channel" gorm:"size:16;not null"`
	Amount         int64     `json:"amount"`
	Currency       string    `json:"currency"`
	Subject        string    `json:"subject"`
	PlayerID       string    `json:"player_id" gorm:"index;size:64"`
	ProductID      string    `json:"product_id" gorm:"index;size:64"`
	Status         Status    `json:"status" gorm:"size:16;default:pending"`
	ChannelOrderNo string    `json:"channel_order_no" gorm:"size:128"`
	QRCodeData     string    `json:"qr_code_data" gorm:"type:text"`
	PayURL         string    `json:"pay_url" gorm:"size:512"`
	ExpiresAt      time.Time `json:"expires_at"`
	PaidAt         *time.Time `json:"paid_at"`
	FailReason     string    `json:"fail_reason" gorm:"size:256"`
	RawRequest     string    `json:"raw_request" gorm:"type:text"`
	RawResponse    string    `json:"raw_response" gorm:"type:text"`
	Metadata       string    `json:"metadata" gorm:"type:text"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (PaymentOrder) TableName() string { return "payment_orders" }

// RefundOrder 退款记录
type RefundOrder struct {
	ID              string     `json:"id" gorm:"primaryKey;size:64"`
	RefundID        string     `json:"refund_id" gorm:"uniqueIndex;size:64;not null"`
	OrderID         string     `json:"order_id" gorm:"index;size:64;not null"`
	PaymentOrderNo  string     `json:"payment_order_no" gorm:"index;size:64"`
	Channel         Channel    `json:"channel" gorm:"size:16"`
	Amount          int64      `json:"amount"`
	Currency        string     `json:"currency"`
	Reason          string     `json:"reason" gorm:"size:256"`
	OperatorID      string     `json:"operator_id" gorm:"size:64"`
	Status          Status     `json:"status" gorm:"size:16;default:pending"`
	ChannelRefundNo string     `json:"channel_refund_no" gorm:"size:128"`
	FailReason      string     `json:"fail_reason" gorm:"size:256"`
	ProcessedAt      *time.Time `json:"processed_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (RefundOrder) TableName() string { return "payment_refunds" }

// NewService 创建支付服务
func NewService(cfg *Config, db *gorm.DB, redis *redis.Client) *Service {
	return &Service{cfg: cfg, db: db, redis: redis, providers: make(map[Channel]PaymentProvider)}
}

// RegisterProvider 注册支付通道
func (s *Service) RegisterProvider(provider PaymentProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[provider.Name()] = provider
}

// SetCallback 设置回调处理
func (s *Service) SetCallback(onPaymentSuccess, onRefundSuccess func(context.Context, *NotifyPayload) error) {
	s.onPaymentSuccess = onPaymentSuccess
	s.onRefundSuccess = onRefundSuccess
}

// GetProvider 获取支付通道
func (s *Service) GetProvider(channel Channel) (PaymentProvider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[channel]
	if !ok || !p.IsAvailable() {
		return nil, ErrChannelUnavailable
	}
	return p, nil
}

// Charge 发起支付
func (s *Service) Charge(ctx context.Context, req *ChargeRequest) (*ChargeResponse, error) {
	provider, err := s.GetProvider(req.Channel)
	if err != nil {
		return nil, err
	}

	// 创建本地支付记录
	paymentOrder := &PaymentOrder{
		ID:         fmt.Sprintf("pay_%d", time.Now().UnixNano()),
		OrderID:    req.OrderID,
		OrderNo:    req.OrderNo,
		Channel:    req.Channel,
		Amount:     req.Amount,
		Currency:   req.Currency,
		Subject:    req.Subject,
		PlayerID:   req.PlayerID,
		ProductID:  req.ProductID,
		Status:     StatusPending,
		ExpiresAt:  time.Now().Add(2 * time.Hour),
		Metadata:   req.Metadata,
	}

	// 合并 extras
	if req.Extras != nil {
		raw, _ := JSONMarshal(req.Extras)
		paymentOrder.Metadata = string(raw)
	}

	// 设置回调地址
	notifyURL := req.NotifyURL
	if notifyURL == "" && s.cfg.NotifyURL != "" {
		notifyURL = s.cfg.NotifyURL
	}
	req.NotifyURL = notifyURL

	// 调用支付通道
	resp, err := provider.Charge(ctx, req)
	if err != nil {
		paymentOrder.Status = StatusFailed
		paymentOrder.FailReason = err.Error()
		s.savePaymentOrder(ctx, paymentOrder)
		return nil, err
	}

	// 更新记录
	paymentOrder.ChannelOrderNo = resp.ChannelOrderNo
	paymentOrder.QRCodeData = resp.QRCodeData
	paymentOrder.PayURL = resp.PayURL
	if !resp.ExpiresAt.IsZero() {
		paymentOrder.ExpiresAt = resp.ExpiresAt
	}

	rawReq, _ := JSONMarshal(req)
	rawResp, _ := JSONMarshal(resp)
	paymentOrder.RawRequest = string(rawReq)
	paymentOrder.RawResponse = string(rawResp)

	s.savePaymentOrder(ctx, paymentOrder)

	return resp, nil
}

// QueryOrder 查询本地支付订单
func (s *Service) QueryOrder(ctx context.Context, orderNo string) (*PaymentOrder, error) {
	// 先查库
	var order PaymentOrder
	err := s.db.WithContext(ctx).Where("order_no = ?", orderNo).First(&order).Error
	if err == nil {
		return &order, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// 查 Redis
	key := fmt.Sprintf("payment:order:%s", orderNo)
	if s.redis != nil {
		data, err := s.redis.Get(ctx, key).Bytes()
		if err == nil {
			JSONUnmarshal(data, &order)
			return &order, nil
		}
	}

	return nil, ErrOrderNotFound
}

// GetPaymentOrderByOrderID 通过业务订单ID查询支付订单
func (s *Service) GetPaymentOrderByOrderID(ctx context.Context, orderID string) (*PaymentOrder, error) {
	var order PaymentOrder
	err := s.db.WithContext(ctx).Where("order_id = ?", orderID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOrderNotFound
	}
	return &order, err
}

// Query 通过支付通道查询订单
func (s *Service) Query(ctx context.Context, channel Channel, query *OrderQuery) (*OrderResponse, error) {
	provider, err := s.GetProvider(channel)
	if err != nil {
		return nil, err
	}
	return provider.Query(ctx, query)
}

// Refund 发起退款
func (s *Service) Refund(ctx context.Context, req *RefundRequest) (*RefundResponse, error) {
	// 查询原支付订单
	paymentOrder, err := s.GetPaymentOrderByOrderID(ctx, req.OrderID)
	if err != nil {
		return nil, err
	}
	if paymentOrder.Status != StatusSuccess {
		return nil, errors.New("原订单未支付成功")
	}

	provider, err := s.GetProvider(paymentOrder.Channel)
	if err != nil {
		return nil, err
	}

	// 创建退款记录
	refundOrder := &RefundOrder{
		ID:             fmt.Sprintf("ref_%d", time.Now().UnixNano()),
		RefundID:       req.RefundID,
		OrderID:        req.OrderID,
		PaymentOrderNo: paymentOrder.OrderNo,
		Channel:        paymentOrder.Channel,
		Amount:         req.Amount,
		Currency:       paymentOrder.Currency,
		Reason:         req.Reason,
		OperatorID:     req.OperatorID,
		Status:         StatusPending,
	}
	s.db.WithContext(ctx).Save(refundOrder)

	// 发起退款
	resp, err := provider.Refund(ctx, req)
	if err != nil {
		s.db.WithContext(ctx).Model(&RefundOrder{}).Where("id = ?", refundOrder.ID).
			Updates(map[string]any{"status": StatusFailed, "fail_reason": err.Error()})
		return nil, err
	}

	refundOrder.ChannelRefundNo = resp.ChannelRefundNo
	refundOrder.Status = resp.Status
	now := time.Now()
	refundOrder.ProcessedAt = &now
	s.db.WithContext(ctx).Save(refundOrder)

	return resp, nil
}

// HandleNotify 处理支付回调
func (s *Service) HandleNotify(ctx context.Context, channel Channel, notifyType NotifyType, params map[string]string, body []byte) *NotifyResult {
	provider, err := s.GetProvider(channel)
	if err != nil {
		return &NotifyResult{Handled: false, Reply: "fail", Message: err.Error()}
	}

	payload, err := provider.ParseNotify(ctx, notifyType, params, body)
	if err != nil {
		log.Printf("[Payment] ParseNotify failed: %v", err)
		return &NotifyResult{Handled: false, Reply: "fail", Message: err.Error()}
	}

	// 验证签名（跳过如果没签名）
	if payload.Signature != "" {
		if !provider.VerifySign(payload.OrderNo, params, body, payload.Signature) {
			log.Printf("[Payment] Sign verify failed for order: %s", payload.OrderNo)
			return &NotifyResult{Handled: false, Reply: "fail", Message: "sign error"}
		}
	}

	// 更新本地状态
	switch payload.Type {
	case NotifyTypePayment:
		s.handlePaymentNotify(ctx, payload)
	case NotifyTypeRefund:
		s.handleRefundNotify(ctx, payload)
	}

	return &NotifyResult{Handled: true, Reply: "success", Message: "OK"}
}

// ========== 内部方法 ==========

func (s *Service) savePaymentOrder(ctx context.Context, order *PaymentOrder) {
	s.db.WithContext(ctx).Save(order)

	if s.redis != nil {
		key := fmt.Sprintf("payment:order:%s", order.OrderNo)
		data, _ := JSONMarshal(order)
		s.redis.Set(ctx, key, data, time.Hour)
	}
}

func (s *Service) handlePaymentNotify(ctx context.Context, payload *NotifyPayload) {
	updates := map[string]any{
		"status": payload.Status,
	}
	if payload.ChannelOrderNo != "" {
		updates["channel_order_no"] = payload.ChannelOrderNo
	}
	if !payload.PaidAt.IsZero() {
		updates["paid_at"] = payload.PaidAt
	}
	s.db.WithContext(ctx).Model(&PaymentOrder{}).Where("order_no = ?", payload.OrderNo).Updates(updates)

	if s.redis != nil {
		key := fmt.Sprintf("payment:order:%s", payload.OrderNo)
		s.redis.Del(ctx, key)
	}

	if s.onPaymentSuccess != nil && payload.Status == StatusSuccess {
		if err := s.onPaymentSuccess(ctx, payload); err != nil {
			log.Printf("[Payment] onPaymentSuccess failed: %v", err)
		}
	}
}

func (s *Service) handleRefundNotify(ctx context.Context, payload *NotifyPayload) {
	updates := map[string]any{
		"status": payload.Status,
	}
	if payload.RefundAmount > 0 {
		updates["refund_amount"] = payload.RefundAmount
	}
	if !payload.RefundAt.IsZero() {
		updates["processed_at"] = payload.RefundAt
	}
	s.db.WithContext(ctx).Model(&RefundOrder{}).Where("payment_order_no = ?", payload.OrderNo).Updates(updates)

	if s.onRefundSuccess != nil {
		if err := s.onRefundSuccess(ctx, payload); err != nil {
			log.Printf("[Payment] onRefundSuccess failed: %v", err)
		}
	}
}

// AutoMigrate 自动迁移表
func (s *Service) AutoMigrate() error {
	return s.db.AutoMigrate(&PaymentOrder{}, &RefundOrder{})
}

// IsChannelEnabled 检查通道是否启用
func (s *Service) IsChannelEnabled(channel Channel) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[channel]
	return ok && p.IsAvailable()
}

// ListEnabledChannels 列出已启用的支付通道
func (s *Service) ListEnabledChannels() []Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var channels []Channel
	for ch, p := range s.providers {
		if p.IsAvailable() {
			channels = append(channels, ch)
		}
	}
	return channels
}
