package player

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/astra-go/astra/log"
	"gorm.io/gorm"
)

// ========== 属性相关数据结构 ==========

// AttributeType 属性类型
type AttributeType string

const (
	AttrHP         AttributeType = "hp"
	AttrMaxHP      AttributeType = "max_hp"
	AttrAttack     AttributeType = "attack"
	AttrDefense    AttributeType = "defense"
	AttrSpeed      AttributeType = "speed"
	AttrCritRate   AttributeType = "crit_rate"
	AttrCritDamage AttributeType = "crit_damage"
)

// PlayerAttributes 玩家属性表
type PlayerAttributes struct {
	PlayerID    string    `json:"player_id" gorm:"primaryKey"`
	HP          int32     `json:"hp" gorm:"default:100"`
	MaxHP       int32     `json:"max_hp" gorm:"default:100"`
	Attack      int32     `json:"attack" gorm:"default:10"`
	Defense     int32     `json:"defense" gorm:"default:5"`
	Speed       int32     `json:"speed" gorm:"default:5"`
	CritRate    int32     `json:"crit_rate" gorm:"default:5"`  // 百分比，5表示5%
	CritDamage  int32     `json:"crit_damage" gorm:"default:50"` // 百分比，50表示150%暴击伤害
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名
func (PlayerAttributes) TableName() string {
	return "player_attributes"
}

// Redis缓存Key前缀
const attrCachePrefix = "player:attr:"

// Redis缓存TTL
const attrCacheTTL = 10 * time.Minute

// CritRate上限
const critRateCap int32 = 75

// ========== 等级成长表 ==========

// LevelGrowthTable 等级成长表：每升级一次的属性增长
var LevelGrowthTable = map[AttributeType]int32{
	AttrHP:         10,
	AttrMaxHP:      10,
	AttrAttack:     2,
	AttrDefense:    1,
	AttrSpeed:      1,
	AttrCritRate:   1, // 每2级实际+0.5，但int32存储，此处存1，调用处做/2处理
	AttrCritDamage: 1,
}

// RequiredExp 计算从当前level升到下一级所需经验值
// 公式: level * 50 (level 1→2需要50, 2→3需要100, ...)
func RequiredExp(level int32) int64 {
	if level < 1 {
		return 0
	}
	return int64(level) * 50
}

// ========== PlayerAttributeComponent ==========

// PlayerAttributeComponent 玩家属性组件
type PlayerAttributeComponent struct {
	db     *gorm.DB
	redis  *redis.Client
	logger *log.Logger
}

// NewPlayerAttributeComponent 创建玩家属性组件
func NewPlayerAttributeComponent(db *gorm.DB, redis *redis.Client, logger *log.Logger) *PlayerAttributeComponent {
	return &PlayerAttributeComponent{
		db:     db,
		redis:  redis,
		logger: logger,
	}
}

// Init 初始化：AutoMigrate属性表
func (a *PlayerAttributeComponent) Init() error {
	a.logger.Info("PlayerAttributeComponent 初始化")
	return a.db.AutoMigrate(&PlayerAttributes{})
}

// GetAttributes 获取玩家所有属性，优先从Redis缓存读取
func (a *PlayerAttributeComponent) GetAttributes(playerID string) (*PlayerAttributes, error) {
	// 先查Redis缓存
	cacheKey := attrCachePrefix + playerID
	cached, err := a.redis.Get(ctx, cacheKey).Bytes()
	if err == nil {
		var attrs PlayerAttributes
		if jsonErr := json.Unmarshal(cached, &attrs); jsonErr == nil {
			return &attrs, nil
		}
		// 缓存数据损坏，删除后走DB
		a.redis.Del(ctx, cacheKey)
	}

	// 从DB查询
	var attrs PlayerAttributes
	err = a.db.Where("player_id = ?", playerID).First(&attrs).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("玩家属性不存在: %s", playerID)
		}
		return nil, err
	}

	// 写入Redis缓存
	a.setAttrCache(playerID, &attrs)

	return &attrs, nil
}

// UpdateAttribute 更新玩家单个属性，同时写DB和Redis
func (a *PlayerAttributeComponent) UpdateAttribute(playerID string, attrType AttributeType, value int32) error {
	// 先确保属性记录存在
	var attrs PlayerAttributes
	err := a.db.Where("player_id = ?", playerID).First(&attrs).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("玩家属性不存在: %s", playerID)
		}
		return err
	}

	// 更新对应属性
	switch attrType {
	case AttrHP:
		attrs.HP = value
	case AttrMaxHP:
		attrs.MaxHP = value
	case AttrAttack:
		attrs.Attack = value
	case AttrDefense:
		attrs.Defense = value
	case AttrSpeed:
		attrs.Speed = value
	case AttrCritRate:
		if value > critRateCap {
			value = critRateCap
		}
		attrs.CritRate = value
	case AttrCritDamage:
		attrs.CritDamage = value
	default:
		return fmt.Errorf("未知属性类型: %s", attrType)
	}

	attrs.UpdatedAt = time.Now()
	err = a.db.Save(&attrs).Error
	if err != nil {
		return err
	}

	// 更新Redis缓存
	a.setAttrCache(playerID, &attrs)

	a.logger.Debug("属性更新",
		"player_id", playerID,
		"attr", string(attrType),
		"value", value,
	)

	return nil
}

// AddExp 增加经验值，自动处理升级逻辑
func (a *PlayerAttributeComponent) AddExp(playerID string, exp int64) error {
	// 查询玩家当前等级
	var player PlayerAttributes
	err := a.db.Where("player_id = ?", playerID).First(&player).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("玩家属性不存在: %s", playerID)
		}
		return err
	}

	// 需要从Player表获取当前等级（属性表不存level）
	// 但为了简化，我们在属性组件内直接用DB查Player表
	// 实际level存在Player表的level字段
	// 这里通过UpdateAttribute间接操作，但level的更新需要直接操作Player表
	return a.addExpInternal(playerID, exp)
}

// addExpInternal 内部增加经验值逻辑
func (a *PlayerAttributeComponent) addExpInternal(playerID string, exp int64) error {
	// 查询Player表获取当前等级和经验
	var level int32
	var currentExp int64
	err := a.db.Raw("SELECT level, exp FROM players WHERE id = ?", playerID).Row().Scan(&level, &currentExp)
	if err != nil {
		return fmt.Errorf("查询玩家等级失败: %w", err)
	}

	currentExp += exp

	// 循环检查升级
	leveled := false
	for {
		required := RequiredExp(level)
		if currentExp < required {
			break
		}
		currentExp -= required
		level++
		leveled = true
	}

	// 更新Player表
	err = a.db.Exec("UPDATE players SET level = ?, exp = ? WHERE id = ?", level, currentExp, playerID).Error
	if err != nil {
		return fmt.Errorf("更新玩家等级失败: %w", err)
	}

	// 如果升级了，按成长表增加属性
	if leveled {
		err = a.applyGrowth(playerID)
		if err != nil {
			return fmt.Errorf("应用成长属性失败: %w", err)
		}
		a.logger.Info("玩家升级",
			"player_id", playerID,
			"new_level", level,
		)
	}

	// 清除属性缓存（等级变化可能影响属性）
	a.redis.Del(ctx, attrCachePrefix+playerID)

	return nil
}

// LevelUp 手动触发升级检查
func (a *PlayerAttributeComponent) LevelUp(playerID string) error {
	return a.addExpInternal(playerID, 0)
}

// applyGrowth 按成长表增加属性
func (a *PlayerAttributeComponent) applyGrowth(playerID string) error {
	var attrs PlayerAttributes
	err := a.db.Where("player_id = ?", playerID).First(&attrs).Error
	if err != nil {
		return fmt.Errorf("获取玩家属性失败: %w", err)
	}

	attrs.HP += LevelGrowthTable[AttrHP]
	attrs.MaxHP += LevelGrowthTable[AttrMaxHP]
	attrs.Attack += LevelGrowthTable[AttrAttack]
	attrs.Defense += LevelGrowthTable[AttrDefense]
	attrs.Speed += LevelGrowthTable[AttrSpeed]
	// CritRate每2级+1（即平均每级+0.5）
	if attrs.CritRate%2 == 0 {
		attrs.CritRate++
	}
	if attrs.CritRate > critRateCap {
		attrs.CritRate = critRateCap
	}
	attrs.CritDamage += LevelGrowthTable[AttrCritDamage]
	attrs.UpdatedAt = time.Now()

	err = a.db.Save(&attrs).Error
	if err != nil {
		return err
	}

	return nil
}

// Heal 回血，不超过MaxHP
func (a *PlayerAttributeComponent) Heal(playerID string, amount int32) error {
	var attrs PlayerAttributes
	err := a.db.Where("player_id = ?", playerID).First(&attrs).Error
	if err != nil {
		return err
	}

	attrs.HP += amount
	if attrs.HP > attrs.MaxHP {
		attrs.HP = attrs.MaxHP
	}
	attrs.UpdatedAt = time.Now()

	err = a.db.Save(&attrs).Error
	if err != nil {
		return err
	}

	a.setAttrCache(playerID, &attrs)
	return nil
}

// TakeDamage 扣血，HP不低于0
func (a *PlayerAttributeComponent) TakeDamage(playerID string, amount int32) error {
	var attrs PlayerAttributes
	err := a.db.Where("player_id = ?", playerID).First(&attrs).Error
	if err != nil {
		return err
	}

	attrs.HP -= amount
	if attrs.HP < 0 {
		attrs.HP = 0
	}
	attrs.UpdatedAt = time.Now()

	err = a.db.Save(&attrs).Error
	if err != nil {
		return err
	}

	a.setAttrCache(playerID, &attrs)
	return nil
}

// AddGold 加金币，不低于0
func (a *PlayerAttributeComponent) AddGold(playerID string, amount int64) error {
	// 先查询当前金币
	var gold int64
	err := a.db.Raw("SELECT gold FROM players WHERE id = ?", playerID).Row().Scan(&gold)
	if err != nil {
		return fmt.Errorf("玩家不存在: %s", playerID)
	}
	gold += amount
	if gold < 0 {
		gold = 0
	}
	return a.db.Exec("UPDATE players SET gold = ? WHERE id = ?", gold, playerID).Error
}

// AddDiamond 加钻石，不低于0
func (a *PlayerAttributeComponent) AddDiamond(playerID string, amount int64) error {
	var diamond int64
	err := a.db.Raw("SELECT diamond FROM players WHERE id = ?", playerID).Row().Scan(&diamond)
	if err != nil {
		return fmt.Errorf("玩家不存在: %s", playerID)
	}
	diamond += amount
	if diamond < 0 {
		diamond = 0
	}
	return a.db.Exec("UPDATE players SET diamond = ? WHERE id = ?", diamond, playerID).Error
}

// setAttrCache 写入Redis属性缓存
func (a *PlayerAttributeComponent) setAttrCache(playerID string, attrs *PlayerAttributes) {
	data, err := json.Marshal(attrs)
	if err != nil {
		a.logger.Warn("序列化属性缓存失败", "error", err)
		return
	}
	if err := a.redis.Set(ctx, attrCachePrefix+playerID, data, attrCacheTTL).Err(); err != nil {
		a.logger.Warn("写入属性缓存失败", "error", err)
	}
}

// EnsureDefaultAttributes 确保玩家有默认属性记录（用于首次创建时调用）
func (a *PlayerAttributeComponent) EnsureDefaultAttributes(playerID string) error {
	var attrs PlayerAttributes
	err := a.db.Where("player_id = ?", playerID).First(&attrs).Error
	if err == nil {
		return nil // 已存在
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	attrs = PlayerAttributes{
		PlayerID:   playerID,
		HP:         100,
		MaxHP:      100,
		Attack:     10,
		Defense:    5,
		Speed:      5,
		CritRate:   5,
		CritDamage: 50,
	}
	return a.db.Create(&attrs).Error
}
