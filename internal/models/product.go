package models

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// ========== 枚举常量 ==========

// ProductStatus 商品状态
type ProductStatus string

const (
	ProductStatusActive   ProductStatus = "active"   // 上架
	ProductStatusInactive ProductStatus = "inactive" // 下架
	ProductStatusHidden  ProductStatus = "hidden"    // 隐藏（仅管理员可见）
)

// OrderStatus 订单状态
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"   // 待支付
	OrderStatusPaid      OrderStatus = "paid"      // 已支付（待发货）
	OrderStatusDelivered OrderStatus = "delivered" // 已发货（完成）
	OrderStatusCancelled OrderStatus = "cancelled" // 已取消
	OrderStatusExpired   OrderStatus = "expired"   // 已过期
	OrderStatusRefunded  OrderStatus = "refunded"  // 已退款
)

// PaymentMethod 支付方式
type PaymentMethod string

const (
	PaymentMethodDiamond   PaymentMethod = "diamond"    // 钻石
	PaymentMethodGold      PaymentMethod = "gold"       // 金币
	PaymentMethodCoupon    PaymentMethod = "coupon"     // 点券
	PaymentMethodAlipay    PaymentMethod = "alipay"    // 支付宝
	PaymentMethodWechatPay PaymentMethod = "wechat"    // 微信支付
	PaymentMethodAppleIAP  PaymentMethod = "apple_iap" // Apple IAP
	PaymentMethodGoogleIAP PaymentMethod = "google_iap" // Google IAP
)

// CurrencyType 货币类型
type CurrencyType string

const (
	CurrencyDiamond CurrencyType = "diamond" // 钻石
	CurrencyGold    CurrencyType = "gold"    // 金币
	CurrencyCoupon  CurrencyType = "coupon"  // 点券
)

// ProductCategory 商品分类
type ProductCategory string

const (
	ProductCategorySkin        ProductCategory = "skin"         // 皮肤
	ProductCategoryHero        ProductCategory = "hero"         // 英雄
	ProductCategoryItem        ProductCategory = "item"         // 道具
	ProductCategoryMount       ProductCategory = "mount"        // 坐骑
	ProductCategoryTitle       ProductCategory = "title"        // 称号
	ProductCategoryDiamond     ProductCategory = "diamond"      // 钻石充值
	ProductCategoryGold        ProductCategory = "gold"        // 金币充值
	ProductCategoryExp         ProductCategory = "exp"          // 经验
	ProductCategorySubscription ProductCategory = "subscription" // 订阅
	ProductCategoryGift        ProductCategory = "gift"         // 礼包
	ProductCategoryBundle      ProductCategory = "bundle"       // 组合包
)

// ========== 辅助函数 ==========

// genID 生成随机ID
func genID(prefix string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// TodayDate 当前日期字符串 YYYY-MM-DD
func TodayDate() string {
	return time.Now().Format("2006-01-02")
}

// ========== Product 商品 ==========

// Product 商品
type Product struct {
	ID            string           `json:"id" gorm:"primaryKey;size:64" example:"prod_abc123"`
	Name          string           `json:"name" gorm:"size:128;not null" example:"传说皮肤-烈焰凤凰"`
	Description   string           `json:"description" gorm:"size:1024" example:"稀有的传说级皮肤"`
	Category      ProductCategory  `json:"category" gorm:"size:32;index:idx_category_status" example:"skin"`
	SubCategory   string           `json:"sub_category" gorm:"size:32" example:"传说"` // 子分类
	Price         int64            `json:"price" gorm:"not null" example:"600"`
	OriginalPrice int64            `json:"original_price" example:"1000"` // 原价（用于显示折扣）
	Currency      CurrencyType     `json:"currency" gorm:"size:16;not null" example:"diamond"`
	StockCount    int              `json:"stock_count" example:"-1"`  // 库存（-1=无限）
	SoldCount     int              `json:"sold_count" gorm:"default:0" example:"5200"` // 已售数量
	LimitPerDay   int              `json:"limit_per_day" example:"1"`   // 每日限购（0=不限）
	LimitPerUser  int              `json:"limit_per_user" example:"1"`  // 总限购（0=不限）
	MinPlayerLv   int              `json:"min_player_lv" gorm:"default:1" example:"10"` // 最低玩家等级
	VipLevel      int              `json:"vip_level" gorm:"default:0" example:"0"` // 最低VIP等级
	ImageURL      string           `json:"image_url" gorm:"size:512" example:"https://cdn.xxx/skin_001.png"`
	PreviewURL    string           `json:"preview_url" gorm:"size:512"` // 预览视频/动图URL
	ItemID        string           `json:"item_id" gorm:"size:64;index:idx_item_id" example:"skin_fire_phoenix"`
	ItemCount     int              `json:"item_count" example:"1"` // 赠送物品数量
	Tags          string           `json:"tags" gorm:"size:256"`    // 标签，逗号分隔 "hot,limit"
	ExtData       string           `json:"ext_data" gorm:"type:text"` // 扩展数据（JSON）
	IsHot         bool             `json:"is_hot" gorm:"default:false;index:idx_category_status"`
	IsNew         bool             `json:"is_new" gorm:"default:false;index:idx_category_status"`
	IsLimited     bool             `json:"is_limited" gorm:"default:false"` // 限时
	IsGiftable    bool             `json:"is_giftable" gorm:"default:false"` // 可送礼
	IsBundle      bool             `json:"is_bundle" gorm:"default:false"`  // 是否为组合包
	SortOrder     int              `json:"sort_order" gorm:"default:0;index:idx_sort"` // 排序权重
	Status        ProductStatus    `json:"status" gorm:"size:16;default:active;index:idx_category_status;index:idx_status"`
	StartTime     *time.Time       `json:"start_time"` // 上架开始时间
	EndTime       *time.Time       `json:"end_time"`   // 上架结束时间
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// TableName GORM表名
func (Product) TableName() string { return "shop_products" }

// IsAvailable 商品是否可售（状态+时间窗口）
func (p *Product) IsAvailable() bool {
	now := time.Now()
	if p.Status != ProductStatusActive {
		return false
	}
	if p.StartTime != nil && now.Before(*p.StartTime) {
		return false
	}
	if p.EndTime != nil && now.After(*p.EndTime) {
		return false
	}
	return true
}

// IsOutOfStock 是否缺货
func (p *Product) IsOutOfStock() bool {
	return p.StockCount >= 0 && p.StockCount <= 0
}

// GetDiscountRate 获取折扣率（0.0-1.0）
func (p *Product) GetDiscountRate() float64 {
	if p.OriginalPrice <= 0 || p.Price >= p.OriginalPrice {
		return 0
	}
	return float64(p.OriginalPrice-p.Price) / float64(p.OriginalPrice)
}

// ========== ProductBundleItem 组合包内容项 ==========

// ProductBundleItem 组合包商品内容项
type ProductBundleItem struct {
	ID        string `json:"id" gorm:"primaryKey;size:64"`
	ProductID string `json:"product_id" gorm:"index;size:64;not null"`
	ItemID    string `json:"item_id" gorm:"size:64;not null"`
	ItemName  string `json:"item_name" gorm:"size:128"`
	ItemCount int    `json:"item_count" gorm:"default:1"`
	SortOrder int    `json:"sort_order" gorm:"default:0"`
}

// TableName GORM表名
func (ProductBundleItem) TableName() string { return "shop_bundle_items" }

// ========== Order 订单 ==========

// Order 订单
type Order struct {
	ID              string         `json:"id" gorm:"primaryKey;size:64" example:"ord_abc123"`
	OrderNo         string         `json:"order_no" gorm:"uniqueIndex;size:64;not null" example:"ORD202606011234567890"`
	PlayerID        string         `json:"player_id" gorm:"index;size:64;not null"`
	ProductID       string         `json:"product_id" gorm:"index;size:64;not null"`
	ProductSnapshot string         `json:"product_snapshot" gorm:"type:text"` // 下单时商品快照JSON
	Count           int            `json:"count" gorm:"default:1" example:"1"`
	UnitPrice       int64          `json:"unit_price"`   // 单价
	TotalAmount     int64          `json:"total_amount" example:"600"` // 总价
	Currency        CurrencyType   `json:"currency" gorm:"size:16;not null"`
	PaymentMethod   PaymentMethod  `json:"payment_method" gorm:"size:32;not null"`
	Status          OrderStatus    `json:"status" gorm:"size:16;default:pending;index;index:idx_player_status"`
	// 关联玩家昵称（冗余存储，减少查询）
	PlayerName     string    `json:"player_name" gorm:"size:64"`
	ProductName    string    `json:"product_name" gorm:"size:128"`
	PayTime        *time.Time `json:"pay_time"`    // 支付时间
	DeliverTime    *time.Time `json:"deliver_time"` // 发货时间
	ExpireTime     *time.Time `json:"expire_time"` // 过期时间（待支付超时）
	// 第三方支付信息
	PaymentOrderNo  string `json:"payment_order_no" gorm:"size:128;index"` // 支付平台订单号
	ThirdPartyID    string `json:"third_party_id" gorm:"size:128"`    // 第三方订单号
	ThirdPartyResp string `json:"third_party_resp" gorm:"type:text"` // 第三方响应原文
	// 退款信息
	RefundAmount  int64     `json:"refund_amount" gorm:"default:0"`
	RefundReason  string    `json:"refund_reason" gorm:"size:256"`
	RefundTime    *time.Time `json:"refund_time"`
	RefunderID    string    `json:"refunder_id" gorm:"size:64"` // 处理退款的管理员ID
	// 送礼信息
	GiftToPlayerID string `json:"gift_to_player_id" gorm:"size:64"` // 送礼目标玩家ID
	GiftMessage    string `json:"gift_message" gorm:"size:256"`     // 祝福语
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TableName GORM表名
func (Order) TableName() string { return "shop_orders" }

// NewOrder 创建新订单
func NewOrder(playerID, productID, playerName, productName string, count int, unitPrice int64, currency CurrencyType, paymentMethod PaymentMethod) *Order {
	return &Order{
		ID:            genID("ord"),
		OrderNo:       GenOrderNo(),
		PlayerID:      playerID,
		ProductID:     productID,
		PlayerName:    playerName,
		ProductName:   productName,
		Count:         count,
		UnitPrice:     unitPrice,
		TotalAmount:   unitPrice * int64(count),
		Currency:      currency,
		PaymentMethod: paymentMethod,
		Status:        OrderStatusPending,
		ExpireTime:    timePtr(time.Now().Add(30 * time.Minute)), // 30分钟过期
	}
}

// GenOrderNo 生成商户订单号
func GenOrderNo() string {
	return time.Now().Format("ORD20060102150405") + RandString(8)
}

// RandString 生成随机字符串
func RandString(n int) string {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

// CanCancel 订单是否可取消
func (o *Order) CanCancel() bool {
	return o.Status == OrderStatusPending
}

// CanPay 订单是否可支付
func (o *Order) CanPay() bool {
	if o.Status != OrderStatusPending {
		return false
	}
	if o.ExpireTime != nil && time.Now().After(*o.ExpireTime) {
		return false
	}
	return true
}

// CanRefund 订单是否可退款
func (o *Order) CanRefund() bool {
	return o.Status == OrderStatusPaid || o.Status == OrderStatusDelivered
}

// IsExpired 订单是否已过期
func (o *Order) IsExpired() bool {
	return o.ExpireTime != nil && time.Now().After(*o.ExpireTime) && o.Status == OrderStatusPending
}

// ========== OrderDeliveryLog 发货记录 ==========

// OrderDeliveryLog 发货记录（幂等发货，审计用）
type OrderDeliveryLog struct {
	ID        string    `json:"id" gorm:"primaryKey;size:64"`
	OrderID   string    `json:"order_id" gorm:"uniqueIndex;size:64;not null"`
	PlayerID  string    `json:"player_id" gorm:"index;size:64"`
	ItemID    string    `json:"item_id" gorm:"size:64;not null"`
	ItemCount int       `json:"item_count"`
	Status    string    `json:"status" gorm:"size:16"` // success/failed/retry
	Reason    string    `json:"reason" gorm:"size:256"`
	Retry     int       `json:"retry" gorm:"default:0"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName GORM表名
func (OrderDeliveryLog) TableName() string { return "shop_delivery_logs" }

// ========== ProductStockSnapshot 库存快照 ==========

// ProductStockSnapshot 商品库存快照（用于每日重置限购）
type ProductStockSnapshot struct {
	ID         string    `json:"id" gorm:"primaryKey;size:64"`
	ProductID  string    `json:"product_id" gorm:"index;size:64;not null"`
	Date       string    `json:"date" gorm:"index;size:10;not null"` // YYYY-MM-DD
	TotalStock int       `json:"total_stock"` // 当日总库存
	SoldCount  int       `json:"sold_count"`  // 当日已售
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TableName GORM表名
func (ProductStockSnapshot) TableName() string { return "shop_stock_snapshots" }

// ========== ProductDailyLimit 用户每日限购记录 ==========

// ProductDailyLimit 用户每日限购记录
type ProductDailyLimit struct {
	ID        string    `json:"id" gorm:"primaryKey;size:64"`
	PlayerID  string    `json:"player_id" gorm:"size:64;not null"`
	ProductID string    `json:"product_id" gorm:"size:64;not null"`
	Date      string    `json:"date" gorm:"size:10;not null"` // YYYY-MM-DD
	Count     int       `json:"count" gorm:"default:0"`       // 当日已购数量
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName GORM表名 + 复合唯一索引
func (ProductDailyLimit) TableName() string { return "shop_daily_limits" }

// GetDailyLimitID 生成每日限购记录ID
func GetDailyLimitID(playerID, productID, date string) string {
	return playerID + "_" + productID + "_" + date
}

// ========== ProductTotalLimit 用户总限购记录 ==========

// ProductTotalLimit 用户总限购记录
type ProductTotalLimit struct {
	ID        string    `json:"id" gorm:"primaryKey;size:64"`
	PlayerID  string    `json:"player_id" gorm:"size:64;not null"`
	ProductID string    `json:"product_id" gorm:"size:64;not null"`
	Count     int       `json:"count" gorm:"default:0"` // 总已购数量
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName GORM表名
func (ProductTotalLimit) TableName() string { return "shop_total_limits" }

// GetTotalLimitID 生成总限购记录ID
func GetTotalLimitID(playerID, productID string) string {
	return playerID + "_" + productID
}

// ========== ProductPurchaseRecord 购买记录（统计用）==========

// ProductPurchaseRecord 用户购买记录（方便统计查询）
type ProductPurchaseRecord struct {
	ID         string        `json:"id" gorm:"primaryKey;size:64"`
	PlayerID   string        `json:"player_id" gorm:"index;size:64;not null"`
	ProductID  string        `json:"product_id" gorm:"index;size:64;not null"`
	OrderID    string        `json:"order_id" gorm:"index;size:64"`
	Count      int           `json:"count"`
	TotalCost  int64         `json:"total_cost"`
	Currency   CurrencyType  `json:"currency" gorm:"size:16"`
	DayDate    string        `json:"day_date" gorm:"index;size:10"` // YYYY-MM-DD
	CreatedAt  time.Time     `json:"created_at"`
}

// TableName GORM表名
func (ProductPurchaseRecord) TableName() string { return "shop_purchase_records" }

// ========== 辅助工具 ==========

// timePtr 返回time.Time指针
func timePtr(t time.Time) *time.Time { return &t }

// strPtr 返回string指针
func strPtr(s string) *string { return &s }

// intPtr 返回int指针
func intPtr(i int) *int { return &i }
