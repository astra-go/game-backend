package player

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/astra-go/astra/log"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupEngineTest(t *testing.T) (*AttributeEngine, *gorm.DB, *miniredis.Miniredis) {
	t.Helper()

	// 内存SQLite
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	// miniredis
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	logger := log.Default()
	engine := NewAttributeEngine(db, rdb, logger)
	err = engine.Init()
	assert.NoError(t, err)

	t.Cleanup(func() { rdb.Close() })

	return engine, db, mr
}

func TestAttributeEngineInit(t *testing.T) {
	engine, db, _ := setupEngineTest(t)

	// 检查表是否存在
	var count int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='player_modifiers'").Scan(&count)
	assert.Equal(t, int64(1), count)

	// 再次Init不应报错
	err := engine.Init()
	assert.NoError(t, err)
}

func TestAddModifier(t *testing.T) {
	engine, db, _ := setupEngineTest(t)

	mod := AttributeModifier{
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
		Duration:   60,
		Priority:   100,
	}

	err := engine.AddModifier("p1", mod)
	assert.NoError(t, err)

	// 检查数据库
	var pm PlayerModifier
	err = db.Where("player_id = ?", "p1").First(&pm).Error
	assert.NoError(t, err)
	assert.Equal(t, "buff", pm.SourceType)
	assert.Equal(t, "buff_001", pm.SourceID)
	assert.Equal(t, "attack", pm.AttrType)
	assert.Equal(t, "flat", pm.ModType)
	assert.Equal(t, int32(10), pm.Value)
	assert.Equal(t, int64(60), pm.Duration)
	assert.Greater(t, pm.ExpiresAt, time.Now().Unix())
}

func TestAddModifier_AutoGenerateID(t *testing.T) {
	engine, db, _ := setupEngineTest(t)

	mod := AttributeModifier{
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
		Duration:   0, // 永久
		Priority:   100,
	}

	err := engine.AddModifier("p1", mod)
	assert.NoError(t, err)

	var pm PlayerModifier
	err = db.Where("player_id = ?", "p1").First(&pm).Error
	assert.NoError(t, err)
	assert.NotEmpty(t, pm.ModifierID)
	assert.Equal(t, int64(0), pm.ExpiresAt) // 永久修改器
}

func TestRemoveModifier(t *testing.T) {
	engine, db, _ := setupEngineTest(t)

	mod := AttributeModifier{
		ID:         "mod_001",
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
		Duration:   0,
		Priority:   100,
	}

	engine.AddModifier("p1", mod)

	err := engine.RemoveModifier("p1", "mod_001")
	assert.NoError(t, err)

	// 检查已删除
	var count int64
	db.Model(&PlayerModifier{}).Where("player_id = ? AND modifier_id = ?", "p1", "mod_001").Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestRemoveModifiersBySource(t *testing.T) {
	engine, db, _ := setupEngineTest(t)

	// 添加多个修改器
	engine.AddModifier("p1", AttributeModifier{
		ID:         "mod_001",
		SourceType: "equipment",
		SourceID:   "sword_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
	})
	engine.AddModifier("p1", AttributeModifier{
		ID:         "mod_002",
		SourceType: "equipment",
		SourceID:   "sword_001",
		AttrType:   AttrCritRate,
		ModType:    ModTypeFlat,
		Value:      5,
	})
	engine.AddModifier("p1", AttributeModifier{
		ID:         "mod_003",
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrSpeed,
		ModType:    ModTypeFlat,
		Value:      2,
	})

	// 移除装备来源的所有修改器
	err := engine.RemoveModifiersBySource("p1", "equipment", "sword_001")
	assert.NoError(t, err)

	// 检查装备修改器已删除
	var count int64
	db.Model(&PlayerModifier{}).Where("player_id = ? AND source_type = ? AND source_id = ?", "p1", "equipment", "sword_001").Count(&count)
	assert.Equal(t, int64(0), count)

	// 检查buff修改器仍存在
	db.Model(&PlayerModifier{}).Where("player_id = ? AND source_type = ?", "p1", "buff").Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestGetModifiers(t *testing.T) {
	engine, _, _ := setupEngineTest(t)

	// 添加多个修改器
	engine.AddModifier("p1", AttributeModifier{
		ID:         "mod_001",
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
		Priority:   100,
	})
	engine.AddModifier("p1", AttributeModifier{
		ID:         "mod_002",
		SourceType: "equipment",
		SourceID:   "sword_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      20,
		Priority:   50,
	})

	mods, err := engine.GetModifiers("p1")
	assert.NoError(t, err)
	assert.Len(t, mods, 2)

	// 检查按优先级排序（降序）
	assert.Equal(t, int32(100), mods[0].Priority)
	assert.Equal(t, int32(50), mods[1].Priority)
}

func TestGetModifiers_FilterExpired(t *testing.T) {
	engine, _, mr := setupEngineTest(t)

	// 添加一个已过期的修改器
	engine.AddModifier("p1", AttributeModifier{
		ID:         "mod_expired",
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
		Duration:   1, // 1秒后过期
		Priority:   100,
	})

	// 添加一个永久修改器
	engine.AddModifier("p1", AttributeModifier{
		ID:         "mod_permanent",
		SourceType: "equipment",
		SourceID:   "sword_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      20,
		Duration:   0,
		Priority:   50,
	})

	// 快进时间
	mr.FastForward(2 * time.Second)

	mods, err := engine.GetModifiers("p1")
	assert.NoError(t, err)
	assert.Len(t, mods, 1) // 只返回未过期的
	assert.Equal(t, "mod_permanent", mods[0].ID)
}

func TestCleanExpiredModifiers(t *testing.T) {
	engine, db, mr := setupEngineTest(t)

	// 添加过期修改器
	engine.AddModifier("p1", AttributeModifier{
		ID:         "mod_expired",
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
		Duration:   1,
	})

	// 快进时间
	mr.FastForward(2 * time.Second)

	err := engine.CleanExpiredModifiers("p1")
	assert.NoError(t, err)

	// 检查已删除
	var count int64
	db.Model(&PlayerModifier{}).Where("player_id = ?", "p1").Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestComputeAttributes_FlatModifier(t *testing.T) {
	engine, _, _ := setupEngineTest(t)

	base := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}

	// 添加固定值修改器
	engine.AddModifier("p1", AttributeModifier{
		SourceType: "equipment",
		SourceID:   "sword_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      20,
	})

	computed, err := engine.ComputeAttributes("p1", base)
	assert.NoError(t, err)
	assert.Equal(t, int32(30), computed.Final[AttrAttack]) // 10 + 20
	assert.Equal(t, int32(5), computed.Final[AttrDefense])  // 未修改
}

func TestComputeAttributes_PercentModifier(t *testing.T) {
	engine, _, _ := setupEngineTest(t)

	base := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}

	// 添加百分比修改器（基于基础值）
	engine.AddModifier("p1", AttributeModifier{
		SourceType: "skill",
		SourceID:   "skill_001",
		AttrType:   AttrAttack,
		ModType:    ModTypePercent,
		Value:      50, // +50%
	})

	computed, err := engine.ComputeAttributes("p1", base)
	assert.NoError(t, err)
	assert.Equal(t, int32(15), computed.Final[AttrAttack]) // 10 + (10 * 50 / 100) = 15
}

func TestComputeAttributes_PercentAllModifier(t *testing.T) {
	engine, _, _ := setupEngineTest(t)

	base := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}

	// 添加百分比修改器（基于总值）
	engine.AddModifier("p1", AttributeModifier{
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypePercentAll,
		Value:      50, // +50%
	})

	computed, err := engine.ComputeAttributes("p1", base)
	assert.NoError(t, err)
	assert.Equal(t, int32(15), computed.Final[AttrAttack]) // 10 + (10 * 50 / 100) = 15
}

func TestComputeAttributes_MixedModifiers(t *testing.T) {
	engine, _, _ := setupEngineTest(t)

	base := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}

	// 添加多种修改器
	engine.AddModifier("p1", AttributeModifier{
		SourceType: "equipment",
		SourceID:   "sword_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10, // +10
		Priority:   50,
	})
	engine.AddModifier("p1", AttributeModifier{
		SourceType: "skill",
		SourceID:   "skill_001",
		AttrType:   AttrAttack,
		ModType:    ModTypePercent,
		Value:      50, // +50% of base
		Priority:   100,
	})
	engine.AddModifier("p1", AttributeModifier{
		SourceType: "buff",
		SourceID:   "buff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypePercentAll,
		Value:      20, // +20% of total
		Priority:   200,
	})

	computed, err := engine.ComputeAttributes("p1", base)
	assert.NoError(t, err)

	// 计算顺序：
	// 1. base = 10
	// 2. flat: 10 + 10 = 20
	// 3. percent (base): 20 + (10 * 50 / 100) = 20 + 5 = 25
	// 4. percent_all: 25 + (25 * 20 / 100) = 25 + 5 = 30
	assert.Equal(t, int32(30), computed.Final[AttrAttack])
}

func TestComputeAttributes_CritRateCap(t *testing.T) {
	engine, _, _ := setupEngineTest(t)

	base := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   50,
		CritDamage: 50,
	}

	// 添加暴击率修改器，超过上限
	engine.AddModifier("p1", AttributeModifier{
		SourceType: "equipment",
		SourceID:   "ring_001",
		AttrType:   AttrCritRate,
		ModType:    ModTypeFlat,
		Value:      50,
	})

	computed, err := engine.ComputeAttributes("p1", base)
	assert.NoError(t, err)
	assert.Equal(t, critRateCap, computed.Final[AttrCritRate]) // 不超过75
}

func TestComputeAttributes_NegativeValue(t *testing.T) {
	engine, _, _ := setupEngineTest(t)

	base := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}

	// 添加负值修改器（debuff）
	engine.AddModifier("p1", AttributeModifier{
		SourceType: "debuff",
		SourceID:   "debuff_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      -20, // -20攻击
	})

	computed, err := engine.ComputeAttributes("p1", base)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), computed.Final[AttrAttack]) // 不低于0
}

func TestComputeAttributes_Breakdown(t *testing.T) {
	engine, _, _ := setupEngineTest(t)

	base := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}

	engine.AddModifier("p1", AttributeModifier{
		SourceType: "equipment",
		SourceID:   "sword_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
	})

	computed, err := engine.ComputeAttributes("p1", base)
	assert.NoError(t, err)

	// 检查breakdown
	breakdown := computed.Breakdown[AttrAttack]
	assert.Len(t, breakdown, 2) // base + equipment
	assert.Equal(t, "base", breakdown[0].Source)
	assert.Equal(t, int32(10), breakdown[0].Value)
	assert.Equal(t, "equipment:sword_001", breakdown[1].Source)
	assert.Equal(t, int32(10), breakdown[1].Value)
}

func TestGetFinalAttributes_Cache(t *testing.T) {
	engine, _, mr := setupEngineTest(t)

	base := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}

	engine.AddModifier("p1", AttributeModifier{
		SourceType: "equipment",
		SourceID:   "sword_001",
		AttrType:   AttrAttack,
		ModType:    ModTypeFlat,
		Value:      10,
	})

	// 第一次获取（写缓存）
	final, err := engine.GetFinalAttributes("p1", base)
	assert.NoError(t, err)
	assert.Equal(t, int32(20), final[AttrAttack])

	// 检查缓存
	cached, _ := mr.Get("player:final_attr:p1")
	assert.NotEmpty(t, cached)

	// 第二次获取（从缓存）
	final2, err := engine.GetFinalAttributes("p1", base)
	assert.NoError(t, err)
	assert.Equal(t, int32(20), final2[AttrAttack])
}

func TestCreateBuffModifier(t *testing.T) {
	mod := CreateBuffModifier("buff_001", AttrAttack, 10, 60)
	assert.Equal(t, "buff", mod.SourceType)
	assert.Equal(t, "buff_001", mod.SourceID)
	assert.Equal(t, AttrAttack, mod.AttrType)
	assert.Equal(t, ModTypeFlat, mod.ModType)
	assert.Equal(t, int32(10), mod.Value)
	assert.Equal(t, int64(60), mod.Duration)
	assert.Equal(t, int32(100), mod.Priority)
}

func TestCreateDebuffModifier(t *testing.T) {
	mod := CreateDebuffModifier("debuff_001", AttrAttack, 10, 60)
	assert.Equal(t, "debuff", mod.SourceType)
	assert.Equal(t, "debuff_001", mod.SourceID)
	assert.Equal(t, AttrAttack, mod.AttrType)
	assert.Equal(t, ModTypeFlat, mod.ModType)
	assert.Equal(t, int32(-10), mod.Value) // 负值
	assert.Equal(t, int64(60), mod.Duration)
	assert.Equal(t, int32(100), mod.Priority)
}

func TestCreateEquipmentModifier(t *testing.T) {
	mod := CreateEquipmentModifier("sword_001", AttrAttack, 20, ModTypeFlat)
	assert.Equal(t, "equipment", mod.SourceType)
	assert.Equal(t, "sword_001", mod.SourceID)
	assert.Equal(t, AttrAttack, mod.AttrType)
	assert.Equal(t, ModTypeFlat, mod.ModType)
	assert.Equal(t, int32(20), mod.Value)
	assert.Equal(t, int64(0), mod.Duration) // 永久
	assert.Equal(t, int32(50), mod.Priority)
}

func TestCreateSkillModifier(t *testing.T) {
	mod := CreateSkillModifier("skill_001", AttrAttack, 50, 30)
	assert.Equal(t, "skill", mod.SourceType)
	assert.Equal(t, "skill_001", mod.SourceID)
	assert.Equal(t, AttrAttack, mod.AttrType)
	assert.Equal(t, ModTypePercent, mod.ModType)
	assert.Equal(t, int32(50), mod.Value)
	assert.Equal(t, int64(30), mod.Duration)
	assert.Equal(t, int32(200), mod.Priority)
}
