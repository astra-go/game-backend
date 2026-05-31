package common

import (
	"time"
)

// ========== 通用常量 ==========

// GameMode 游戏模式
type GameMode string

const (
	GameMode1v1       GameMode = "1v1"
	GameMode5v5       GameMode = "5v5"
	GameModeCasual    GameMode = "casual"
	GameModeCustom    GameMode = "custom"
	GameModeFrameSync GameMode = "frame_sync"
	GameModeStateSync GameMode = "state_sync"
)

// RoomStatus 房间状态
type RoomStatus string

const (
	RoomStatusWaiting  RoomStatus = "waiting"
	RoomStatusPlaying RoomStatus = "playing"
	RoomStatusEnded   RoomStatus = "ended"
)

// SyncMode 同步模式
type SyncMode string

const (
	SyncModeFrame SyncMode = "frame"  // 帧同步
	SyncModeState SyncMode = "state"  // 状态同步
)

// InputType 输入类型
type InputType int32

const (
	InputTypeMove  InputType = 1
	InputTypeSkill InputType = 2
	InputTypeAttack InputType = 3
	InputTypeDefend InputType = 4
	InputTypeItem  InputType = 5
)

// ========== 核心数据实体 ==========

// Player 玩家数据
type Player struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	Username     string    `json:"username" gorm:"uniqueIndex;size:32"`
	PasswordHash string    `json:"-" gorm:"size:64"`
	Nickname     string    `json:"nickname" gorm:"size:64"`
	Avatar       string    `json:"avatar" gorm:"size:256"`
	Level        int32     `json:"level" gorm:"default:1"`
	Exp          int64     `json:"exp"`
	Gold         int64     `json:"gold"`
	Diamond      int64     `json:"diamond"`
	MMR          int32     `json:"mmr"`
	ELO          int32     `json:"elo"`
	WinCount     int32     `json:"win_count"`
	LoseCount    int32     `json:"lose_count"`
	Online       bool      `json:"online" gorm:"-"`
	LastLoginAt  time.Time `json:"last_login_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Room 游戏房间
type Room struct {
	ID          string     `json:"id" gorm:"primaryKey"`
	Name        string     `json:"name"`
	OwnerID     string     `json:"owner_id"`
	Status      RoomStatus `json:"status"`
	MaxPlayers  int32      `json:"max_players"`
	CurrentTick int64      `json:"current_tick"`
	MapID       int32      `json:"map_id"`
	Mode        GameMode   `json:"mode"`
	CreatedAt   time.Time  `json:"created_at"`
	EndedAt     *time.Time `json:"ended_at"`
}

// RoomMember 房间成员
type RoomMember struct {
	RoomID       string     `json:"room_id" gorm:"index"`
	PlayerID     string     `json:"player_id" gorm:"index"`
	TeamID       int32      `json:"team_id"`
	Role         string     `json:"role"`
	HeroID       int32      `json:"hero_id"`
	MMR          int32      `json:"mmr"`
	Online       bool       `json:"online"`
	LastHeartbeat int64     `json:"last_heartbeat"`
	QuitAt       *time.Time `json:"quit_at"`
}

// GameSession 游戏会话
type GameSession struct {
	ID             string    `json:"id" gorm:"primaryKey"`
	RoomID         string    `json:"room_id" gorm:"index"`
	PlayerID       string    `json:"player_id" gorm:"index"`
	StartFrame     int64     `json:"start_frame"`
	EndFrame       int64     `json:"end_frame"`
	IsActive       bool      `json:"is_active"`
	ReconnectToken string    `json:"reconnect_token" gorm:"uniqueIndex;size:64"`
	LastHeartbeat  int64     `json:"last_heartbeat"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ========== 游戏消息结构 ==========

// InputCommand 玩家输入指令
type InputCommand struct {
	Type      InputType `json:"type"`
	Frame     int64     `json:"frame"`
	PlayerID  string    `json:"player_id"`
	Data      []byte    `json:"data"`
	Timestamp int64     `json:"timestamp"`
}

// EntityState 实体状态
type EntityState struct {
	EntityID int64      `json:"entity_id"`
	Position Position   `json:"position"`
	Velocity Velocity   `json:"velocity"`
	Health   Health     `json:"health"`
	LastSync int64      `json:"last_sync"`
	Dirty    bool       `json:"dirty"`
}

// Position 位置
type Position struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	Z float32 `json:"z"`
}

// Velocity 速度
type Velocity struct {
	DX float32 `json:"dx"`
	DY float32 `json:"dy"`
	DZ float32 `json:"dz"`
}

// Health 生命值
type Health struct {
	Current int32 `json:"current"`
	Max     int32 `json:"max"`
}

// EntityDelta 状态增量
type EntityDelta struct {
	EntityID int64      `json:"entity_id"`
	Position *Position  `json:"position,omitempty"`
	Health   *Health    `json:"health,omitempty"`
	Frame    int64      `json:"frame"`
}

// DeltaMessage 增量广播消息
type DeltaMessage struct {
	Frame int64         `json:"frame"`
	Delta []EntityDelta `json:"delta"`
}

// ========== 匹配相关 ==========

// MatchTicket 匹配票据
type MatchTicket struct {
	PlayerID  string    `json:"player_id"`
	Mode      GameMode  `json:"mode"`
	MMR       int32     `json:"mmr"`
	ELO       int32     `json:"elo"`
	Latency   int32     `json:"latency"`
	Timestamp int64     `json:"timestamp"`
}

// MatchResult 匹配结果
type MatchResult struct {
	RoomID    string   `json:"room_id"`
	Players   []string `json:"players"`
	TeamA     []string `json:"team_a,omitempty"`
	TeamB     []string `json:"team_b,omitempty"`
	WaitTime  int64    `json:"wait_time"`
	AvgMMR    int32    `json:"avg_mmr"`
}

// ========== 匹配历史 ==========

// MatchHistory 匹配历史记录
type MatchHistory struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	PlayerID  string    `json:"player_id" gorm:"index"`
	RoomID    string    `json:"room_id" gorm:"index"`
	Mode      GameMode `json:"mode"`
	MMRBefore int32     `json:"mmr_before"`
	MMRAfter  int32     `json:"mmr_after"`
	IsWin    bool      `json:"is_win"`
	IsDraw   bool      `json:"is_draw"`
	WaitTime  int64     `json:"wait_time"`
	Duration  int64     `json:"duration"`
	CreatedAt time.Time `json:"created_at"`
}

// ========== 好友系统 ==========

// Friend 好友关系（双向存储）
type Friend struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	PlayerID  string    `json:"player_id" gorm:"index;size:64"`
	FriendID  string    `json:"friend_id" gorm:"index;size:64"`
	CreatedAt time.Time `json:"created_at"`
}

// FriendRequest 好友请求
type FriendRequest struct {
	ID        string    `json:"id" gorm:"primaryKey;size:64"`
	PlayerID  string    `json:"player_id" gorm:"index;size:64"`
	TargetID  string    `json:"target_id" gorm:"index;size:64"`
	Status    string    `json:"status" gorm:"type:enum('pending','accepted','rejected');default:'pending'"`
	Message   string    `json:"message" gorm:"size:256"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FriendInfo 好友信息（API响应）
type FriendInfo struct {
	PlayerID    string    `json:"player_id"`
	Username    string    `json:"username"`
	Nickname    string    `json:"nickname"`
	Avatar      string    `json:"avatar"`
	Level       int32     `json:"level"`
	Online      bool      `json:"online"`
	LastLoginAt time.Time `json:"last_login_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// ========== 游戏快照 ==========

// GameSnapshot 游戏状态快照（用于模式切换）
type GameSnapshot struct {
	Frame     int64                      `json:"frame"`
	Inputs    map[int64]map[string]InputCommand `json:"inputs"`
	Timestamp int64                      `json:"timestamp"`
}

// ========== 房间会话接口 ==========

// RoomSessionInterface 房间会话接口（解耦循环依赖）
type RoomSessionInterface interface {
	GetRoomID() string
	GetFrame() int64
	GetPlayers() map[string]*RoomPlayer
	IsRunning() bool
}

// RoomPlayer 房间内的玩家（复用定义）
type RoomPlayer struct {
	PlayerID  string    `json:"player_id"`
	TeamID    int32     `json:"team_id"`
	HeroID    int32     `json:"hero_id"`
	IsOnline  bool      `json:"is_online"`
	LastFrame int64     `json:"last_frame"`
	JoinTime  time.Time `json:"join_time"`
}

// ========== WebSocket消息 ==========

// WSMessage WebSocket消息
type WSMessage struct {
	Type    string      `json:"type"`
	RoomID  string      `json:"room_id,omitempty"`
	Frame   int64       `json:"frame,omitempty"`
	Data    any         `json:"data,omitempty"`
}

// WSMessageType 消息类型
const (
	WSMsgJoin       = "join"
	WSMsgLeave      = "leave"
	WSMsgInput      = "input"
	WSMsgFrame      = "frame"
	WSMsgStateDelta = "state_delta"
	WSMsgHeartbeat  = "heartbeat"
	WSMsgReconnect  = "reconnect"
	WSMsgError      = "error"
)
