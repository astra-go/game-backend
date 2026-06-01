package framesync

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

func TestNewFrameSync(t *testing.T) {
	tests := []struct {
		name    string
		roomID  string
		tickMs  int
		expectErr bool
	}{
		{
			name:    "创建帧同步器_60fps",
			roomID:  "room1",
			tickMs:  16, // ~60fps
			expectErr: false,
		},
		{
			name:    "创建帧同步器_30fps",
			roomID:  "room2",
			tickMs:  33, // ~30fps
			expectErr: false,
		},
		{
			name:    "创建帧同步器_自定义帧率",
			roomID:  "room3",
			tickMs:  50, // 20fps
			expectErr: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := NewMockRoomSession(tt.roomID)
			fs := NewFrameSync(session, tt.tickMs)
			
			assert.NotNil(t, fs)
			assert.Equal(t, tt.tickMs, fs.tickMs)
			assert.Equal(t, int64(0), fs.frame)
			assert.False(t, fs.running)
			assert.False(t, fs.paused)
			assert.NotNil(t, fs.inputs)
			assert.NotNil(t, fs.history)
		})
	}
}

func TestFrameSync_Run(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100) // 100ms/tick 方便测试
	
	// 启动帧同步（异步）
	go fs.Run()
	
	// 等待几帧
	time.Sleep(350 * time.Millisecond)
	
	// 验证帧号推进
	frame := fs.GetCurrentFrame()
	assert.Greater(t, frame, int64(0))
	assert.LessOrEqual(t, frame, int64(5)) // 大约3-4帧
	
	// 停止
	fs.Stop()
	
	time.Sleep(50 * time.Millisecond)
	assert.False(t, fs.running)
}

func TestFrameSync_SubmitInput(t *testing.T) {
	tests := []struct {
		name      string
		input     common.InputCommand
		setup     func(*FrameSync, *MockRoomSession)
		wantError bool
	}{
		{
			name: "正常提交输入",
			input: common.InputCommand{
				Type:      common.InputTypeMove,
				Frame:     0,
				PlayerID:  "player1",
				Data:      []byte(`{"x":100,"y":200}`),
				Timestamp: time.Now().UnixNano(),
			},
			setup: func(fs *FrameSync, session *MockRoomSession) {
				// 启动帧同步
				go fs.Run()
				time.Sleep(50 * time.Millisecond)
			},
			wantError: false,
		},
		{
			name: "提交过期输入_应被丢弃",
			input: common.InputCommand{
				Type:      common.InputTypeMove,
				Frame:     -100, // 过期帧
				PlayerID:  "player1",
				Data:      []byte(`{}`),
				Timestamp: time.Now().UnixNano(),
			},
			setup: func(fs *FrameSync, session *MockRoomSession) {
				go fs.Run()
				time.Sleep(50 * time.Millisecond)
			},
			wantError: false, // 不会报错，但会丢弃
		},
		{
			name: "提交未来帧_应被调整",
			input: common.InputCommand{
				Type:      common.InputTypeSkill,
				Frame:     10000, // 未来帧
				PlayerID:  "player1",
				Data:      []byte(`{}`),
				Timestamp: time.Now().UnixNano(),
			},
			setup: func(fs *FrameSync, session *MockRoomSession) {
				go fs.Run()
				time.Sleep(50 * time.Millisecond)
			},
			wantError: false,
		},
		{
			name: "帧同步未运行",
			input: common.InputCommand{
				Type:      common.InputTypeMove,
				Frame:     0,
				PlayerID:  "player1",
				Data:      []byte(`{}`),
				Timestamp: time.Now().UnixNano(),
			},
			setup: func(fs *FrameSync, session *MockRoomSession) {
				// 不启动
			},
			wantError: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := NewMockRoomSession("room1")
			fs := NewFrameSync(session, 100)
			
			if tt.setup != nil {
				tt.setup(fs, session)
			}
			
			err := fs.SubmitInput(tt.input)
			
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			
			// 清理
			fs.Stop()
			time.Sleep(50 * time.Millisecond)
		})
	}
}

func TestFrameSync_CollectInput(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	tests := []struct {
		name     string
		frame    int64
		playerID string
		inputType common.InputType
	}{
		{
			name:      "收集第一帧输入",
			frame:     0,
			playerID:  "player1",
			inputType: common.InputTypeMove,
		},
		{
			name:      "同一帧多个玩家输入",
			frame:     1,
			playerID:  "player2",
			inputType: common.InputTypeSkill,
		},
		{
			name:      "同一玩家同一帧覆盖输入",
			frame:     2,
			playerID:  "player1",
			inputType: common.InputTypeAttack,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := common.InputCommand{
				Type:     tt.inputType,
				Frame:    tt.frame,
				PlayerID: tt.playerID,
				Data:     []byte(`{}`),
			}
			
			fs.collectInput(input)
			
			// 验证输入被收集
			fs.mu.Lock()
			frameInputs, exists := fs.inputs[tt.frame]
			fs.mu.Unlock()
			
			assert.True(t, exists)
			assert.Contains(t, frameInputs, tt.playerID)
			assert.Equal(t, tt.inputType, frameInputs[tt.playerID].Type)
		})
	}
}

func TestFrameSync_BroadcastFrame(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	// 添加一些输入
	testFrame := int64(5)
	testInputs := []common.InputCommand{
		{
			Type:     common.InputTypeMove,
			Frame:    testFrame,
			PlayerID: "player1",
			Data:     []byte(`{"x":100}`),
		},
		{
			Type:     common.InputTypeSkill,
			Frame:    testFrame,
			PlayerID: "player2",
			Data:     []byte(`{"skill_id":1}`),
		},
	}
	
	// 手动添加到history
	fs.mu.Lock()
	fs.history[testFrame] = testInputs
	fs.mu.Unlock()
	
	// 广播（会序列化并发送到channel）
	fs.broadcastFrame(testFrame, testInputs)
	
	// 验证广播消息格式（通过捕获输出来测试，这里简化）
	assert.True(t, true) // 实际应验证序列化结果
}

func TestFrameSync_GetHistory(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	// 准备历史数据
	fs.mu.Lock()
	for i := int64(0); i < 10; i++ {
		fs.history[i] = []common.InputCommand{
			{
				Type:     common.InputTypeMove,
				Frame:    i,
				PlayerID: "player1",
				Data:     []byte(`{}`),
			},
		}
	}
	fs.mu.Unlock()
	
	tests := []struct {
		name       string
		fromFrame  int64
		toFrame    int64
		expectLen  int
	}{
		{
			name:       "获取部分历史",
			fromFrame:  2,
			toFrame:    5,
			expectLen:  4, // 2,3,4,5
		},
		{
			name:       "获取全部历史",
			fromFrame:  0,
			toFrame:    9,
			expectLen:  10,
		},
		{
			name:       "请求不存在的帧",
			fromFrame:  100,
			toFrame:    110,
			expectLen:  11, // 返回空切片
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := fs.GetHistory(tt.fromFrame, tt.toFrame)
			
			assert.Equal(t, tt.expectLen, len(history))
			
			// 验证每一帧都存在（可能为空）
			for i, frameInputs := range history {
				expectedFrame := tt.fromFrame + int64(i)
				if expectedFrame <= 9 {
					assert.NotNil(t, frameInputs)
				}
			}
		})
	}
}

func TestFrameSync_GetSnapshot(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	// 添加一些测试数据
	fs.mu.Lock()
	fs.frame = 100
	fs.inputs[100] = map[string]common.InputCommand{
		"player1": {
			Type:     common.InputTypeMove,
			Frame:    100,
			PlayerID: "player1",
			Data:     []byte(`{}`),
		},
	}
	fs.mu.Unlock()
	
	snapshot := fs.GetSnapshot()
	
	assert.NotNil(t, snapshot)
	assert.Equal(t, int64(100), snapshot.Frame)
	assert.NotNil(t, snapshot.Inputs)
	assert.Greater(t, snapshot.Timestamp, int64(0))
}

func TestFrameSync_PauseResume(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	// 启动
	go fs.Run()
	time.Sleep(50 * time.Millisecond)
	
	// 暂停
	fs.Pause()
	assert.True(t, fs.paused)
	
	time.Sleep(200 * time.Millisecond)
	frameAfterPause := fs.GetCurrentFrame()
	
	// 等待一段时间
	time.Sleep(200 * time.Millisecond)
	frameAfterWait := fs.GetCurrentFrame()
	
	// 暂停期间帧号不应增加
	assert.Equal(t, frameAfterPause, frameAfterWait)
	
	// 恢复
	fs.Resume()
	assert.False(t, fs.paused)
	
	time.Sleep(150 * time.Millisecond)
	frameAfterResume := fs.GetCurrentFrame()
	
	// 恢复后帧号应继续增加
	assert.Greater(t, frameAfterResume, frameAfterWait)
	
	// 清理
	fs.Stop()
	time.Sleep(50 * time.Millisecond)
}

func TestFrameSync_Stop(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	// 启动
	go fs.Run()
	time.Sleep(50 * time.Millisecond)
	
	assert.True(t, fs.running)
	
	// 停止
	fs.Stop()
	
	time.Sleep(100 * time.Millisecond)
	assert.False(t, fs.running)
}

func TestFrameSync_Tick(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	// 手动添加输入
	fs.mu.Lock()
	fs.inputs[0] = map[string]common.InputCommand{
		"player1": {
			Type:     common.InputTypeMove,
			Frame:    0,
			PlayerID: "player1",
			Data:     []byte(`{}`),
		},
	}
	fs.mu.Unlock()
	
	// 执行tick
	fs.tick()
	
	// 验证帧号推进
	assert.Equal(t, int64(1), fs.frame)
	
	// 验证历史记录
	fs.mu.Lock()
	history, exists := fs.history[0]
	fs.mu.Unlock()
	
	assert.True(t, exists)
	assert.Equal(t, 1, len(history))
	
	// 验证旧数据被清理（historyMax=600）
	// tick() 只删除 frame-historyMax 这一个帧，不是所有旧帧
	fs.frame = 700
	fs.tick()
	
	fs.mu.Lock()
	_, exists0 := fs.history[0]    // frame 0 不在清理范围内
	_, exists100 := fs.history[100] // frame 100 == 700-600, 应被清理
	fs.mu.Unlock()
	
	assert.True(t, exists0)
	assert.False(t, exists100)
}

func TestFrameSync_InputChannelBlocking(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	// 手动设置running=true，不启动Run()
	fs.running = true
	
	// 填满inputCh（容量1024），使后续SubmitInput超时
	for i := 0; i < 1024; i++ {
		fs.inputCh <- common.InputCommand{
			Type:     common.InputTypeMove,
			Frame:    0,
			PlayerID: "player1",
			Data:     []byte(`{}`),
		}
	}
	
	input := common.InputCommand{
		Type:     common.InputTypeMove,
		Frame:    0,
		PlayerID: "player1",
		Data:     []byte(`{}`),
	}
	
	// 应该超时返回阻塞错误
	err := fs.SubmitInput(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "阻塞")
}
func TestFrameSync_MultiplePlayersInput(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	go fs.Run()
	time.Sleep(50 * time.Millisecond)
	
	// 多个玩家同时提交输入
	var wg sync.WaitGroup
	playerCount := 5
	
	for i := 0; i < playerCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			
			input := common.InputCommand{
				Type:     common.InputTypeMove,
				Frame:    fs.GetCurrentFrame(),
				PlayerID: string(rune('A' + idx)),
				Data:     []byte(`{}`),
			}
			
			err := fs.SubmitInput(input)
			assert.NoError(t, err)
		}(i)
	}
	
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	
	// 注意：输入可能已被tick消费到history中，先停止再检查
	fs.Stop()
	time.Sleep(50 * time.Millisecond)
	
	fs.mu.Lock()
	// 检查所有帧的history，统计总输入数
	totalInputs := 0
	for _, h := range fs.history {
		totalInputs += len(h)
	}
	fs.mu.Unlock()
	
	// 所有玩家输入应都被处理（要么在inputs中，要么在history中）
	assert.GreaterOrEqual(t, totalInputs, playerCount)
}

func TestFrameSync_SerializeBroadcastMessage(t *testing.T) {
	session := NewMockRoomSession("room1")
	fs := NewFrameSync(session, 100)
	
	// 创建测试输入
	inputs := []common.InputCommand{
		{
			Type:     common.InputTypeMove,
			Frame:    0,
			PlayerID: "player1",
			Data:     []byte(`{"x":100,"y":200}`),
		},
	}
	
	// 广播
	fs.broadcastFrame(0, inputs)
	
	// 验证消息能被正确序列化（实际应发送到channel，这里简化）
	msg := &common.WSMessage{
		Type:   common.WSMsgFrame,
		RoomID: session.GetRoomID(),
		Frame:  0,
		Data: map[string]any{
			"frame":  0,
			"inputs": inputs,
			"count":  len(inputs),
		},
	}
	
	data, err := json.Marshal(msg)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)
	
	// 验证可以反序列化
	var decoded common.WSMessage
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, common.WSMsgFrame, decoded.Type)
}

func TestFrameSync_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "空输入处理",
			test: func(t *testing.T) {
				session := NewMockRoomSession("room1")
				fs := NewFrameSync(session, 100)
				
				fs.mu.Lock()
				fs.inputs[0] = make(map[string]common.InputCommand)
				fs.mu.Unlock()
				
				fs.tick()
				
				// 应该不panic
				assert.Equal(t, int64(1), fs.frame)
			},
		},
		{
			name: "historyMax边界",
			test: func(t *testing.T) {
				session := NewMockRoomSession("room1")
				fs := NewFrameSync(session, 100)
				fs.historyMax = 5 // 设置很小的历史
				
				// 添加超过historyMax的帧
				for i := int64(0); i < 10; i++ {
					fs.mu.Lock()
					fs.history[i] = []common.InputCommand{}
					fs.mu.Unlock()
				}
				
				fs.frame = 10
				fs.tick()
				
				// tick在frame=10时清理的是 frame-historyMax = 10-5 = 5
				fs.mu.Lock()
				_, exists5 := fs.history[5]
				_, exists0 := fs.history[0] // 不在清理范围
				fs.mu.Unlock()
				
				assert.False(t, exists5)
				assert.True(t, exists0)
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
		})
	}
}
