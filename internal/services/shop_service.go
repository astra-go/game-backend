package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/astra-go/game-backend/internal/models"
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/payment"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrProductNotFound     = errors.New("商品不存在")
	ErrProductOffShelf     = errors.New("商品已下架")
	ErrProductOutOfStock   = errors.New("商品库存不足")
	ErrInsufficientBalance = errors.New("余额不足")
	ErrDailyLimitExceeded  = errors.New("今日购买次数已达上限")
	ErrTotalLimitExceeded  = errors.New("总购买次数已达上限")
	ErrOrderNotFound       = errors.New("订单不存在")
	ErrOrderAlreadyPaid    = errors.New("订单已支付")
	ErrOrderExpired        = errors.New("订单已过期")
)

// ShopService 商城服务
type ShopService struct {
	db      *gorm.DB
	redis   *redis.Client
	payment *payment.Service
}

// NewShopService 创建商城服务
func NewShopService(db *gorm.DB, redis *redis.Client) *ShopService {
	return &ShopService{db: db, redis: redis}
}

// SetPaymentService 设置支付服务
func (s *ShopService) SetPaymentService(p *payment.Service) {
	s.payment = p
}

// ========== 商品管理 ==========

// ListProducts 获取商品列表
func (s *ShopService) ListProducts(ctx context.Context, category string, status string, page, pageSize int) ([]*models.Product, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.Product{})
	
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	} else {
		query = query.Where("status = ?", "active")
	}
	
	// 只显示在有效期内的商品
	query = query.Where("start_time IS NULL OR start_time <= ?", time.Now())
	query = query.Where("end_time IS NULL OR end_time >= ?", time.Now())
	
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	
	var products []*models.Product
	offset := (page - 1) * pageSize
	if err := query.Order("sort_order DESC, created_at DESC").Offset(offset).Limit(pageSize).Find(&products).Error; err != nil {
		return nil, 0, err
	}
	
	return products, total, nil
}

// GetProduct 获取单个商品
func (s *ShopService) GetProduct(ctx context.Context, productID string) (*models.Product, error) {
	var product models.Product
	key := fmt.Sprintf("product:%s", productID)
	
	// 尝试从 Redis 获取
	if s.redis != nil {
		data, err := s.redis.Get(ctx, key).Bytes()
		if err == nil {
			if err := json.Unmarshal(data, &product); err == nil {
				return &product, nil
			}
		}
	}
	
	// 从数据库获取
	if err := s.db.WithContext(ctx).Where("id = ?", productID).First(&product).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProductNotFound
		}
		return nil, err
	}
	
	// 缓存到 Redis（10分钟）
	if s.redis != nil {
		if data, err := json.Marshal(product); err == nil {
			s.redis.Set(ctx, key, data, 10*time.Minute)
		}
	}
	
	return &product, nil
}

// CreateProduct 创建商品
func (s *ShopService) CreateProduct(ctx context.Context, product *models.Product) error {
	if product.ID == "" {
		product.ID = fmt.Sprintf("p_%d", time.Now().UnixNano())
	}
	return s.db.WithContext(ctx).Create(product).Error
}

// UpdateProduct 更新商品
func (s *ShopService) UpdateProduct(ctx context.Context, product *models.Product) error {
	// 清除缓存
	if s.redis != nil {
		key := fmt.Sprintf("product:%s", product.ID)
		s.redis.Del(ctx, key)
	}
	return s.db.WithContext(ctx).Save(product).Error
}

// DeleteProduct 删除商品
func (s *ShopService) DeleteProduct(ctx context.Context, productID string) error {
	// 清除缓存
	if s.redis != nil {
		key := fmt.Sprintf("product:%s", productID)
		s.redis.Del(ctx, key)
	}
	return s.db.WithContext(ctx).Delete(&models.Product{}, "id = ?", productID).Error
}

// ========== 库存管理 ==========

// GetStock 获取商品库存（优先 Redis）
func (s *ShopService) GetStock(ctx context.Context, productID string) (int, error) {
	key := fmt.Sprintf("product:stock:%s", productID)
	
	// 尝试从 Redis 获取
	if s.redis != nil {
		count, err := s.redis.Get(ctx, key).Int()
		if err == nil {
			return count, nil
		}
	}
	
	// 从数据库获取
	var product models.Product
	if err := s.db.WithContext(ctx).Where("id = ?", productID).Select("stock_count").First(&product).Error; err != nil {
		return 0, err
	}
	
	// 缓存到 Redis（1分钟）
	if s.redis != nil {
		s.redis.Set(ctx, key, product.StockCount, time.Minute)
	}
	
	return product.StockCount, nil
}

// DeductStock 扣减库存（原子操作）
func (s *ShopService) DeductStock(ctx context.Context, productID string, count int) error {
	key := fmt.Sprintf("product:stock:%s", productID)
	
	if s.redis != nil {
		// 使用 Lua 脚本保证原子性
		script := redis.NewScript(`
			local stock = redis.call('GET', KEYS[1])
			if stock == false then
				return -2  -- 缓存未命中
			end
			stock = tonumber(stock)
			local need = tonumber(ARGV[1])
			if stock < need then
				return -1  -- 库存不足
			end
			redis.call('DECRBY', KEYS[1], need)
			return stock - need
		`)
		
		result, err := script.Run(ctx, s.redis, []string{key}, count).Int()
		if err == nil {
			if result == -1 {
				return ErrProductOutOfStock
			}
			if result == -2 {
				// 缓存未命中，需要查库
			} else {
				return nil
			}
		}
	}
	
	// 数据库原子扣减
	result := s.db.WithContext(ctx).Model(&models.Product{}).
		Where("id = ? AND stock_count >= ?", productID, count).
		Update("stock_count", gorm.Expr("stock_count - ?", count))
	
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrProductOutOfStock
	}
	
	return nil
}

// RestoreStock 恢复库存（退款时）
func (s *ShopService) RestoreStock(ctx context.Context, productID string, count int) error {
	// 清除库存缓存
	if s.redis != nil {
		key := fmt.Sprintf("product:stock:%s", productID)
		s.redis.Del(ctx, key)
	}
	
	// 数据库恢复
	return s.db.WithContext(ctx).Model(&models.Product{}).
		Where("id = ?", productID).
		Update("stock_count", gorm.Expr("stock_count + ?", count)).Error
}

// ========== 限购检查 ==========

// CheckDailyLimit 检查每日限购
func (s *ShopService) CheckDailyLimit(ctx context.Context, playerID, productID string) (bool, int, error) {
	today := time.Now().Format("2006-01-02")
	key := fmt.Sprintf("product:limit:daily:%s:%s:%s", playerID, productID, today)
	
	// 尝试从 Redis 获取
	if s.redis != nil {
		count, err := s.redis.Get(ctx, key).Int()
		if err == nil {
			return true, count, nil
		}
	}
	
	// 从数据库获取
	var record models.ProductDailyLimit
	err := s.db.WithContext(ctx).Where(
		"player_id = ? AND product_id = ? AND date = ?", playerID, productID, today,
	).First(&record).Error
	
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	
	// 缓存到 Redis（当日剩余时间）
	if s.redis != nil {
		ttl := time.Duration(23-time.Now().Hour())*time.Hour + time.Duration(60-time.Now().Minute())*time.Minute
		if ttl > 0 {
			s.redis.Set(ctx, key, record.Count, ttl)
		}
	}
	
	return true, record.Count, nil
}

// CheckTotalLimit 检查总限购
func (s *ShopService) CheckTotalLimit(ctx context.Context, playerID, productID string) (bool, int, error) {
	key := fmt.Sprintf("product:limit:total:%s:%s", playerID, productID)
	
	// 尝试从 Redis 获取
	if s.redis != nil {
		count, err := s.redis.Get(ctx, key).Int()
		if err == nil {
			return true, count, nil
		}
	}
	
	// 从数据库获取
	var record models.ProductTotalLimit
	err := s.db.WithContext(ctx).Where(
		"player_id = ? AND product_id = ?", playerID, productID,
	).First(&record).Error
	
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	
	// 缓存到 Redis（1小时）
	if s.redis != nil {
		s.redis.Set(ctx, key, record.Count, time.Hour)
	}
	
	return true, record.Count, nil
}

// IncrementDailyLimit 增加每日购买计数
func (s *ShopService) IncrementDailyLimit(ctx context.Context, playerID, productID string, count int) error {
	today := time.Now().Format("2006-01-02")
	
	// Redis 原子递增
	if s.redis != nil {
		key := fmt.Sprintf("product:limit:daily:%s:%s:%s", playerID, productID, today)
		s.redis.IncrBy(ctx, key, int64(count))
		// 设置过期时间（次日0点）
		ttl := time.Duration(23-time.Now().Hour())*time.Hour + time.Duration(60-time.Now().Minute())*time.Minute + time.Second
		s.redis.Expire(ctx, key, ttl+time.Hour)
	}
	
	// 数据库记录
	record := &models.ProductDailyLimit{
		ID:        fmt.Sprintf("dl_%d", time.Now().UnixNano()),
		PlayerID:  playerID,
		ProductID: productID,
		Date:      today,
		Count:     count,
	}
	
	return s.db.WithContext(ctx).
		Exec(`INSERT INTO product_daily_limits (id, player_id, product_id, date, count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE count = count + ?`,
			record.ID, record.PlayerID, record.ProductID, record.Date, record.Count, time.Now(), time.Now(), count).
		Error
}

// IncrementTotalLimit 增加总购买计数
func (s *ShopService) IncrementTotalLimit(ctx context.Context, playerID, productID string, count int) error {
	// Redis 原子递增
	if s.redis != nil {
		key := fmt.Sprintf("product:limit:total:%s:%s", playerID, productID)
		s.redis.IncrBy(ctx, key, int64(count))
	}
	
	// 数据库记录
	record := &models.ProductTotalLimit{
		ID:        fmt.Sprintf("tl_%d", time.Now().UnixNano()),
		PlayerID:  playerID,
		ProductID: productID,
		Count:     count,
	}
	
	return s.db.WithContext(ctx).
		Exec(`INSERT INTO product_total_limits (id, player_id, product_id, count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE count = count + ?`,
			record.ID, record.PlayerID, record.ProductID, record.Count, time.Now(), time.Now(), count).
		Error
}

// ========== 订单管理 ==========

// CreateOrder 创建订单
func (s *ShopService) CreateOrder(ctx context.Context, playerID, productID string, count int, paymentMethod models.PaymentMethod) (*models.Order, error) {
	// 1. 获取商品信息
	product, err := s.GetProduct(ctx, productID)
	if err != nil {
		return nil, err
	}
	
	// 2. 检查商品状态
	if product.Status != "active" {
		return nil, ErrProductOffShelf
	}
	
	// 3. 检查库存
	if product.StockCount >= 0 && product.StockCount < count {
		return nil, ErrProductOutOfStock
	}
	
	// 4. 检查每日限购
	if product.LimitPerDay > 0 {
		_, dailyCount, err := s.CheckDailyLimit(ctx, playerID, productID)
		if err != nil {
			return nil, err
		}
		if dailyCount+count > product.LimitPerDay {
			return nil, ErrDailyLimitExceeded
		}
	}
	
	// 5. 检查总限购
	if product.LimitPerUser > 0 {
		_, totalCount, err := s.CheckTotalLimit(ctx, playerID, productID)
		if err != nil {
			return nil, err
		}
		if totalCount+count > product.LimitPerUser {
			return nil, ErrTotalLimitExceeded
		}
	}
	
	// 6. 计算总价
	totalAmount := product.Price * int64(count)
	
	// 7. 快照商品信息
	snapshot, _ := json.Marshal(product)
	
	// 8. 创建订单
	order := &models.Order{
		ID:              fmt.Sprintf("o_%d", time.Now().UnixNano()),
		OrderNo:         fmt.Sprintf("ASTRA%s%d", time.Now().Format("20060102150405"), time.Now().Nanosecond()%10000),
		PlayerID:        playerID,
		ProductID:       productID,
		ProductSnapshot: string(snapshot),
		Count:           count,
		TotalAmount:     totalAmount,
		Currency:        product.Currency,
		PaymentMethod:   paymentMethod,
		Status:          models.OrderStatusPending,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	
	// 设置订单过期时间（15分钟）
	expireTime := time.Now().Add(15 * time.Minute)
	order.ExpireTime = &expireTime
	
	if err := s.db.WithContext(ctx).Create(order).Error; err != nil {
		return nil, err
	}
	
	return order, nil
}

// GetOrder 获取订单
func (s *ShopService) GetOrder(ctx context.Context, orderID string) (*models.Order, error) {
	var order models.Order
	if err := s.db.WithContext(ctx).Where("id = ?", orderID).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	return &order, nil
}

// GetOrderByOrderNo 根据订单号获取订单
func (s *ShopService) GetOrderByOrderNo(ctx context.Context, orderNo string) (*models.Order, error) {
	var order models.Order
	if err := s.db.WithContext(ctx).Where("order_no = ?", orderNo).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	return &order, nil
}

// ListOrders 获取用户订单列表
func (s *ShopService) ListOrders(ctx context.Context, playerID string, status string, page, pageSize int) ([]*models.Order, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.Order{}).Where("player_id = ?", playerID)
	
	if status != "" {
		query = query.Where("status = ?", status)
	}
	
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	
	var orders []*models.Order
	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	
	return orders, total, nil
}

// PayOrder 支付订单（内部货币支付）
func (s *ShopService) PayOrder(ctx context.Context, orderID, playerID string) error {
	// 1. 获取订单
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	
	// 2. 验证订单归属
	if order.PlayerID != playerID {
		return ErrOrderNotFound
	}
	
	// 3. 检查订单状态
	if order.Status != models.OrderStatusPending {
		return ErrOrderAlreadyPaid
	}
	
	// 4. 检查订单过期
	if order.ExpireTime != nil && time.Now().After(*order.ExpireTime) {
		return s.ExpireOrder(ctx, orderID)
	}
	
	// 5. 扣减库存
	if err := s.DeductStock(ctx, order.ProductID, order.Count); err != nil {
		return err
	}
	
	// 6. 更新订单状态为已支付
	now := time.Now()
	return s.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", orderID).Updates(map[string]any{
		"status":    models.OrderStatusPaid,
		"pay_time":  &now,
		"updated_at": &now,
	}).Error
}

// DeliverOrder 发货订单
func (s *ShopService) DeliverOrder(ctx context.Context, orderID string) error {
	// 1. 获取订单
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	
	// 2. 检查订单状态
	if order.Status != models.OrderStatusPaid {
		return errors.New("订单状态不正确")
	}
	
	// 3. 更新限购计数
	if err := s.IncrementDailyLimit(ctx, order.PlayerID, order.ProductID, order.Count); err != nil {
		return err
	}
	if err := s.IncrementTotalLimit(ctx, order.PlayerID, order.ProductID, order.Count); err != nil {
		return err
	}
	
	// 4. 记录购买记录
	record := &models.ProductPurchaseRecord{
		ID:        fmt.Sprintf("pr_%d", time.Now().UnixNano()),
		PlayerID:  order.PlayerID,
		ProductID: order.ProductID,
		Count:     order.Count,
		TotalCost: order.TotalAmount,
		Currency:  order.Currency,
		DayDate:   time.Now().Format("2006-01-02"),
		CreatedAt: time.Now(),
	}
	if err := s.db.WithContext(ctx).Create(record).Error; err != nil {
		return err
	}
	
	// 5. 更新订单状态为已发货
	now := time.Now()
	return s.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", orderID).Updates(map[string]any{
		"status":       models.OrderStatusDelivered,
		"deliver_time": &now,
		"updated_at":   &now,
	}).Error
}

// CancelOrder 取消订单
func (s *ShopService) CancelOrder(ctx context.Context, orderID, playerID string) error {
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	
	if order.PlayerID != playerID {
		return ErrOrderNotFound
	}
	
	if order.Status != models.OrderStatusPending {
		return errors.New("只有待支付订单可以取消")
	}
	
	now := time.Now()
	return s.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", orderID).Updates(map[string]any{
		"status":     models.OrderStatusCancelled,
		"updated_at": &now,
	}).Error
}

// ExpireOrder 过期订单
func (s *ShopService) ExpireOrder(ctx context.Context, orderID string) error {
	now := time.Now()
	return s.db.WithContext(ctx).Model(&models.Order{}).Where("id = ? AND status = ?", orderID, models.OrderStatusPending).Updates(map[string]any{
		"status":     models.OrderStatusExpired,
		"updated_at": &now,
	}).Error
}

// RefundOrder 退款订单
func (s *ShopService) RefundOrder(ctx context.Context, orderID string) error {
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	
	if order.Status != models.OrderStatusPaid && order.Status != models.OrderStatusDelivered {
		return errors.New("只有已支付或已发货的订单可以退款")
	}
	
	// 1. 恢复库存
	if err := s.RestoreStock(ctx, order.ProductID, order.Count); err != nil {
		return err
	}
	
	// 2. 减少限购计数（尽量减少，可能不够扣）
	if err := s.db.WithContext(ctx).Model(&models.ProductDailyLimit{}).
		Where("player_id = ? AND product_id = ?", order.PlayerID, order.ProductID).
		Update("count", gorm.Expr("GREATEST(count - ?)", order.Count)).Error; err != nil {
		// 忽略错误
	}
	
	if err := s.db.WithContext(ctx).Model(&models.ProductTotalLimit{}).
		Where("player_id = ? AND product_id = ?", order.PlayerID, order.ProductID).
		Update("count", gorm.Expr("GREATEST(count - ?)", order.Count)).Error; err != nil {
		// 忽略错误
	}
	
	// 3. 更新订单状态
	now := time.Now()
	return s.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", orderID).Updates(map[string]any{
		"status":     models.OrderStatusRefunded,
		"updated_at": &now,
	}).Error
}

// ========== 玩家余额 ==========

// DeductPlayerBalance 扣减玩家余额（需要集成玩家服务）
func (s *ShopService) DeductPlayerBalance(ctx context.Context, playerID string, amount int64, currency models.CurrencyType) error {
	var field string
	switch currency {
	case models.CurrencyDiamond:
		field = "diamond"
	case models.CurrencyGold:
		field = "gold"
	case models.CurrencyCoupon:
		field = "coupon"
	default:
		return errors.New("不支持的货币类型")
	}
	
	result := s.db.WithContext(ctx).Model(&common.Player{}).
		Where("id = ?", playerID).
		Where(field+" >= ?", amount).
		Update(field, gorm.Expr(field+" - ?", amount))
	
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInsufficientBalance
	}
	
	return nil
}

// AddPlayerBalance 增加玩家余额（需要集成玩家服务）
func (s *ShopService) AddPlayerBalance(ctx context.Context, playerID string, amount int64, currency models.CurrencyType) error {
	var field string
	switch currency {
	case models.CurrencyDiamond:
		field = "diamond"
	case models.CurrencyGold:
		field = "gold"
	case models.CurrencyCoupon:
		field = "coupon"
	default:
		return errors.New("不支持的货币类型")
	}
	
	return s.db.WithContext(ctx).Model(&common.Player{}).
		Where("id = ?", playerID).
		Update(field, gorm.Expr(field+" + ?", amount)).Error
}

// ========== 数据库迁移 ==========

// AutoMigrate 自动迁移数据库表
func (s *ShopService) AutoMigrate() error {
	return s.db.AutoMigrate(
		&models.Product{},
		&models.ProductPurchaseRecord{},
		&models.Order{},
		&models.ProductStockSnapshot{},
		&models.ProductDailyLimit{},
		&models.ProductTotalLimit{},
	)
}

// DB 暴露数据库连接（供测试用）
func (s *ShopService) DB() *gorm.DB {
	return s.db
}

// ========== 商品购买入口（完整流程） ==========

// Purchase 商品购买（完整流程）
func (s *ShopService) Purchase(ctx context.Context, playerID, productID string, count int, paymentMethod models.PaymentMethod) (*models.Order, error) {
	// 1. 创建订单
	order, err := s.CreateOrder(ctx, playerID, productID, count, paymentMethod)
	if err != nil {
		return nil, err
	}
	
	// 2. 如果是内部货币支付，直接处理
	if paymentMethod == models.PaymentMethodDiamond || paymentMethod == models.PaymentMethodGold || paymentMethod == models.PaymentMethodCoupon {
		// 扣减余额
		if err := s.DeductPlayerBalance(ctx, playerID, order.TotalAmount, order.Currency); err != nil {
			// 取消订单
			s.CancelOrder(ctx, order.ID, playerID)
			return nil, err
		}
		
		// 支付订单
		if err := s.PayOrder(ctx, order.ID, playerID); err != nil {
			// 恢复余额
			s.AddPlayerBalance(ctx, playerID, order.TotalAmount, order.Currency)
			s.CancelOrder(ctx, order.ID, playerID)
			return nil, err
		}
		
		// 发货
		if err := s.DeliverOrder(ctx, order.ID); err != nil {
			return nil, err
		}
		
		return order, nil
	}
	
	// 3. 第三方支付，返回订单等待支付
	return order, nil
}

// CompleteThirdPartyPayment 完成第三方支付
func (s *ShopService) CompleteThirdPartyPayment(ctx context.Context, orderID, thirdPartyID, thirdPartyResp string) error {
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	
	if order.Status != models.OrderStatusPending {
		return ErrOrderAlreadyPaid
	}
	
	// 扣减库存
	if err := s.DeductStock(ctx, order.ProductID, order.Count); err != nil {
		return err
	}
	
	// 更新订单
	now := time.Now()
	updates := map[string]any{
		"status":           models.OrderStatusPaid,
		"pay_time":         &now,
		"third_party_id":   thirdPartyID,
		"third_party_resp": thirdPartyResp,
		"updated_at":       &now,
	}
	
	if err := s.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", orderID).Updates(updates).Error; err != nil {
		return err
	}
	
	// 发货
	return s.DeliverOrder(ctx, orderID)
}

// ========== 支付集成 ==========

// CreatePayment 创建第三方支付订单
func (s *ShopService) CreatePayment(ctx context.Context, orderID string, channel payment.Channel, clientIP string) (*payment.ChargeResponse, error) {
	if s.payment == nil {
		return nil, errors.New("支付服务未配置")
	}

	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.Status != models.OrderStatusPending {
		return nil, ErrOrderAlreadyPaid
	}

	product, err := s.GetProduct(ctx, order.ProductID)
	if err != nil {
		return nil, err
	}

	// 生成支付订单号
	orderNo := s.genOrderNo()

	req := &payment.ChargeRequest{
		OrderID:   order.ID,
		OrderNo:   orderNo,
		Amount:    order.TotalAmount,
		Currency:  "CNY",
		Subject:   product.Name,
		Body:      product.Description,
		Channel:   channel,
		PlayerID:  order.PlayerID,
		ProductID: order.ProductID,
		NotifyURL: s.getPaymentNotifyURL(channel),
		ClientIP:  clientIP,
	}

	// 存储支付订单号
	updates := map[string]any{"payment_order_no": orderNo}
	s.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", orderID).Updates(updates)

	return s.payment.Charge(ctx, req)
}

// HandlePaymentNotify 处理支付回调
func (s *ShopService) HandlePaymentNotify(ctx context.Context, channel payment.Channel, params map[string]string, body []byte) *payment.NotifyResult {
	if s.payment == nil {
		return &payment.NotifyResult{Handled: false, Reply: "fail", Message: "支付服务未配置"}
	}
	return s.payment.HandleNotify(ctx, channel, payment.NotifyTypePayment, params, body)
}

// RefundPayment 退款
func (s *ShopService) RefundPayment(ctx context.Context, orderID string, reason, operatorID string) (*payment.RefundResponse, error) {
	if s.payment == nil {
		return nil, errors.New("支付服务未配置")
	}

	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.Status != models.OrderStatusPaid {
		return nil, errors.New("订单未支付，无法退款")
	}

	refundID := fmt.Sprintf("REF%s%d", time.Now().Format("20060102150405"), time.Now().Nanosecond()/100000%10000)

	req := &payment.RefundRequest{
		OrderID:        order.ID,
		ChannelOrderNo: order.PaymentOrderNo,
		RefundID:       refundID,
		Amount:         order.TotalAmount,
		Reason:         reason,
		OperatorID:     operatorID,
	}

	resp, err := s.payment.Refund(ctx, req)
	if err != nil {
		return nil, err
	}

	// 更新订单状态
	s.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", orderID).Updates(map[string]any{
		"status":      models.OrderStatusRefunded,
		"refund_time": time.Now(),
	})

	// 恢复库存
	s.RestoreStock(ctx, order.ProductID, order.Count)

	return resp, nil
}

// ListEnabledPaymentChannels 获取启用的支付通道
func (s *ShopService) ListEnabledPaymentChannels() []payment.Channel {
	if s.payment == nil {
		return nil
	}
	return s.payment.ListEnabledChannels()
}

func (s *ShopService) genOrderNo() string {
	return fmt.Sprintf("ORD%s%d%04d",
		time.Now().Format("20060102"),
		time.Now().Unix()%100000,
		time.Now().Nanosecond()/100000%10000)
}

func (s *ShopService) getPaymentNotifyURL(channel payment.Channel) string {
	// 根据不同支付通道返回对应的回调地址
	switch channel {
	case payment.ChannelAlipay:
		return "https://your-domain.com/api/payment/alipay/notify"
	case payment.ChannelWechat:
		return "https://your-domain.com/api/payment/wechat/notify"
	default:
		return "https://your-domain.com/api/payment/notify"
	}
}

