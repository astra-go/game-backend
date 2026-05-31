package player

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var ctx = context.Background()

// PlayerComponent 玩家服务组件
type PlayerComponent struct {
	db             *gorm.DB
	redis          *redis.Client
	logger         *zap.Logger
	sessionManager *SessionManager
}

// NewPlayerComponent 创建玩家组件
func NewPlayerComponent(db *gorm.DB, redis *redis.Client, logger *zap.Logger) *PlayerComponent {
	return &PlayerComponent{
		db:             db,
		redis:          redis,
		logger:         logger,
		sessionManager: NewSessionManager(redis, logger),
	}
}

// Init 初始化
func (p *PlayerComponent) Init() error {
	p.logger.Info("PlayerComponent 初始化")

	// 自动迁移数据库
	err := p.db.AutoMigrate(&common.Player{})
	if err != nil {
		return err
	}

	return nil
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=32"`
	Password string `json:"password" binding:"required,min=6,max=32"`
	Nickname string `json:"nickname" binding:"max=64"`
}

// LoginRequest 登录请求
type LoginRequest struct {
	Username   string     `json:"username" binding:"required"`
	Password   string     `json:"password" binding:"required"`
	DeviceType DeviceType `json:"device_type" binding:"required"`
	DeviceID   string     `json:"device_id" binding:"required"`
	IP         string     `json:"ip"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	Player       *common.Player `json:"player"`
	Token        string         `json:"token"`
	ExpiresAt    int64          `json:"expires_at"`
	KickedDevice string         `json:"kicked_device,omitempty"`
}

// Register 注册新玩家
func (p *PlayerComponent) Register(req *RegisterRequest) (*common.Player, error) {
	// 检查用户名是否存在
	var existing common.Player
	err := p.db.Where("username = ?", req.Username).First(&existing).Error
	if err == nil {
		return nil, errors.New("用户名已存在")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// 密码强度验证
	if len(req.Password) < 6 {
		return nil, errors.New("密码长度至少6位")
	}

	// 加密密码（使用bcrypt，cost=12）
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		return nil, fmt.Errorf("密码加密失败: %w", err)
	}

	// 设置默认昵称
	nickname := req.Nickname
	if nickname == "" {
		nickname = req.Username
	}

	player := &common.Player{
		ID:           generatePlayerID(),
		Username:     req.Username,
		PasswordHash: string(hash),
		Nickname:     nickname,
		Level:        1,
		Exp:          0,
		Gold:         1000,
		Diamond:      100,
		MMR:          1000,
		ELO:          1000,
		WinCount:     0,
		LoseCount:    0,
		LastLoginAt:  time.Now(),
	}

	err = p.db.Create(player).Error
	if err != nil {
		return nil, fmt.Errorf("创建玩家失败: %w", err)
	}

	p.logger.Info("玩家注册成功",
		zap.String("player_id", player.ID),
		zap.String("username", player.Username),
	)

	return player, nil
}

// Login 登录
func (p *PlayerComponent) Login(req *LoginRequest) (*LoginResponse, error) {
	var player common.Player
	err := p.db.Where("username = ?", req.Username).First(&player).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("用户不存在")
		}
		return nil, err
	}

	// 验证密码
	err = bcrypt.CompareHashAndPassword([]byte(player.PasswordHash), []byte(req.Password))
	if err != nil {
		p.logger.Warn("密码验证失败",
			zap.String("username", req.Username),
			zap.String("ip", req.IP),
		)
		return nil, errors.New("密码错误")
	}

	// 生成JWT token
	token, err := common.GenerateToken(player.ID, player.Username)
	if err != nil {
		p.logger.Error("生成JWT token失败", zap.Error(err))
		return nil, fmt.Errorf("生成token失败: %w", err)
	}

	// 检查是否有同设备类型的旧会话，如果有则踢掉
	var kickedDevice string
	sessions, _ := p.sessionManager.GetActiveSessions(ctx, player.ID)
	for _, session := range sessions {
		if session.DeviceType == req.DeviceType {
			kickedDevice = session.DeviceID
			p.sessionManager.KickSession(ctx, player.ID, req.DeviceType)
			p.logger.Info("踢掉旧会话",
				zap.String("player_id", player.ID),
				zap.String("device_type", string(req.DeviceType)),
				zap.String("old_device_id", session.DeviceID),
			)
			break
		}
	}

	// 创建新会话
	err = p.sessionManager.CreateSession(ctx, player.ID, token, req.DeviceType, req.DeviceID, req.IP)
	if err != nil {
		p.logger.Error("创建会话失败", zap.Error(err))
		return nil, fmt.Errorf("创建会话失败: %w", err)
	}

	// 更新最后登录时间
	player.LastLoginAt = time.Now()
	p.db.Save(&player)

	// 设置在线状态（1小时过期）
	p.redis.Set(ctx, fmt.Sprintf("online:%s", player.ID), "1", 1*time.Hour)

	p.logger.Info("玩家登录成功",
		zap.String("player_id", player.ID),
		zap.String("username", player.Username),
		zap.String("device_type", string(req.DeviceType)),
		zap.String("device_id", req.DeviceID),
		zap.String("ip", req.IP),
	)

	return &LoginResponse{
		Player:       &player,
		Token:        token,
		ExpiresAt:    time.Now().Add(24 * time.Hour).Unix(),
		KickedDevice: kickedDevice,
	}, nil
}

// Logout 登出
func (p *PlayerComponent) Logout(playerID string, deviceType DeviceType) error {
	// 删除会话
	err := p.sessionManager.KickSession(ctx, playerID, deviceType)
	if err != nil {
		return err
	}

	// 检查是否还有其他活跃会话
	sessions, _ := p.sessionManager.GetActiveSessions(ctx, playerID)
	if len(sessions) == 0 {
		// 没有其他会话，设置离线
		p.redis.Del(ctx, fmt.Sprintf("online:%s", playerID))
	}

	p.logger.Info("玩家登出",
		zap.String("player_id", playerID),
		zap.String("device_type", string(deviceType)),
	)

	return nil
}

// ValidateSession 验证会话
func (p *PlayerComponent) ValidateSession(playerID string, token string, deviceType DeviceType) (bool, error) {
	return p.sessionManager.ValidateSession(ctx, playerID, token, deviceType)
}

// GetActiveSessions 获取活跃会话列表
func (p *PlayerComponent) GetActiveSessions(playerID string) ([]SessionInfo, error) {
	return p.sessionManager.GetActiveSessions(ctx, playerID)
}

// KickDevice 踢掉指定设备
func (p *PlayerComponent) KickDevice(playerID string, deviceType DeviceType) error {
	return p.sessionManager.KickSession(ctx, playerID, deviceType)
}

// GetByID 根据ID获取玩家
func (p *PlayerComponent) GetByID(playerID string) (*common.Player, error) {
	var player common.Player
	err := p.db.Where("id = ?", playerID).First(&player).Error
	if err != nil {
		return nil, err
	}
	return &player, nil
}

// GetByUsername 根据用户名获取玩家
func (p *PlayerComponent) GetByUsername(username string) (*common.Player, error) {
	var player common.Player
	err := p.db.Where("username = ?", username).First(&player).Error
	if err != nil {
		return nil, err
	}
	return &player, nil
}

// UpdateMMR 更新MMR/ELO（使用标准 ELO/Glicko-2 公式）
func (p *PlayerComponent) UpdateMMR(playerID string, won bool, isDraw bool, opponentsMMR []int32, gamesPlayed int) (int32, error) {
	var player common.Player
	err := p.db.Where("id = ?", playerID).First(&player).Error
	if err != nil {
		return 0, err
	}

	// 使用 Glicko-2 风格公式计算新 MMR
	newMMR, change := common.CalculateMMR(player.MMR, opponentsMMR, won, isDraw, gamesPlayed, 0)

	// 获取连胜/连败信息
	var recentGames []common.MatchHistory
	p.db.Where("player_id = ?", playerID).Order("created_at desc").Limit(10).Find(&recentGames)

	winStreak, loseStreak := p.calculateStreaks(recentGames)
	bonus := common.StreakBonus(change, winStreak, loseStreak)

	finalChange := change + bonus
	newMMR = player.MMR + finalChange

	// 更新玩家数据
	if won && !isDraw {
		player.WinCount++
	} else if !won && !isDraw {
		player.LoseCount++
	}

	player.MMR = newMMR

	// 更新 ELO（使用标准 ELO 公式）
	if len(opponentsMMR) > 0 {
		_, newELO, _ := common.CalculateELO(player.ELO, opponentsMMR[0])
		player.ELO = newELO
	}

	// 确保不低于 0
	if player.MMR < 0 {
		player.MMR = 0
	}
	if player.ELO < 0 {
		player.ELO = 0
	}

	// 保存到数据库
	err = p.db.Save(&player).Error
	if err != nil {
		return 0, err
	}

	// 更新Redis缓存
	err = p.redis.Set(ctx, fmt.Sprintf("mmr:%s", playerID), player.MMR, 1*time.Hour).Err()
	if err != nil {
		p.logger.Warn("更新Redis MMR缓存失败", zap.Error(err))
	}

	p.logger.Info("MMR更新",
		zap.String("player_id", playerID),
		zap.Bool("won", won),
		zap.Bool("is_draw", isDraw),
		zap.Int32("old_mmr", player.MMR-finalChange),
		zap.Int32("new_mmr", player.MMR),
		zap.Int32("change", finalChange),
		zap.Int("win_streak", winStreak),
		zap.Int("lose_streak", loseStreak),
	)

	return finalChange, nil
}

// calculateStreaks 计算连胜/连败场次
func (p *PlayerComponent) calculateStreaks(recentGames []common.MatchHistory) (winStreak, loseStreak int) {
	for _, g := range recentGames {
		if g.IsWin {
			winStreak++
			loseStreak = 0
		} else {
			loseStreak++
			winStreak = 0
		}
	}
	return
}

// UpdatePlayer 更新玩家信息
func (p *PlayerComponent) UpdatePlayer(player *common.Player) error {
	return p.db.Save(player).Error
}

// ChangePassword 修改密码
func (p *PlayerComponent) ChangePassword(playerID, oldPassword, newPassword string) error {
	var player common.Player
	err := p.db.Where("id = ?", playerID).First(&player).Error
	if err != nil {
		return errors.New("玩家不存在")
	}

	// 验证旧密码
	err = bcrypt.CompareHashAndPassword([]byte(player.PasswordHash), []byte(oldPassword))
	if err != nil {
		return errors.New("旧密码错误")
	}

	// 密码强度验证
	if len(newPassword) < 6 {
		return errors.New("新密码长度至少6位")
	}

	// 加密新密码
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return fmt.Errorf("密码加密失败: %w", err)
	}

	player.PasswordHash = string(hash)
	err = p.db.Save(&player).Error
	if err != nil {
		return err
	}

	// 修改密码后踢掉所有会话，要求重新登录
	p.sessionManager.KickAllSessions(ctx, playerID)

	p.logger.Info("修改密码成功",
		zap.String("player_id", playerID),
	)

	return nil
}

// IsOnline 检查玩家是否在线
func (p *PlayerComponent) IsOnline(playerID string) bool {
	val, err := p.redis.Get(ctx, fmt.Sprintf("online:%s", playerID)).Result()
	if err != nil {
		return false
	}
	return val == "1"
}

// SetOffline 设置玩家离线
func (p *PlayerComponent) SetOffline(playerID string) {
	p.redis.Del(ctx, fmt.Sprintf("online:%s", playerID))
}

// GetLeaderboard 获取排行榜
func (p *PlayerComponent) GetLeaderboard(limit int) ([]common.Player, error) {
	var players []common.Player
	err := p.db.Order("mmr DESC").Limit(limit).Find(&players).Error
	if err != nil {
		return nil, err
	}
	return players, nil
}

// ========== 辅助函数 ==========

func generatePlayerID() string {
	return fmt.Sprintf("player_%d_%d", time.Now().UnixNano(), rand.Int63())
}
