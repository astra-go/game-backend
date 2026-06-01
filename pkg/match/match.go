package match

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/astra-go/astra/log"
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

// MatchComponent 匹配组件
type MatchComponent struct {
	redis        *redis.Client
	nats          NATSClient
	logger        *log.Logger
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
	MinMatchQuality float64       // 最低匹配质量（0.0-1.0）
}

// DefaultMatchConfig 默认配置
func DefaultMatchConfig() MatchConfig {
	return MatchConfig{
		MMRDeltaInitial: 100,
		MMRDeltaMax:     800,
		MMRDeltaGrowth:  100,
		MatchTimeout:     30 * time.Second,
		QueueTTL:        10 * time.Minute,
		MinMatchQuality: 0.5,
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
func NewMatchComponent(redis *redis.Client, nats NATSClient, logger *log.Logger, cfg MatchConfig) *MatchComponent {
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

	go m.processMatchQueue()
	go m.processTimeout()

	return nil
}

// PlayerMatchInfo 玩家匹配信息
type PlayerMatchInfo struct {
	PlayerID  string    `json:"player_id"`
	MMR       int32     `json:"mmr"`
	EnqueueAt int64     `json:"enqueue_at"`
	Mode      string    `json:"mode"`
	IsRanked  bool      `json:"is_ranked"` // 是否排位模式
}

// RankTier 排位等级
type RankTier string

const (
	RankBronze   RankTier = "bronze"
	RankSilver   RankTier = "silver"
	RankGold     RankTier = "gold"
	RankPlatinum RankTier = "platinum"
	RankDiamond RankTier = "diamond"
	RankMaster   RankTier = "master"
	RankGrandmaster RankTier = "grandmaster"
)

// RankInfo 排位信息
type RankInfo struct {
	Tier        RankTier `json:"tier"`
	Division    int      `json:"division"` // 段位小级（1-4）
	LP          int      `json:"lp"` // 胜点（League Points）
	Wins        int      `json:"wins"`
	Losses      int      `json:"losses"`
	MMR         int32    `json:"mmr"`
	SeasonID    string   `json:"season_id"`
}

// ========== ELO/MMR 系统 ==========

// calculateELOUpdate 计算 ELO 变化
// K: K因子（新手K=40，高手K=10）
// expectedScore: 期望胜率（0-1）
// actualScore: 实际得分（1胜，0负，0.5平）
func calculateELOUpdate(playerMMR, opponentMMR int32, actualScore float64) int32 {
	// 期望胜率公式: E = 1 / (1 + 10^((opponentMMR - playerMMR) / 400))
	expectedScore := 1.0 / (1.0 + math.Pow(10, float64(opponentMMR-playerMMR)/400.0))
	
	// 动态K因子
	var K float64
	switch {
	case playerMMR < 1000:
		K = 40.0 // 新手
	case playerMMR < 2000:
		K = 20.0 // 中等
	default:
		K = 10.0 // 高手
	}
	
	// ELO变化 = K * (实际得分 - 期望胜率)
	change := int32(K * (actualScore - expectedScore))
	
	// 防止变化过大
	if change > 50 {
		change = 50
	} else if change < -50 {
		change = -50
	}
	
	return change
}

// calculateMatchMMRChange 计算比赛后的MMR变化
func (m *MatchComponent) calculateMatchMMRChange(players []string, winners []string) map[string]int32 {
	changes := make(map[string]int32)
	
	// 计算平均MMR
	var totalMMR int32
	for _, playerID := range players {
		info, err := m.GetPlayerRankInfo(playerID)
		if err != nil {
			continue
		}
		totalMMR += info.MMR
	}
	averageMMR := totalMMR / int32(len(players))
	
	// 计算胜负
	winnerSet := make(map[string]bool)
	for _, w := range winners {
		winnerSet[w] = true
	}
	
	// 计算MMR变化
	for _, playerID := range players {
		info, err := m.GetPlayerRankInfo(playerID)
		if err != nil {
			changes[playerID] = 0
			continue
		}
		
		// 确定实际得分
		var actualScore float64
		if winnerSet[playerID] {
			actualScore = 1.0 // 胜利
		} else {
			actualScore = 0.0 // 失败
		}
		
		// 计算MMR变化（以平均MMR作为对手）
		change := calculateELOUpdate(info.MMR, averageMMR, actualScore)
		changes[playerID] = change
	}
	
	return changes
}

// UpdatePlayerMMRAfterMatch 比赛后更新玩家MMR
func (m *MatchComponent) UpdatePlayerMMRAfterMatch(playerID string, mmrChange int32, won bool) error {
	// 获取当前排位信息
	info, err := m.GetPlayerRankInfo(playerID)
	if err != nil {
		return err
	}
	
	// 更新MMR
	info.MMR += mmrChange
	if info.MMR < 0 {
		info.MMR = 0
	}
	
	// 更新胜负场
	if won {
		info.Wins++
	} else {
		info.Losses++
	}
	
	// 更新LP（胜点）
	info.LP += int(mmrChange)
	if info.LP < 0 {
		info.LP = 0
	}
	
	// 检查晋级/降级
	m.checkRankPromotion(info)
	
	// 保存到Redis
	return m.SavePlayerRankInfo(playerID, info)
}

// checkRankPromotion 检查排位晋级/降级
func (m *MatchComponent) checkRankPromotion(info *RankInfo) {
	// 简化的晋级逻辑：LP达到100则晋级到下一小级
	if info.LP >= 100 {
		info.LP -= 100
		info.Division++
		
		// 如果Division超过4，晋升到下一大段
		if info.Division > 4 {
			info.Division = 1
			info.Tier = m.getNextTier(info.Tier)
		}
	}
	
	// LP为0且连败可能降级（简化：不实现）
}

// getNextTier 获取下一大段
func (m *MatchComponent) getNextTier(current RankTier) RankTier {
	switch current {
	case RankBronze:
		return RankSilver
	case RankSilver:
		return RankGold
	case RankGold:
		return RankPlatinum
	case RankPlatinum:
		return RankDiamond
	case RankDiamond:
		return RankMaster
	case RankMaster:
		return RankGrandmaster
	default:
		return RankGrandmaster
	}
}

// GetPlayerRankInfo 获取玩家排位信息
func (m *MatchComponent) GetPlayerRankInfo(playerID string) (*RankInfo, error) {
	key := fmt.Sprintf("rank:%s", playerID)
	
	data, err := m.redis.Get(ctx, key).Result()
	if err != nil {
		// 如果没有排位信息，创建新的
		return &RankInfo{
			Tier:     RankBronze,
			Division: 1,
			LP:       0,
			Wins:     0,
			Losses:   0,
			MMR:      1000, // 初始MMR
		}, nil
	}
	
	var info RankInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		return nil, err
	}
	
	return &info, nil
}

// SavePlayerRankInfo 保存玩家排位信息
func (m *MatchComponent) SavePlayerRankInfo(playerID string, info *RankInfo) error {
	key := fmt.Sprintf("rank:%s", playerID)
	
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	
	return m.redis.Set(ctx, key, string(data), 0).Err()
}

// ========== 排位匹配 ==========

// EnqueueRanked 加入排位匹配队列
func (m *MatchComponent) EnqueueRanked(playerID string, mmr int32) error {
	return m.Enqueue(playerID, common.GameMode1v1, mmr)
}

// ========== 快速匹配 ==========

// EnqueueQuick 加入快速匹配队列（MMR范围更宽松）
func (m *MatchComponent) EnqueueQuick(playerID string, mmr int32) error {
	// 快速匹配使用更宽松的MMR范围
	// 通过修改config实现
	oldDelta := m.config.MMRDeltaInitial
	m.config.MMRDeltaInitial = 200 // 更大的初始范围
	
	err := m.Enqueue(playerID, common.GameModeCasual, mmr)
	
	// 恢复配置
	m.config.MMRDeltaInitial = oldDelta
	
	return err
}

// ========== 自定义房间匹配 ==========

// CustomRoomMatch 自定义房间匹配（不直接使用MMR）
type CustomRoomMatch struct {
	RoomID   string   `json:"room_id"`
	Password string   `json:"password"`
	Mode     string   `json:"mode"`
	MaxPlayers int    `json:"max_players"`
	CurrentPlayers []string `json:"current_players"`
}

// JoinCustomRoom 加入自定义房间
func (m *MatchComponent) JoinCustomRoom(playerID, roomID, password string) error {
	// 这里应该调用RoomComponent的API
	// 简化实现：只记录匹配信息
	
	// 验证密码（如果有）
	// ...
	
	// 加入房间逻辑
	m.logger.Info("玩家加入自定义房间",
		"player_id", playerID,
		"room_id", roomID,
	)
	
	return nil
}

// ========== 匹配取消机制（已有Dequeue） ==========

// CancelMatch 取消匹配（别名，更语义化）
func (m *MatchComponent) CancelMatch(playerID string) error {
	return m.Dequeue(playerID)
}

// ========== 匹配质量评估 ==========

// evaluateMatchQuality 评估匹配质量（0-1，1最高）
func (m *MatchComponent) evaluateMatchQuality(players []string) float64 {
	if len(players) == 0 {
		return 0.0
	}
	
	// 获取所有玩家的MMR
	var totalMMR float64
	var mmrs []float64
	for _, playerID := range players {
		info, err := m.GetPlayerRankInfo(playerID)
		if err != nil {
			continue
		}
		mmrs = append(mmrs, float64(info.MMR))
		totalMMR += float64(info.MMR)
	}
	
	if len(mmrs) == 0 {
		return 0.0
	}
	
	// 计算标准差
	average := totalMMR / float64(len(mmrs))
	var variance float64
	for _, mmr := range mmrs {
		diff := mmr - average
		variance += diff * diff
	}
	variance /= float64(len(mmrs))
	stdDev := math.Sqrt(variance)
	
	// 质量 = 1 / (1 + 标准差/100)
	quality := 1.0 / (1.0 + stdDev/100.0)
	
	return quality
}

// Enqueue 玩家加入匹配队列
func (m *MatchComponent) Enqueue(playerID string, mode common.GameMode, mmr int32) error {
	queueKey := fmt.Sprintf("match:queue:%s", mode)
	processingKey := "match:processing"

	exists, err := m.redis.HExists(ctx, processingKey, playerID).Result()
	if err != nil {
		return fmt.Errorf("检查匹配状态失败: %w", err)
	}
	if exists {
		return fmt.Errorf("玩家已在匹配队列中")
	}

	now := time.Now().Unix()

	matchInfo := PlayerMatchInfo{
		PlayerID:  playerID,
		MMR:       mmr,
		EnqueueAt: now,
		Mode:      string(mode),
	}

	infoData, err := json.Marshal(matchInfo)
	if err != nil {
		return fmt.Errorf("序列化匹配信息失败: %w", err)
	}

	err = m.redis.ZAdd(ctx, queueKey, redis.Z{
		Score:  float64(mmr),
		Member: playerID,
	}).Err()
	if err != nil {
		return fmt.Errorf("加入匹配队列失败: %w", err)
	}

	err = m.redis.HSet(ctx, processingKey, playerID, infoData).Err()
	if err != nil {
		m.redis.ZRem(ctx, queueKey, playerID)
		return fmt.Errorf("记录匹配状态失败: %w", err)
	}

	m.redis.Expire(ctx, queueKey, m.config.QueueTTL)
	m.redis.Expire(ctx, processingKey, m.config.QueueTTL)

	m.updateQueueSize(mode)

	m.logger.Info("玩家加入匹配队列",
		"player_id", playerID,
		"mode", string(mode),
		"mmr", mmr,
		"enqueue_at", now,
	)

	return nil
}

// Dequeue 玩家退出匹配队列
func (m *MatchComponent) Dequeue(playerID string) error {
	processingKey := "match:processing"

	infoData, err := m.redis.HGet(ctx, processingKey, playerID).Result()
	if err != nil {
		return fmt.Errorf("玩家不在匹配队列中")
	}

	var matchInfo PlayerMatchInfo
	if err := json.Unmarshal([]byte(infoData), &matchInfo); err != nil {
		return fmt.Errorf("解析匹配信息失败: %w", err)
	}

	queueKey := fmt.Sprintf("match:queue:%s", matchInfo.Mode)

	m.redis.ZRem(ctx, queueKey, playerID)
	m.redis.HDel(ctx, processingKey, playerID)

	m.updateQueueSize(common.GameMode(matchInfo.Mode))

	waitTime := time.Now().Unix() - matchInfo.EnqueueAt

	m.logger.Info("玩家退出匹配队列",
		"player_id", playerID,
		"mode", matchInfo.Mode,
		"wait_time", waitTime,
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

		players, err := m.redis.ZRangeByScoreWithScores(ctx, queueKey, &redis.ZRangeBy{
			Min: fmt.Sprintf("%f", lower),
			Max: fmt.Sprintf("%f", upper),
		}).Result()

		if err != nil {
			return nil, fmt.Errorf("匹配查询失败: %w", err)
		}

		requiredPlayers := m.getRequiredPlayers(mode)
		if len(players) >= requiredPlayers {
			selected := m.selectClosestPlayers(players, mmr, requiredPlayers)

			removed, err := m.removePlayersAtomically(queueKey, selected)
			if err != nil {
				m.logger.Error("原子移除玩家失败", "error", err)
				continue
			}

			if len(removed) >= requiredPlayers {
				m.redis.HDel(ctx, "match:processing", removed...)

				quality := m.calculateMatchQuality(players[:requiredPlayers])

				m.logger.Info("匹配成功",
					"mode", string(mode),
					"players", removed,
					"mmr_delta", delta,
					"quality", quality,
				)

				return removed, nil
			}
		}

		delta += m.config.MMRDeltaGrowth
	}

	return nil, nil
}

// calculateMatchQuality 计算匹配质量
func (m *MatchComponent) calculateMatchQuality(players []redis.Z) float64 {
	if len(players) < 2 {
		return 1.0
	}

	var totalMMR float64
	for _, p := range players {
		totalMMR += p.Score
	}
	avgMMR := totalMMR / float64(len(players))

	var variance float64
	for _, p := range players {
		diff := p.Score - avgMMR
		variance += diff * diff
	}
	variance /= float64(len(players))

	stdDev := math.Sqrt(variance)

	quality := 1.0 / (1.0 + stdDev/100.0)

	return quality
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
func (m *MatchComponent) selectClosestPlayers(players []redis.Z, targetMMR int32, n int) []string {
	type playerMMR struct {
		playerID string
		mmr      float64
		diff     float64
	}

	playerMMRs := make([]playerMMR, 0, len(players))
	for _, z := range players {
		playerID := z.Member.(string)
		mmr := z.Score
		diff := math.Abs(mmr - float64(targetMMR))

		playerMMRs = append(playerMMRs, playerMMR{
			playerID: playerID,
			mmr:      mmr,
			diff:     diff,
		})
	}

	sort.Slice(playerMMRs, func(i, j int) bool {
		return playerMMRs[i].diff < playerMMRs[j].diff
	})

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
		return 2
	case common.GameModeCustom:
		return 1
	default:
		return 2
	}
}

// processMatchQueue 处理匹配队列（协程）
func (m *MatchComponent) processMatchQueue() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		modes := []common.GameMode{
			common.GameMode1v1,
			common.GameMode5v5,
			common.GameModeCasual,
		}

		for _, mode := range modes {
			queueKey := fmt.Sprintf("match:queue:%s", mode)
			players, err := m.redis.ZRangeWithScores(ctx, queueKey, 0, -1).Result()
			if err != nil {
				m.logger.Error("获取匹配队列失败", "error", err)
				continue
			}

			if len(players) < m.getRequiredPlayers(mode) {
				continue
			}

			for _, z := range players {
				playerID := z.Member.(string)
				mmr := int32(z.Score)

				matched, err := m.MatchRange(mode, playerID, mmr)
				if err != nil {
					m.logger.Error("匹配失败", "error", err, "player_id", playerID)
					continue
				}

				if len(matched) > 0 {
					m.onCreateRoom(mode, matched)
					break
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
		processingKey := "match:processing"
		players, err := m.redis.HGetAll(ctx, processingKey).Result()
		if err != nil {
			m.logger.Error("获取匹配中玩家失败", "error", err)
			continue
		}

		now := time.Now().Unix()
		timeoutThreshold := int64(m.config.MatchTimeout.Seconds())

		for playerID, infoData := range players {
			var matchInfo PlayerMatchInfo
			if err := json.Unmarshal([]byte(infoData), &matchInfo); err != nil {
				m.logger.Error("解析匹配信息失败", "error", err)
				continue
			}

			waitTime := now - matchInfo.EnqueueAt

			if waitTime > timeoutThreshold {
				m.logger.Warn("匹配超时，移除玩家",
					"player_id", playerID,
					"mode", matchInfo.Mode,
					"wait_time", waitTime,
				)

				queueKey := fmt.Sprintf("match:queue:%s", matchInfo.Mode)
				m.redis.ZRem(ctx, queueKey, playerID)
				m.redis.HDel(ctx, processingKey, playerID)

				m.notifyMatchTimeout(playerID, matchInfo.Mode, waitTime)
			}
		}
	}
}

// notifyMatchTimeout 通知客户端匹配超时
func (m *MatchComponent) notifyMatchTimeout(playerID, mode string, waitTime int64) {
	timeoutMsg := map[string]interface{}{
		"player_id": playerID,
		"mode":      mode,
		"wait_time": waitTime,
		"reason":    "timeout",
	}

	msgData, _ := json.Marshal(timeoutMsg)
	m.nats.Publish(fmt.Sprintf("match.%s.timeout", playerID), msgData)
}

// onCreateRoom 创建房间（匹配成功后）
func (m *MatchComponent) onCreateRoom(mode common.GameMode, players []string) {
	roomID := fmt.Sprintf("room_%d", time.Now().UnixNano())

	processingKey := "match:processing"
	var totalMMR int32
	var enqueueTime int64 = math.MaxInt64

	for _, playerID := range players {
		infoData, err := m.redis.HGet(ctx, processingKey, playerID).Result()
		if err != nil {
			continue
		}

		var matchInfo PlayerMatchInfo
		if err := json.Unmarshal([]byte(infoData), &matchInfo); err != nil {
			continue
		}

		totalMMR += matchInfo.MMR
		if matchInfo.EnqueueAt < enqueueTime {
			enqueueTime = matchInfo.EnqueueAt
		}
	}

	avgMMR := totalMMR / int32(len(players))
	waitTime := time.Now().Unix() - enqueueTime

	room := &common.Room{
		ID:         roomID,
		Name:       fmt.Sprintf("Room-%s", roomID[:8]),
		Status:     common.RoomStatusWaiting,
		MaxPlayers: int32(len(players)),
		Mode:       mode,
		CreatedAt:  time.Now(),
	}

	roomKey := fmt.Sprintf("room:%s", roomID)
	roomData, _ := json.Marshal(room)
	m.redis.HSet(ctx, roomKey, "info", roomData)
	m.redis.Expire(ctx, roomKey, 1*time.Hour)

	for i, playerID := range players {
		member := common.RoomMember{
			RoomID:   roomID,
			PlayerID: playerID,
			TeamID:   int32(i / (len(players) / 2)),
			Role:     "member",
		}
		memberData, _ := json.Marshal(member)
		m.redis.HSet(ctx, fmt.Sprintf("%s:members", roomKey), playerID, memberData)
	}

	matchResult := common.MatchResult{
		RoomID:   roomID,
		Players:  players,
		WaitTime: waitTime,
		AvgMMR:   avgMMR,
	}

	resultData, _ := json.Marshal(matchResult)
	m.nats.Publish(fmt.Sprintf("room.%s.created", roomID), resultData)

	for _, playerID := range players {
		m.nats.Publish(fmt.Sprintf("match.%s.success", playerID), resultData)
	}

	m.logger.Info("房间创建成功",
		"room_id", roomID,
		"players", players,
		"avg_mmr", avgMMR,
		"wait_time", waitTime,
	)
}

// updateQueueSize 更新队列大小指标
func (m *MatchComponent) updateQueueSize(mode common.GameMode) {
	queueKey := fmt.Sprintf("match:queue:%s", mode)
	_, err := m.redis.ZCard(ctx, queueKey).Result()
	if err != nil {
		m.logger.Error("获取队列大小失败", "error", err)
		return
	}
}

// GetQueueStatus 获取队列状态
func (m *MatchComponent) GetQueueStatus(mode common.GameMode) (int64, error) {
	queueKey := fmt.Sprintf("match:queue:%s", mode)
	return m.redis.ZCard(ctx, queueKey).Result()
}

// GetPlayerMatchInfo 获取玩家匹配信息
func (m *MatchComponent) GetPlayerMatchInfo(playerID string) (*PlayerMatchInfo, error) {
	processingKey := "match:processing"

	infoData, err := m.redis.HGet(ctx, processingKey, playerID).Result()
	if err != nil {
		return nil, fmt.Errorf("玩家不在匹配队列中")
	}

	var matchInfo PlayerMatchInfo
	if err := json.Unmarshal([]byte(infoData), &matchInfo); err != nil {
		return nil, fmt.Errorf("解析匹配信息失败: %w", err)
	}

	return &matchInfo, nil
}
