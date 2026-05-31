package room

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/framesync"
	"github.com/astra-go/game-backend/pkg/statesync"
)

var ctx = context.Background()

// RoomComponent 房间管理组件
type RoomComponent struct {
	redis       *redis.Client
	nats        NATSClient
	logger      *zap.Logger
	rooms       sync.Map // roomID -> *RoomSession
	config      RoomConfig
}

// RoomConfig 房间配置
type RoomConfig struct {
	MaxRoomsPerNode   int
	RoomTTL           time.Duration
	FrameSyncTickMs   int
	StateSyncHz       int
	ReconnectWindow   time.Duration
	MaxPlayersPerRoom int
}

// DefaultRoomConfig 默认配置
func DefaultRoomConfig() RoomConfig {
	return RoomConfig{
		MaxRoomsPerNode:   1000,
		RoomTTL:            1 * time.Hour,
		FrameSyncTickMs:    16, // 60Hz
		StateSyncHz:        20, // 20Hz for state sync
		ReconnectWindow:    5 * time.Minute,
		MaxPlayersPerRoom:  10,
	}
}

// RoomSession 房间会话（实现 RoomSessionInterface）
type RoomSession struct {
	mu          sync.Mutex
	roomID      string
	players     map[string]*common.RoomPlayer // playerID -> Player
	mode        common.GameMode
	syncMode    common.SyncMode
	frame       int64
	isRunning   bool
	quitCh      chan struct{}
	msgCh       chan common.InputCommand
	stateCh     chan *common.EntityDelta
	frameSync   *framesync.FrameSync
	stateSync   *statesync.StateSync
}

// NATSClient NATS接口
type NATSClient interface {
	Publish(subject string, data []byte) error
	Subscribe(subject string, cb func(msg []byte)) error
	Close() error
}

// NewRoomComponent 创建房间组件
func NewRoomComponent(redis *redis.Client, nats NATSClient, logger *zap.Logger, cfg RoomConfig) *RoomComponent {
	return &RoomComponent{
		redis:  redis,
		nats:   nats,
		logger: logger,
		config: cfg,
	}
}

// Init 初始化
func (r *RoomComponent) Init() error {
	r.logger.Info("RoomComponent 初始化")
	
	// 订阅NATS消息
	if r.nats != nil {
		r.nats.Subscribe("room.*.player_join", r.onPlayerJoin)
		r.nats.Subscribe("room.*.player_leave", r.onPlayerLeave)
		r.nats.Subscribe("room.*.input", r.onPlayerInput)
	}
	
	// 启动房间清理协程
	go r.cleanupLoop()
	
	return nil
}

// CreateRoom 创建房间
func (r *RoomComponent) CreateRoom(ownerID string, mode common.GameMode, maxPlayers int32, mapID int32) (*common.Room, error) {
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
	
	// 保存到Redis
	roomKey := fmt.Sprintf("room:%s", roomID)
	roomData, _ := json.Marshal(room)
	r.redis.HSet(ctx, roomKey, "info", roomData)
	r.redis.Expire(ctx, roomKey, r.config.RoomTTL)
	
	// 创建房间会话
	session := &RoomSession{
		roomID:   roomID,
		players:  make(map[string]*common.RoomPlayer),
		mode:     mode,
		syncMode: common.SyncModeFrame, // 默认帧同步
		frame:    0,
		isRunning: true,
		quitCh:   make(chan struct{}),
		msgCh:    make(chan common.InputCommand, 256),
		stateCh:   make(chan *common.EntityDelta, 256),
	}
	
	r.rooms.Store(roomID, session)
	
	// 添加房主
	r.addPlayer(session, ownerID, 0, 0)
	
	// 根据模式启动同步循环
	if mode == common.GameModeFrameSync || mode == common.GameMode1v1 || mode == common.GameMode5v5 {
		session.syncMode = common.SyncModeFrame
		session.frameSync = framesync.NewFrameSync(session, r.config.FrameSyncTickMs)
		go session.frameSync.Run()
	} else {
		session.syncMode = common.SyncModeState
		session.stateSync = statesync.NewStateSync(session, r.config.StateSyncHz)
		go session.stateSync.Run()
	}
	
	r.logger.Info("房间创建成功",
		zap.String("room_id", roomID),
		zap.String("owner_id", ownerID),
		zap.String("mode", string(mode)),
	)
	
	return room, nil
}

// AddPlayer 添加玩家到房间
func (r *RoomComponent) AddPlayer(roomID, playerID string, teamID, heroID int32) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在: %s", roomID)
	}
	
	session := val.(*RoomSession)
	
	session.mu.Lock()
	defer session.mu.Unlock()
	
	if len(session.players) >= int(r.config.MaxPlayersPerRoom) {
		return fmt.Errorf("房间已满")
	}
	
	if _, exists := session.players[playerID]; exists {
		return fmt.Errorf("玩家已在房间中")
	}
	
	r.addPlayer(session, playerID, teamID, heroID)
	
	// 保存到Redis
	member := common.RoomMember{
		RoomID:   roomID,
		PlayerID: playerID,
		TeamID:   teamID,
		HeroID:   heroID,
		Role:     "member",
		Online:   true,
	}
	memberData, _ := json.Marshal(member)
	r.redis.HSet(ctx, fmt.Sprintf("room:%s", roomID), fmt.Sprintf("member:%s", playerID), memberData)
	
	// 通知房间内所有玩家
	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   common.WSMsgJoin,
		RoomID: roomID,
		Data:   map[string]interface{}{"player_id": playerID},
	})
	
	r.logger.Info("玩家加入房间",
		zap.String("room_id", roomID),
		zap.String("player_id", playerID),
	)
	
	return nil
}

// RemovePlayer 移除玩家
func (r *RoomComponent) RemovePlayer(roomID, playerID string) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}
	
	session := val.(*RoomSession)
	
	session.mu.Lock()
	defer session.mu.Unlock()
	
	delete(session.players, playerID)
	
	// 从Redis移除
	r.redis.HDel(ctx, fmt.Sprintf("room:%s", roomID), fmt.Sprintf("member:%s", playerID))
	
	// 通知房间
	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   common.WSMsgLeave,
		RoomID: roomID,
		Data:   map[string]interface{}{"player_id": playerID},
	})
	
	// 如果房间空了，销毁
	if len(session.players) == 0 {
		r.DestroyRoom(roomID)
	}
	
	r.logger.Info("玩家离开房间",
		zap.String("room_id", roomID),
		zap.String("player_id", playerID),
	)
	
	return nil
}

// SubmitInput 提交玩家输入（帧同步）
func (r *RoomComponent) SubmitInput(roomID, playerID string, input common.InputCommand) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}
	
	session := val.(*RoomSession)
	
	if session.syncMode != common.SyncModeFrame {
		return fmt.Errorf("房间不是帧同步模式")
	}
	
	// 将输入发送到帧同步器
	if session.frameSync != nil {
		return session.frameSync.SubmitInput(input)
	}
	
	return fmt.Errorf("帧同步器未初始化")
}

// broadcastToRoom 广播消息到房间内所有玩家
func (r *RoomComponent) broadcastToRoom(roomID string, msg common.WSMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	
	// 通过NATS发布
	if r.nats != nil {
		r.nats.Publish(fmt.Sprintf("room.%s.broadcast", roomID), data)
	}
	
	return nil
}

// DestroyRoom 销毁房间
func (r *RoomComponent) DestroyRoom(roomID string) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}
	
	session := val.(*RoomSession)
	
	// 停止同步循环
	if session.quitCh != nil {
		close(session.quitCh)
	}
	
	// 停止帧同步器
	if session.frameSync != nil {
		session.frameSync.Stop()
	}
	
	// 停止状态同步器
	if session.stateSync != nil {
		session.stateSync.Stop()
	}
	
	// 保存快照
	session.saveSnapshot()
	
	// 从内存移除
	r.rooms.Delete(roomID)
	
	// 从Redis移除
	r.redis.Del(ctx, fmt.Sprintf("room:%s", roomID))
	
	r.logger.Info("房间销毁", zap.String("room_id", roomID))
	
	return nil
}

// ========== RoomSessionInterface 实现 ==========

// GetRoomID 获取房间ID
func (s *RoomSession) GetRoomID() string {
	return s.roomID
}

// GetFrame 获取当前帧号
func (s *RoomSession) GetFrame() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame
}

// GetPlayers 获取所有玩家
func (s *RoomSession) GetPlayers() map[string]*common.RoomPlayer {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// 返回副本
	players := make(map[string]*common.RoomPlayer)
	for k, v := range s.players {
		playerCopy := *v
		players[k] = &playerCopy
	}
	return players
}

// IsRunning 检查是否运行中
func (s *RoomSession) IsRunning() bool {
	return s.isRunning
}

// SetFrame 设置帧号
func (s *RoomSession) SetFrame(frame int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frame = frame
}

// ========== 内部方法 ==========

func (r *RoomComponent) addPlayer(session *RoomSession, playerID string, teamID, heroID int32) {
	session.players[playerID] = &common.RoomPlayer{
		PlayerID: playerID,
		TeamID:   teamID,
		HeroID:   heroID,
		IsOnline: true,
		JoinTime: time.Now(),
	}
}

func (r *RoomComponent) onPlayerJoin(data []byte) {
	var payload map[string]string
	json.Unmarshal(data, &payload)
	roomID := payload["room_id"]
	playerID := payload["player_id"]
	
	r.AddPlayer(roomID, playerID, 0, 0)
}

func (r *RoomComponent) onPlayerLeave(data []byte) {
	var payload map[string]string
	json.Unmarshal(data, &payload)
	roomID := payload["room_id"]
	playerID := payload["player_id"]
	
	r.RemovePlayer(roomID, playerID)
}

func (r *RoomComponent) onPlayerInput(data []byte) {
	var msg common.WSMessage
	json.Unmarshal(data, &msg)
	
	if msg.Type != common.WSMsgInput {
		return
	}
	
	roomID := msg.RoomID
	// 解析input
	var input common.InputCommand
	inputData, ok := msg.Data.(map[string]interface{})
	if !ok {
		return
	}
	
	input.PlayerID = inputData["player_id"].(string)
	input.Type = common.InputType(int32(inputData["type"].(float64)))
	input.Frame = int64(inputData["frame"].(float64))
	
	r.SubmitInput(roomID, input.PlayerID, input)
}

func (r *RoomComponent) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		now := time.Now()
		_ = now // 实际使用中应检查最后活动时间
		
		r.rooms.Range(func(key, value interface{}) bool {
			session := value.(*RoomSession)
			
			// 检查房间是否过期（所有玩家离线超过5分钟）
			allOffline := true
			for _, p := range session.players {
				if p.IsOnline {
					allOffline = false
					break
				}
			}
			
			if allOffline {
				// 检查最后一个玩家离线时间
				// 简化：直接销毁
				r.DestroyRoom(session.roomID)
			}
			
			return true
		})
	}
}

func generateRoomID() string {
	return fmt.Sprintf("room_%d", time.Now().UnixNano())
}

// ========== RoomSession快照 ==========

func (s *RoomSession) saveSnapshot() {
	snapshot := map[string]interface{}{
		"room_id":   s.roomID,
		"frame":     s.frame,
		"players":   s.players,
		"sync_mode": s.syncMode,
		"timestamp": time.Now().Unix(),
	}
	
	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	
	// 保存到Redis
	_ = data // 实际使用中应保存到Redis
}
