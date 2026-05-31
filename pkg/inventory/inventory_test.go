package inventory

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupInvTest(t *testing.T) (*InventoryComponent, *gorm.DB, *miniredis.Miniredis) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	logger := zap.NewNop()
	comp := NewInventoryComponent(db, rdb, logger)
	err = comp.Init()
	assert.NoError(t, err)

	t.Cleanup(func() { rdb.Close() })

	return comp, db, mr
}

func TestInventoryInit(t *testing.T) {
	comp, db, _ := setupInvTest(t)
	_ = comp

	var count int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='item_templates'").Scan(&count)
	assert.Equal(t, int64(1), count)

	var count2 int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='inventory_slots'").Scan(&count2)
	assert.Equal(t, int64(1), count2)
}

func TestRegisterItemTemplate(t *testing.T) {
	comp, db, _ := setupInvTest(t)

	tmpl := ItemTemplate{
		ItemID:      "potion_hp",
		Name:        "生命药水",
		Description: "恢复50HP",
		ItemType:    ItemTypeConsumable,
		MaxStack:    99,
		Rarity:      RarityCommon,
		PriceGold:   10,
	}

	err := comp.RegisterItemTemplate(tmpl)
	assert.NoError(t, err)

	var found ItemTemplate
	db.Where("item_id = ?", "potion_hp").First(&found)
	assert.Equal(t, "生命药水", found.Name)
}

func TestRegisterItemTemplate_Update(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	tmpl := ItemTemplate{ItemID: "sword1", Name: "铁剑", ItemType: ItemTypeEquipment, MaxStack: 1}
	comp.RegisterItemTemplate(tmpl)

	// 更新
	tmpl.Name = "精良铁剑"
	err := comp.RegisterItemTemplate(tmpl)
	assert.NoError(t, err)

	var found ItemTemplate
	comp.db.Where("item_id = ?", "sword1").First(&found)
	assert.Equal(t, "精良铁剑", found.Name)
}

func TestGetInventory_Empty(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	slots, err := comp.GetInventory("p1")
	assert.NoError(t, err)
	assert.Empty(t, slots)
}

func TestAddItem(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	// 注册物品模板
	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_hp", Name: "生命药水", ItemType: ItemTypeConsumable, MaxStack: 99, Rarity: RarityCommon,
	})

	slot, err := comp.AddItem("p1", "potion_hp", 5)
	assert.NoError(t, err)
	assert.NotNil(t, slot)
	assert.Equal(t, int32(5), slot.Quantity)

	// 验证背包
	slots, _ := comp.GetInventory("p1")
	assert.Len(t, slots, 1)
	assert.Equal(t, int32(5), slots[0].Quantity)
}

func TestAddItem_Stack(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_hp", Name: "生命药水", ItemType: ItemTypeConsumable, MaxStack: 10, Rarity: RarityCommon,
	})

	// 先加5个
	comp.AddItem("p1", "potion_hp", 5)

	// 再加8个，已有5个，最大10，叠加5个后新开格放3个
	comp.AddItem("p1", "potion_hp", 8)

	slots, _ := comp.GetInventory("p1")
	totalQty := int32(0)
	for _, s := range slots {
		totalQty += s.Quantity
	}
	assert.Equal(t, int32(13), totalQty)
}

func TestAddItem_StackOverflow_NewSlot(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_hp", Name: "生命药水", ItemType: ItemTypeConsumable, MaxStack: 5, Rarity: RarityCommon,
	})

	// 加10个，max_stack=5，应该分到2个格子
	comp.AddItem("p1", "potion_hp", 10)

	slots, _ := comp.GetInventory("p1")
	assert.Len(t, slots, 2)
}

func TestAddItem_InvalidQuantity(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	_, err := comp.AddItem("p1", "potion_hp", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "数量必须大于0")
}

func TestAddItem_NoTemplate(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	_, err := comp.AddItem("p1", "nonexist", 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "物品模板不存在")
}

func TestRemoveItem(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_hp", Name: "生命药水", ItemType: ItemTypeConsumable, MaxStack: 99, Rarity: RarityCommon,
	})
	comp.AddItem("p1", "potion_hp", 10)

	err := comp.RemoveItem("p1", 1, 3)
	assert.NoError(t, err)

	slots, _ := comp.GetInventory("p1")
	assert.Len(t, slots, 1)
	assert.Equal(t, int32(7), slots[0].Quantity)
}

func TestRemoveItem_ClearAll(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_hp", Name: "生命药水", ItemType: ItemTypeConsumable, MaxStack: 99, Rarity: RarityCommon,
	})
	comp.AddItem("p1", "potion_hp", 10)

	err := comp.RemoveItem("p1", 1, 10)
	assert.NoError(t, err)

	slots, _ := comp.GetInventory("p1")
	assert.Len(t, slots, 0)
}

func TestRemoveItem_NotFound(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	err := comp.RemoveItem("p1", 999, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "格子不存在")
}

func TestUseItem(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_hp", Name: "生命药水", ItemType: ItemTypeConsumable, MaxStack: 99, Rarity: RarityCommon,
	})
	comp.AddItem("p1", "potion_hp", 5)

	err := comp.UseItem("p1", 1)
	assert.NoError(t, err)

	slots, _ := comp.GetInventory("p1")
	assert.Len(t, slots, 1)
	assert.Equal(t, int32(4), slots[0].Quantity)
}

func TestUseItem_LastOne(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_hp", Name: "生命药水", ItemType: ItemTypeConsumable, MaxStack: 99, Rarity: RarityCommon,
	})
	comp.AddItem("p1", "potion_hp", 1)

	err := comp.UseItem("p1", 1)
	assert.NoError(t, err)

	slots, _ := comp.GetInventory("p1")
	assert.Len(t, slots, 0) // 最后一个用完，格子删除
}

func TestUseItem_NotConsumable(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "sword1", Name: "铁剑", ItemType: ItemTypeEquipment, MaxStack: 1, Rarity: RarityCommon,
	})
	comp.AddItem("p1", "sword1", 1)

	err := comp.UseItem("p1", 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "只能使用消耗品")
}

func TestSwapSlots(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_hp", Name: "生命药水", ItemType: ItemTypeConsumable, MaxStack: 99, Rarity: RarityCommon,
	})
	comp.RegisterItemTemplate(ItemTemplate{
		ItemID: "potion_mp", Name: "魔法药水", ItemType: ItemTypeConsumable, MaxStack: 99, Rarity: RarityCommon,
	})
	comp.AddItem("p1", "potion_hp", 5)
	comp.AddItem("p1", "potion_mp", 3)

	err := comp.SwapSlots("p1", 1, 2)
	assert.NoError(t, err)

	slots, _ := comp.GetInventory("p1")
	// slot 1应该现在是potion_mp, slot 2应该是potion_hp
	assert.Len(t, slots, 2)
}

func TestSwapSlots_NotFound(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	err := comp.SwapSlots("p1", 1, 999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "格子")
}

func TestGetEquipped_Empty(t *testing.T) {
	comp, _, _ := setupInvTest(t)

	equipped, err := comp.GetEquipped("p1")
	assert.NoError(t, err)
	assert.Empty(t, equipped)
}
