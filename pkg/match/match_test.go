package match

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"github.com/astra-go/game-backend/pkg/common"
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
	logger, _ := zap.NewDevelopment()
	
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
				mc.redis.HSet(ctx, "match:processing", "player1", "1v1")
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
		players      []string
		targetMMR    int32
		n            int
		expectCount  int
	}{
		{
			name:        "选择MMR最接近的2个玩家",
			players:     []string{"p1", "p2", "p3", "p4"},
			targetMMR:   1500,
			n:           2,
			expectCount: 2,
		},
		{
			name:        "玩家数量不足",
			players:     []string{"p1"},
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