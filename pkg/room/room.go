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

// SpectatorInfo 观战者信息
type SpectatorInfo struct {
	PlayerID    string    `json:"player_id"`
	JoinAt      time.Time `json:"join_at"`
	IsMuted     bool      `json:"is_muted"` // 是否被静音（不听语音）
}

// RoomSession 房间会话（实现 RoomSessionInterface）
type RoomSession struct {
	mu           sync.Mutex
	roomID       string
	ownerID      string
	players      map[string]*common.RoomPlayer // playerID -> Player
	readyStatus  map[string]bool               // playerID -> ready
	spectators   map[string]*SpectatorInfo    // playerID -> SpectatorInfo 观战者列表
	mode         common.GameMode
	syncMode     common.SyncMode
	status       common.RoomStatus
	frame        int64
	isRunning    bool
	quitCh       chan struct{}
	msgCh        chan common.InputCommand
	stateCh      chan *common.EntityDelta
	frameSync    *framesync.FrameSync
	stateSync    *statesync.StateSync
	createdAt    time.Time
	startedAt    *time.Time
	maxSpectators int // 最大观战人数，0表示不限制
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

	if r.nats != nil {
		r.nats.Subscribe("room.*.player_join", r.onPlayerJoin)
		r.nats.Subscribe("room.*.player_leave", r.onPlayerLeave)
		r.nats.Subscribe("room.*.input", r.onPlayerInput)
	}

	go r.cleanupLoop()

	return nil
}

// CreateRoom 创建房间
func (r *RoomComponent) CreateRoom(ownerID string, mode common.GameMode, maxPlayers int32, mapID int32) (*common.Room, error) {
	if mapID <= 0 {
		return nil, fmt.Errorf("无效的地图ID: %d", mapID)
	}

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

	roomKey := fmt.Sprintf("room:%s", roomID)
	roomData, _ := json.Marshal(room)
	r.redis.HSet(ctx, roomKey, "info", roomData)
	r.redis.Expire(ctx, roomKey, r.config.RoomTTL)

	session := &RoomSession{
		roomID:       roomID,
		ownerID:      ownerID,
		players:      make(map[string]*common.RoomPlayer),
		readyStatus: make(map[string]bool),
		spectators:   make(map[string]*SpectatorInfo), // 初始化观战者映射
		mode:         mode,
		syncMode:     common.SyncModeFrame,
		status:       common.RoomStatusWaiting,
		frame:        0,
		isRunning:    true,
		quitCh:       make(chan struct{}),
		msgCh:        make(chan common.InputCommand, 256),
		stateCh:      make(chan *common.EntityDelta, 256),
		createdAt:    time.Now(),
		maxSpectators: 50, // 默认最大50个观战者
	}

	r.rooms.Store(roomID, session)

	r.addPlayer(session, ownerID, 0, 0)
	session.readyStatus[ownerID] = false

	if mode == common.GameModeFrameSync || mode == common.GameMode1v1 || mode == common.GameMode5v5 {
		session.syncMode = common.SyncModeFrame
		session.frameSync = framesync.NewFrameSync(session, r.config.FrameSyncTickMs)
	} else {
		session.syncMode = common.SyncModeState
		session.stateSync = statesync.NewStateSync(session, r.config.StateSyncHz)
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

	if session.status != common.RoomStatusWaiting {
		return fmt.Errorf("房间已开始游戏，无法加入")
	}

	if heroID <= 0 {
		return fmt.Errorf("无效的英雄ID: %d", heroID)
	}

	if len(session.players) >= int(r.config.MaxPlayersPerRoom) {
		return fmt.Errorf("房间已满")
	}

	if _, exists := session.players[playerID]; exists {
		return fmt.Errorf("玩家已在房间中")
	}

	r.addPlayer(session, playerID, teamID, heroID)
	session.readyStatus[playerID] = false

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

	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   common.WSMsgJoin,
		RoomID: roomID,
		Data:   map[string]any{"player_id": playerID, "team_id": teamID},
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
	delete(session.readyStatus, playerID)

	r.redis.HDel(ctx, fmt.Sprintf("room:%s", roomID), fmt.Sprintf("member:%s", playerID))

	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   common.WSMsgLeave,
		RoomID: roomID,
		Data:   map[string]any{"player_id": playerID},
	})

	if playerID == session.ownerID && len(session.players) > 0 {
		for newOwnerID := range session.players {
			session.ownerID = newOwnerID
			r.broadcastToRoom(roomID, common.WSMessage{
				Type:   "owner_changed",
				RoomID: roomID,
				Data:   map[string]any{"new_owner": newOwnerID},
			})
			break
		}
	}

	if len(session.players) == 0 {
		r.DestroyRoom(roomID)
	}

	r.logger.Info("玩家离开房间",
		zap.String("room_id", roomID),
		zap.String("player_id", playerID),
	)

	return nil
}

// KickPlayer 踢出玩家（仅房主可用）
func (r *RoomComponent) KickPlayer(roomID, operatorID, targetID string) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}

	session := val.(*RoomSession)

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.ownerID != operatorID {
		return fmt.Errorf("只有房主可以踢人")
	}

	if targetID == operatorID {
		return fmt.Errorf("不能踢出自己")
	}

	if _, exists := session.players[targetID]; !exists {
		return fmt.Errorf("目标玩家不在房间中")
	}

	delete(session.players, targetID)
	delete(session.readyStatus, targetID)

	r.redis.HDel(ctx, fmt.Sprintf("room:%s", roomID), fmt.Sprintf("member:%s", targetID))

	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   "kicked",
		RoomID: roomID,
		Data:   map[string]any{"player_id": targetID, "by": operatorID},
	})

	r.logger.Info("玩家被踢出",
		zap.String("room_id", roomID),
		zap.String("target", targetID),
		zap.String("operator", operatorID),
	)

	return nil
}

// SetPlayerReady 设置玩家准备状态
func (r *RoomComponent) SetPlayerReady(roomID, playerID string, ready bool) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}

	session := val.(*RoomSession)

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.status != common.RoomStatusWaiting {
		return fmt.Errorf("房间已开始游戏")
	}

	if _, exists := session.players[playerID]; !exists {
		return fmt.Errorf("玩家不在房间中")
	}

	session.readyStatus[playerID] = ready

	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   "player_ready",
		RoomID: roomID,
		Data:   map[string]any{"player_id": playerID, "ready": ready},
	})

	allReady := true
	for pid := range session.players {
		if !session.readyStatus[pid] {
			allReady = false
			break
		}
	}

	if allReady && len(session.players) >= 2 {
		r.broadcastToRoom(roomID, common.WSMessage{
			Type:   "all_ready",
			RoomID: roomID,
			Data:   map[string]any{"can_start": true},
		})
	}

	return nil
}

// StartGame 开始游戏
func (r *RoomComponent) StartGame(roomID, operatorID string) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}

	session := val.(*RoomSession)

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.ownerID != operatorID {
		return fmt.Errorf("只有房主可以开始游戏")
	}

	if session.status != common.RoomStatusWaiting {
		return fmt.Errorf("房间状态不正确")
	}

	for pid := range session.players {
		if !session.readyStatus[pid] {
			return fmt.Errorf("还有玩家未准备")
		}
	}

	// 验证队伍平衡（5v5模式）
	if session.mode == common.GameMode5v5 {
		teamCounts := make(map[int32]int)
		for _, player := range session.players {
			teamCounts[player.TeamID]++
		}
		if len(teamCounts) != 2 || teamCounts[0] != 5 || teamCounts[1] != 5 {
			return fmt.Errorf("队伍人数不平衡，5v5模式需要每队5人")
		}
	}

	session.status = common.RoomStatusPlaying
	now := time.Now()
	session.startedAt = &now

	roomKey := fmt.Sprintf("room:%s", roomID)
	r.redis.HSet(ctx, roomKey, "status", string(common.RoomStatusPlaying))
	r.redis.HSet(ctx, roomKey, "started_at", now.Unix())

	if session.frameSync != nil {
		go session.frameSync.Run()
	}
	if session.stateSync != nil {
		go session.stateSync.Run()
	}

	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   "game_start",
		RoomID: roomID,
		Data:   map[string]any{"started_at": now.Unix()},
	})

	r.logger.Info("游戏开始",
		zap.String("room_id", roomID),
		zap.Int("player_count", len(session.players)),
	)

	return nil
}

// EndGame 结束游戏
func (r *RoomComponent) EndGame(roomID string, winnerTeamID int32) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}

	session := val.(*RoomSession)

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.status != common.RoomStatusPlaying {
		return fmt.Errorf("游戏未在进行中")
	}

	session.status = common.RoomStatusEnded
	now := time.Now()

	roomKey := fmt.Sprintf("room:%s", roomID)
	r.redis.HSet(ctx, roomKey, "status", string(common.RoomStatusEnded))
	r.redis.HSet(ctx, roomKey, "ended_at", now.Unix())

	if session.frameSync != nil {
		session.frameSync.Stop()
	}
	if session.stateSync != nil {
		session.stateSync.Stop()
	}

	var duration int64
	if session.startedAt != nil {
		duration = now.Unix() - session.startedAt.Unix()
	}

	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   "game_end",
		RoomID: roomID,
		Data: map[string]any{
			"winner_team": winnerTeamID,
			"duration":    duration,
			"ended_at":    now.Unix(),
		},
	})

	r.logger.Info("游戏结束",
		zap.String("room_id", roomID),
		zap.Int32("winner_team", winnerTeamID),
		zap.Int64("duration", duration),
	)

	return nil
}

// GetRoom 获取房间信息
func (r *RoomComponent) GetRoom(roomID string) (*common.Room, error) {
	roomKey := fmt.Sprintf("room:%s", roomID)
	infoData, err := r.redis.HGet(ctx, roomKey, "info").Result()
	if err != nil {
		return nil, fmt.Errorf("房间不存在")
	}

	var room common.Room
	if err := json.Unmarshal([]byte(infoData), &room); err != nil {
		return nil, fmt.Errorf("解析房间信息失败: %w", err)
	}

	return &room, nil
}

// GetRoomPlayers 获取房间玩家列表
func (r *RoomComponent) GetRoomPlayers(roomID string) ([]common.RoomMember, error) {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return nil, fmt.Errorf("房间不存在")
	}

	session := val.(*RoomSession)

	session.mu.Lock()
	defer session.mu.Unlock()

	members := make([]common.RoomMember, 0, len(session.players))
	for playerID, player := range session.players {
		members = append(members, common.RoomMember{
			RoomID:   roomID,
			PlayerID: playerID,
			TeamID:   player.TeamID,
			HeroID:   player.HeroID,
			Online:   player.IsOnline,
		})
	}

	return members, nil
}

// ListRooms 获取房间列表
func (r *RoomComponent) ListRooms(mode common.GameMode, limit int) ([]*common.Room, error) {
	pattern := "room:*"
	keys, err := r.redis.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	rooms := make([]*common.Room, 0)
	for _, key := range keys {
		infoData, err := r.redis.HGet(ctx, key, "info").Result()
		if err != nil {
			continue
		}

		var room common.Room
		if err := json.Unmarshal([]byte(infoData), &room); err != nil {
			continue
		}

		if mode != "" && room.Mode != mode {
			continue
		}

		if room.Status == common.RoomStatusWaiting {
			rooms = append(rooms, &room)
		}

		if len(rooms) >= limit {
			break
		}
	}

	return rooms, nil
}

// GenerateReconnectToken 生成重连令牌
func (r *RoomComponent) GenerateReconnectToken(roomID, playerID string) (string, error) {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return "", fmt.Errorf("房间不存在")
	}

	session := val.(*RoomSession)

	session.mu.Lock()
	defer session.mu.Unlock()

	if _, exists := session.players[playerID]; !exists {
		return "", fmt.Errorf("玩家不在房间中")
	}

	token := fmt.Sprintf("%s:%s:%d", roomID, playerID, time.Now().UnixNano())

	tokenKey := fmt.Sprintf("reconnect:%s", token)
	tokenData := map[string]any{
		"room_id":   roomID,
		"player_id": playerID,
		"frame":     session.frame,
	}
	data, _ := json.Marshal(tokenData)
	r.redis.Set(ctx, tokenKey, data, r.config.ReconnectWindow)

	return token, nil
}

// Reconnect 玩家重连
func (r *RoomComponent) Reconnect(token string) (string, string, int64, error) {
	tokenKey := fmt.Sprintf("reconnect:%s", token)
	data, err := r.redis.Get(ctx, tokenKey).Result()
	if err != nil {
		return "", "", 0, fmt.Errorf("重连令牌无效或已过期")
	}

	var tokenData map[string]any
	if err := json.Unmarshal([]byte(data), &tokenData); err != nil {
		return "", "", 0, fmt.Errorf("解析令牌失败")
	}

	roomID := tokenData["room_id"].(string)
	playerID := tokenData["player_id"].(string)
	frame := int64(tokenData["frame"].(float64))

	val, ok := r.rooms.Load(roomID)
	if !ok {
		return "", "", 0, fmt.Errorf("房间已不存在")
	}

	session := val.(*RoomSession)

	session.mu.Lock()
	defer session.mu.Unlock()

	if player, exists := session.players[playerID]; exists {
		player.IsOnline = true

		r.broadcastToRoom(roomID, common.WSMessage{
			Type:   common.WSMsgReconnect,
			RoomID: roomID,
			Data:   map[string]any{"player_id": playerID, "frame": session.frame},
		})

		r.logger.Info("玩家重连成功",
			zap.String("room_id", roomID),
			zap.String("player_id", playerID),
			zap.Int64("current_frame", session.frame),
		)

		return roomID, playerID, frame, nil
	}

	return "", "", 0, fmt.Errorf("玩家已不在房间中")
}

// UpdateRoomConfig 更新房间配置（仅等待状态可用）
func (r *RoomComponent) UpdateRoomConfig(roomID, operatorID string, mapID *int32, maxPlayers *int32) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}

	session := val.(*RoomSession)

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.ownerID != operatorID {
		return fmt.Errorf("只有房主可以修改房间配置")
	}

	if session.status != common.RoomStatusWaiting {
		return fmt.Errorf("游戏已开始，无法修改配置")
	}

	roomKey := fmt.Sprintf("room:%s", roomID)

	if mapID != nil {
		r.redis.HSet(ctx, roomKey, "map_id", *mapID)
	}

	if maxPlayers != nil {
		if *maxPlayers < int32(len(session.players)) {
			return fmt.Errorf("最大玩家数不能小于当前玩家数")
		}
		r.redis.HSet(ctx, roomKey, "max_players", *maxPlayers)
	}

	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   "config_updated",
		RoomID: roomID,
		Data: map[string]any{
			"map_id":      mapID,
			"max_players": maxPlayers,
		},
	})

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

	if session.frameSync != nil {
		return session.frameSync.SubmitInput(input)
	}

	return fmt.Errorf("帧同步器未初始化")
}

// broadcastToRoom 广播消息到房间内所有玩家和观战者
func (r *RoomComponent) broadcastToRoom(roomID string, msg common.WSMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	
	// 广播给所有玩家
	if r.nats != nil {
		r.nats.Publish(fmt.Sprintf("room.%s.broadcast", roomID), data)
	}
	
	// 广播给所有观战者（如果消息类型允许观战者接收）
	if r.nats != nil && r.shouldBroadcastToSpectators(msg.Type) {
		val, ok := r.rooms.Load(roomID)
		if ok {
			session := val.(*RoomSession)
			for spectatorID := range session.spectators {
				r.nats.Publish(fmt.Sprintf("room.%s.spectator.%s", roomID, spectatorID), data)
			}
		}
	}
	
	return nil
}

// shouldBroadcastToSpectators 判断消息类型是否应该广播给观战者
func (r *RoomComponent) shouldBroadcastToSpectators(msgType string) bool {
	// 这些消息类型应该广播给观战者
	broadcastTypes := []string{
		common.WSMsgJoin,      // 玩家加入
		common.WSMsgLeave,     // 玩家离开
		"player_ready",        // 玩家准备
		"game_start",          // 游戏开始
		"game_end",            // 游戏结束
		"spectator_join",      // 观战者加入
		"spectator_leave",     // 观战者离开
	}
	
	for _, t := range broadcastTypes {
		if msgType == t {
			return true
		}
	}
	
	// 游戏中的帧同步消息也给观战者（延迟3秒）
	if msgType == common.WSMsgFrame {
		return true
	}
	
	return false
}

// DestroyRoom 销毁房间
func (r *RoomComponent) DestroyRoom(roomID string) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}

	session := val.(*RoomSession)

	if session.quitCh != nil {
		close(session.quitCh)
	}

	if session.frameSync != nil {
		session.frameSync.Stop()
	}

	if session.stateSync != nil {
		session.stateSync.Stop()
	}

	r.rooms.Delete(roomID)

	r.redis.Del(ctx, fmt.Sprintf("room:%s", roomID))

	r.logger.Info("房间销毁", zap.String("room_id", roomID))

	return nil
}

// ========== RoomSessionInterface 实现 ==========

func (s *RoomSession) GetRoomID() string {
	return s.roomID
}

func (s *RoomSession) GetFrame() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame
}

func (s *RoomSession) GetPlayers() map[string]*common.RoomPlayer {
	s.mu.Lock()
	defer s.mu.Unlock()

	players := make(map[string]*common.RoomPlayer)
	for k, v := range s.players {
		playerCopy := *v
		players[k] = &playerCopy
	}
	return players
}

func (s *RoomSession) IsRunning() bool {
	return s.isRunning
}

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
	var input common.InputCommand
	inputData, ok := msg.Data.(map[string]any)
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

		r.rooms.Range(func(key, value any) bool {
			session := value.(*RoomSession)

			allOffline := true
			var lastOnlineTime time.Time

			for _, p := range session.players {
				if p.IsOnline {
					allOffline = false
					break
				}
				if p.JoinTime.After(lastOnlineTime) {
					lastOnlineTime = p.JoinTime
				}
			}

			if allOffline && now.Sub(lastOnlineTime) > r.config.ReconnectWindow {
				r.DestroyRoom(session.roomID)
			}

			if session.status == common.RoomStatusEnded && session.startedAt != nil {
				if now.Sub(*session.startedAt) > 30*time.Minute {
					r.DestroyRoom(session.roomID)
				}
			}

			return true
		})
	}
}

func generateRoomID() string {
	return fmt.Sprintf("room_%d", time.Now().UnixNano())
}

// ========== 观战模式功能 ==========

// AddSpectator 添加观战者到房间
func (r *RoomComponent) AddSpectator(roomID, playerID string) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在: %s", roomID)
	}
	
	session := val.(*RoomSession)
	
	session.mu.Lock()
	defer session.mu.Unlock()
	
	// 检查是否已经是玩家
	if _, exists := session.players[playerID]; exists {
		return fmt.Errorf("玩家已在房间中，不能观战")
	}
	
	// 检查是否已经是观战者
	if _, exists := session.spectators[playerID]; exists {
		return fmt.Errorf("玩家已经在观战中")
	}
	
	// 检查观战者数量限制
	if session.maxSpectators > 0 && len(session.spectators) >= session.maxSpectators {
		return fmt.Errorf("观战者数量已达上限: %d", session.maxSpectators)
	}
	
	// 添加观战者
	session.spectators[playerID] = &SpectatorInfo{
		PlayerID: playerID,
		JoinAt:   time.Now(),
		IsMuted:  false,
	}
	
	// 保存到Redis
	spectatorKey := fmt.Sprintf("room:%s:spectators", roomID)
	r.redis.HSet(ctx, spectatorKey, playerID, time.Now().Unix())
	r.redis.Expire(ctx, spectatorKey, r.config.RoomTTL)
	
	// 广播观战者加入消息
	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   "spectator_join",
		RoomID: roomID,
		Data:   map[string]any{"player_id": playerID, "join_at": time.Now().Unix()},
	})
	
	// 向观战者发送当前房间状态
	r.sendRoomStateToSpectator(roomID, playerID)
	
	r.logger.Info("观战者加入",
		zap.String("room_id", roomID),
		zap.String("player_id", playerID),
		zap.Int("total_spectators", len(session.spectators)),
	)
	
	return nil
}

// RemoveSpectator 移除观战者
func (r *RoomComponent) RemoveSpectator(roomID, playerID string) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}
	
	session := val.(*RoomSession)
	
	session.mu.Lock()
	defer session.mu.Unlock()
	
	// 检查是否是观战者
	if _, exists := session.spectators[playerID]; !exists {
		return fmt.Errorf("玩家不是观战者")
	}
	
	// 移除观战者
	delete(session.spectators, playerID)
	
	// 从Redis移除
	spectatorKey := fmt.Sprintf("room:%s:spectators", roomID)
	r.redis.HDel(ctx, spectatorKey, playerID)
	
	// 广播观战者离开消息
	r.broadcastToRoom(roomID, common.WSMessage{
		Type:   "spectator_leave",
		RoomID: roomID,
		Data:   map[string]any{"player_id": playerID},
	})
	
	r.logger.Info("观战者离开",
		zap.String("room_id", roomID),
		zap.String("player_id", playerID),
		zap.Int("total_spectators", len(session.spectators)),
	)
	
	return nil
}

// GetSpectators 获取房间内所有观战者
func (r *RoomComponent) GetSpectators(roomID string) ([]*SpectatorInfo, error) {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return nil, fmt.Errorf("房间不存在")
	}
	
	session := val.(*RoomSession)
	
	session.mu.Lock()
	defer session.mu.Unlock()
	
	spectators := make([]*SpectatorInfo, 0, len(session.spectators))
	for _, spec := range session.spectators {
		spectators = append(spectators, spec)
	}
	
	return spectators, nil
}

// SetMaxSpectators 设置房间最大观战者数量
func (r *RoomComponent) SetMaxSpectators(roomID, operatorID string, max int) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}
	
	session := val.(*RoomSession)
	
	session.mu.Lock()
	defer session.mu.Unlock()
	
	// 检查权限（只有房主可以设置）
	if session.ownerID != operatorID {
		return fmt.Errorf("只有房主可以设置观战者数量")
	}
	
	if max < 0 {
		max = 0
	}
	
	session.maxSpectators = max
	
	r.logger.Info("设置房间最大观战者数量",
		zap.String("room_id", roomID),
		zap.String("operator", operatorID),
		zap.Int("max_spectators", max),
	)
	
	return nil
}

// MuteSpectator 静音观战者（不听语音）
func (r *RoomComponent) MuteSpectator(roomID, operatorID, targetID string, mute bool) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}
	
	session := val.(*RoomSession)
	
	session.mu.Lock()
	defer session.mu.Unlock()
	
	// 检查权限（只有房主可以静音）
	if session.ownerID != operatorID {
		return fmt.Errorf("只有房主可以静音观战者")
	}
	
	// 检查目标是否是观战者
	spec, exists := session.spectators[targetID]
	if !exists {
		return fmt.Errorf("目标玩家不是观战者")
	}
	
	spec.IsMuted = mute
	
	action := "unmute"
	if mute {
		action = "mute"
	}
	
	r.logger.Info("观战者静音状态更新",
		zap.String("room_id", roomID),
		zap.String("operator", operatorID),
		zap.String("target", targetID),
		zap.String("action", action),
	)
	
	return nil
}

// sendRoomStateToSpectator 向观战者发送房间状态
func (r *RoomComponent) sendRoomStateToSpectator(roomID, spectatorID string) error {
	// 获取房间信息
	room, err := r.GetRoom(roomID)
	if err != nil {
		return err
	}
	
	// 获取玩家列表
	players, err := r.GetRoomPlayers(roomID)
	if err != nil {
		return err
	}
	
	// 构造状态消息
	stateMsg := common.WSMessage{
		Type:   "room_state",
		RoomID: roomID,
		Data: map[string]any{
			"room":    room,
			"players": players,
			"is_spectator": true,
		},
	}
	
	// 发送给观战者（通过NATS单播）
	if r.nats != nil {
		data, _ := json.Marshal(stateMsg)
		r.nats.Publish(fmt.Sprintf("room.%s.spectator.%s", roomID, spectatorID), data)
	}
	
	return nil
}

// broadcastToSpectators 广播消息给所有观战者
func (r *RoomComponent) broadcastToSpectators(roomID string, msg common.WSMessage) error {
	val, ok := r.rooms.Load(roomID)
	if !ok {
		return fmt.Errorf("房间不存在")
	}
	
	session := val.(*RoomSession)
	
	session.mu.Lock()
	defer session.mu.Unlock()
	
	// 向所有观战者发送消息
	for spectatorID := range session.spectators {
		if r.nats != nil {
			data, _ := json.Marshal(msg)
			r.nats.Publish(fmt.Sprintf("room.%s.spectator.%s", roomID, spectatorID), data)
		}
	}
	
	return nil
}
