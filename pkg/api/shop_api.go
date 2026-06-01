package api

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/astra-go/astra"
	"github.com/astra-go/game-backend/internal/models"
	"github.com/astra-go/game-backend/internal/services"
	"github.com/astra-go/astra/log"
)

// ShopAPI 商城 API
type ShopAPI struct {
	shopService *services.ShopService
	logger      *log.Logger
}

// NewShopAPI 创建商城 API
func NewShopAPI(shopService *services.ShopService, logger *log.Logger) *ShopAPI {
	return &ShopAPI{shopService: shopService, logger: logger}
}

// RegisterRoutes 注册商城路由
func (api *ShopAPI) RegisterRoutes(app *astra.App) {
	// 商品列表
	app.GET("/api/v1/shop/products", api.ListProducts)
	app.GET("/api/v1/shop/products/:id", api.GetProduct)
	app.GET("/api/v1/shop/products/:id/stock", api.GetStock)

	// 商品管理（管理员）
	app.POST("/api/v1/shop/products", api.CreateProduct)
	app.PUT("/api/v1/shop/products/:id", api.UpdateProduct)
	app.DELETE("/api/v1/shop/products/:id", api.DeleteProduct)

	// 订单
	app.POST("/api/v1/shop/orders", api.CreateOrder)
	app.GET("/api/v1/shop/orders", api.ListOrders)
	app.GET("/api/v1/shop/orders/:id", api.GetOrder)
	app.POST("/api/v1/shop/orders/:id/pay", api.PayOrder)
	app.POST("/api/v1/shop/orders/:id/cancel", api.CancelOrder)
	app.POST("/api/v1/shop/orders/:id/refund", api.RefundOrder)

	// 一键购买
	app.POST("/api/v1/shop/purchase", api.Purchase)
}

// ========== 商品 API ==========

// ListProducts 获取商品列表
// GET /api/v1/shop/products?category=skin&page=1&page_size=20
func (api *ShopAPI) ListProducts(c *astra.Ctx) error {
	category := c.Query("category")
	status := c.DefaultQuery("status", "active")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	products, total, err := api.shopService.ListProducts(c.Request().Context(), category, status, page, pageSize)
	if err != nil {
		api.logger.Error("获取商品列表失败", "error", err)
		return ResponseError(c, 500, "获取商品列表失败")
	}

	return ResponseOK(c, map[string]any{
		"products":  products,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetProduct 获取单个商品
// GET /api/v1/shop/products/:id
func (api *ShopAPI) GetProduct(c *astra.Ctx) error {
	productID := c.Param("id")

	product, err := api.shopService.GetProduct(c.Request().Context(), productID)
	if err != nil {
		if errors.Is(err, services.ErrProductNotFound) {
			return ResponseError(c, 404, "商品不存在")
		}
		api.logger.Error("获取商品失败", "error", err)
		return ResponseError(c, 500, "获取商品失败")
	}

	stock, _ := api.shopService.GetStock(c.Request().Context(), productID)

	return ResponseOK(c, map[string]any{
		"product": product,
		"stock":   stock,
	})
}

type CreateProductRequest struct {
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	Category      models.ProductCategory `json:"category"`
	Price         int64                 `json:"price"`
	OriginalPrice int64                 `json:"original_price"`
	Currency      models.CurrencyType   `json:"currency"`
	StockCount    int                   `json:"stock_count"`
	LimitPerDay   int                   `json:"limit_per_day"`
	LimitPerUser  int                   `json:"limit_per_user"`
	ImageURL      string                `json:"image_url"`
	PreviewURL    string                `json:"preview_url"`
	ItemID        string                `json:"item_id"`
	ItemCount     int                   `json:"item_count"`
	ExtData       string                `json:"ext_data"`
	IsHot         bool                  `json:"is_hot"`
	IsNew         bool                  `json:"is_new"`
	IsLimited     bool                  `json:"is_limited"`
	StartTime     string                `json:"start_time"`
	EndTime       string                `json:"end_time"`
}

// CreateProduct 创建商品（管理员）
// POST /api/v1/shop/products
func (api *ShopAPI) CreateProduct(c *astra.Ctx) error {
	var req CreateProductRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, 400, "参数错误")
	}

	if req.Name == "" || req.Category == "" || req.Price < 0 {
		return ResponseError(c, 400, "缺少必填参数")
	}

	product := &models.Product{
		Name:          req.Name,
		Description:   req.Description,
		Category:      req.Category,
		Price:         req.Price,
		OriginalPrice: req.OriginalPrice,
		Currency:      req.Currency,
		StockCount:    req.StockCount,
		LimitPerDay:   req.LimitPerDay,
		LimitPerUser:  req.LimitPerUser,
		ImageURL:      req.ImageURL,
		PreviewURL:    req.PreviewURL,
		ItemID:        req.ItemID,
		ItemCount:     req.ItemCount,
		ExtData:       req.ExtData,
		IsHot:         req.IsHot,
		IsNew:         req.IsNew,
		IsLimited:     req.IsLimited,
		Status:        "active",
	}

	if req.StartTime != "" {
		if t, err := parseTime(req.StartTime); err == nil {
			product.StartTime = &t
		}
	}
	if req.EndTime != "" {
		if t, err := parseTime(req.EndTime); err == nil {
			product.EndTime = &t
		}
	}

	if err := api.shopService.CreateProduct(c.Request().Context(), product); err != nil {
		api.logger.Error("创建商品失败", "error", err)
		return ResponseError(c, 500, "创建商品失败")
	}

	return ResponseOK(c, map[string]any{"product": product})
}

type UpdateProductRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Price         *int64  `json:"price"`
	OriginalPrice *int64  `json:"original_price"`
	StockCount    *int    `json:"stock_count"`
	LimitPerDay   *int    `json:"limit_per_day"`
	LimitPerUser  *int    `json:"limit_per_user"`
	ImageURL      *string `json:"image_url"`
	PreviewURL    *string `json:"preview_url"`
	ItemID        *string `json:"item_id"`
	ItemCount     *int    `json:"item_count"`
	ExtData       *string `json:"ext_data"`
	IsHot         *bool   `json:"is_hot"`
	IsNew         *bool   `json:"is_new"`
	IsLimited     *bool   `json:"is_limited"`
	Status        *models.ProductStatus `json:"status"`
	StartTime     *string `json:"start_time"`
	EndTime       *string `json:"end_time"`
}

// UpdateProduct 更新商品（管理员）
// PUT /api/v1/shop/products/:id
func (api *ShopAPI) UpdateProduct(c *astra.Ctx) error {
	productID := c.Param("id")

	product, err := api.shopService.GetProduct(c.Request().Context(), productID)
	if err != nil {
		if errors.Is(err, services.ErrProductNotFound) {
			return ResponseError(c, 404, "商品不存在")
		}
		return ResponseError(c, 500, "获取商品失败")
	}

	var req UpdateProductRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, 400, "参数错误")
	}

	if req.Name != nil {
		product.Name = *req.Name
	}
	if req.Description != nil {
		product.Description = *req.Description
	}
	if req.Price != nil {
		product.Price = *req.Price
	}
	if req.OriginalPrice != nil {
		product.OriginalPrice = *req.OriginalPrice
	}
	if req.StockCount != nil {
		product.StockCount = *req.StockCount
	}
	if req.LimitPerDay != nil {
		product.LimitPerDay = *req.LimitPerDay
	}
	if req.LimitPerUser != nil {
		product.LimitPerUser = *req.LimitPerUser
	}
	if req.ImageURL != nil {
		product.ImageURL = *req.ImageURL
	}
	if req.PreviewURL != nil {
		product.PreviewURL = *req.PreviewURL
	}
	if req.ItemID != nil {
		product.ItemID = *req.ItemID
	}
	if req.ItemCount != nil {
		product.ItemCount = *req.ItemCount
	}
	if req.ExtData != nil {
		product.ExtData = *req.ExtData
	}
	if req.IsHot != nil {
		product.IsHot = *req.IsHot
	}
	if req.IsNew != nil {
		product.IsNew = *req.IsNew
	}
	if req.IsLimited != nil {
		product.IsLimited = *req.IsLimited
	}
	if req.Status != nil {
		product.Status = *req.Status
	}
	if req.StartTime != nil {
		if t, err := parseTime(*req.StartTime); err == nil {
			product.StartTime = &t
		}
	}
	if req.EndTime != nil {
		if t, err := parseTime(*req.EndTime); err == nil {
			product.EndTime = &t
		}
	}

	if err := api.shopService.UpdateProduct(c.Request().Context(), product); err != nil {
		api.logger.Error("更新商品失败", "error", err)
		return ResponseError(c, 500, "更新商品失败")
	}

	return ResponseOK(c, map[string]any{"product": product})
}

// DeleteProduct 删除商品（管理员）
// DELETE /api/v1/shop/products/:id
func (api *ShopAPI) DeleteProduct(c *astra.Ctx) error {
	productID := c.Param("id")

	if err := api.shopService.DeleteProduct(c.Request().Context(), productID); err != nil {
		api.logger.Error("删除商品失败", "error", err)
		return ResponseError(c, 500, "删除商品失败")
	}

	return ResponseOK(c, nil)
}

// ========== 订单 API ==========

type CreateOrderRequest struct {
	ProductID     string               `json:"product_id"`
	Count         int                  `json:"count"`
	PaymentMethod models.PaymentMethod `json:"payment_method"`
}

// CreateOrder 创建订单
// POST /api/v1/shop/orders
func (api *ShopAPI) CreateOrder(c *astra.Ctx) error {
	playerID := c.GetString("player_id")
	if playerID == "" {
		return ResponseError(c, 401, "未登录")
	}

	var req CreateOrderRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, 400, "参数错误")
	}

	if req.ProductID == "" || req.Count <= 0 {
		return ResponseError(c, 400, "缺少必填参数")
	}

	order, err := api.shopService.CreateOrder(c.Request().Context(), playerID, req.ProductID, req.Count, req.PaymentMethod)
	if err != nil {
		return handleShopError(c, err)
	}

	return ResponseOK(c, map[string]any{"order": order})
}

// ListOrders 获取订单列表
// GET /api/v1/shop/orders?status=pending&page=1&page_size=20
func (api *ShopAPI) ListOrders(c *astra.Ctx) error {
	playerID := c.GetString("player_id")
	if playerID == "" {
		return ResponseError(c, 401, "未登录")
	}

	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	orders, total, err := api.shopService.ListOrders(c.Request().Context(), playerID, status, page, pageSize)
	if err != nil {
		api.logger.Error("获取订单列表失败", "error", err)
		return ResponseError(c, 500, "获取订单列表失败")
	}

	return ResponseOK(c, map[string]any{
		"orders":    orders,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetOrder 获取订单详情
// GET /api/v1/shop/orders/:id
func (api *ShopAPI) GetOrder(c *astra.Ctx) error {
	playerID := c.GetString("player_id")
	if playerID == "" {
		return ResponseError(c, 401, "未登录")
	}

	orderID := c.Param("id")

	order, err := api.shopService.GetOrder(c.Request().Context(), orderID)
	if err != nil {
		return handleShopError(c, err)
	}

	if order.PlayerID != playerID {
		return ResponseError(c, 403, "无权访问此订单")
	}

	return ResponseOK(c, map[string]any{"order": order})
}

// PayOrder 支付订单（内部货币）
// POST /api/v1/shop/orders/:id/pay
func (api *ShopAPI) PayOrder(c *astra.Ctx) error {
	playerID := c.GetString("player_id")
	if playerID == "" {
		return ResponseError(c, 401, "未登录")
	}

	orderID := c.Param("id")

	if err := api.shopService.PayOrder(c.Request().Context(), orderID, playerID); err != nil {
		return handleShopError(c, err)
	}

	order, _ := api.shopService.GetOrder(c.Request().Context(), orderID)
	return ResponseOK(c, map[string]any{"order": order, "message": "支付成功"})
}

// CancelOrder 取消订单
// POST /api/v1/shop/orders/:id/cancel
func (api *ShopAPI) CancelOrder(c *astra.Ctx) error {
	playerID := c.GetString("player_id")
	if playerID == "" {
		return ResponseError(c, 401, "未登录")
	}

	orderID := c.Param("id")

	if err := api.shopService.CancelOrder(c.Request().Context(), orderID, playerID); err != nil {
		return handleShopError(c, err)
	}

	return ResponseOK(c, map[string]any{"message": "订单已取消"})
}

// RefundOrder 退款订单（管理员）
// POST /api/v1/shop/orders/:id/refund
func (api *ShopAPI) RefundOrder(c *astra.Ctx) error {
	orderID := c.Param("id")

	if err := api.shopService.RefundOrder(c.Request().Context(), orderID); err != nil {
		return handleShopError(c, err)
	}

	return ResponseOK(c, map[string]any{"message": "退款成功"})
}

// ========== 购买 API ==========

type PurchaseRequest struct {
	ProductID     string               `json:"product_id"`
	Count         int                  `json:"count"`
	PaymentMethod models.PaymentMethod `json:"payment_method"`
}

// Purchase 一键购买（内部货币）
// POST /api/v1/shop/purchase
func (api *ShopAPI) Purchase(c *astra.Ctx) error {
	playerID := c.GetString("player_id")
	if playerID == "" {
		return ResponseError(c, 401, "未登录")
	}

	var req PurchaseRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, 400, "参数错误")
	}

	if req.ProductID == "" || req.Count <= 0 {
		return ResponseError(c, 400, "缺少必填参数")
	}

	order, err := api.shopService.Purchase(c.Request().Context(), playerID, req.ProductID, req.Count, req.PaymentMethod)
	if err != nil {
		return handleShopError(c, err)
	}

	return ResponseOK(c, map[string]any{
		"order":       order,
		"message":     "购买成功",
		"order_no":    order.OrderNo,
		"total_price": order.TotalAmount,
		"currency":    order.Currency,
	})
}

// ========== 库存 API ==========

// GetStock 获取库存
// GET /api/v1/shop/products/:id/stock
func (api *ShopAPI) GetStock(c *astra.Ctx) error {
	productID := c.Param("id")

	stock, err := api.shopService.GetStock(c.Request().Context(), productID)
	if err != nil {
		api.logger.Error("获取库存失败", "error", err)
		return ResponseError(c, 500, "获取库存失败")
	}

	return ResponseOK(c, map[string]any{
		"product_id": productID,
		"stock":      stock,
		"infinite":    stock < 0,
	})
}

// ========== 辅助函数 ==========

func handleShopError(c *astra.Ctx, err error) error {
	switch {
	case errors.Is(err, services.ErrProductNotFound):
		return ResponseError(c, 404, "商品不存在")
	case errors.Is(err, services.ErrProductOffShelf):
		return ResponseError(c, 400, "商品已下架")
	case errors.Is(err, services.ErrProductOutOfStock):
		return ResponseError(c, 400, "商品库存不足")
	case errors.Is(err, services.ErrInsufficientBalance):
		return ResponseError(c, 400, "余额不足")
	case errors.Is(err, services.ErrDailyLimitExceeded):
		return ResponseError(c, 400, "今日购买次数已达上限")
	case errors.Is(err, services.ErrTotalLimitExceeded):
		return ResponseError(c, 400, "总购买次数已达上限")
	case errors.Is(err, services.ErrOrderNotFound):
		return ResponseError(c, 404, "订单不存在")
	case errors.Is(err, services.ErrOrderAlreadyPaid):
		return ResponseError(c, 400, "订单已支付")
	case errors.Is(err, services.ErrOrderExpired):
		return ResponseError(c, 400, "订单已过期")
	default:
		return ResponseError(c, 500, "操作失败")
	}
}

func parseTime(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}
