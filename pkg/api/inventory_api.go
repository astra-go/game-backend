package api

import (
	"net/http"
	"strconv"

	"github.com/astra-go/game-backend/pkg/inventory"
	"github.com/astra-go/game-backend/pkg/middleware"
	"github.com/astra-go/astra"
	"go.uber.org/zap"
)

// InventoryAPI 背包API路由组
type InventoryAPI struct {
	ic     *inventory.InventoryComponent
	logger *zap.Logger
}

// NewInventoryAPI 创建背包API实例
func NewInventoryAPI(ic *inventory.InventoryComponent, logger *zap.Logger) *InventoryAPI {
	return &InventoryAPI{
		ic:     ic,
		logger: logger,
	}
}

// RegisterRoutes 注册所有背包相关路由
func (api *InventoryAPI) RegisterRoutes(app *astra.App) {
	// 需要认证的路由
	app.GET("/api/v1/inventory", api.AuthRequired(), api.GetInventory)
	app.POST("/api/v1/inventory/use", api.AuthRequired(), api.UseItem)
	app.POST("/api/v1/inventory/equip", api.AuthRequired(), api.EquipItem)
	app.POST("/api/v1/inventory/unequip", api.AuthRequired(), api.UnequipItem)
	app.GET("/api/v1/inventory/equipped", api.AuthRequired(), api.GetEquipped)
	app.POST("/api/v1/inventory/swap", api.AuthRequired(), api.SwapSlots)
	app.POST("/api/v1/inventory/remove", api.AuthRequired(), api.RemoveItem)

	// 物品模板路由（公开）
	app.GET("/api/v1/items", api.ListItemTemplates)
	app.GET("/api/v1/items/:id", api.GetItemTemplate)
}

// AuthRequired 返回认证中间件
func (api *InventoryAPI) AuthRequired() astra.HandlerFunc {
	return middleware.AuthMiddleware(api.logger)
}

// GetInventory 获取玩家背包
// GET /api/v1/inventory
func (api *InventoryAPI) GetInventory(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	slots, err := api.ic.GetInventory(pid)
	if err != nil {
		api.logger.Error("获取背包失败", zap.String("player_id", pid), zap.Error(err))
		return ResponseError(c, http.StatusInternalServerError, "获取背包失败")
	}

	return ResponseOK(c, slots)
}

// UseItem 使用物品
// POST /api/v1/inventory/use
func (api *InventoryAPI) UseItem(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	var req struct {
		SlotIndex int32 `json:"slot_index" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("使用物品请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	result, err := api.ic.UseItem(pid, req.SlotIndex)
	if err != nil {
		api.logger.Warn("使用物品失败",
			zap.String("player_id", pid),
			zap.Int32("slot_index", req.SlotIndex),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, result)
}

// EquipItem 装备物品
// POST /api/v1/inventory/equip
func (api *InventoryAPI) EquipItem(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	var req struct {
		SlotIndex int32 `json:"slot_index" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("装备物品请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	err := api.ic.EquipItem(pid, req.SlotIndex)
	if err != nil {
		api.logger.Warn("装备物品失败",
			zap.String("player_id", pid),
			zap.Int32("slot_index", req.SlotIndex),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]string{"msg": "装备成功"})
}

// UnequipItem 卸下装备
// POST /api/v1/inventory/unequip
func (api *InventoryAPI) UnequipItem(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	var req struct {
		EquipmentSlotID int32 `json:"equipment_slot_id" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("卸下装备请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	err := api.ic.UnequipItem(pid, req.EquipmentSlotID)
	if err != nil {
		api.logger.Warn("卸下装备失败",
			zap.String("player_id", pid),
			zap.Int32("equipment_slot_id", req.EquipmentSlotID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]string{"msg": "卸下成功"})
}

// GetEquipped 获取已装备列表
// GET /api/v1/inventory/equipped
func (api *InventoryAPI) GetEquipped(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	slots, err := api.ic.GetEquipped(pid)
	if err != nil {
		api.logger.Error("获取装备列表失败", zap.String("player_id", pid), zap.Error(err))
		return ResponseError(c, http.StatusInternalServerError, "获取装备列表失败")
	}

	return ResponseOK(c, slots)
}

// SwapSlots 交换两个格子
// POST /api/v1/inventory/swap
func (api *InventoryAPI) SwapSlots(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	var req struct {
		SlotA int32 `json:"slot_a" binding:"required"`
		SlotB int32 `json:"slot_b" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("交换格子请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	err := api.ic.SwapSlots(pid, req.SlotA, req.SlotB)
	if err != nil {
		api.logger.Warn("交换格子失败",
			zap.String("player_id", pid),
			zap.Int32("slot_a", req.SlotA),
			zap.Int32("slot_b", req.SlotB),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]string{"msg": "交换成功"})
}

// RemoveItem 移除物品
// POST /api/v1/inventory/remove
func (api *InventoryAPI) RemoveItem(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	var req struct {
		SlotIndex int32 `json:"slot_index" binding:"required"`
		Quantity  int32 `json:"quantity" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("移除物品请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	err := api.ic.RemoveItem(pid, req.SlotIndex, req.Quantity)
	if err != nil {
		api.logger.Warn("移除物品失败",
			zap.String("player_id", pid),
			zap.Int32("slot_index", req.SlotIndex),
			zap.Int32("quantity", req.Quantity),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]string{"msg": "移除成功"})
}

// ListItemTemplates 列出物品模板
// GET /api/v1/items?type=consumable&rarity=common&limit=20&offset=0
func (api *InventoryAPI) ListItemTemplates(c *astra.Ctx) error {
	itemType := inventory.ItemType(c.Query("type"))
	rarity := inventory.Rarity(c.Query("rarity"))

	limitStr := c.Query("limit")
	limit := 20
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
			limit = val
		}
	}

	offsetStr := c.Query("offset")
	offset := 0
	if offsetStr != "" {
		if val, err := strconv.Atoi(offsetStr); err == nil && val >= 0 {
			offset = val
		}
	}

	templates, err := api.ic.ListItemTemplates(itemType, rarity, limit, offset)
	if err != nil {
		api.logger.Error("列出物品模板失败", zap.Error(err))
		return ResponseError(c, http.StatusInternalServerError, "获取物品列表失败")
	}

	return ResponseOK(c, templates)
}

// GetItemTemplate 获取物品模板详情
// GET /api/v1/items/:id
func (api *InventoryAPI) GetItemTemplate(c *astra.Ctx) error {
	itemID := c.Param("id")
	if itemID == "" {
		return ResponseError(c, http.StatusBadRequest, "物品ID不能为空")
	}

	template, err := api.ic.GetItemTemplate(itemID)
	if err != nil {
		api.logger.Warn("获取物品模板失败", zap.String("item_id", itemID), zap.Error(err))
		return ResponseError(c, http.StatusNotFound, "物品不存在")
	}

	return ResponseOK(c, template)
}
