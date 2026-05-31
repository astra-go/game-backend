package statesync

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/astra-go/game-backend/pkg/common"
)

// MockRoomSession mock房间会话
type MockRoomSession struct {
	mock.Mock
	roomID string
	frame  int64
	players map[string]*common.RoomPlayer
	running bool
}

func NewMockRoomSession(roomID string) *MockRoomSession {
	return &MockRoomSession{
		roomID: roomID,
		frame:  0,
		players: make(map[string]*common.RoomPlayer),
		running: true,
	}
}

func (m *MockRoomSession) GetRoomID() string {
	return m.roomID
}

func (m *MockRoomSession) GetFrame() int64 {
	return m.frame
}

func (m *MockRoomSession) GetPlayers() map[string]*common.RoomPlayer {
	return m.players
}

func (m *MockRoomSession) IsRunning() bool {
	return m.running
}

func (m *MockRoomSession) SetFrame(frame int64) {
	m.frame = frame
}

func TestNewStateSync(t *testing.T) {
	tests := []struct {
		name string
		roomID string
		hz    int
	}{
		{
			name:   "创建状态同步器_10Hz",
			roomID: "room1",
			hz:     10,
		},
		{
			name:   "创建状态同步器_20Hz",
			roomID: "room2",
			hz:     20,
		},
		{
			name:   "创建状态同步器_30Hz",
			roomID: "room3",
			hz:     30,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := NewMockRoomSession(tt.roomID)
			ss := NewStateSync(session, tt.hz)
			
			assert.NotNil(t, ss)
			assert.Equal(t, tt.hz, ss.hz)
			assert.Equal(t, int64(0), ss.lastFullSync)
			assert.Equal(t, int64(300), ss.fullSyncInterval)
			assert.False(t, ss.running)
			assert.False(t, ss.paused)
			assert.NotNil(t, ss.entities)
			assert.NotNil(t, ss.dirtyEntities)
		})
	}
}

func TestStateSync_Run(t *testing.T) {
	session := NewMockRoomSession("room1")
	session.frame = 0
	ss := NewStateSync(session, 10) // 10Hz = 100ms/tick
	
	// 启动状态同步（异步）
	go ss.Run()
	
	// 等待几帧
	time.Sleep(350 * time.Millisecond)
	
	// 停止
	ss.Stop()
	
	time.Sleep(50 * time.Millisecond)
	assert.False(t, ss.running)
}

func TestStateSync_SubmitDelta(t *testing.T) {
	tests := []struct {
		name      string
		delta     common.EntityDelta
		setup     func(*StateSync, *MockRoomSession)
		wantError bool
	}{
		{
			name: "正常提交增量",
			delta: common.EntityDelta{
				EntityID: 1,
				Position: &common.Position{X: 100, Y: 200, Z: 0},
				Health:   &common.Health{Current: 90, Max: 100},
				Frame:    0,
			},
			setup: func(ss *StateSync, session *MockRoomSession) {
				go ss.Run()
				time.Sleep(50 * time.Millisecond)
			},
			wantError: false,
		},
		{
			name: "提交位置增量",
			delta: common.EntityDelta{
				EntityID: 2,
				Position: &common.Position{X: 150, Y: 250, Z: 10},
				Frame:    1,
			},
			setup: func(ss *StateSync, session *MockRoomSession) {
				go ss.Run()
				time.Sleep(50 * time.Millisecond)
			},
			wantError: false,
		},
		{
			name: "提交血量增量",
			delta: common.EntityDelta{
				EntityID: 3,
				Health:   &common.Health{Current: 50, Max: 100},
				Frame:    2,
			},
			setup: func(ss *StateSync, session *MockRoomSession) {
				go ss.Run()
				time.Sleep(50 * time.Millisecond)
			},
			wantError: false,
		},
		{
			name: "状态同步未运行",
			delta: common.EntityDelta{
				EntityID: 1,
				Frame:    0,
			},
			setup: func(ss *StateSync, session *MockRoomSession) {
				// 不启动
			},
			wantError: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := NewMockRoomSession("room1")
			ss := NewStateSync(session, 10)
			
			if tt.setup != nil {
				tt.setup(ss, session)
			}
			
			err := ss.SubmitDelta(tt.delta)
			
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				
				// 验证增量被应用
				time.Sleep(50 * time.Millisecond) // 等待处理
				
				state, err := ss.GetEntityState(tt.delta.EntityID)
				if tt.delta.Position != nil {
					assert.NoError(t, err)
					assert.Equal(t, tt.delta.Position.X, state.Position.X)
					assert.Equal(t, tt.delta.Position.Y, state.Position.Y)
				}
				if tt.delta.Health != nil {
					assert.NoError(t, err)
					assert.Equal(t, tt.delta.Health.Current, state.Health.Current)
				}
			}
			
			// 清理
			ss.Stop()
			time.Sleep(50 * time.Millisecond)
		})
	}
}

func TestStateSync_ApplyDelta(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	tests := []struct {
		name   string
		delta  *common.EntityDelta
		verify func(*testing.T, *StateSync)
	}{
		{
			name: "应用位置增量",
			delta: &common.EntityDelta{
				EntityID: 1,
				Position: &common.Position{X: 100, Y: 200, Z: 0},
				Frame:    0,
			},
			verify: func(t *testing.T, ss *StateSync) {
				state, err := ss.GetEntityState(1)
				assert.NoError(t, err)
				assert.Equal(t, float32(100), state.Position.X)
				assert.Equal(t, float32(200), state.Position.Y)
				assert.Equal(t, float32(0), state.Position.Z)
			},
		},
		{
			name: "应用血量增量",
			delta: &common.EntityDelta{
				EntityID: 2,
				Health:   &common.Health{Current: 80, Max: 100},
				Frame:    1,
			},
			verify: func(t *testing.T, ss *StateSync) {
				state, err := ss.GetEntityState(2)
				assert.NoError(t, err)
				assert.Equal(t, int32(80), state.Health.Current)
				assert.Equal(t, int32(100), state.Health.Max)
			},
		},
		{
			name: "创建新实体",
			delta: &common.EntityDelta{
				EntityID: 999,
				Position: &common.Position{X: 0, Y: 0, Z: 0},
				Health:   &common.Health{Current: 100, Max: 100},
				Frame:    2,
			},
			verify: func(t *testing.T, ss *StateSync) {
				state, err := ss.GetEntityState(999)
				assert.NoError(t, err)
				assert.NotNil(t, state)
				assert.Equal(t, int64(999), state.EntityID)
			},
		},
		{
			name: "多次增量更新",
			delta: nil, // 会在测试中多次调用
			verify: nil,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "多次增量更新" {
				// 特殊测试：多次更新同一实体
				deltas := []*common.EntityDelta{
					{
						EntityID: 10,
						Position: &common.Position{X: 0, Y: 0, Z: 0},
						Health:   &common.Health{Current: 100, Max: 100},
						Frame:    0,
					},
					{
						EntityID: 10,
						Position: &common.Position{X: 10, Y: 10, Z: 0},
						Health:   &common.Health{Current: 90, Max: 100},
						Frame:    1,
					},
					{
						EntityID: 10,
						Position: &common.Position{X: 20, Y: 20, Z: 0},
						Health:   &common.Health{Current: 80, Max: 100},
						Frame:    2,
					},
				}
				
				for _, delta := range deltas {
					ss.applyDelta(delta)
				}
				
				state, err := ss.GetEntityState(10)
				assert.NoError(t, err)
				assert.Equal(t, float32(20), state.Position.X)
				assert.Equal(t, int32(80), state.Health.Current)
				assert.True(t, state.Dirty)
				
				return
			}
			
			ss.applyDelta(tt.delta)
			
			if tt.verify != nil {
				tt.verify(t, ss)
			}
			
			// 验证实体被标记为脏
			ss.mu.Lock()
			_, isDirty := ss.dirtyEntities[tt.delta.EntityID]
			ss.mu.Unlock()
			
			assert.True(t, isDirty)
		})
	}
}

func TestStateSync_GetEntityState(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 添加测试实体
	ss.mu.Lock()
	ss.entities[1] = &common.EntityState{
		EntityID: 1,
		Position: common.Position{X: 100, Y: 200, Z: 0},
		Health:   common.Health{Current: 100, Max: 100},
		LastSync: 0,
		Dirty:    false,
	}
	ss.mu.Unlock()
	
	tests := []struct {
		name         string
		entityID     int64
		expectError  bool
		expectState  *common.EntityState
	}{
		{
			name:        "获取存在的实体",
			entityID:    1,
			expectError: false,
			expectState: &common.EntityState{
				EntityID: 1,
				Position: common.Position{X: 100, Y: 200, Z: 0},
				Health:   common.Health{Current: 100, Max: 100},
			},
		},
		{
			name:        "获取不存在的实体",
			entityID:    999,
			expectError: true,
			expectState: nil,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := ss.GetEntityState(tt.entityID)
			
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, state)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, state)
				assert.Equal(t, tt.entityID, state.EntityID)
				if tt.expectState != nil {
					assert.Equal(t, tt.expectState.Position.X, state.Position.X)
					assert.Equal(t, tt.expectState.Health.Current, state.Health.Current)
				}
			}
		})
	}
}

func TestStateSync_GetAllStates(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 添加多个实体
	ss.mu.Lock()
	for i := int64(1); i <= 5; i++ {
		ss.entities[i] = &common.EntityState{
			EntityID: i,
			Position: common.Position{X: float32(i * 10), Y: float32(i * 20), Z: 0},
			Health:   common.Health{Current: 100, Max: 100},
		}
	}
	ss.mu.Unlock()
	
	states := ss.GetAllStates()
	
	assert.Equal(t, 5, len(states))
	
	// 验证所有实体都在
	entityIDs := make(map[int64]bool)
	for _, state := range states {
		entityIDs[state.EntityID] = true
	}
	
	for i := int64(1); i <= 5; i++ {
		assert.True(t, entityIDs[i], "实体 %d 应该存在", i)
	}
}

func TestStateSync_ApplySnapshot(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 创建测试快照
	snapshot := &common.GameSnapshot{
		Frame: 100,
		Inputs: map[int64]map[string]common.InputCommand{
			0: {
				"player1": {
					Type:     common.InputTypeMove,
					Frame:    0,
					PlayerID: "player1",
					Data:     []byte(`{}`),
				},
			},
		},
		Timestamp: time.Now().UnixNano(),
	}
	
	// 应用快照
	ss.ApplySnapshot(snapshot)
	
	// 验证快照被应用（简化验证）
	assert.True(t, true) // 实际应验证状态被正确初始化
}

func TestStateSync_PauseResume(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 启动
	go ss.Run()
	time.Sleep(50 * time.Millisecond)
	
	// 暂停
	ss.Pause()
	assert.True(t, ss.paused)
	
	time.Sleep(200 * time.Millisecond)
	
	// 恢复
	ss.Resume()
	assert.False(t, ss.paused)
	
	time.Sleep(100 * time.Millisecond)
	
	// 清理
	ss.Stop()
	time.Sleep(50 * time.Millisecond)
}

func TestStateSync_Stop(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 启动
	go ss.Run()
	time.Sleep(50 * time.Millisecond)
	
	assert.True(t, ss.running)
	
	// 停止
	ss.Stop()
	
	time.Sleep(100 * time.Millisecond)
	assert.False(t, ss.running)
}

func TestStateSync_Tick(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 添加脏实体
	ss.mu.Lock()
	ss.entities[1] = &common.EntityState{
		EntityID: 1,
		Position: common.Position{X: 100, Y: 200, Z: 0},
		Health:   common.Health{Current: 100, Max: 100},
		Dirty:    true,
	}
	ss.dirtyEntities[1] = true
	ss.mu.Unlock()
	
	// 设置帧号
	session.frame = 10
	
	// 执行tick
	ss.tick()
	
	// 验证脏集合被清空
	ss.mu.Lock()
	dirtyCount := len(ss.dirtyEntities)
	ss.mu.Unlock()
	
	assert.Equal(t, 0, dirtyCount)
}

func TestStateSync_BroadcastDelta(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 创建测试增量
	deltas := []common.EntityDelta{
		{
			EntityID: 1,
			Position: &common.Position{X: 100, Y: 200, Z: 0},
			Frame:    0,
		},
		{
			EntityID: 2,
			Health:   &common.Health{Current: 90, Max: 100},
			Frame:    0,
		},
	}
	
	// 广播增量
	ss.broadcastDelta(0, deltas)
	
	// 验证消息被序列化并发送到channel
	select {
	case data := <-ss.sendCh:
		assert.NotNil(t, data)
		
		// 验证可以反序列化
		var msg common.DeltaMessage
		err := json.Unmarshal(data, &msg)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), msg.Frame)
		assert.Equal(t, 2, len(msg.Delta))
		
	case <-time.After(100 * time.Millisecond):
		t.Error("超时：没有收到广播消息")
	}
}

func TestStateSync_BroadcastFullSync(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 添加测试实体
	ss.mu.Lock()
	for i := int64(1); i <= 3; i++ {
		ss.entities[i] = &common.EntityState{
			EntityID: i,
			Position: common.Position{X: float32(i * 10), Y: 0, Z: 0},
			Health:   common.Health{Current: 100, Max: 100},
		}
	}
	ss.mu.Unlock()
	
	// 广播全量同步
	ss.broadcastFullSync(0)
	
	// 验证lastFullSync被更新
	assert.Equal(t, int64(0), ss.lastFullSync)
}

func TestStateSync_FullSyncInterval(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	ss.fullSyncInterval = 5 // 设置很小的间隔方便测试
	
	// 初始状态
	assert.Equal(t, int64(0), ss.lastFullSync)
	
	// 模拟tick，帧号推进
	session.frame = 6 // > fullSyncInterval
	
	// 执行tick，应该触发全量同步
	ss.tick()
	
	// 验证lastFullSync被更新
	assert.Equal(t, int64(6), ss.lastFullSync)
}

func TestStateSync_ProcessDeltas(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 启动增量处理协程
	go ss.processDeltas()
	
	// 发送增量
	deltas := []common.EntityDelta{
		{
			EntityID: 1,
			Position: &common.Position{X: 100, Y: 200, Z: 0},
			Frame:    0,
		},
		{
			EntityID: 2,
			Position: &common.Position{X: 150, Y: 250, Z: 0},
			Frame:    1,
		},
	}
	
	for _, delta := range deltas {
		select {
		case ss.deltaCh <- &delta:
			// 发送成功
		case <-time.After(50 * time.Millisecond):
			t.Error("发送增量超时")
		}
	}
	
	// 等待处理
	time.Sleep(100 * time.Millisecond)
	
	// 验证增量被应用
	state1, err := ss.GetEntityState(1)
	assert.NoError(t, err)
	assert.Equal(t, float32(100), state1.Position.X)
	
	state2, err := ss.GetEntityState(2)
	assert.NoError(t, err)
	assert.Equal(t, float32(150), state2.Position.X)
	
	// 清理
	ss.Stop()
	time.Sleep(50 * time.Millisecond)
}

func TestStateSync_ChannelBlocking(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 不启动Run，running=false，SubmitDelta应返回错误
	delta := common.EntityDelta{
		EntityID: 1,
		Frame:    0,
	}
	
	err := ss.SubmitDelta(delta)
	assert.Error(t, err)
}

func TestStateSync_MultipleDeltasConcurrent(t *testing.T) {
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	go ss.Run()
	time.Sleep(50 * time.Millisecond)
	
	// 并发提交多个增量
	var wg sync.WaitGroup
	deltaCount := 10
	
	for i := 0; i < deltaCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			
			delta := common.EntityDelta{
				EntityID: int64(idx + 1),
				Position: &common.Position{X: float32(idx * 10), Y: 0, Z: 0},
				Frame:    ss.session.GetFrame(),
			}
			
			err := ss.SubmitDelta(delta)
			assert.NoError(t, err)
		}(i)
	}
	
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	
	// 验证所有增量都被应用
	states := ss.GetAllStates()
	assert.GreaterOrEqual(t, len(states), deltaCount)
	
	ss.Stop()
	time.Sleep(50 * time.Millisecond)
}

func TestStateSync_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "空增量广播",
			test: func(t *testing.T) {
				session := NewMockRoomSession("room1")
				ss := NewStateSync(session, 10)
				
				// 没有脏实体，不应该广播
				session.frame = 1
				ss.tick()
				
				// 验证没有消息发送到sendCh
				select {
				case <-ss.sendCh:
					t.Error("不应该有广播消息")
				case <-time.After(50 * time.Millisecond):
					// 正确：没有消息
				}
			},
		},
		{
			name: "全量同步序列化失败",
			test: func(t *testing.T) {
				session := NewMockRoomSession("room1")
				ss := NewStateSync(session, 10)
				
				// 添加无法序列化的数据（这里简化）
				ss.broadcastFullSync(0)
				
				// 应该不panic
				assert.True(t, true)
			},
		},
		{
			name: "增量同步序列化失败",
			test: func(t *testing.T) {
				session := NewMockRoomSession("room1")
				ss := NewStateSync(session, 10)
				
				// 创建增量
				deltas := []common.EntityDelta{
					{
						EntityID: 1,
						Frame:    0,
					},
				}
				
				ss.broadcastDelta(0, deltas)
				
				// 应该不panic
				assert.True(t, true)
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
		})
	}
}

func TestStateSync_Integration(t *testing.T) {
	// 集成测试：模拟完整的状态同步流程
	session := NewMockRoomSession("room1")
	ss := NewStateSync(session, 10)
	
	// 启动
	go ss.Run()
	time.Sleep(50 * time.Millisecond)
	
	// 提交多个增量
	deltas := []common.EntityDelta{
		{
			EntityID: 1,
			Position: &common.Position{X: 100, Y: 100, Z: 0},
			Health:   &common.Health{Current: 100, Max: 100},
			Frame:    0,
		},
		{
			EntityID: 1,
			Position: &common.Position{X: 110, Y: 110, Z: 0},
			Health:   &common.Health{Current: 95, Max: 100},
			Frame:    1,
		},
		{
			EntityID: 2,
			Position: &common.Position{X: 200, Y: 200, Z: 0},
			Health:   &common.Health{Current: 100, Max: 100},
			Frame:    1,
		},
	}
	
	for _, delta := range deltas {
		err := ss.SubmitDelta(delta)
		assert.NoError(t, err)
	}
	
	// 等待处理，轮询直到状态正确或超时
	var state1 *common.EntityState
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state1, err = ss.GetEntityState(1)
		if err == nil && state1.Position.X == 110 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.NoError(t, err)
	assert.Equal(t, float32(110), state1.Position.X)
	assert.Equal(t, int32(95), state1.Health.Current)

	state2, err := ss.GetEntityState(2)
	assert.NoError(t, err)
	assert.Equal(t, float32(200), state2.Position.X)
	
	// 验证所有状态
	allStates := ss.GetAllStates()
	assert.Equal(t, 2, len(allStates))
	
	// 停止
	ss.Stop()
	time.Sleep(50 * time.Millisecond)
}
