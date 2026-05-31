# 背包/道具系统

## 功能概述

完整的游戏背包和道具管理系统，支持：

1. **道具增删改查**
   - 添加物品到背包（自动堆叠）
   - 移除指定数量的物品
   - 查询背包所有物品
   - 查询单个格子信息

2. **道具堆叠逻辑**
   - 根据物品模板的 `MaxStack` 自动堆叠
   - 超出堆叠上限自动分配到新格子
   - 支持可堆叠物品（消耗品、材料）和不可堆叠物品（装备）

3. **道具使用效果**
   - 恢复生命/法力
   - 应用 Buff/Debuff
   - 属性加成
   - 经验/金币/钻石奖励
   - 解锁新物品
   - 随机奖励

## 快速开始

### 1. 初始化组件

```go
import (
    "game-backend/pkg/inventory"
    "github.com/redis/go-redis/v9"
    "go.uber.org/zap"
    "gorm.io/gorm"
)

// 创建背包组件
invComp := inventory.NewInventoryComponent(db, redisClient, logger)

// 初始化数据库表
err := invComp.Init()
if err != nil {
    panic(err)
}

// 设置依赖（可选，用于物品效果）
invComp.SetPlayerComponent(playerComponent)
invComp.SetAttributeEngine(attributeEngine)
```

### 2. 注册物品模板

```go
// 注册消耗品：生命药水
hpPotion := inventory.ItemTemplate{
    ItemID:      "potion_hp_small",
    Name:        "小型生命药水",
    Description: "恢复100点生命值",
    ItemType:    inventory.ItemTypeConsumable,
    MaxStack:    99,
    Rarity:      inventory.RarityCommon,
    PriceGold:   50,
    Effects: json.RawMessage(`[
        {
            "type": "heal",
            "value": 100,
            "duration": 0
        }
    ]`),
}
invComp.RegisterItemTemplate(hpPotion)

// 注册装备：铁剑
ironSword := inventory.ItemTemplate{
    ItemID:      "sword_iron",
    Name:        "铁剑",
    Description: "普通的铁制长剑",
    ItemType:    inventory.ItemTypeEquipment,
    MaxStack:    1,
    Rarity:      inventory.RarityCommon,
    PriceGold:   200,
    BaseAttrs: json.RawMessage(`{
        "attack": 15,
        "durability": 100
    }`),
    Requirements: json.RawMessage(`{
        "min_level": 5
    }`),
}
invComp.RegisterItemTemplate(ironSword)

// 注册礼包：新手礼包
starterPack := inventory.ItemTemplate{
    ItemID:      "pack_starter",
    Name:        "新手礼包",
    Description: "包含金币和经验",
    ItemType:    inventory.ItemTypeGift,
    MaxStack:    10,
    Rarity:      inventory.RarityUncommon,
    Effects: json.RawMessage(`[
        {
            "type": "gold",
            "value": 1000
        },
        {
            "type": "exp",
            "value": 500
        },
        {
            "type": "unlock_item",
            "params": {
                "item_id": "potion_hp_small",
                "quantity": 5
            }
        }
    ]`),
}
invComp.RegisterItemTemplate(starterPack)
```

### 3. 道具增删改查

```go
playerID := "player_123"

// 添加物品（自动堆叠）
slot, err := invComp.AddItem(playerID, "potion_hp_small", 10)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("添加到格子 %d，当前数量 %d\n", slot.SlotIndex, slot.Quantity)

// 再次添加，会自动堆叠
invComp.AddItem(playerID, "potion_hp_small", 5)

// 查询背包
slots, err := invComp.GetInventory(playerID)
if err != nil {
    log.Fatal(err)
}
for _, slot := range slots {
    fmt.Printf("格子 %d: %s x%d\n", slot.SlotIndex, slot.ItemID, slot.Quantity)
}

// 移除物品
err = invComp.RemoveItem(playerID, 1, 3)
if err != nil {
    log.Fatal(err)
}

// 交换格子
err = invComp.SwapSlots(playerID, 1, 2)
```

### 4. 使用物品

```go
// 使用消耗品
result, err := invComp.UseItem(playerID, 1)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("使用结果: %s\n", result.Message)
fmt.Printf("属性变化: %+v\n", result.Changes)
fmt.Printf("获得物品: %+v\n", result.RewardItems)
fmt.Printf("获得Buff: %+v\n", result.BuffsApplied)
```

### 5. 装备系统

```go
// 添加装备到背包
invComp.AddItem(playerID, "sword_iron", 1)

// 装备物品
err = invComp.EquipItem(playerID, 1)
if err != nil {
    log.Fatal(err)
}

// 查询已装备列表
equipped, err := invComp.GetEquipped(playerID)
for _, item := range equipped {
    fmt.Printf("装备部位 %d: %s\n", item.EquipmentSlotID, item.ItemID)
}

// 卸下装备
err = invComp.UnequipItem(playerID, 1)
```

## 数据结构

### ItemTemplate（物品模板）

```go
type ItemTemplate struct {
    ItemID       string          // 物品ID（唯一）
    Name         string          // 物品名称
    Description  string          // 描述
    ItemType     ItemType        // 物品类型
    MaxStack     int32           // 最大堆叠数量
    Rarity       Rarity          // 稀有度
    BaseAttrs    json.RawMessage // 基础属性（JSON）
    Effects      json.RawMessage // 效果列表（JSON）
    Requirements json.RawMessage // 使用要求（JSON）
    PriceGold    int64           // 金币价格
    PriceDiamond int64           // 钻石价格
    Tradable     bool            // 是否可交易
}
```

### InventorySlot（背包格子）

```go
type InventorySlot struct {
    PlayerID        string          // 玩家ID
    SlotIndex       int32           // 格子索引
    ItemID          string          // 物品ID
    ItemType        ItemType        // 物品类型
    Quantity        int32           // 数量
    StackLimit      int32           // 堆叠上限
    EquipmentSlotID int32           // 装备部位ID（0=非装备）
    ExtraAttrs      json.RawMessage // 额外属性
}
```

## 物品效果类型

| 效果类型 | 说明 | 参数 |
|---------|------|------|
| `heal` | 恢复生命值 | `value`: 恢复量 |
| `mana` | 恢复法力值 | `value`: 恢复量 |
| `buff` | 应用增益效果 | `buff_id`: Buff ID, `duration`: 持续时间 |
| `attribute` | 属性加成 | `attribute`: 属性名, `value`: 加成值 |
| `exp` | 经验奖励 | `value`: 经验值 |
| `gold` | 金币奖励 | `value`: 金币数量 |
| `diamond` | 钻石奖励 | `value`: 钻石数量 |
| `unlock_item` | 解锁物品 | `item_id`: 物品ID, `quantity`: 数量 |
| `random_reward` | 随机奖励 | `reward_pool`: 奖励池 |

## 物品类型

- `consumable`: 消耗品（可使用，可堆叠）
- `equipment`: 装备（可装备，不可堆叠）
- `material`: 材料（可堆叠）
- `quest`: 任务物品（不可交易）
- `gift`: 礼包（可使用）

## 稀有度

- `common`: 普通（白色）
- `uncommon`: 优秀（绿色）
- `rare`: 稀有（蓝色）
- `epic`: 史诗（紫色）
- `legendary`: 传说（橙色）

## 缓存策略

- 背包数据缓存 5 分钟（Redis）
- 物品模板缓存 5 分钟（Redis）
- 增删改操作自动失效缓存

## 测试

运行单元测试：

```bash
cd game-backend/pkg/inventory
go test -v
```

## 注意事项

1. **背包容量**：默认 30 格，可通过 `DefaultCapacity` 常量修改
2. **装备部位**：装备部位ID从物品的 `ExtraAttrs.equipment_slot_id` 获取，默认为 1
3. **物品效果**：需要设置 `PlayerComponent` 和 `AttributeEngine` 才能正常工作
4. **并发安全**：数据库操作使用事务保证一致性，Redis缓存可能存在短暂不一致
5. **堆叠逻辑**：先填充已有格子，再创建新格子

## 扩展建议

1. **背包扩容**：添加背包容量升级功能
2. **物品锁定**：防止误删重要物品
3. **物品过期**：限时物品自动删除
4. **物品强化**：装备强化系统
5. **物品分解**：将物品分解为材料
6. **批量操作**：批量使用、批量出售
7. **物品排序**：按类型、稀有度、获得时间排序
8. **快捷栏**：独立的快捷使用栏
