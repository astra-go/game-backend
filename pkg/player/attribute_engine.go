package player

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ========== 属性计算引擎 ==========

// AttributeModifier 属性修改器（buff/debuff/装备加成）
type AttributeModifier struct {
	ID         string        `json:"id"`
	SourceType string        `json:"source_type"` // equipment, buff, skill, passive
	SourceID   string        `json:"source_id"`
	AttrType   AttributeType `json:"attr_type"`
	ModType    ModifierType  `json:"mod_type"`
	Value      int32         `json:"value"`
	Duration   int64         `json:"duration"`   // 持续时间（秒），0表示永久
	ExpiresAt  int64         `json:"expires_at"` // 过期时间戳
	Priority   int32         `json:"priority"`   // 优先级，用于排序
}

// ModifierType 修改器类型
type ModifierType string

const (
	ModTypeFlat       ModifierType = "flat"        // 固定值加成
	ModTypePercent    ModifierType = "percent"     // 百分比加成（基于基础值）
	ModTypePercentAll ModifierType = "percent_all" // 百分比加成（基于总值）
)

// ComputedAttributes 计算后的属性
type ComputedAttributes struct {
	PlayerID   string                       `json:"player_id"`
	Base       *PlayerAttributes            `json:"base"`       // 基础属性
	Modifiers  []AttributeModifier          `json:"modifiers"`  // 所有修改器
	Final      map[AttributeType]int32      `json:"final"`      // 最终属性
	Breakdown  map[AttributeType][]Modifier `json:"breakdown"`  // 属性来源分解
	ComputedAt int64                        `json:"computed_at"`
}

// Modifier 修改器详情（用于breakdown）
type Modifier struct {
	Source string `json:"source"`
	Value  int32  `json:"value"`
	Type   string `json:"type"`
}

// AttributeEngine 属性计算引擎
type AttributeEngine struct {
	db     *gorm.DB
	redis  *redis.Client
	logger *zap.Logger
}

// NewAttributeEngine 创建属性计算引擎
func NewAttributeEngine(db *gorm.DB, redis *redis.Client, logger *zap.Logger) *AttributeEngine {
	return &AttributeEngine{
		db:     db,
		redis:  redis,
		logger: logger,
	}
}

// Init 初始化
func (e *AttributeEngine) Init() error {
	e.logger.Info("AttributeEngine 初始化")
	return e.db.AutoMigrate(&PlayerModifier{})
}

// PlayerModifier 玩家修改器持久化表
type PlayerModifier struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	PlayerID   string    `json:"player_id" gorm:"index"`
	ModifierID string    `json:"modifier_id" gorm:"uniqueIndex"`
	SourceType string    `json:"source_type"`
	SourceID   string    `json:"source_id"`
	AttrType   string    `json:"attr_type"`
	ModType    string    `json:"mod_type"`
	Value      int32     `json:"value"`
	Duration   int64     `json:"duration"`
	ExpiresAt  int64     `json:"expires_at"`
	Priority   int32     `json:"priority"`
	CreatedAt  time.Time `json:"created_at"`
}

// TableName 指定表名
func (PlayerModifier) TableName() string {
	return "player_modifiers"
}

// AddModifier 添加修改器
func (e *AttributeEngine) AddModifier(playerID string, mod AttributeModifier) error {
	if mod.ID == "" {
		mod.ID = fmt.Sprintf("%s_%s_%d", mod.SourceType, mod.SourceID, time.Now().UnixNano())
	}

	if mod.Duration > 0 {
		mod.ExpiresAt = time.Now().Unix() + mod.Duration
	}

	pm := &PlayerModifier{
		PlayerID:   playerID,
		ModifierID: mod.ID,
		SourceType: mod.SourceType,
		SourceID:   mod.SourceID,
		AttrType:   string(mod.AttrType),
		ModType:    string(mod.ModType),
		Value:      mod.Value,
		Duration:   mod.Duration,
		ExpiresAt:  mod.ExpiresAt,
		Priority:   mod.Priority,
	}

	err := e.db.Create(pm).Error
	if err != nil {
		return err
	}

	e.clearAttrCache(playerID)

	e.logger.Debug("添加修改器",
		zap.String("player_id", playerID),
		zap.String("modifier_id", mod.ID),
		zap.String("attr", string(mod.AttrType)),
		zap.Int32("value", mod.Value),
	)

	return nil
}

// RemoveModifier 移除修改器
func (e *AttributeEngine) RemoveModifier(playerID, modifierID string) error {
	err := e.db.Where("player_id = ? AND modifier_id = ?", playerID, modifierID).Delete(&PlayerModifier{}).Error
	if err != nil {
		return err
	}

	e.clearAttrCache(playerID)
	return nil
}

// RemoveModifiersBySource 移除指定来源的所有修改器
func (e *AttributeEngine) RemoveModifiersBySource(playerID, sourceType, sourceID string) error {
	err := e.db.Where("player_id = ? AND source_type = ? AND source_id = ?", playerID, sourceType, sourceID).
		Delete(&PlayerModifier{}).Error
	if err != nil {
		return err
	}

	e.clearAttrCache(playerID)
	return nil
}

// GetModifiers 获取玩家所有有效修改器
func (e *AttributeEngine) GetModifiers(playerID string) ([]AttributeModifier, error) {
	var pms []PlayerModifier
	now := time.Now().Unix()

	err := e.db.Where("player_id = ? AND (expires_at = 0 OR expires_at > ?)", playerID, now).
		Order("priority DESC").
		Find(&pms).Error
	if err != nil {
		return nil, err
	}

	mods := make([]AttributeModifier, len(pms))
	for i, pm := range pms {
		mods[i] = AttributeModifier{
			ID:         pm.ModifierID,
			SourceType: pm.SourceType,
			SourceID:   pm.SourceID,
			AttrType:   AttributeType(pm.AttrType),
			ModType:    ModifierType(pm.ModType),
			Value:      pm.Value,
			Duration:   pm.Duration,
			ExpiresAt:  pm.ExpiresAt,
			Priority:   pm.Priority,
		}
	}

	return mods, nil
}

// CleanExpiredModifiers 清理过期修改器
func (e *AttributeEngine) CleanExpiredModifiers(playerID string) error {
	now := time.Now().Unix()
	err := e.db.Where("player_id = ? AND expires_at > 0 AND expires_at <= ?", playerID, now).
		Delete(&PlayerModifier{}).Error
	if err != nil {
		return err
	}

	e.clearAttrCache(playerID)
	return nil
}

// ComputeAttributes 计算玩家最终属性
func (e *AttributeEngine) ComputeAttributes(playerID string, base *PlayerAttributes) (*ComputedAttributes, error) {
	// 清理过期修改器
	e.CleanExpiredModifiers(playerID)

	// 获取所有修改器
	mods, err := e.GetModifiers(playerID)
	if err != nil {
		return nil, err
	}

	// 初始化最终属性为基础属性
	final := map[AttributeType]int32{
		AttrHP:         base.HP,
		AttrMaxHP:      base.MaxHP,
		AttrAttack:     base.Attack,
		AttrDefense:    base.Defense,
		AttrSpeed:      base.Speed,
		AttrCritRate:   base.CritRate,
		AttrCritDamage: base.CritDamage,
	}

	// 属性来源分解
	breakdown := make(map[AttributeType][]Modifier)
	for attr := range final {
		breakdown[attr] = []Modifier{
			{Source: "base", Value: final[attr], Type: "base"},
		}
	}

	// 按优先级分组修改器
	flatMods := make(map[AttributeType][]AttributeModifier)
	percentMods := make(map[AttributeType][]AttributeModifier)
	percentAllMods := make(map[AttributeType][]AttributeModifier)

	for _, mod := range mods {
		switch mod.ModType {
		case ModTypeFlat:
			flatMods[mod.AttrType] = append(flatMods[mod.AttrType], mod)
		case ModTypePercent:
			percentMods[mod.AttrType] = append(percentMods[mod.AttrType], mod)
		case ModTypePercentAll:
			percentAllMods[mod.AttrType] = append(percentAllMods[mod.AttrType], mod)
		}
	}

	// 计算顺序：基础值 → 固定加成 → 百分比加成（基于基础） → 百分比加成（基于总值）
	for attr := range final {
		baseValue := final[attr]

		// 1. 固定加成
		for _, mod := range flatMods[attr] {
			final[attr] += mod.Value
			breakdown[attr] = append(breakdown[attr], Modifier{
				Source: fmt.Sprintf("%s:%s", mod.SourceType, mod.SourceID),
				Value:  mod.Value,
				Type:   "flat",
			})
		}

		// 2. 百分比加成（基于基础值）
		for _, mod := range percentMods[attr] {
			bonus := baseValue * mod.Value / 100
			final[attr] += bonus
			breakdown[attr] = append(breakdown[attr], Modifier{
				Source: fmt.Sprintf("%s:%s", mod.SourceType, mod.SourceID),
				Value:  bonus,
				Type:   "percent",
			})
		}

		// 3. 百分比加成（基于当前总值）
		for _, mod := range percentAllMods[attr] {
			bonus := final[attr] * mod.Value / 100
			final[attr] += bonus
			breakdown[attr] = append(breakdown[attr], Modifier{
				Source: fmt.Sprintf("%s:%s", mod.SourceType, mod.SourceID),
				Value:  bonus,
				Type:   "percent_all",
			})
		}

		// 暴击率上限
		if attr == AttrCritRate && final[attr] > critRateCap {
			final[attr] = critRateCap
		}

		// 属性不能为负
		if final[attr] < 0 {
			final[attr] = 0
		}
	}

	result := &ComputedAttributes{
		PlayerID:   playerID,
		Base:       base,
		Modifiers:  mods,
		Final:      final,
		Breakdown:  breakdown,
		ComputedAt: time.Now().Unix(),
	}

	return result, nil
}

// GetFinalAttributes 获取玩家最终属性（带缓存）
func (e *AttributeEngine) GetFinalAttributes(playerID string, base *PlayerAttributes) (map[AttributeType]int32, error) {
	// 尝试从缓存读取
	cacheKey := fmt.Sprintf("player:final_attr:%s", playerID)
	cached, err := e.redis.Get(ctx, cacheKey).Bytes()
	if err == nil {
		var final map[AttributeType]int32
		if jsonErr := json.Unmarshal(cached, &final); jsonErr == nil {
			return final, nil
		}
	}

	// 计算属性
	computed, err := e.ComputeAttributes(playerID, base)
	if err != nil {
		return nil, err
	}

	// 写入缓存（5分钟）
	data, _ := json.Marshal(computed.Final)
	e.redis.Set(ctx, cacheKey, data, 5*time.Minute)

	return computed.Final, nil
}

// clearAttrCache 清除属性缓存
func (e *AttributeEngine) clearAttrCache(playerID string) {
	e.redis.Del(ctx, fmt.Sprintf("player:final_attr:%s", playerID))
	e.redis.Del(ctx, attrCachePrefix+playerID)
}

// ========== 预设修改器工厂 ==========

// CreateBuffModifier 创建Buff修改器
func CreateBuffModifier(buffID string, attrType AttributeType, value int32, duration int64) AttributeModifier {
	return AttributeModifier{
		SourceType: "buff",
		SourceID:   buffID,
		AttrType:   attrType,
		ModType:    ModTypeFlat,
		Value:      value,
		Duration:   duration,
		Priority:   100,
	}
}

// CreateDebuffModifier 创建Debuff修改器
func CreateDebuffModifier(debuffID string, attrType AttributeType, value int32, duration int64) AttributeModifier {
	return AttributeModifier{
		SourceType: "debuff",
		SourceID:   debuffID,
		AttrType:   attrType,
		ModType:    ModTypeFlat,
		Value:      -value, // 负值
		Duration:   duration,
		Priority:   100,
	}
}

// CreateEquipmentModifier 创建装备修改器
func CreateEquipmentModifier(equipID string, attrType AttributeType, value int32, modType ModifierType) AttributeModifier {
	return AttributeModifier{
		SourceType: "equipment",
		SourceID:   equipID,
		AttrType:   attrType,
		ModType:    modType,
		Value:      value,
		Duration:   0, // 永久
		Priority:   50,
	}
}

// CreateSkillModifier 创建技能修改器
func CreateSkillModifier(skillID string, attrType AttributeType, value int32, duration int64) AttributeModifier {
	return AttributeModifier{
		SourceType: "skill",
		SourceID:   skillID,
		AttrType:   attrType,
		ModType:    ModTypePercent,
		Value:      value,
		Duration:   duration,
		Priority:   200,
	}
}
