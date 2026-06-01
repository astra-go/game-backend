package match

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/astra-go/astra/log"
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockNATSClient mock NATS客户端
type MockNATSClient struct {
	mock.Mock
}

func (m *MockNATSClient) Publish(subject string, data []byte) error {
	args := m.Called(subject, data)
	return args.Error(0)
}

func (m *MockNATSClient) Subscribe(subject string, cb func(msg []byte)) error {
	args := m.Called(subject, cb)
	return args.Error(0)
}

func (m *MockNATSClient) Request(subject string, data []byte, timeout time.Duration) ([]byte, error) {
	args := m.Called(subject, data, timeout)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockNATSClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// setupTestMatchComponent 创建测试用的MatchComponent，使用miniredis替代真实Redis
func setupTestMatchComponent(t *testing.T) (*MatchComponent, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	
	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	
	natsClient := &MockNATSClient{}
	logger := log.Default()
	
	config := DefaultMatchConfig()
	
	mc := NewMatchComponent(redisClient, natsClient, logger, config)
	
	return mc, mr
}

func TestNewMatchComponent(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	assert.NotNil(t, mc)
	assert.NotNil(t, mc.redis)
	assert.NotNil(t, mc.nats)
	assert.NotNil(t, mc.logger)
	assert.Equal(t, 30*time.Second, mc.matchTimeout)
}

func TestDefaultMatchConfig(t *testing.T) {
	tests := []struct {
		name     string
		expected MatchConfig
	}{
		{
			name: "默认配置",
			expected: MatchConfig{
				MMRDeltaInitial: 100,
				MMRDeltaMax:     800,
				MMRDeltaGrowth:  100,
				MatchTimeout:     30 * time.Second,
				QueueTTL:        10 * time.Minute,
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultMatchConfig()
			assert.Equal(t, tt.expected.MMRDeltaInitial, config.MMRDeltaInitial)
			assert.Equal(t, tt.expected.MMRDeltaMax, config.MMRDeltaMax)
			assert.Equal(t, tt.expected.MMRDeltaGrowth, config.MMRDeltaGrowth)
			assert.Equal(t, tt.expected.MatchTimeout, config.MatchTimeout)
			assert.Equal(t, tt.expected.QueueTTL, config.QueueTTL)
		})
	}
}

func TestEnqueue(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	ctx := context.Background()
	
	tests := []struct {
		name      string
		playerID  string
		mode      common.GameMode
		mmr       int32
		wantError bool
		setup     func()
	}{
		{
			name:      "正常加入队列",
			playerID:  "player1",
			mode:      common.GameMode1v1,
			mmr:       1500,
			wantError: false,
			setup: func() {
				mc.redis.Del(ctx, "match:queue:1v1", "match:processing")
			},
		},
		{
			name:      "重复加入队列",
			playerID:  "player2",
			mode:      common.GameMode1v1,
			mmr:       1600,
			wantError: true,
			setup: func() {
				mc.redis.Del(ctx, "match:queue:1v1", "match:processing")
				// 先加入一次
				mc.redis.HSet(ctx, "match:processing", "player2", "1v1")
				mc.redis.ZAdd(ctx, "match:queue:1v1", redis.Z{
					Score:  float64(1600),
					Member: "player2",
				})
			},
		},
		{
			name:      "不同模式加入队列",
			playerID:  "player3",
			mode:      common.GameMode5v5,
			mmr:       1700,
			wantError: false,
			setup: func() {
				mc.redis.Del(ctx, "match:queue:5v5", "match:processing")
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			
			err := mc.Enqueue(tt.playerID, tt.mode, tt.mmr)
			
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				
				// 验证是否加入队列
				queueKey := "match:queue:" + string(tt.mode)
				exists, err := mc.redis.ZScore(ctx, queueKey, tt.playerID).Result()
				assert.NoError(t, err)
				assert.Equal(t, float64(tt.mmr), exists)
				
				// 验证是否记录到processing
				exists2, err := mc.redis.HExists(ctx, "match:processing", tt.playerID).Result()
				assert.NoError(t, err)
				assert.True(t, exists2)
			}
		})
	}
}

func TestDequeue(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	ctx := context.Background()
	
	tests := []struct {
		name      string
		playerID  string
		setup     func() string // 返回 mode
		wantError bool
	}{
		{
			name:     "正常退出队列",
			playerID: "player1",
			setup: func() string {
				mc.redis.Del(ctx, "match:queue:1v1", "match:processing")
				mc.redis.ZAdd(ctx, "match:queue:1v1", redis.Z{
					Score:  float64(1500),
					Member: "player1",
				})
				matchInfo := PlayerMatchInfo{
					PlayerID:  "player1",
					MMR:       1500,
					EnqueueAt: time.Now().Unix(),
					Mode:      "1v1",
				}
				infoData, _ := json.Marshal(matchInfo)
				mc.redis.HSet(ctx, "match:processing", "player1", infoData)
				return "1v1"
			},
			wantError: false,
		},
		{
			name:     "玩家不在队列中",
			playerID: "player_not_exist",
			setup: func() string {
				mc.redis.Del(ctx, "match:queue:1v1", "match:processing")
				return "1v1"
			},
			wantError: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := ""
			if tt.setup != nil {
				mode = tt.setup()
			}
			
			err := mc.Dequeue(tt.playerID)
			
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				
				// 验证是否从队列移除
				queueKey := "match:queue:" + mode
				_, zerr := mc.redis.ZRank(ctx, queueKey, tt.playerID).Result()
				assert.True(t, zerr != nil) // redis.Nil error 表示不存在
				
				// 验证是否从processing移除
				exists2, _ := mc.redis.HExists(ctx, "match:processing", tt.playerID).Result()
				assert.False(t, exists2)
			}
		})
	}
}

func TestMatchRange(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	ctx := context.Background()
	
	tests := []struct {
		name          string
		mode          common.GameMode
		playerID      string
		mmr           int32
		setupPlayers  []struct {
			playerID string
			mmr      int32
		}
		expectMatched bool
	}{
		{
			name:     "MMR相近匹配成功",
			mode:     common.GameMode1v1,
			playerID: "player1",
			mmr:      1500,
			setupPlayers: []struct {
				playerID string
				mmr      int32
			}{
				{"player2", 1550}, // MMR相差50，在初始范围100内
			},
			expectMatched: true,
		},
		{
			name:     "MMR差异大需要扩大范围",
			mode:     common.GameMode1v1,
			playerID: "player1",
			mmr:      1500,
			setupPlayers: []struct {
				playerID string
				mmr      int32
			}{
				{"player2", 1700}, // MMR相差200，需要扩大范围
			},
			expectMatched: true,
		},
		{
			name:     "玩家不足无法匹配",
			mode:     common.GameMode5v5,
			playerID: "player1",
			mmr:      1500,
			setupPlayers: []struct {
				playerID string
				mmr      int32
			}{
				{"player2", 1500},
				{"player3", 1500},
			},
			expectMatched: false, // 5v5需要10人
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 清理
			queueKey := "match:queue:" + string(tt.mode)
			mc.redis.Del(ctx, queueKey, "match:processing")
			
			// 添加测试玩家到队列
			for _, p := range tt.setupPlayers {
				mc.redis.ZAdd(ctx, queueKey, redis.Z{
					Score:  float64(p.mmr),
					Member: p.playerID,
				})
				mc.redis.HSet(ctx, "match:processing", p.playerID, string(tt.mode))
			}
			
			// 添加发起匹配的玩家
			mc.redis.ZAdd(ctx, queueKey, redis.Z{
				Score:  float64(tt.mmr),
				Member: tt.playerID,
			})
			mc.redis.HSet(ctx, "match:processing", tt.playerID, string(tt.mode))
			
			// 执行匹配
			matched, err := mc.MatchRange(tt.mode, tt.playerID, tt.mmr)
			
			assert.NoError(t, err)
			
			if tt.expectMatched {
				assert.NotNil(t, matched)
				assert.GreaterOrEqual(t, len(matched), mc.getRequiredPlayers(tt.mode))
			} else {
				// 可能返回nil或空切片
				if matched != nil {
					assert.Less(t, len(matched), mc.getRequiredPlayers(tt.mode))
				}
			}
		})
	}
}

func TestGetRequiredPlayers(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	tests := []struct {
		mode     common.GameMode
		expected int
	}{
		{common.GameMode1v1, 2},
		{common.GameMode5v5, 10},
		{common.GameModeCasual, 2},
		{common.GameModeCustom, 1},
		{common.GameMode("unknown"), 2}, // 默认返回2
	}
	
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			result := mc.getRequiredPlayers(tt.mode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSelectClosestPlayers(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()

	tests := []struct {
		name         string
		players      []redis.Z
		targetMMR    int32
		n            int
		expectCount  int
	}{
		{
			name: "选择MMR最接近的2个玩家",
			players: []redis.Z{
				{Score: 1500, Member: "p1"},
				{Score: 1550, Member: "p2"},
				{Score: 1600, Member: "p3"},
				{Score: 1450, Member: "p4"},
			},
			targetMMR:   1500,
			n:           2,
			expectCount: 2,
		},
		{
			name: "玩家数量不足",
			players: []redis.Z{
				{Score: 1500, Member: "p1"},
			},
			targetMMR:   1500,
			n:           3,
			expectCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected := mc.selectClosestPlayers(tt.players, tt.targetMMR, tt.n)
			assert.Equal(t, tt.expectCount, len(selected))
		})
	}
}

func TestRemovePlayersAtomically(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	ctx := context.Background()
	
	tests := []struct {
		name          string
		players       []string
		setup         func() string
		expectRemoved int
	}{
		{
			name:    "原子性移除玩家",
			players: []string{"player1", "player2"},
			setup: func() string {
				queueKey := "match:queue:1v1_test"
				mc.redis.Del(ctx, queueKey)
				
				// 添加玩家到队列
				for _, p := range []string{"player1", "player2", "player3"} {
					mc.redis.ZAdd(ctx, queueKey, redis.Z{
						Score:  float64(1500),
						Member: p,
					})
				}
				
				return queueKey
			},
			expectRemoved: 2,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queueKey := ""
			if tt.setup != nil {
				queueKey = tt.setup()
			}
			
			removed, err := mc.removePlayersAtomically(queueKey, tt.players)
			
			assert.NoError(t, err)
			assert.Equal(t, tt.expectRemoved, len(removed))
			
			// 验证玩家已被移除
			for _, playerID := range removed {
				_, zerr := mc.redis.ZRank(ctx, queueKey, playerID).Result()
				assert.True(t, zerr != nil) // redis.Nil error 表示不存在
			}
		})
	}
}

func TestProcessTimeout(t *testing.T) {
	// 测试超时处理逻辑
	// 由于processTimeout是长期运行的协程，这里测试其能正确启动和处理
	
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	// 验证组件能正常初始化
	assert.NotNil(t, mc)
	
	// 实际超时测试需要模拟时间流逝，这里简化
	// 可以改用 fake clock 库如 github.com/benbjohnson/clock
	t.Skip("需要模拟时间进行测试")
}
// ========== ELO/MMR 系统测试 ==========

func TestCalculateELOUpdate(t *testing.T) {
	tests := []struct {
		name           string
		playerMMR      int32
		opponentMMR   int32
		actualScore    float64 // 1.0胜, 0.0负, 0.5平
		expectNonZero  bool
	}{
		{
			name:          "低MMR玩家击败高MMR玩家，获得大量分",
			playerMMR:     1000,
			opponentMMR:  2000,
			actualScore:   1.0,
			expectNonZero: true,
		},
		{
			name:          "高MMR玩家击败低MMR玩家，获得少量分",
			playerMMR:     2000,
			opponentMMR:  1000,
			actualScore:   1.0,
			expectNonZero: false,
		},
		{
			name:          "平局",
			playerMMR:     1500,
			opponentMMR:  1500,
			actualScore:   0.5,
			expectNonZero: false, // 期望分0.5，变化应该接近0
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			change := calculateELOUpdate(tt.playerMMR, tt.opponentMMR, tt.actualScore)
			
			if tt.expectNonZero {
				assert.NotZero(t, change)
			} else {
				// 平局变化应该很小
				assert.True(t, change >= -5 && change <= 5)
			}
			
			// 验证变化在合理范围内
			assert.True(t, change >= -50 && change <= 50)
		})
	}
}

func TestCalculateMatchMMRChange(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	// 设置测试玩家的排位信息
	players := []string{"player1", "player2", "player3", "player4"}
	
	for i, playerID := range players {
		info := &RankInfo{
			Tier:  RankBronze,
			Division: 1,
			LP:    0,
			Wins:  0,
			Losses: 0,
			MMR:   1000 + int32(i*100), // 1000, 1100, 1200, 1300
		}
		mc.SavePlayerRankInfo(playerID, info)
	}
	
	winners := []string{"player1", "player2"}
	
	changes := mc.calculateMatchMMRChange(players, winners)
	
	assert.Equal(t, len(players), len(changes))
	
	// 胜者应该获得正分
	for _, winner := range winners {
		assert.Greater(t, changes[winner], int32(0))
	}
	
	// 负者应该获得负分或0
	losers := []string{"player3", "player4"}
	for _, loser := range losers {
		assert.LessOrEqual(t, changes[loser], int32(0))
	}
}

func TestUpdatePlayerMMRAfterMatch(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	playerID := "test_player"
	
	// 初始化排位信息
	info := &RankInfo{
		Tier:     RankBronze,
		Division: 1,
		LP:       50,
		Wins:     5,
		Losses:   3,
		MMR:      1200,
	}
	mc.SavePlayerRankInfo(playerID, info)
	
	// 测试胜利
	err := mc.UpdatePlayerMMRAfterMatch(playerID, 25, true)
	assert.NoError(t, err)
	
	updatedInfo, err := mc.GetPlayerRankInfo(playerID)
	assert.NoError(t, err)
	assert.Equal(t, int32(1225), updatedInfo.MMR)
	assert.Equal(t, 6, updatedInfo.Wins)
	assert.Equal(t, 75, updatedInfo.LP) // 50 + 25
	
	// 测试失败
	err = mc.UpdatePlayerMMRAfterMatch(playerID, -20, false)
	assert.NoError(t, err)
	
	updatedInfo2, err := mc.GetPlayerRankInfo(playerID)
	assert.NoError(t, err)
	assert.Equal(t, int32(1205), updatedInfo2.MMR)
	assert.Equal(t, 4, updatedInfo2.Losses)
	assert.Equal(t, 55, updatedInfo2.LP) // 75 - 20
}

func TestGetPlayerRankInfo(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	playerID := "test_player"
	
	// 测试获取不存在的玩家（应该返回默认值）
	info, err := mc.GetPlayerRankInfo(playerID)
	assert.NoError(t, err)
	assert.Equal(t, RankBronze, info.Tier)
	assert.Equal(t, 1, info.Division)
	assert.Equal(t, 0, info.LP)
	assert.Equal(t, int32(1000), info.MMR)
	
	// 测试获取已存在的玩家
	info2 := &RankInfo{
		Tier:     RankSilver,
		Division: 2,
		LP:       75,
		Wins:     10,
		Losses:   5,
		MMR:      1500,
	}
	mc.SavePlayerRankInfo(playerID, info2)
	
	info3, err := mc.GetPlayerRankInfo(playerID)
	assert.NoError(t, err)
	assert.Equal(t, RankSilver, info3.Tier)
	assert.Equal(t, 2, info3.Division)
	assert.Equal(t, 75, info3.LP)
	assert.Equal(t, int32(1500), info3.MMR)
}

// ========== 排位匹配测试 ==========

func TestEnqueueRanked(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	playerID := "player1"
	mmr := int32(1500)
	
	err := mc.EnqueueRanked(playerID, mmr)
	assert.NoError(t, err)
	
	// 验证是否加入了1v1队列
	queueKey := "match:queue:1v1"
	exists, err := mc.redis.ZScore(ctx, queueKey, playerID).Result()
	assert.NoError(t, err)
	assert.Equal(t, float64(mmr), exists)
}

// ========== 快速匹配测试 ==========

func TestEnqueueQuick(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	playerID := "player1"
	mmr := int32(1500)
	
	// 记录原始配置
	originalDelta := mc.config.MMRDeltaInitial
	
	err := mc.EnqueueQuick(playerID, mmr)
	assert.NoError(t, err)
	
	// 验证配置是否已恢复
	assert.Equal(t, originalDelta, mc.config.MMRDeltaInitial)
	
	// 验证是否加入了Casual队列
	queueKey := "match:queue:" + string(common.GameModeCasual)
	exists, err := mc.redis.ZScore(ctx, queueKey, playerID).Result()
	assert.NoError(t, err)
	assert.Equal(t, float64(mmr), exists)
}

// ========== 自定义房间匹配测试 ==========

func TestJoinCustomRoom(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	playerID := "player1"
	roomID := "custom_room_123"
	password := "secret"
	
	err := mc.JoinCustomRoom(playerID, roomID, password)
	assert.NoError(t, err)
	
	// 简化实现，只验证不报错
	// 实际应该验证房间存在、密码正确等
}

// ========== 匹配取消机制测试 ==========

func TestCancelMatch(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	ctx := context.Background()
	
	playerID := "player1"
	mode := common.GameMode1v1
	mmr := int32(1500)
	
	// 先加入队列
	err := mc.Enqueue(playerID, mode, mmr)
	assert.NoError(t, err)
	
	// 取消匹配
	err = mc.CancelMatch(playerID)
	assert.NoError(t, err)
	
	// 验证是否已从队列移除
	queueKey := "match:queue:" + string(mode)
	_, zerr := mc.redis.ZRank(ctx, queueKey, playerID).Result()
	assert.True(t, zerr != nil) // redis.Nil error 表示不存在
	
	// 验证是否已从processing移除
	exists, _ := mc.redis.HExists(ctx, "match:processing", playerID).Result()
	assert.False(t, exists)
}

// ========== 匹配质量评估测试 ==========

func TestEvaluateMatchQuality(t *testing.T) {
	mc, mr := setupTestMatchComponent(t)
	defer mr.Close()
	
	// 先设置测试玩家的排位信息
	players := []string{"player1", "player2", "player3", "player4"}
	
	for i, playerID := range players {
		info := &RankInfo{
			Tier:  RankBronze,
			Division: 1,
			LP:    0,
			Wins:  0,
			Losses: 0,
			MMR:   1000 + int32(i*10), // 1000, 1010, 1020, 1030 - MMR很接近
		}
		mc.SavePlayerRankInfo(playerID, info)
	}
	
	quality := mc.evaluateMatchQuality(players)
	
	// MMR接近的玩家，质量应该较高
	assert.Greater(t, quality, 0.5)
	assert.LessOrEqual(t, quality, 1.0)
	
	// 测试空列表
	qualityEmpty := mc.evaluateMatchQuality([]string{})
	assert.Equal(t, 0.0, qualityEmpty)
	
	// 测试MMR差异大的情况
	players2 := []string{"player1", "player2"}
	
	info1 := &RankInfo{MMR: 1000}
	info2 := &RankInfo{MMR: 2000}
	mc.SavePlayerRankInfo("player1", info1)
	mc.SavePlayerRankInfo("player2", info2)
	
	qualityDiff := mc.evaluateMatchQuality(players2)
	
	// MMR差异大，质量应该较低
	assert.Less(t, qualityDiff, quality)
}
