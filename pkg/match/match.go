package match

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"github.com/astra-go/game-backend/pkg/common"
)

var ctx = context.Background()

// ========== Prometheus指标 ==========

// MatchComponent 匹配组件
type MatchComponent struct {
	redis        *redis.Client
	nats          NATSClient
	logger        *zap.Logger
	config        MatchConfig
	matchTimeout  time.Duration
	queueSize     int
}

// MatchConfig 匹配配置
type MatchConfig struct {
	MMRDeltaInitial int32         // 初始MMR搜索范围
	MMRDeltaMax     int32         // 最大MMR搜索范围
	MMRDeltaGrowth  int32         // 每次扩大的增量
	MatchTimeout    time.Duration // 匹配超时
	QueueTTL        time.Duration // 队列过期时间
}

// DefaultMatchConfig 默认配置
func DefaultMatchConfig() MatchConfig {
	return MatchConfig{
		MMRDeltaInitial: 100,
		MMRDeltaMax:     800,
		MMRDeltaGrowth:  100,
		MatchTimeout:     30 * time.Second,
		QueueTTL:        10 * time.Minute,
	}
}

// NATSClient NATS接口
type NATSClient interface {
	Publish(subject string, data []byte) error
	Subscribe(subject string, cb func(msg []byte)) error
	Request(subject string, data []byte, timeout time.Duration) ([]byte, error)
	Close() error
}

// NewMatchComponent 创建匹配组件
func NewMatchComponent(redis *redis.Client, nats NATSClient, logger *zap.Logger, cfg MatchConfig) *MatchComponent {
	return &MatchComponent{
		redis:       redis,
		nats:         nats,
		logger:       logger,
		config:       cfg,
		matchTimeout: cfg.MatchTimeout,
		queueSize:    0,
	}
}

// Init 初始化
func (m *MatchComponent) Init() error {
	m.logger.Info("MatchComponent 初始化")
	
	// 启动匹配处理协程
	go m.processMatchQueue()
	
	// 启动超时处理协程
	go m.processTimeout()
	
	return nil
}

// Enqueue 玩家加入匹配队列
func (m *MatchComponent) Enqueue(playerID string, mode common.GameMode, mmr int32) error {
	queueKey := fmt.Sprintf("match:queue:%s", mode)
	processingKey := "match:processing"
	
	// 检查是否已在匹配中
	exists, err := m.redis.HExists(ctx, processingKey, playerID).Result()
	if err != nil {
		return fmt.Errorf("检查匹配状态失败: %w", err)
	}
	if exists {
		return fmt.Errorf("玩家已在匹配队列中")
	}
	
	// 使用ZAdd，score=MMR，member=playerID
	err = m.redis.ZAdd(ctx, queueKey, redis.Z{
		Score:  float64(mmr),
		Member: playerID,
	}).Err()
	if err != nil {
		return fmt.Errorf("加入匹配队列失败: %w", err)
	}
	
	// 记录到processing集合（防止重复入队）
	err = m.redis.HSet(ctx, processingKey, playerID, string(mode)).Err()
	if err != nil {
		// 回滚
		m.redis.ZRem(ctx, queueKey, playerID)
		return fmt.Errorf("记录匹配状态失败: %w", err)
	}
	
	// 设置过期时间
	m.redis.Expire(ctx, queueKey, m.config.QueueTTL)
	m.redis.Expire(ctx, processingKey, m.config.QueueTTL)
	
	// 更新队列大小指标
	m.updateQueueSize(mode)
	
	m.logger.Info("玩家加入匹配队列",
		zap.String("player_id", playerID),
		zap.String("mode", string(mode)),
		zap.Int32("mmr", mmr),
	)
	
	return nil
}

// Dequeue 玩家退出匹配队列
func (m *MatchComponent) Dequeue(playerID string) error {
	processingKey := "match:processing"
	
	// 获取玩家所在的队列
	modeStr, err := m.redis.HGet(ctx, processingKey, playerID).Result()
	if err != nil {
		return fmt.Errorf("玩家不在匹配队列中")
	}
	
	queueKey := fmt.Sprintf("match:queue:%s", modeStr)
	
	// 从队列中移除
	m.redis.ZRem(ctx, queueKey, playerID)
	m.redis.HDel(ctx, processingKey, playerID)
	
	// 更新队列大小指标
	m.updateQueueSize(common.GameMode(modeStr))
	
	m.logger.Info("玩家退出匹配队列",
		zap.String("player_id", playerID),
		zap.String("mode", modeStr),
	)
	
	return nil
}

// MatchRange 核心匹配逻辑：范围匹配
func (m *MatchComponent) MatchRange(mode common.GameMode, playerID string, mmr int32) ([]string, error) {
	queueKey := fmt.Sprintf("match:queue:%s", mode)
	
	delta := m.config.MMRDeltaInitial
	
	for delta <= m.config.MMRDeltaMax {
		lower := float64(mmr - delta)
		upper := float64(mmr + delta)
		
		// ZRANGEBYSCORE 获取范围内所有玩家
		players, err := m.redis.ZRangeByScore(ctx, queueKey, &redis.ZRangeBy{
			Min: fmt.Sprintf("%d", int64(lower)),
			Max: fmt.Sprintf("%d", int64(upper)),
		}).Result()
		
		if err != nil {
			return nil, fmt.Errorf("匹配查询失败: %w", err)
		}
		
		// 找到足够玩家创建房间
		requiredPlayers := m.getRequiredPlayers(mode)
		if len(players) >= requiredPlayers {
			// 选择MMR最接近的N个玩家
			selected := m.selectClosestPlayers(players, mmr, requiredPlayers)
			
			// 原子性移除（使用Lua脚本）
			removed, err := m.removePlayersAtomically(queueKey, selected)
			if err != nil {
				m.logger.Error("原子移除玩家失败", zap.Error(err))
				continue
			}
			
			if len(removed) >= requiredPlayers {
				// 从processing中移除
				m.redis.HDel(ctx, "match:processing", removed...)
				
				m.logger.Info("匹配成功",
					zap.String("mode", string(mode)),
					zap.Strings("players", removed),
				)
				
				return removed, nil
			}
		}
		
		// 扩大搜索范围
		delta += m.config.MMRDeltaGrowth
	}
	
	return nil, nil // 超时无匹配
}

// removePlayersAtomically 原子性移除玩家（Lua脚本）
func (m *MatchComponent) removePlayersAtomically(queueKey string, players []string) ([]string, error) {
	luaScript := `
local queue_key = KEYS[1]
local players = ARGV
local removed = {}

for i, player_id in ipairs(players) do
    local res = redis.call('ZREM', queue_key, player_id)
    if res == 1 then
        table.insert(removed, player_id)
    end
end

return removed
`
	
	result, err := m.redis.Eval(ctx, luaScript, []string{queueKey}, players).Result()
	if err != nil {
		return nil, err
	}
	
	removed, ok := result.([]interface{})
	if !ok {
		return nil, fmt.Errorf("Lua脚本返回格式错误")
	}
	
	removedStrs := make([]string, 0, len(removed))
	for _, r := range removed {
		if s, ok := r.(string); ok {
			removedStrs = append(removedStrs, s)
		}
	}
	
	return removedStrs, nil
}

// selectClosestPlayers 选择MMR最接近的N个玩家
func (m *MatchComponent) selectClosestPlayers(players []string, targetMMR int32, n int) []string {
	// 获取所有玩家的MMR
	type playerMMR struct {
		playerID string
		mmr      int32
		diff      int32
	}
	
	playerMMRs := make([]playerMMR, 0, len(players))
	for _, pid := range players {
		// 从Redis获取玩家MMR（这里是伪代码，实际需要查询）
		// 简化：假设players已经是按MMR排序的
		playerMMRs = append(playerMMRs, playerMMR{
			playerID: pid,
			mmr:      targetMMR, // 简化
			diff:      int32(math.Abs(float64(targetMMR - targetMMR))),
		})
	}
	
	// 按MMR差异排序
	sort.Slice(playerMMRs, func(i, j int) bool {
		return playerMMRs[i].diff < playerMMRs[j].diff
	})
	
	// 选择前N个
	selected := make([]string, 0, n)
	for i := 0; i < n && i < len(playerMMRs); i++ {
		selected = append(selected, playerMMRs[i].playerID)
	}
	
	return selected
}

// getRequiredPlayers 获取模式所需玩家数
func (m *MatchComponent) getRequiredPlayers(mode common.GameMode) int {
	switch mode {
	case common.GameMode1v1:
		return 2
	case common.GameMode5v5:
		return 10
	case common.GameModeCasual:
		return 2 // 最少2人
	case common.GameModeCustom:
		return 1 // 自定义房间，1人也可以
	default:
		return 2
	}
}

// processMatchQueue 处理匹配队列（协程）
func (m *MatchComponent) processMatchQueue() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		// 遍历所有匹配模式
		modes := []common.GameMode{
			common.GameMode1v1,
			common.GameMode5v5,
			common.GameModeCasual,
		}
		
		for _, mode := range modes {
			// 获取队列中的所有玩家（带MMR分数）
			queueKey := fmt.Sprintf("match:queue:%s", mode)
			players, err := m.redis.ZRangeWithScores(ctx, queueKey, 0, -1).Result()
			if err != nil {
				m.logger.Error("获取匹配队列失败", zap.Error(err))
				continue
			}
			
			// 尝试对队列中每个玩家进行匹配
			for _, z := range players {
				playerID := z.Member.(string)
				mmr := int32(z.Score)
				
				matched, err := m.MatchRange(mode, playerID, mmr)
				if err != nil {
					m.logger.Error("匹配失败", zap.Error(err), zap.String("player_id", playerID))
					continue
				}
				
				if len(matched) > 0 {
					// 匹配成功，创建房间
					m.onCreateRoom(mode, matched)
				}
			}
		}
	}
}

// processTimeout 处理匹配超时
func (m *MatchComponent) processTimeout() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		// 遍历所有匹配中的玩家
		processingKey := "match:processing"
		players, err := m.redis.HGetAll(ctx, processingKey).Result()
		if err != nil {
			m.logger.Error("获取匹配中玩家失败", zap.Error(err))
			continue
		}
		
		now := time.Now().Unix()
		for playerID, modeStr := range players {
			// 检查超时（简化：实际应记录入队时间）
			// 这里应该检查玩家的入队时间
			_ = playerID
			_ = modeStr
			_ = now
			
			// 超时处理：移除队列并通知客户端
			// m.Dequeue(playerID)
			// 通知客户端匹配超时
		}
	}
}

// onCreateRoom 创建房间（匹配成功后）
func (m *MatchComponent) onCreateRoom(mode common.GameMode, players []string) {
	// 生成房间ID
	roomID := fmt.Sprintf("room_%d", time.Now().UnixNano())
	
	// 计算平均MMR
	// avgMMR := m.calculateAvgMMR(players)
	
	// 创建房间
	room := &common.Room{
		ID:         roomID,
		Name:       fmt.Sprintf("Room-%s", roomID[:8]),
		Status:     common.RoomStatusWaiting,
		MaxPlayers: int32(len(players)),
		Mode:       mode,
		CreatedAt:  time.Now(),
	}
	
	// 保存到Redis
	roomKey := fmt.Sprintf("room:%s", roomID)
	roomData, _ := json.Marshal(room)
	m.redis.HSet(ctx, roomKey, "info", roomData)
	m.redis.Expire(ctx, roomKey, 1*time.Hour)
	
	// 添加玩家到房间
	for i, playerID := range players {
		member := common.RoomMember{
			RoomID:   roomID,
			PlayerID: playerID,
			TeamID:   int32(i / (len(players) / 2)), // 简化分队
			Role:     "member",
		}
		memberData, _ := json.Marshal(member)
		m.redis.HSet(ctx, fmt.Sprintf("%s:members", roomKey), playerID, memberData)
	}
	
	// 发布房间创建事件
	matchResult := common.MatchResult{
		RoomID:   roomID,
		Players:  players,
		WaitTime: 0, // 简化
	}
	
	resultData, _ := json.Marshal(matchResult)
	m.nats.Publish(fmt.Sprintf("room.%s.created", roomID), resultData)
	
	m.logger.Info("房间创建成功",
		zap.String("room_id", roomID),
		zap.Strings("players", players),
	)
}

// updateQueueSize 更新队列大小指标
func (m *MatchComponent) updateQueueSize(mode common.GameMode) {
	queueKey := fmt.Sprintf("match:queue:%s", mode)
	_, err := m.redis.ZCard(ctx, queueKey).Result()
	if err != nil {
		m.logger.Error("获取队列大小失败", zap.Error(err))
		return
	}
	
	// 更新Prometheus指标（暂时注释）
	// matchQueueSize.WithLabelValues(string(mode)).Set(float64(size))
}
