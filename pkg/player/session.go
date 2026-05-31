package player

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// SessionManager 会话管理器（多端登录控制）
type SessionManager struct {
	redis  *redis.Client
	logger *zap.Logger
}

// NewSessionManager 创建会话管理器
func NewSessionManager(redis *redis.Client, logger *zap.Logger) *SessionManager {
	return &SessionManager{
		redis:  redis,
		logger: logger,
	}
}

// DeviceType 设备类型
type DeviceType string

const (
	DevicePC      DeviceType = "pc"
	DeviceMobile  DeviceType = "mobile"
	DeviceWeb     DeviceType = "web"
	DeviceConsole DeviceType = "console"
)

// SessionInfo 会话信息
type SessionInfo struct {
	PlayerID   string     `json:"player_id"`
	Token      string     `json:"token"`
	DeviceType DeviceType `json:"device_type"`
	DeviceID   string     `json:"device_id"`
	IP         string     `json:"ip"`
	LoginAt    time.Time  `json:"login_at"`
	LastActive time.Time  `json:"last_active"`
}

// CreateSession 创建会话
// 策略：同一设备类型只允许一个会话，踢掉旧会话
func (sm *SessionManager) CreateSession(ctx context.Context, playerID string, token string, deviceType DeviceType, deviceID string, ip string) error {
	sessionKey := fmt.Sprintf("session:%s:%s", playerID, deviceType)

	session := &SessionInfo{
		PlayerID:   playerID,
		Token:      token,
		DeviceType: deviceType,
		DeviceID:   deviceID,
		IP:         ip,
		LoginAt:    time.Now(),
		LastActive: time.Now(),
	}

	// 存储会话信息（24小时过期）
	err := sm.redis.HSet(ctx, sessionKey,
		"token", session.Token,
		"device_id", session.DeviceID,
		"ip", session.IP,
		"login_at", session.LoginAt.Unix(),
		"last_active", session.LastActive.Unix(),
	).Err()
	if err != nil {
		return fmt.Errorf("创建会话失败: %w", err)
	}

	// 设置过期时间
	sm.redis.Expire(ctx, sessionKey, 24*time.Hour)

	// 添加到玩家的活跃会话列表
	activeSessionsKey := fmt.Sprintf("active_sessions:%s", playerID)
	sm.redis.SAdd(ctx, activeSessionsKey, string(deviceType))
	sm.redis.Expire(ctx, activeSessionsKey, 24*time.Hour)

	sm.logger.Info("创建会话",
		zap.String("player_id", playerID),
		zap.String("device_type", string(deviceType)),
		zap.String("device_id", deviceID),
		zap.String("ip", ip),
	)

	return nil
}

// ValidateSession 验证会话是否有效
func (sm *SessionManager) ValidateSession(ctx context.Context, playerID string, token string, deviceType DeviceType) (bool, error) {
	sessionKey := fmt.Sprintf("session:%s:%s", playerID, deviceType)

	storedToken, err := sm.redis.HGet(ctx, sessionKey, "token").Result()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}

	// 验证token是否匹配
	if storedToken != token {
		sm.logger.Warn("会话token不匹配",
			zap.String("player_id", playerID),
			zap.String("device_type", string(deviceType)),
		)
		return false, nil
	}

	// 更新最后活跃时间
	sm.redis.HSet(ctx, sessionKey, "last_active", time.Now().Unix())

	return true, nil
}

// KickSession 踢掉指定设备的会话
func (sm *SessionManager) KickSession(ctx context.Context, playerID string, deviceType DeviceType) error {
	sessionKey := fmt.Sprintf("session:%s:%s", playerID, deviceType)

	// 删除会话
	err := sm.redis.Del(ctx, sessionKey).Err()
	if err != nil {
		return err
	}

	// 从活跃会话列表移除
	activeSessionsKey := fmt.Sprintf("active_sessions:%s", playerID)
	sm.redis.SRem(ctx, activeSessionsKey, string(deviceType))

	sm.logger.Info("踢掉会话",
		zap.String("player_id", playerID),
		zap.String("device_type", string(deviceType)),
	)

	return nil
}

// KickAllSessions 踢掉玩家的所有会话
func (sm *SessionManager) KickAllSessions(ctx context.Context, playerID string) error {
	activeSessionsKey := fmt.Sprintf("active_sessions:%s", playerID)

	// 获取所有活跃设备类型
	deviceTypes, err := sm.redis.SMembers(ctx, activeSessionsKey).Result()
	if err != nil {
		return err
	}

	// 删除所有会话
	for _, dt := range deviceTypes {
		sessionKey := fmt.Sprintf("session:%s:%s", playerID, dt)
		sm.redis.Del(ctx, sessionKey)
	}

	// 清空活跃会话列表
	sm.redis.Del(ctx, activeSessionsKey)

	sm.logger.Info("踢掉所有会话",
		zap.String("player_id", playerID),
		zap.Int("count", len(deviceTypes)),
	)

	return nil
}

// GetActiveSessions 获取玩家的所有活跃会话
func (sm *SessionManager) GetActiveSessions(ctx context.Context, playerID string) ([]SessionInfo, error) {
	activeSessionsKey := fmt.Sprintf("active_sessions:%s", playerID)

	deviceTypes, err := sm.redis.SMembers(ctx, activeSessionsKey).Result()
	if err != nil {
		return nil, err
	}

	sessions := make([]SessionInfo, 0, len(deviceTypes))
	for _, dt := range deviceTypes {
		sessionKey := fmt.Sprintf("session:%s:%s", playerID, dt)

		data, err := sm.redis.HGetAll(ctx, sessionKey).Result()
		if err != nil || len(data) == 0 {
			continue
		}

		loginAt, _ := time.Parse(time.RFC3339, data["login_at"])
		lastActive, _ := time.Parse(time.RFC3339, data["last_active"])

		sessions = append(sessions, SessionInfo{
			PlayerID:   playerID,
			Token:      data["token"],
			DeviceType: DeviceType(dt),
			DeviceID:   data["device_id"],
			IP:         data["ip"],
			LoginAt:    loginAt,
			LastActive: lastActive,
		})
	}

	return sessions, nil
}

// CleanupExpiredSessions 清理过期会话（定时任务）
func (sm *SessionManager) CleanupExpiredSessions(ctx context.Context) error {
	// Redis的TTL会自动清理，这里只是记录日志
	sm.logger.Info("会话清理任务执行")
	return nil
}
