package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ========== 数据结构 ==========

// ItemType 物品类型
type ItemType string

const (
	ItemTypeConsumable ItemType = "consumable" // 消耗品
	ItemTypeEquipment  ItemType = "equipment"  // 装备
	ItemTypeMaterial   ItemType = "material"   // 材料
	ItemTypeQuest      ItemType = "quest"      // 任务物品
	ItemTypeGift       ItemType = "gift"       // 礼包
)

// Rarity 物品稀有度
type Rarity string

const (
	RarityCommon    Rarity = "common"
	RarityUncommon  Rarity = "uncommon"
	RarityRare      Rarity = "rare"
	RarityEpic      Rarity = "epic"
	RarityLegendary Rarity = "legendary"
)

// DefaultCapacity 默认背包容量
const DefaultCapacity = 30

// InventorySlot 背包格子
type InventorySlot struct {
	PlayerID        string          `json:"player_id" gorm:"primaryKey;size:64"`
	SlotIndex       int32           `json:"slot_index" gorm:"primaryKey"`
	ItemID          string          `json:"item_id" gorm:"size:64;index"`
	ItemType        ItemType        `json:"item_type" gorm:"size:32"`
	Quantity        int32           `json:"quantity" gorm:"default:1"`
	StackLimit      int32           `json:"stack_limit" gorm:"default:99"`
	EquipmentSlotID int32           `json:"equipment_slot_id" gorm:"default:0;index"` // 装备部位ID，0=非装备
	ExtraAttrs      json.RawMessage `json:"extra_attrs" gorm:"type:json"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

func (InventorySlot) TableName() string {
	return "inventory_slots"
}

// ItemTemplate 物品模板
type ItemTemplate struct {
	ItemID       string          `json:"item_id" gorm:"primaryKey;size:64"`
	Name         string          `json:"name" gorm:"size:64"`
	Description  string          `json:"description" gorm:"size:256"`
	ItemType     ItemType        `json:"item_type" gorm:"size:32;index"`
	MaxStack     int32           `json:"max_stack" gorm:"default:99"`
	Rarity       Rarity          `json:"rarity" gorm:"size:32"`
	BaseAttrs    json.RawMessage `json:"base_attrs" gorm:"type:json"`
	Effects      json.RawMessage `json:"effects" gorm:"type:json"`       // 物品效果列表
	Requirements json.RawMessage `json:"requirements" gorm:"type:json"` // 使用要求
	PriceGold    int64           `json:"price_gold" gorm:"default:0"`
	PriceDiamond int64           `json:"price_diamond" gorm:"default:0"`
	Tradable     bool            `json:"tradable" gorm:"default:true"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func (ItemTemplate) TableName() string {
	return "item_templates"
}

// context用于Redis操作
var invCtx = context.Background()

// Redis缓存
const invCachePrefix = "player:inv:"
const templateCachePrefix = "item:template:"
const invCacheTTL = 5 * time.Minute

// ========== InventoryComponent ==========

// InventoryComponent 背包组件
type InventoryComponent struct {
	db              *gorm.DB
	redis           *redis.Client
	logger          *zap.Logger
	effectContext   *EffectContext
	playerComponent PlayerComponentInterface
	attributeEngine AttributeEngineInterface
}

// NewInventoryComponent 创建背包组件
func NewInventoryComponent(db *gorm.DB, redis *redis.Client, logger *zap.Logger) *InventoryComponent {
	return &InventoryComponent{
		db:     db,
		redis:  redis,
		logger: logger,
	}
}

// SetPlayerComponent 设置玩家组件（避免循环依赖）
func (ic *InventoryComponent) SetPlayerComponent(pc PlayerComponentInterface) {
	ic.playerComponent = pc
	ic.updateEffectContext()
}

// SetAttributeEngine 设置属性引擎
func (ic *InventoryComponent) SetAttributeEngine(ae AttributeEngineInterface) {
	ic.attributeEngine = ae
	ic.updateEffectContext()
}

// updateEffectContext 更新效果上下文
func (ic *InventoryComponent) updateEffectContext() {
	ic.effectContext = &EffectContext{
		PlayerComponent:    ic.playerComponent,
		InventoryComponent: ic,
		AttributeEngine:    ic.attributeEngine,
	}
}

// Init 初始化：AutoMigrate
func (ic *InventoryComponent) Init() error {
	ic.logger.Info("InventoryComponent 初始化")
	return ic.db.AutoMigrate(&InventorySlot{}, &ItemTemplate{})
}

// ========== 背包操作 ==========

// GetInventory 获取玩家所有背包格子
func (ic *InventoryComponent) GetInventory(playerID string) ([]InventorySlot, error) {
	cacheKey := invCachePrefix + playerID
	cached, err := ic.redis.Get(invCtx, cacheKey).Bytes()
	if err == nil {
		var slots []InventorySlot
		if jsonErr := json.Unmarshal(cached, &slots); jsonErr == nil {
			return slots, nil
		}
		ic.redis.Del(invCtx, cacheKey)
	}

	var slots []InventorySlot
	err = ic.db.Where("player_id = ? AND equipment_slot_id = 0", playerID).
		Order("slot_index ASC").Find(&slots).Error
	if err != nil {
		return nil, err
	}

	ic.setInvCache(playerID, slots)
	return slots, nil
}

// GetSlot 获取指定格子
func (ic *InventoryComponent) GetSlot(playerID string, slotIndex int32) (*InventorySlot, error) {
	var slot InventorySlot
	err := ic.db.Where("player_id = ? AND slot_index = ?", playerID, slotIndex).First(&slot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("格子不存在")
		}
		return nil, err
	}
	return &slot, nil
}

// AddItem 添加物品到背包（支持堆叠）
func (ic *InventoryComponent) AddItem(playerID string, itemID string, quantity int32) (*InventorySlot, error) {
	if quantity <= 0 {
		return nil, fmt.Errorf("数量必须大于0")
	}

	template, err := ic.GetItemTemplate(itemID)
	if err != nil {
		return nil, fmt.Errorf("物品模板不存在: %s", itemID)
	}
	stackLimit := template.MaxStack

	// 查找已有同物品的格子（非装备）
	var existing []InventorySlot
	ic.db.Where("player_id = ? AND item_id = ? AND equipment_slot_id = 0", playerID, itemID).
		Order("slot_index ASC").Find(&existing)

	remaining := quantity
	var lastSlot *InventorySlot

	// 先尝试堆叠到现有格子
	for i := range existing {
		if remaining <= 0 {
			break
		}
		slot := &existing[i]
		canAdd := stackLimit - slot.Quantity
		if canAdd > 0 {
			add := canAdd
			if add > remaining {
				add = remaining
			}
			slot.Quantity += add
			remaining -= add
			ic.db.Save(slot)
			lastSlot = slot
		}
	}

	// 还有剩余需要创建新格子
	for remaining > 0 {
		slotIndex, err := ic.findEmptySlot(playerID)
		if err != nil {
			return nil, fmt.Errorf("背包已满")
		}

		add := remaining
		if add > stackLimit {
			add = stackLimit
		}

		slot := InventorySlot{
			PlayerID:        playerID,
			SlotIndex:       slotIndex,
			ItemID:          itemID,
			ItemType:        template.ItemType,
			Quantity:        add,
			StackLimit:      stackLimit,
			EquipmentSlotID: 0,
		}
		if err := ic.db.Create(&slot).Error; err != nil {
			return nil, err
		}
		remaining -= add
		lastSlot = &slot
	}

	ic.invalidateInvCache(playerID)

	ic.logger.Info("添加物品",
		zap.String("player_id", playerID),
		zap.String("item_id", itemID),
		zap.Int32("quantity", quantity),
	)

	return lastSlot, nil
}

// RemoveItem 移除物品
func (ic *InventoryComponent) RemoveItem(playerID string, slotIndex int32, quantity int32) error {
	var slot InventorySlot
	err := ic.db.Where("player_id = ? AND slot_index = ?", playerID, slotIndex).First(&slot).Error
	if err != nil {
		return fmt.Errorf("格子不存在")
	}

	if quantity <= 0 {
		return fmt.Errorf("数量必须大于0")
	}

	if quantity >= slot.Quantity {
		// 清空格子
		ic.db.Delete(&slot)
	} else {
		slot.Quantity -= quantity
		ic.db.Save(&slot)
	}

	ic.invalidateInvCache(playerID)

	ic.logger.Info("移除物品",
		zap.String("player_id", playerID),
		zap.Int32("slot_index", slotIndex),
		zap.Int32("quantity", quantity),
	)

	return nil
}

// UseItem 使用物品（消耗品）
func (ic *InventoryComponent) UseItem(playerID string, slotIndex int32) (*EffectResult, error) {
	slot, err := ic.GetSlot(playerID, slotIndex)
	if err != nil {
		return nil, err
	}

	if slot.ItemType != ItemTypeConsumable && slot.ItemType != ItemTypeGift {
		return nil, fmt.Errorf("该物品不可使用")
	}

	template, err := ic.GetItemTemplate(slot.ItemID)
	if err != nil {
		return nil, fmt.Errorf("物品模板不存在")
	}

	// 检查使用要求
	if err := ic.checkRequirements(playerID, template); err != nil {
		return nil, fmt.Errorf("不满足使用条件: %w", err)
	}

	// 解析并应用效果
	effects, err := ParseEffects(template.Effects)
	if err != nil {
		return nil, fmt.Errorf("解析物品效果失败: %w", err)
	}

	combinedResult := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	for _, effect := range effects {
		result, err := ApplyEffect(effect, playerID, ic.effectContext)
		if err != nil {
			ic.logger.Error("应用效果失败",
				zap.String("player_id", playerID),
				zap.String("item_id", slot.ItemID),
				zap.String("effect_type", string(effect.Type)),
				zap.Error(err),
			)
			continue
		}

		// 合并结果
		for k, v := range result.Changes {
			combinedResult.Changes[k] = v
		}
		combinedResult.RewardItems = append(combinedResult.RewardItems, result.RewardItems...)
		combinedResult.BuffsApplied = append(combinedResult.BuffsApplied, result.BuffsApplied...)
		if result.Message != "" {
			if combinedResult.Message != "" {
				combinedResult.Message += "; "
			}
			combinedResult.Message += result.Message
		}
	}

	// 消耗物品
	slot.Quantity--
	if slot.Quantity <= 0 {
		ic.db.Delete(slot)
	} else {
		ic.db.Save(slot)
	}

	ic.invalidateInvCache(playerID)

	ic.logger.Info("使用物品",
		zap.String("player_id", playerID),
		zap.String("item_id", slot.ItemID),
		zap.Int32("slot_index", slotIndex),
	)

	return combinedResult, nil
}

// ========== 装备操作 ==========

// EquipItem 装备物品
func (ic *InventoryComponent) EquipItem(playerID string, slotIndex int32) error {
	var slot InventorySlot
	err := ic.db.Where("player_id = ? AND slot_index = ?", playerID, slotIndex).First(&slot).Error
	if err != nil {
		return fmt.Errorf("格子不存在")
	}

	if slot.ItemType != ItemTypeEquipment {
		return fmt.Errorf("只能装备装备类物品")
	}

	if slot.EquipmentSlotID != 0 {
		return fmt.Errorf("物品已装备")
	}

	template, err := ic.GetItemTemplate(slot.ItemID)
	if err != nil {
		return fmt.Errorf("物品模板不存在")
	}

	// 检查装备要求
	if err := ic.checkRequirements(playerID, template); err != nil {
		return fmt.Errorf("不满足装备条件: %w", err)
	}

	equipSlotID := ic.getEquipmentSlotID(slot)

	// 检查该装备部位是否已有装备
	var equipped InventorySlot
	err = ic.db.Where("player_id = ? AND equipment_slot_id = ?", playerID, equipSlotID).First(&equipped).Error
	if err == nil {
		// 已有装备，交换：旧装备放到背包空格
		emptySlot, findErr := ic.findEmptySlot(playerID)
		if findErr != nil {
			return fmt.Errorf("背包已满，无法卸下旧装备")
		}
		equipped.SlotIndex = emptySlot
		equipped.EquipmentSlotID = 0
		ic.db.Save(&equipped)
	}

	// 装备新物品
	slot.EquipmentSlotID = equipSlotID
	slot.SlotIndex = equipSlotID
	ic.db.Save(&slot)

	ic.invalidateInvCache(playerID)

	ic.logger.Info("装备物品",
		zap.String("player_id", playerID),
		zap.String("item_id", slot.ItemID),
		zap.Int32("equipment_slot_id", equipSlotID),
	)

	return nil
}

// UnequipItem 卸下装备
func (ic *InventoryComponent) UnequipItem(playerID string, equipmentSlotID int32) error {
	var slot InventorySlot
	err := ic.db.Where("player_id = ? AND equipment_slot_id = ?", playerID, equipmentSlotID).First(&slot).Error
	if err != nil {
		return fmt.Errorf("未找到该部位的装备")
	}

	emptySlot, err := ic.findEmptySlot(playerID)
	if err != nil {
		return fmt.Errorf("背包已满")
	}

	slot.EquipmentSlotID = 0
	slot.SlotIndex = emptySlot
	ic.db.Save(&slot)

	ic.invalidateInvCache(playerID)

	ic.logger.Info("卸下装备",
		zap.String("player_id", playerID),
		zap.Int32("equipment_slot_id", equipmentSlotID),
	)

	return nil
}

// GetEquipped 获取已装备列表
func (ic *InventoryComponent) GetEquipped(playerID string) ([]InventorySlot, error) {
	var slots []InventorySlot
	err := ic.db.Where("player_id = ? AND equipment_slot_id != 0", playerID).
		Order("equipment_slot_id ASC").Find(&slots).Error
	if err != nil {
		return nil, err
	}
	return slots, nil
}

// ========== 物品模板管理 ==========

// GetItemTemplate 获取物品模板
func (ic *InventoryComponent) GetItemTemplate(itemID string) (*ItemTemplate, error) {
	cacheKey := templateCachePrefix + itemID
	cached, err := ic.redis.Get(invCtx, cacheKey).Bytes()
	if err == nil {
		var template ItemTemplate
		if jsonErr := json.Unmarshal(cached, &template); jsonErr == nil {
			return &template, nil
		}
		ic.redis.Del(invCtx, cacheKey)
	}

	var template ItemTemplate
	err = ic.db.Where("item_id = ?", itemID).First(&template).Error
	if err != nil {
		return nil, err
	}

	// 缓存模板
	data, _ := json.Marshal(template)
	ic.redis.Set(invCtx, cacheKey, data, invCacheTTL)

	return &template, nil
}

// RegisterItemTemplate 注册物品模板
func (ic *InventoryComponent) RegisterItemTemplate(template ItemTemplate) error {
	var existing ItemTemplate
	err := ic.db.Where("item_id = ?", template.ItemID).First(&existing).Error
	if err == nil {
		// 已存在，更新
		return ic.db.Save(&template).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	err = ic.db.Create(&template).Error
	if err != nil {
		return err
	}

	// 清除缓存
	ic.redis.Del(invCtx, templateCachePrefix+template.ItemID)

	ic.logger.Info("注册物品模板",
		zap.String("item_id", template.ItemID),
		zap.String("name", template.Name),
	)

	return nil
}

// ListItemTemplates 列出所有物品模板
func (ic *InventoryComponent) ListItemTemplates(itemType ItemType, rarity Rarity, limit int, offset int) ([]ItemTemplate, error) {
	query := ic.db.Model(&ItemTemplate{})

	if itemType != "" {
		query = query.Where("item_type = ?", itemType)
	}
	if rarity != "" {
		query = query.Where("rarity = ?", rarity)
	}

	var templates []ItemTemplate
	err := query.Limit(limit).Offset(offset).Find(&templates).Error
	if err != nil {
		return nil, err
	}

	return templates, nil
}

// ========== 辅助方法 ==========

// SwapSlots 交换两个格子
func (ic *InventoryComponent) SwapSlots(playerID string, slotA, slotB int32) error {
	var a, b InventorySlot
	err := ic.db.Where("player_id = ? AND slot_index = ?", playerID, slotA).First(&a).Error
	if err != nil {
		return fmt.Errorf("格子A不存在")
	}
	err = ic.db.Where("player_id = ? AND slot_index = ?", playerID, slotB).First(&b).Error
	if err != nil {
		return fmt.Errorf("格子B不存在")
	}

	a.SlotIndex, b.SlotIndex = b.SlotIndex, a.SlotIndex
	if a.EquipmentSlotID != 0 || b.EquipmentSlotID != 0 {
		a.EquipmentSlotID, b.EquipmentSlotID = b.EquipmentSlotID, a.EquipmentSlotID
	}

	ic.db.Save(&a)
	ic.db.Save(&b)

	ic.invalidateInvCache(playerID)
	return nil
}

// findEmptySlot 找第一个空的背包格
func (ic *InventoryComponent) findEmptySlot(playerID string) (int32, error) {
	var used []int32
	ic.db.Model(&InventorySlot{}).
		Where("player_id = ? AND equipment_slot_id = 0", playerID).
		Pluck("slot_index", &used)

	usedSet := make(map[int32]bool)
	for _, s := range used {
		usedSet[s] = true
	}

	for i := int32(1); i <= DefaultCapacity; i++ {
		if !usedSet[i] {
			return i, nil
		}
	}
	return 0, fmt.Errorf("背包已满")
}

// getEquipmentSlotID 从格子中获取装备部位ID
func (ic *InventoryComponent) getEquipmentSlotID(slot InventorySlot) int32 {
	if slot.ExtraAttrs != nil {
		var m map[string]interface{}
		if json.Unmarshal(slot.ExtraAttrs, &m) == nil {
			if v, ok := m["equipment_slot_id"]; ok {
				switch val := v.(type) {
				case float64:
					return int32(val)
				case int32:
					return val
				}
			}
		}
	}
	return 1 // 默认装备部位
}

// checkRequirements 检查使用/装备要求
func (ic *InventoryComponent) checkRequirements(playerID string, template *ItemTemplate) error {
	if len(template.Requirements) == 0 {
		return nil
	}

	var reqs map[string]interface{}
	if err := json.Unmarshal(template.Requirements, &reqs); err != nil {
		return fmt.Errorf("解析要求失败: %w", err)
	}

	if ic.playerComponent == nil {
		return nil
	}

	player, err := ic.playerComponent.GetByID(playerID)
	if err != nil {
		return fmt.Errorf("获取玩家数据失败: %w", err)
	}

	// 检查等级要求
	if minLevel, ok := reqs["min_level"].(float64); ok {
		if player.GetLevel() < int32(minLevel) {
			return fmt.Errorf("需要等级 %d", int32(minLevel))
		}
	}

	return nil
}

// ========== 缓存辅助 ==========

func (ic *InventoryComponent) setInvCache(playerID string, slots []InventorySlot) {
	data, err := json.Marshal(slots)
	if err != nil {
		ic.logger.Warn("序列化背包缓存失败", zap.Error(err))
		return
	}
	if err := ic.redis.Set(invCtx, invCachePrefix+playerID, data, invCacheTTL).Err(); err != nil {
		ic.logger.Warn("写入背包缓存失败", zap.Error(err))
	}
}

func (ic *InventoryComponent) invalidateInvCache(playerID string) {
	ic.redis.Del(invCtx, invCachePrefix+playerID)
}
