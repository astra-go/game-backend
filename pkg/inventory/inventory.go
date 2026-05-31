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
	ItemID          string          `json:"item_id" gorm:"size:64"`
	ItemType        ItemType        `json:"item_type" gorm:"size:32"`
	Quantity        int32           `json:"quantity" gorm:"default:1"`
	StackLimit      int32           `json:"stack_limit" gorm:"default:99"`
	EquipmentSlotID int32           `json:"equipment_slot_id" gorm:"default:0"` // 装备部位ID，0=非装备
	ExtraAttrs      json.RawMessage `json:"extra_attrs" gorm:"type:json"`
}

func (InventorySlot) TableName() string {
	return "inventory_slots"
}

// ItemTemplate 物品模板
type ItemTemplate struct {
	ItemID      string          `json:"item_id" gorm:"primaryKey;size:64"`
	Name        string          `json:"name" gorm:"size:64"`
	Description string          `json:"description" gorm:"size:256"`
	ItemType    ItemType        `json:"item_type" gorm:"size:32"`
	MaxStack    int32           `json:"max_stack" gorm:"default:99"`
	Rarity      Rarity          `json:"rarity" gorm:"size:32"`
	BaseAttrs   json.RawMessage `json:"base_attrs" gorm:"type:json"`
	PriceGold   int64           `json:"price_gold" gorm:"default:0"`
	PriceDiamond int64          `json:"price_diamond" gorm:"default:0"`
}

func (ItemTemplate) TableName() string {
	return "item_templates"
}

// context用于Redis操作
var invCtx = context.Background()

// Redis缓存
const invCachePrefix = "player:inv:"
const invCacheTTL = 5 * time.Minute

// ========== InventoryComponent ==========

// InventoryComponent 背包组件
type InventoryComponent struct {
	db     *gorm.DB
	redis  *redis.Client
	logger *zap.Logger
}

// NewInventoryComponent 创建背包组件
func NewInventoryComponent(db *gorm.DB, redis *redis.Client, logger *zap.Logger) *InventoryComponent {
	return &InventoryComponent{
		db:     db,
		redis:  redis,
		logger: logger,
	}
}

// Init 初始化：AutoMigrate
func (ic *InventoryComponent) Init() error {
	ic.logger.Info("InventoryComponent 初始化")
	return ic.db.AutoMigrate(&InventorySlot{}, &ItemTemplate{})
}

// GetInventory 获取玩家所有背包格子
func (ic *InventoryComponent) GetInventory(playerID string) ([]InventorySlot, error) {
	// 先查Redis
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

// AddItem 添加物品到背包
func (ic *InventoryComponent) AddItem(playerID string, itemID string, quantity int32) (*InventorySlot, error) {
	if quantity <= 0 {
		return nil, fmt.Errorf("数量必须大于0")
	}

	// 查找物品模板获取堆叠上限
	var template ItemTemplate
	err := ic.db.Where("item_id = ?", itemID).First(&template).Error
	if err != nil {
		return nil, fmt.Errorf("物品模板不存在: %s", itemID)
	}
	stackLimit := template.MaxStack

	// 查找已有同物品的格子（非装备）
	var existing []InventorySlot
	ic.db.Where("player_id = ? AND item_id = ? AND equipment_slot_id = 0", playerID, itemID).
		Find(&existing)

	remaining := quantity
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
		}
	}

	// 还有剩余需要找空格
	for remaining > 0 {
		// 找下一个可用空格
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
	}

	ic.invalidateInvCache(playerID)

	// 返回最终的slot（最后一个创建或更新的）
	var result InventorySlot
	ic.db.Where("player_id = ? AND item_id = ? AND equipment_slot_id = 0", playerID, itemID).
		Order("slot_index ASC").First(&result)
	return &result, nil
}

// RemoveItem 移除物品
func (ic *InventoryComponent) RemoveItem(playerID string, slotIndex int32, quantity int32) error {
	var slot InventorySlot
	err := ic.db.Where("player_id = ? AND slot_index = ?", playerID, slotIndex).First(&slot).Error
	if err != nil {
		return fmt.Errorf("格子不存在")
	}

	if quantity <= 0 || quantity >= slot.Quantity {
		// 清空格子
		ic.db.Delete(&slot)
	} else {
		slot.Quantity -= quantity
		ic.db.Save(&slot)
	}

	ic.invalidateInvCache(playerID)
	return nil
}

// UseItem 使用消耗品
func (ic *InventoryComponent) UseItem(playerID string, slotIndex int32) error {
	var slot InventorySlot
	err := ic.db.Where("player_id = ? AND slot_index = ?", playerID, slotIndex).First(&slot).Error
	if err != nil {
		return fmt.Errorf("格子不存在")
	}

	if slot.ItemType != ItemTypeConsumable {
		return fmt.Errorf("只能使用消耗品")
	}

	slot.Quantity--
	if slot.Quantity <= 0 {
		ic.db.Delete(&slot)
	} else {
		ic.db.Save(&slot)
	}

	ic.invalidateInvCache(playerID)
	return nil
}

// EquipItem 装备物品，交换已装备的到背包
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

	// 获取装备部位的slot_id（从extra_attrs或base_attrs中）
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
	slot.SlotIndex = equipSlotID // 装备格子用装备部位ID作为索引
	ic.db.Save(&slot)

	ic.invalidateInvCache(playerID)
	return nil
}

// UnequipItem 卸下装备到背包
func (ic *InventoryComponent) UnequipItem(playerID string, equipmentSlotID int32) error {
	var slot InventorySlot
	err := ic.db.Where("player_id = ? AND equipment_slot_id = ?", playerID, equipmentSlotID).First(&slot).Error
	if err != nil {
		return fmt.Errorf("未找到该部位的装备")
	}

	// 找空格放装备
	emptySlot, err := ic.findEmptySlot(playerID)
	if err != nil {
		return fmt.Errorf("背包已满")
	}

	slot.EquipmentSlotID = 0
	slot.SlotIndex = emptySlot
	ic.db.Save(&slot)

	ic.invalidateInvCache(playerID)
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

	// 交换slot_index
	a.SlotIndex, b.SlotIndex = b.SlotIndex, a.SlotIndex
	// 如果涉及装备，也交换equipment_slot_id
	if a.EquipmentSlotID != 0 || b.EquipmentSlotID != 0 {
		a.EquipmentSlotID, b.EquipmentSlotID = b.EquipmentSlotID, a.EquipmentSlotID
	}

	ic.db.Save(&a)
	ic.db.Save(&b)

	ic.invalidateInvCache(playerID)
	return nil
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
	return ic.db.Create(&template).Error
}

// findEmptySlot 找第一个空的背包格
func (ic *InventoryComponent) findEmptySlot(playerID string) (int32, error) {
	// 获取所有已占用的slot_index
	var used []int32
	ic.db.Model(&InventorySlot{}).
		Where("player_id = ?", playerID).
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
	// 从extra_attrs解析equipment_slot_id，如果没有则用item_id的hash生成
	if slot.ExtraAttrs != nil {
		var m map[string]interface{}
		if json.Unmarshal(slot.ExtraAttrs, &m) == nil {
			if v, ok := m["equipment_slot_id"]; ok {
				switch val := v.(type) {
				case float64:
					return int32(val)
				}
			}
		}
	}
	// 默认用slot索引作为装备部位
	return slot.SlotIndex
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
