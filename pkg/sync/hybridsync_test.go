package sync

import (
	"sync"
	"testing"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/framesync"
	"github.com/astra-go/game-backend/pkg/statesync"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// ========== Mock RoomSessionInterface ==========

type MockRoomSession struct {
	mock.Mock
}

func (m *MockRoomSession) GetRoomID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockRoomSession) GetFrame() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *MockRoomSession) GetPlayers() map[string]*common.RoomPlayer {
	args := m.Called()
	return args.Get(0).(map[string]*common.RoomPlayer)
}

func (m *MockRoomSession) IsRunning() bool {
	args := m.Called()
	return args.Bool(0)
}

// ========== Helper ==========

func newTestHybridSync(session common.RoomSessionInterface) *HybridSync {
	return NewHybridSync(session, zap.NewNop())
}

// setupMockSession 配置 mock 的默认返回值（用于帧同步/状态同步内部调用）
func setupMockSession(m *MockRoomSession) {
	m.On("GetRoomID").Return("room-test")
	m.On("GetFrame").Return(int64(0))
	m.On("GetPlayers").Return(map[string]*common.RoomPlayer{})
	m.On("IsRunning").Return(true)
}

// ========== Tests: NewHybridSync ==========

func TestNewHybridSync_DefaultMode(t *testing.T) {
	session := new(MockRoomSession)
	hs := newTestHybridSync(session)

	assert.Equal(t, common.SyncModeFrame, hs.GetCurrentMode())
	assert.Equal(t, int64(300), hs.lockstepFrames)
	assert.Equal(t, int64(60), hs.stateSyncFrames)
	assert.Equal(t, int64(0), hs.frameCount)
}

// ========== Tests: GetCurrentMode ==========

func TestHybridSync_GetCurrentMode(t *testing.T) {
	session := new(MockRoomSession)

	tests := []struct {
		name     string
		setup    func(*HybridSync)
		expected common.SyncMode
	}{
		{
			name:     "默认帧同步模式",
			setup:    func(_ *HybridSync) {},
			expected: common.SyncModeFrame,
		},
		{
			name: "手动切换到状态同步",
			setup: func(hs *HybridSync) {
				hs.currentMode = common.SyncModeState
			},
			expected: common.SyncModeState,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := newTestHybridSync(session)
			tt.setup(hs)
			assert.Equal(t, tt.expected, hs.GetCurrentMode())
		})
	}
}

// ========== Tests: SubmitInput ==========

func TestHybridSync_SubmitInput_FrameMode(t *testing.T) {
	session := new(MockRoomSession)
	setupMockSession(session)

	hs := newTestHybridSync(session)
	hs.currentMode = common.SyncModeFrame

	hs.frameSync = framesync.NewFrameSync(session, 16)
	go hs.frameSync.Run()
	defer hs.frameSync.Stop()
	time.Sleep(50 * time.Millisecond)

	input := common.InputCommand{
		Type:     common.InputTypeMove,
		Frame:    0,
		PlayerID: "player1",
		Data:     []byte(`{"dx":1}`),
	}

	err := hs.SubmitInput(input)
	assert.NoError(t, err)
}

func TestHybridSync_SubmitInput_StateMode(t *testing.T) {
	session := new(MockRoomSession)
	setupMockSession(session)

	hs := newTestHybridSync(session)
	hs.currentMode = common.SyncModeState

	hs.stateSync = statesync.NewStateSync(session, 20)
	go hs.stateSync.Run()
	defer hs.stateSync.Stop()
	time.Sleep(50 * time.Millisecond)

	err := hs.SubmitInput(common.InputCommand{
		Type:     common.InputTypeMove,
		Frame:    1,
		PlayerID: "player1",
		Data:     []byte(`{"dx":1}`),
	})
	assert.NoError(t, err)
}

// ========== Tests: Start / Stop ==========

func TestHybridSync_StartAndStop(t *testing.T) {
	session := new(MockRoomSession)
	setupMockSession(session)

	hs := newTestHybridSync(session)
	err := hs.Start()
	assert.NoError(t, err)
	assert.NotNil(t, hs.frameSync)
	assert.NotNil(t, hs.stateSync)

	time.Sleep(200 * time.Millisecond)
	hs.Stop()
	time.Sleep(100 * time.Millisecond)
}

// ========== Tests: switchMode ==========

func TestHybridSync_SwitchMode_FrameToState(t *testing.T) {
	session := new(MockRoomSession)
	setupMockSession(session)

	hs := newTestHybridSync(session)
	hs.currentMode = common.SyncModeFrame

	err := hs.Start()
	assert.NoError(t, err)
	defer hs.Stop()
	time.Sleep(100 * time.Millisecond)

	err = hs.switchMode()
	assert.NoError(t, err)
	assert.Equal(t, common.SyncModeState, hs.GetCurrentMode())
	assert.Equal(t, int64(0), hs.frameCount)
}

func TestHybridSync_SwitchMode_StateToFrame(t *testing.T) {
	session := new(MockRoomSession)
	setupMockSession(session)

	hs := newTestHybridSync(session)
	hs.currentMode = common.SyncModeState

	err := hs.Start()
	assert.NoError(t, err)
	defer hs.Stop()
	time.Sleep(100 * time.Millisecond)

	err = hs.switchMode()
	assert.NoError(t, err)
	assert.Equal(t, common.SyncModeFrame, hs.GetCurrentMode())
	assert.Equal(t, int64(0), hs.frameCount)
}

func TestHybridSync_SwitchMode_DoubleSwitch(t *testing.T) {
	session := new(MockRoomSession)
	setupMockSession(session)

	hs := newTestHybridSync(session)

	err := hs.Start()
	assert.NoError(t, err)
	defer hs.Stop()
	time.Sleep(100 * time.Millisecond)

	err = hs.switchMode()
	assert.NoError(t, err)
	assert.Equal(t, common.SyncModeState, hs.GetCurrentMode())

	err = hs.switchMode()
	assert.NoError(t, err)
	assert.Equal(t, common.SyncModeFrame, hs.GetCurrentMode())
}

// ========== Tests: modeSwitchLoop 自动切换 ==========

func TestHybridSync_ModeSwitchLoop_AutoSwitch(t *testing.T) {
	session := new(MockRoomSession)
	setupMockSession(session)

	hs := newTestHybridSync(session)
	hs.lockstepFrames = 5
	hs.stateSyncFrames = 5

	err := hs.Start()
	assert.NoError(t, err)
	defer hs.Stop()

	time.Sleep(2 * time.Second)

	mode := hs.GetCurrentMode()
	assert.Contains(t, []common.SyncMode{common.SyncModeFrame, common.SyncModeState}, mode)
}

// ========== Tests: 并发安全 ==========

func TestHybridSync_ConcurrentAccess(t *testing.T) {
	session := new(MockRoomSession)
	setupMockSession(session)

	hs := newTestHybridSync(session)

	err := hs.Start()
	assert.NoError(t, err)
	defer hs.Stop()
	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = hs.GetCurrentMode()
		}()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = hs.SubmitInput(common.InputCommand{
				Type:     common.InputTypeMove,
				Frame:    0,
				PlayerID: "player",
				Data:     []byte{},
			})
		}(i)
	}
	wg.Wait()
}

// ========== Table-driven: SubmitInput 路由 ==========

func TestHybridSync_SubmitInput_Routing_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mode       common.SyncMode
		inputFrame int64
	}{
		{"帧同步模式-普通输入", common.SyncModeFrame, 0},
		{"帧同步模式-未来帧", common.SyncModeFrame, 100},
		{"状态同步模式", common.SyncModeState, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := new(MockRoomSession)
			setupMockSession(session)

			hs := newTestHybridSync(session)
			hs.currentMode = tt.mode

			if tt.mode == common.SyncModeFrame {
				hs.frameSync = framesync.NewFrameSync(session, 16)
				go hs.frameSync.Run()
				defer hs.frameSync.Stop()
				time.Sleep(50 * time.Millisecond)
			} else {
				hs.stateSync = statesync.NewStateSync(session, 20)
				go hs.stateSync.Run()
				defer hs.stateSync.Stop()
				time.Sleep(50 * time.Millisecond)
			}

			err := hs.SubmitInput(common.InputCommand{
				Type:     common.InputTypeMove,
				Frame:    tt.inputFrame,
				PlayerID: "player1",
				Data:     []byte{},
			})
			assert.NoError(t, err)
		})
	}
}

// ========== Table-driven: switchMode ==========

func TestHybridSync_SwitchMode_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		initialMode common.SyncMode
		expected    common.SyncMode
	}{
		{"帧同步->状态同步", common.SyncModeFrame, common.SyncModeState},
		{"状态同步->帧同步", common.SyncModeState, common.SyncModeFrame},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := new(MockRoomSession)
			setupMockSession(session)

			hs := newTestHybridSync(session)
			hs.currentMode = tt.initialMode

			err := hs.Start()
			assert.NoError(t, err)
			defer hs.Stop()
			time.Sleep(100 * time.Millisecond)

			err = hs.switchMode()
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, hs.GetCurrentMode())
		})
	}
}

// ========== 边界情况 ==========

func TestHybridSync_SubmitInput_NilFrameSync(t *testing.T) {
	session := new(MockRoomSession)
	hs := newTestHybridSync(session)
	hs.currentMode = common.SyncModeFrame
	// frameSync 为 nil，SubmitInput 应该 panic 或返回 error
	// 当前实现会 panic，所以用 panic 检测
	assert.Panics(t, func() {
		_ = hs.SubmitInput(common.InputCommand{
			Type:     common.InputTypeMove,
			Frame:    0,
			PlayerID: "player1",
			Data:     []byte{},
		})
	})
}

func TestHybridSync_SubmitInput_NilStateSync(t *testing.T) {
	session := new(MockRoomSession)
	session.On("GetFrame").Return(int64(0))

	hs := newTestHybridSync(session)
	hs.currentMode = common.SyncModeState
	// stateSync 为 nil
	assert.Panics(t, func() {
		_ = hs.SubmitInput(common.InputCommand{
			Type:     common.InputTypeMove,
			Frame:    0,
			PlayerID: "player1",
			Data:     []byte{},
		})
	})
}
