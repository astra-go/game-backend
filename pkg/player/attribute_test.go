package player

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 使用sqlite内存数据库+miniredis进行集成测试

func setupAttrTest(t *testing.T) (*PlayerAttributeComponent, *gorm.DB, *miniredis.Miniredis) {
	t.Helper()

	// 内存SQLite
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	// 建Player表（AddExp/AddGold/AddDiamond需要）
	db.Exec("CREATE TABLE IF NOT EXISTS players (id TEXT PRIMARY KEY, level INTEGER DEFAULT 1, exp INTEGER DEFAULT 0, gold INTEGER DEFAULT 0, diamond INTEGER DEFAULT 0)")

	// miniredis
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	logger := zap.NewNop()
	comp := NewPlayerAttributeComponent(db, rdb, logger)
	err = comp.Init()
	assert.NoError(t, err)

	t.Cleanup(func() { rdb.Close() })

	return comp, db, mr
}

func TestAttributeInit(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	// 检查表是否存在
	var count int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='player_attributes'").Scan(&count)
	assert.Equal(t, int64(1), count)

	// 再次Init不应报错
	err := comp.Init()
	assert.NoError(t, err)
}

func TestGetAttributes_NotFound(t *testing.T) {
	comp, _, _ := setupAttrTest(t)

	_, err := comp.GetAttributes("nonexist")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "玩家属性不存在")
}

func TestGetAttributes_CacheHit(t *testing.T) {
	comp, db, mr := setupAttrTest(t)

	// 创建属性
	attrs := &PlayerAttributes{
		PlayerID:   "p1",
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}
	db.Create(attrs)

	// 第一次获取（写缓存）
	got, err := comp.GetAttributes("p1")
	assert.NoError(t, err)
	assert.Equal(t, int32(100), got.HP)

	// 直接修改DB
	db.Model(&PlayerAttributes{}).Where("player_id = ?", "p1").Update("hp", 200)

	// 从缓存获取（应返回旧值）
	cached, _ := mr.Get("player:attr:p1")
	assert.NotEmpty(t, cached)
}

func TestUpdateAttribute(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	// 创建属性
	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 100, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	err := comp.UpdateAttribute("p1", AttrAttack, 20)
	assert.NoError(t, err)

	var updated PlayerAttributes
	db.Where("player_id = ?", "p1").First(&updated)
	assert.Equal(t, int32(20), updated.Attack)
}

func TestUpdateAttribute_CritRateCap(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 100, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	err := comp.UpdateAttribute("p1", AttrCritRate, 100)
	assert.NoError(t, err)

	var updated PlayerAttributes
	db.Where("player_id = ?", "p1").First(&updated)
	assert.Equal(t, critRateCap, updated.CritRate)
}

func TestUpdateAttribute_NotFound(t *testing.T) {
	comp, _, _ := setupAttrTest(t)

	err := comp.UpdateAttribute("nonexist", AttrAttack, 10)
	assert.Error(t, err)
}

func TestHeal(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 50, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	err := comp.Heal("p1", 30)
	assert.NoError(t, err)

	var updated PlayerAttributes
	db.Where("player_id = ?", "p1").First(&updated)
	assert.Equal(t, int32(80), updated.HP)
}

func TestHeal_ExceedMaxHP(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 90, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	err := comp.Heal("p1", 50)
	assert.NoError(t, err)

	var updated PlayerAttributes
	db.Where("player_id = ?", "p1").First(&updated)
	assert.Equal(t, int32(100), updated.HP) // 不超过MaxHP
}

func TestTakeDamage(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 100, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	err := comp.TakeDamage("p1", 30)
	assert.NoError(t, err)

	var updated PlayerAttributes
	db.Where("player_id = ?", "p1").First(&updated)
	assert.Equal(t, int32(70), updated.HP)
}

func TestTakeDamage_NotBelowZero(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 10, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	err := comp.TakeDamage("p1", 50)
	assert.NoError(t, err)

	var updated PlayerAttributes
	db.Where("player_id = ?", "p1").First(&updated)
	assert.Equal(t, int32(0), updated.HP) // 不低于0
}

func TestAddExp_LevelUp(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	// 创建玩家（level 1, exp 0）
	db.Exec("INSERT INTO players (id, level, exp, gold, diamond) VALUES (?, 1, 0, 0, 0)", "p1")

	// 创建属性
	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 100, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	// 加50 exp → level 1→2需要50 exp
	err := comp.AddExp("p1", 50)
	assert.NoError(t, err)

	// 检查等级
	var level int32
	var exp int64
	db.Raw("SELECT level, exp FROM players WHERE id = ?", "p1").Row().Scan(&level, &exp)
	assert.Equal(t, int32(2), level)
	assert.Equal(t, int64(0), exp) // exp重置

	// 检查属性成长
	var attrs PlayerAttributes
	db.Where("player_id = ?", "p1").First(&attrs)
	assert.Equal(t, int32(110), attrs.HP)     // +10
	assert.Equal(t, int32(110), attrs.MaxHP)  // +10
	assert.Equal(t, int32(12), attrs.Attack)  // +2
	assert.Equal(t, int32(6), attrs.Defense)  // +1
	assert.Equal(t, int32(6), attrs.Speed)     // +1
}

func TestAddExp_MultipleLevelUp(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Exec("INSERT INTO players (id, level, exp, gold, diamond) VALUES (?, 1, 0, 0, 0)", "p1")
	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 100, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	// 加150 exp → level 1→2 (50), 2→3 (100), 共升2级
	err := comp.AddExp("p1", 150)
	assert.NoError(t, err)

	var level int32
	var exp int64
	db.Raw("SELECT level, exp FROM players WHERE id = ?", "p1").Row().Scan(&level, &exp)
	assert.Equal(t, int32(3), level)
	assert.Equal(t, int64(0), exp) // 150 - 50 - 100 = 0
}

func TestAddExp_NotEnough(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Exec("INSERT INTO players (id, level, exp, gold, diamond) VALUES (?, 1, 0, 0, 0)", "p1")
	db.Create(&PlayerAttributes{
		PlayerID: "p1", HP: 100, MaxHP: 100, Attack: 10, Defense: 5, Speed: 5, CritRate: 5, CritDamage: 50,
	})

	err := comp.AddExp("p1", 20)
	assert.NoError(t, err)

	var level int32
	var exp int64
	db.Raw("SELECT level, exp FROM players WHERE id = ?", "p1").Row().Scan(&level, &exp)
	assert.Equal(t, int32(1), level)
	assert.Equal(t, int64(20), exp) // 不够升级
}

func TestAddGold(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Exec("INSERT INTO players (id, level, exp, gold, diamond) VALUES (?, 1, 0, 100, 0)", "p1")

	err := comp.AddGold("p1", 50)
	assert.NoError(t, err)

	var gold int64
	db.Raw("SELECT gold FROM players WHERE id = ?", "p1").Row().Scan(&gold)
	assert.Equal(t, int64(150), gold)
}

func TestAddGold_NotBelowZero(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Exec("INSERT INTO players (id, level, exp, gold, diamond) VALUES (?, 1, 0, 10, 0)", "p1")

	err := comp.AddGold("p1", -100)
	assert.NoError(t, err)

	var gold int64
	db.Raw("SELECT gold FROM players WHERE id = ?", "p1").Row().Scan(&gold)
	assert.Equal(t, int64(0), gold)
}

func TestAddDiamond(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	db.Exec("INSERT INTO players (id, level, exp, gold, diamond) VALUES (?, 1, 0, 0, 100)", "p1")

	err := comp.AddDiamond("p1", 50)
	assert.NoError(t, err)

	var diamond int64
	db.Raw("SELECT diamond FROM players WHERE id = ?", "p1").Row().Scan(&diamond)
	assert.Equal(t, int64(150), diamond)
}

func TestEnsureDefaultAttributes(t *testing.T) {
	comp, db, _ := setupAttrTest(t)

	err := comp.EnsureDefaultAttributes("newplayer")
	assert.NoError(t, err)

	var attrs PlayerAttributes
	err = db.Where("player_id = ?", "newplayer").First(&attrs).Error
	assert.NoError(t, err)
	assert.Equal(t, int32(100), attrs.HP)
	assert.Equal(t, int32(50), attrs.CritDamage)

	// 再次调用不应报错
	err = comp.EnsureDefaultAttributes("newplayer")
	assert.NoError(t, err)
}

func TestRequiredExp(t *testing.T) {
	assert.Equal(t, int64(50), RequiredExp(1))   // 1→2
	assert.Equal(t, int64(100), RequiredExp(2))  // 2→3
	assert.Equal(t, int64(150), RequiredExp(3))  // 3→4
	assert.Equal(t, int64(0), RequiredExp(0))    // 无效
	assert.Equal(t, int64(0), RequiredExp(-1))   // 无效
}

func TestCritRateCap(t *testing.T) {
	assert.Equal(t, int32(75), critRateCap)
}
