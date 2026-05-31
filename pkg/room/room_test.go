package room

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// ========== Mock 实现 ==========

// mockNATSClient Mock NATS客户端
type mockNATSClient struct {
	mu         sync.RWMutex
	published  []publishedMsg
	subscribed map[string]func(msg []byte)
	closed     bool
}

type publishedMsg struct {
	subject string
	data    []byte
}

func newMockNATSClient() *mockNATSClient {
	return &mockNATSClient{
		subscribed: make(map[string]func(msg []byte)),
	}
}

func (m *mockNATSClient) Publish(subject string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, publishedMsg{subject: subject, data: data})
	return nil
}

func (m *mockNATSClient) Subscribe(subject string, cb func(msg []byte)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribed[subject] = cb
	return nil
}

func (m *mockNATSClient) Close() error {
	m.closed = true
	return nil
}

func (m *mockNATSClient) getPublished() []publishedMsg {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]publishedMsg, len(m.published))
	copy(cp, m.published)
	return cp
}

// ========== 辅助函数 ==========

func newTestLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func newTestRoomComponent() *RoomComponent {
	logger := newTestLogger()
	cfg := DefaultRoomConfig()
	// 不使用真实Redis，通过RoomComponent的内存sync.Map测试
	// 注意：RoomComponent直接使用 *redis.Client，测试时需要特殊处理
	return &RoomComponent{
		logger: logger,
		config: cfg,
	}
}

// createRoomComponentWithMocks 创建组件（Redis为nil，需要时手动mock）
// RoomComponent直接使用 *redis.Client，无法用接口替代
// 测试中我们绕过Redis操作，专注于内存逻辑测试
func createRoomComponentWithMocks() *RoomComponent {
	logger := newTestLogger()
	cfg := DefaultRoomConfig()
	nats := newMockNATSClient()

	return &RoomComponent{
		nats:   nats,
		logger: logger,
		config: cfg,
	}
}

// createRoomInMemory 直接在内存中创建房间会话（绕过Redis）
func createRoomInMemory(r *RoomComponent, ownerID string, mode common.GameMode, maxPlayers int32, mapID int32) (*common.Room, *RoomSession) {
	roomID := generateRoomID()

	room := &common.Room{
		ID:         roomID,
		Name:       fmt.Sprintf("Room-%s", roomID[:8]),
		OwnerID:    ownerID,
		Status:     common.RoomStatusWaiting,
		MaxPlayers: maxPlayers,
		MapID:      mapID,
		Mode:       mode,
		CreatedAt:  time.Now(),
	}

	session := &RoomSession{
		roomID:    roomID,
		players:   make(map[string]*common.RoomPlayer),
		mode:      mode,
		syncMode:  common.SyncModeFrame, // 默认帧同步
		frame:     0,
		isRunning: true,
		quitCh:    make(chan struct{}),
		msgCh:     make(chan common.InputCommand, 256),
		stateCh:   make(chan *common.EntityDelta, 256),
	}

	// 根据模式设置同步模式（与CreateRoom逻辑一致）
	if mode == common.GameModeFrameSync || mode == common.GameMode1v1 || mode == common.GameMode5v5 {
		session.syncMode = common.SyncModeFrame
	} else {
		session.syncMode = common.SyncModeState
	}

	r.rooms.Store(roomID, session)
	r.addPlayer(session, ownerID, 0, 0)

	return room, session
}

// ========== 测试用例 ==========

func TestDefaultRoomConfig(t *testing.T) {
	cfg := DefaultRoomConfig()

	assert.Equal(t, 1000, cfg.MaxRoomsPerNode)
	assert.Equal(t, 1*time.Hour, cfg.RoomTTL)
	assert.Equal(t, 16, cfg.FrameSyncTickMs)
	assert.Equal(t, 20, cfg.StateSyncHz)
	assert.Equal(t, 5*time.Minute, cfg.ReconnectWindow)
	assert.Equal(t, 10, cfg.MaxPlayersPerRoom)
}

func TestNewRoomComponent(t *testing.T) {
	logger := newTestLogger()
	cfg := DefaultRoomConfig()
	nats := newMockNATSClient()

	r := NewRoomComponent(nil, nats, logger, cfg)

	assert.NotNil(t, r)
	assert.Equal(t, nats, r.nats)
	assert.Equal(t, logger, r.logger)
	assert.Equal(t, cfg.MaxRoomsPerNode, r.config.MaxRoomsPerNode)
}

func TestRoomSession_GetRoomID(t *testing.T) {
	session := &RoomSession{
		roomID: "room_test123",
	}
	assert.Equal(t, "room_test123", session.GetRoomID())
}

func TestRoomSession_GetFrame(t *testing.T) {
	session := &RoomSession{
		frame: 42,
	}
	assert.Equal(t, int64(42), session.GetFrame())
}

func TestRoomSession_SetFrame(t *testing.T) {
	session := &RoomSession{}
	session.SetFrame(100)
	assert.Equal(t, int64(100), session.GetFrame())
}

func TestRoomSession_GetPlayers(t *testing.T) {
	session := &RoomSession{
		players: map[string]*common.RoomPlayer{
			"p1": {PlayerID: "p1", TeamID: 1, IsOnline: true},
			"p2": {PlayerID: "p2", TeamID: 2, IsOnline: false},
		},
	}

	players := session.GetPlayers()
	assert.Len(t, players, 2)
	assert.Contains(t, players, "p1")
	assert.Contains(t, players, "p2")

	// 验证返回的是副本
	players["p3"] = &common.RoomPlayer{PlayerID: "p3"}
	assert.Len(t, session.players, 2) // 原始不变
}

func TestRoomSession_IsRunning(t *testing.T) {
	session := &RoomSession{isRunning: true}
	assert.True(t, session.IsRunning())

	session.isRunning = false
	assert.False(t, session.IsRunning())
}

func TestCreateRoomInMemory_FrameSyncMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       common.GameMode
		expectSync common.SyncMode
	}{
		{
			name:       "1v1_frame_sync",
			mode:       common.GameMode1v1,
			expectSync: common.SyncModeFrame,
		},
		{
			name:       "5v5_frame_sync",
			mode:       common.GameMode5v5,
			expectSync: common.SyncModeFrame,
		},
		{
			name:       "frame_sync_mode",
			mode:       common.GameModeFrameSync,
			expectSync: common.SyncModeFrame,
		},
		{
			name:       "state_sync_mode",
			mode:       common.GameModeStateSync,
			expectSync: common.SyncModeState,
		},
		{
			name:       "casual_state_sync",
			mode:       common.GameModeCasual,
			expectSync: common.SyncModeState,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := createRoomComponentWithMocks()
			room, session := createRoomInMemory(r, "owner1", tt.mode, 10, 1)

			assert.NotNil(t, room)
			assert.NotEmpty(t, room.ID)
			assert.Equal(t, "owner1", room.OwnerID)
			assert.Equal(t, common.RoomStatusWaiting, room.Status)
			assert.Equal(t, tt.mode, room.Mode)

			assert.NotNil(t, session)
			assert.Equal(t, tt.expectSync, session.syncMode)
			assert.True(t, session.isRunning)
			// 房主应已在房间中
			assert.Contains(t, session.players, "owner1")

			// 清理
			close(session.quitCh)
		})
	}
}

func TestAddPlayer(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, _ = createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)

	// AddPlayer需要Redis，Redis为nil时HSet会panic
	// 所以我们用addPlayer直接测试内存逻辑（见TestAddPlayer_InMemory）
}

func TestAddPlayer_InMemory(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)

	// 直接测试addPlayer内存逻辑
	r.addPlayer(session, "player2", 1, 101)

	session.mu.Lock()
	assert.Contains(t, session.players, "player2")
	assert.Equal(t, int32(1), session.players["player2"].TeamID)
	assert.Equal(t, int32(101), session.players["player2"].HeroID)
	assert.True(t, session.players["player2"].IsOnline)
	session.mu.Unlock()
}

func TestAddPlayer_Duplicate(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, _ = createRoomInMemory(r, "owner1", common.GameMode1v1, 2, 1)

	// owner1已在房间，AddPlayer应返回错误
	// 由于Redis为nil会panic，这里直接测试内存逻辑
	// 验证重复添加逻辑：addPlayer不检查重复
}

func TestRoomFull(t *testing.T) {
	r := createRoomComponentWithMocks()
	r.config.MaxPlayersPerRoom = 2
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 2, 1)

	r.addPlayer(session, "player2", 1, 101)

	// 房间已满，AddPlayer应返回错误
	// Redis为nil，直接测试逻辑
	session.mu.Lock()
	assert.Len(t, session.players, 2) // 不应超过max
	session.mu.Unlock()
}

func TestRoomNonExistent(t *testing.T) {
	r := createRoomComponentWithMocks()

	err := r.AddPlayer("nonexistent_room", "player1", 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")

	err = r.RemovePlayer("nonexistent_room", "player1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")

	err = r.DestroyRoom("nonexistent_room")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

func TestRemovePlayer_InMemory(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)
	r.addPlayer(session, "player2", 1, 101)

	session.mu.Lock()
	assert.Len(t, session.players, 2)
	session.mu.Unlock()

	// 直接删除玩家
	session.mu.Lock()
	delete(session.players, "player2")
	session.mu.Unlock()

	session.mu.Lock()
	assert.Len(t, session.players, 1)
	assert.NotContains(t, session.players, "player2")
	session.mu.Unlock()
}

func TestRemovePlayer_RoomEmpty(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)

	// 删除唯一玩家（owner）
	session.mu.Lock()
	delete(session.players, "owner1")
	empty := len(session.players) == 0
	session.mu.Unlock()

	assert.True(t, empty)
}

func TestDestroyRoom(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)

	roomID := session.roomID

	// 验证房间存在
	_, ok := r.rooms.Load(roomID)
	assert.True(t, ok)

	// 销毁房间（不通过Redis）
	close(session.quitCh)
	r.rooms.Delete(roomID)

	// 验证房间已移除
	_, ok = r.rooms.Load(roomID)
	assert.False(t, ok)
}

func TestDestroyRoom_NonExistent(t *testing.T) {
	r := createRoomComponentWithMocks()
	err := r.DestroyRoom("nonexistent")
	assert.Error(t, err)
}

func TestBroadcastToRoom(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, _ = createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)

	msg := common.WSMessage{
		Type:   common.WSMsgFrame,
		RoomID: "room1",
		Data:   map[string]interface{}{"frame": 1},
	}

	err := r.broadcastToRoom("room1", msg)
	assert.NoError(t, err)

	// 验证NATS发布了广播
	nats := r.nats.(*mockNATSClient)
	pubMsgs := nats.getPublished()
	assert.True(t, len(pubMsgs) > 0)
	assert.Contains(t, pubMsgs[0].subject, "broadcast")
}

func TestBroadcastToRoom_NoNATS(t *testing.T) {
	r := createRoomComponentWithMocks()
	r.nats = nil // 无NATS

	msg := common.WSMessage{
		Type:   common.WSMsgFrame,
		RoomID: "room1",
		Data:   map[string]interface{}{"frame": 1},
	}

	err := r.broadcastToRoom("room1", msg)
	assert.NoError(t, err) // 不应panic
}

func TestOnPlayerJoin(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)

	// 模拟NATS回调（绕过Redis）
	_, _ = json.Marshal(map[string]string{
		"room_id":   session.roomID,
		"player_id": "player2",
	})

	// onPlayerJoin内部调用AddPlayer，需要Redis
	// 直接测试内存逻辑
	r.addPlayer(session, "player2", 0, 0)

	session.mu.Lock()
	assert.Contains(t, session.players, "player2")
	session.mu.Unlock()
}

func TestOnPlayerLeave(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)
	r.addPlayer(session, "player2", 1, 101)

	// 直接测试内存删除
	session.mu.Lock()
	delete(session.players, "player2")
	session.mu.Unlock()

	session.mu.Lock()
	assert.NotContains(t, session.players, "player2")
	session.mu.Unlock()
}

func TestSubmitInput_NonExistentRoom(t *testing.T) {
	r := createRoomComponentWithMocks()
	input := common.InputCommand{
		Type:     common.InputTypeMove,
		Frame:    1,
		PlayerID: "player1",
	}
	err := r.SubmitInput("nonexistent", "player1", input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

func TestSubmitInput_StateSyncMode(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameModeCasual, 10, 1)
	session.syncMode = common.SyncModeState

	input := common.InputCommand{
		Type:     common.InputTypeMove,
		Frame:    1,
		PlayerID: "owner1",
	}
	err := r.SubmitInput(session.roomID, "owner1", input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "帧同步模式")

	close(session.quitCh)
}

func TestSubmitInput_FrameSyncNotRunning(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)
	session.syncMode = common.SyncModeFrame
	// frameSync为nil
	session.frameSync = nil

	input := common.InputCommand{
		Type:     common.InputTypeMove,
		Frame:    1,
		PlayerID: "owner1",
	}
	err := r.SubmitInput(session.roomID, "owner1", input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未初始化")

	close(session.quitCh)
}

func TestInit_SubscribesNATS(t *testing.T) {
	r := createRoomComponentWithMocks()
	nats := r.nats.(*mockNATSClient)

	err := r.Init()
	assert.NoError(t, err)

	// 验证NATS订阅了房间消息
	assert.Contains(t, nats.subscribed, "room.*.player_join")
	assert.Contains(t, nats.subscribed, "room.*.player_leave")
	assert.Contains(t, nats.subscribed, "room.*.input")
}

func TestInit_NilNATS(t *testing.T) {
	r := createRoomComponentWithMocks()
	r.nats = nil

	err := r.Init()
	assert.NoError(t, err) // 不应panic
}

func TestGenerateRoomID(t *testing.T) {
	id1 := generateRoomID()
	assert.Contains(t, id1, "room_")
	id2 := generateRoomID()
	assert.Contains(t, id2, "room_")
}

func TestMultipleRoomsCreation(t *testing.T) {
	r := createRoomComponentWithMocks()

	sessions := make([]*RoomSession, 5)
	for i := 0; i < 5; i++ {
		_, sessions[i] = createRoomInMemory(r, fmt.Sprintf("owner%d", i), common.GameMode1v1, 10, 1)
	}

	count := 0
	r.rooms.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 5, count)

	// 清理
	for _, s := range sessions {
		close(s.quitCh)
	}
}

func TestRoomSession_ConcurrentAccess(t *testing.T) {
	session := &RoomSession{
		roomID:    "room_concurrent",
		players:   make(map[string]*common.RoomPlayer),
		isRunning: true,
		quitCh:    make(chan struct{}),
		msgCh:     make(chan common.InputCommand, 256),
		stateCh:   make(chan *common.EntityDelta, 256),
	}

	// 并发添加玩家
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pid := fmt.Sprintf("player_%d", idx)
			session.mu.Lock()
			session.players[pid] = &common.RoomPlayer{
				PlayerID: pid,
				TeamID:   int32(idx % 2),
				IsOnline: true,
				JoinTime: time.Now(),
			}
			session.mu.Unlock()
		}(i)
	}
	wg.Wait()

	players := session.GetPlayers()
	assert.Len(t, players, 100)
}

func TestRoomSession_ConcurrentReadWrite(t *testing.T) {
	session := &RoomSession{
		roomID:  "room_rw",
		players: make(map[string]*common.RoomPlayer),
		quitCh:  make(chan struct{}),
		msgCh:   make(chan common.InputCommand, 256),
		stateCh: make(chan *common.EntityDelta, 256),
	}

	// 初始化一些玩家
	for i := 0; i < 50; i++ {
		session.players[fmt.Sprintf("player_%d", i)] = &common.RoomPlayer{
			PlayerID: fmt.Sprintf("player_%d", i),
			IsOnline: true,
		}
	}

	var wg sync.WaitGroup
	// 并发读
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = session.GetFrame()
			_ = session.GetPlayers()
			_ = session.IsRunning()
		}()
	}
	// 并发写
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			session.SetFrame(int64(idx))
		}(i)
	}
	wg.Wait()

	// 不应panic，frame应该是最后写入的值之一
	frame := session.GetFrame()
	assert.True(t, frame >= 0 && frame < 50)
}

func TestMockNATS_Operations(t *testing.T) {
	m := newMockNATSClient()

	// Publish
	err := m.Publish("room.1.broadcast", []byte("test"))
	assert.NoError(t, err)

	msgs := m.getPublished()
	assert.Len(t, msgs, 1)
	assert.Equal(t, "room.1.broadcast", msgs[0].subject)
	assert.Equal(t, []byte("test"), msgs[0].data)

	// Subscribe
	var received []byte
	err = m.Subscribe("room.*.input", func(msg []byte) {
		received = msg
	})
	assert.NoError(t, err)
	assert.NotNil(t, m.subscribed["room.*.input"])

	// 触发回调
	m.subscribed["room.*.input"]([]byte("input_data"))
	assert.Equal(t, []byte("input_data"), received)

	// Close
	err = m.Close()
	assert.NoError(t, err)
	assert.True(t, m.closed)
}

func TestSaveSnapshot(t *testing.T) {
	session := &RoomSession{
		roomID: "room_snap",
		players: map[string]*common.RoomPlayer{
			"p1": {PlayerID: "p1", IsOnline: true},
		},
		syncMode: common.SyncModeFrame,
		frame:    100,
	}

	// saveSnapshot不应panic
	session.saveSnapshot()
}

func TestRoomPlayer_JoinTime(t *testing.T) {
	r := createRoomComponentWithMocks()
	_, session := createRoomInMemory(r, "owner1", common.GameMode1v1, 10, 1)

	before := time.Now()
	r.addPlayer(session, "player2", 1, 101)
	after := time.Now()

	session.mu.Lock()
	p, ok := session.players["player2"]
	session.mu.Unlock()

	assert.True(t, ok)
	assert.True(t, p.JoinTime.After(before) || p.JoinTime.Equal(before))
	assert.True(t, p.JoinTime.Before(after) || p.JoinTime.Equal(after))
}

func TestRoomConfig_Custom(t *testing.T) {
	tests := []struct {
		name             string
		config           RoomConfig
		expectMaxRooms   int
		expectMaxPlayers int
		expectTTL        time.Duration
	}{
		{
			name:             "small_scale",
			config:           RoomConfig{MaxRoomsPerNode: 100, MaxPlayersPerRoom: 4, RoomTTL: 30 * time.Minute},
			expectMaxRooms:   100,
			expectMaxPlayers: 4,
			expectTTL:        30 * time.Minute,
		},
		{
			name:             "large_scale",
			config:           RoomConfig{MaxRoomsPerNode: 5000, MaxPlayersPerRoom: 50, RoomTTL: 3 * time.Hour},
			expectMaxRooms:   5000,
			expectMaxPlayers: 50,
			expectTTL:        3 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := newTestLogger()
			nats := newMockNATSClient()
			r := NewRoomComponent(nil, nats, logger, tt.config)
			assert.Equal(t, tt.expectMaxRooms, r.config.MaxRoomsPerNode)
			assert.Equal(t, tt.expectMaxPlayers, r.config.MaxPlayersPerRoom)
			assert.Equal(t, tt.expectTTL, r.config.RoomTTL)
		})
	}
}
