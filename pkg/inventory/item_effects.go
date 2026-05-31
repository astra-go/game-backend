package inventory

import (
	"encoding/json"
	"fmt"
)

// EffectType 效果类型
type EffectType string

const (
	EffectTypeHeal         EffectType = "heal"          // 恢复生命
	EffectTypeMana         EffectType = "mana"          // 恢复法力
	EffectTypeBuff         EffectType = "buff"          // 增益效果
	EffectTypeDebuff       EffectType = "debuff"        // 减益效果
	EffectTypeAttribute    EffectType = "attribute"     // 属性加成
	EffectTypeExp          EffectType = "exp"           // 经验加成
	EffectTypeGold         EffectType = "gold"          // 金币奖励
	EffectTypeDiamond      EffectType = "diamond"       // 钻石奖励
	EffectTypeUnlockItem   EffectType = "unlock_item"   // 解锁物品
	EffectTypeRandomReward EffectType = "random_reward" // 随机奖励
)

// ItemEffect 物品效果
type ItemEffect struct {
	Type     EffectType             `json:"type"`
	Value    float64                `json:"value"`
	Duration int32                  `json:"duration"` // 持续时间（秒），0表示永久
	Params   map[string]interface{} `json:"params"`   // 额外参数
}

// EffectResult 效果执行结果
type EffectResult struct {
	Success      bool                   `json:"success"`
	Message      string                 `json:"message"`
	Changes      map[string]interface{} `json:"changes"`       // 变化的属性
	RewardItems  []RewardItem           `json:"reward_items"`  // 奖励的物品
	BuffsApplied []BuffInfo             `json:"buffs_applied"` // 应用的buff
}

// RewardItem 奖励物品
type RewardItem struct {
	ItemID   string `json:"item_id"`
	Quantity int32  `json:"quantity"`
}

// BuffInfo buff信息
type BuffInfo struct {
	BuffID   string `json:"buff_id"`
	Duration int32  `json:"duration"`
	Value    float64 `json:"value"`
}

// ParseEffects 从JSON解析效果列表
func ParseEffects(data json.RawMessage) ([]ItemEffect, error) {
	if len(data) == 0 {
		return nil, nil
	}

	var effects []ItemEffect
	err := json.Unmarshal(data, &effects)
	if err != nil {
		return nil, fmt.Errorf("解析效果失败: %w", err)
	}

	return effects, nil
}

// ApplyEffect 应用单个效果
func ApplyEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	switch effect.Type {
	case EffectTypeHeal:
		return applyHealEffect(effect, playerID, context)
	case EffectTypeMana:
		return applyManaEffect(effect, playerID, context)
	case EffectTypeBuff:
		return applyBuffEffect(effect, playerID, context)
	case EffectTypeAttribute:
		return applyAttributeEffect(effect, playerID, context)
	case EffectTypeExp:
		return applyExpEffect(effect, playerID, context)
	case EffectTypeGold:
		return applyGoldEffect(effect, playerID, context)
	case EffectTypeDiamond:
		return applyDiamondEffect(effect, playerID, context)
	case EffectTypeUnlockItem:
		return applyUnlockItemEffect(effect, playerID, context)
	case EffectTypeRandomReward:
		return applyRandomRewardEffect(effect, playerID, context)
	default:
		return nil, fmt.Errorf("未知效果类型: %s", effect.Type)
	}
}

// EffectContext 效果执行上下文
type EffectContext struct {
	PlayerComponent    PlayerComponentInterface
	InventoryComponent *InventoryComponent
	AttributeEngine    AttributeEngineInterface
}

// PlayerComponentInterface 玩家组件接口（避免循环依赖）
type PlayerComponentInterface interface {
	GetByID(playerID string) (PlayerData, error)
	UpdatePlayer(playerID string, updates map[string]interface{}) error
}

// PlayerData 玩家数据接口
type PlayerData interface {
	GetID() string
	GetLevel() int32
	GetExp() int64
	GetGold() int64
	GetDiamond() int64
}

// AttributeEngineInterface 属性引擎接口
type AttributeEngineInterface interface {
	AddBuff(playerID string, buffID string, duration int32, value float64) error
	GetAttribute(playerID string, attrName string) (float64, error)
	SetAttribute(playerID string, attrName string, value float64) error
}

// applyHealEffect 恢复生命
func applyHealEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.AttributeEngine == nil {
		return nil, fmt.Errorf("属性引擎未初始化")
	}

	currentHP, err := context.AttributeEngine.GetAttribute(playerID, "hp")
	if err != nil {
		return nil, fmt.Errorf("获取当前生命值失败: %w", err)
	}

	maxHP, err := context.AttributeEngine.GetAttribute(playerID, "max_hp")
	if err != nil {
		maxHP = 1000 // 默认最大生命值
	}

	healAmount := effect.Value
	newHP := currentHP + healAmount
	if newHP > maxHP {
		newHP = maxHP
		healAmount = maxHP - currentHP
	}

	err = context.AttributeEngine.SetAttribute(playerID, "hp", newHP)
	if err != nil {
		return nil, fmt.Errorf("设置生命值失败: %w", err)
	}

	result.Changes["hp"] = newHP
	result.Changes["heal_amount"] = healAmount
	result.Message = fmt.Sprintf("恢复了 %.0f 点生命值", healAmount)

	return result, nil
}

// applyManaEffect 恢复法力
func applyManaEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.AttributeEngine == nil {
		return nil, fmt.Errorf("属性引擎未初始化")
	}

	currentMP, err := context.AttributeEngine.GetAttribute(playerID, "mp")
	if err != nil {
		return nil, fmt.Errorf("获取当前法力值失败: %w", err)
	}

	maxMP, err := context.AttributeEngine.GetAttribute(playerID, "max_mp")
	if err != nil {
		maxMP = 500 // 默认最大法力值
	}

	manaAmount := effect.Value
	newMP := currentMP + manaAmount
	if newMP > maxMP {
		newMP = maxMP
		manaAmount = maxMP - currentMP
	}

	err = context.AttributeEngine.SetAttribute(playerID, "mp", newMP)
	if err != nil {
		return nil, fmt.Errorf("设置法力值失败: %w", err)
	}

	result.Changes["mp"] = newMP
	result.Changes["mana_amount"] = manaAmount
	result.Message = fmt.Sprintf("恢复了 %.0f 点法力值", manaAmount)

	return result, nil
}

// applyBuffEffect 应用buff
func applyBuffEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.AttributeEngine == nil {
		return nil, fmt.Errorf("属性引擎未初始化")
	}

	buffID, ok := effect.Params["buff_id"].(string)
	if !ok {
		return nil, fmt.Errorf("buff_id参数缺失")
	}

	err := context.AttributeEngine.AddBuff(playerID, buffID, effect.Duration, effect.Value)
	if err != nil {
		return nil, fmt.Errorf("应用buff失败: %w", err)
	}

	result.BuffsApplied = append(result.BuffsApplied, BuffInfo{
		BuffID:   buffID,
		Duration: effect.Duration,
		Value:    effect.Value,
	})
	result.Message = fmt.Sprintf("获得了buff: %s", buffID)

	return result, nil
}

// applyAttributeEffect 属性加成
func applyAttributeEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.AttributeEngine == nil {
		return nil, fmt.Errorf("属性引擎未初始化")
	}

	attrName, ok := effect.Params["attribute"].(string)
	if !ok {
		return nil, fmt.Errorf("attribute参数缺失")
	}

	currentValue, err := context.AttributeEngine.GetAttribute(playerID, attrName)
	if err != nil {
		currentValue = 0
	}

	newValue := currentValue + effect.Value
	err = context.AttributeEngine.SetAttribute(playerID, attrName, newValue)
	if err != nil {
		return nil, fmt.Errorf("设置属性失败: %w", err)
	}

	result.Changes[attrName] = newValue
	result.Message = fmt.Sprintf("%s 增加了 %.0f", attrName, effect.Value)

	return result, nil
}

// applyExpEffect 经验加成
func applyExpEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.PlayerComponent == nil {
		return nil, fmt.Errorf("玩家组件未初始化")
	}

	player, err := context.PlayerComponent.GetByID(playerID)
	if err != nil {
		return nil, fmt.Errorf("获取玩家数据失败: %w", err)
	}

	expGain := int64(effect.Value)
	newExp := player.GetExp() + expGain

	updates := map[string]interface{}{
		"exp": newExp,
	}

	err = context.PlayerComponent.UpdatePlayer(playerID, updates)
	if err != nil {
		return nil, fmt.Errorf("更新经验失败: %w", err)
	}

	result.Changes["exp"] = newExp
	result.Changes["exp_gain"] = expGain
	result.Message = fmt.Sprintf("获得了 %d 点经验", expGain)

	return result, nil
}

// applyGoldEffect 金币奖励
func applyGoldEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.PlayerComponent == nil {
		return nil, fmt.Errorf("玩家组件未初始化")
	}

	player, err := context.PlayerComponent.GetByID(playerID)
	if err != nil {
		return nil, fmt.Errorf("获取玩家数据失败: %w", err)
	}

	goldGain := int64(effect.Value)
	newGold := player.GetGold() + goldGain

	updates := map[string]interface{}{
		"gold": newGold,
	}

	err = context.PlayerComponent.UpdatePlayer(playerID, updates)
	if err != nil {
		return nil, fmt.Errorf("更新金币失败: %w", err)
	}

	result.Changes["gold"] = newGold
	result.Changes["gold_gain"] = goldGain
	result.Message = fmt.Sprintf("获得了 %d 金币", goldGain)

	return result, nil
}

// applyDiamondEffect 钻石奖励
func applyDiamondEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.PlayerComponent == nil {
		return nil, fmt.Errorf("玩家组件未初始化")
	}

	player, err := context.PlayerComponent.GetByID(playerID)
	if err != nil {
		return nil, fmt.Errorf("获取玩家数据失败: %w", err)
	}

	diamondGain := int64(effect.Value)
	newDiamond := player.GetDiamond() + diamondGain

	updates := map[string]interface{}{
		"diamond": newDiamond,
	}

	err = context.PlayerComponent.UpdatePlayer(playerID, updates)
	if err != nil {
		return nil, fmt.Errorf("更新钻石失败: %w", err)
	}

	result.Changes["diamond"] = newDiamond
	result.Changes["diamond_gain"] = diamondGain
	result.Message = fmt.Sprintf("获得了 %d 钻石", diamondGain)

	return result, nil
}

// applyUnlockItemEffect 解锁物品
func applyUnlockItemEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.InventoryComponent == nil {
		return nil, fmt.Errorf("背包组件未初始化")
	}

	itemID, ok := effect.Params["item_id"].(string)
	if !ok {
		return nil, fmt.Errorf("item_id参数缺失")
	}

	quantity := int32(1)
	if q, ok := effect.Params["quantity"].(float64); ok {
		quantity = int32(q)
	}

	_, err := context.InventoryComponent.AddItem(playerID, itemID, quantity)
	if err != nil {
		return nil, fmt.Errorf("添加物品失败: %w", err)
	}

	result.RewardItems = append(result.RewardItems, RewardItem{
		ItemID:   itemID,
		Quantity: quantity,
	})
	result.Message = fmt.Sprintf("获得了物品: %s x%d", itemID, quantity)

	return result, nil
}

// applyRandomRewardEffect 随机奖励
func applyRandomRewardEffect(effect ItemEffect, playerID string, context *EffectContext) (*EffectResult, error) {
	result := &EffectResult{
		Success: true,
		Changes: make(map[string]interface{}),
	}

	if context.InventoryComponent == nil {
		return nil, fmt.Errorf("背包组件未初始化")
	}

	// 从params中获取奖励池
	rewardPool, ok := effect.Params["reward_pool"].([]interface{})
	if !ok || len(rewardPool) == 0 {
		return nil, fmt.Errorf("reward_pool参数缺失或为空")
	}

	// 简单随机选择一个奖励
	selectedReward := rewardPool[0].(map[string]interface{})
	itemID := selectedReward["item_id"].(string)
	quantity := int32(selectedReward["quantity"].(float64))

	_, err := context.InventoryComponent.AddItem(playerID, itemID, quantity)
	if err != nil {
		return nil, fmt.Errorf("添加随机奖励失败: %w", err)
	}

	result.RewardItems = append(result.RewardItems, RewardItem{
		ItemID:   itemID,
		Quantity: quantity,
	})
	result.Message = fmt.Sprintf("随机获得了物品: %s x%d", itemID, quantity)

	return result, nil
}
