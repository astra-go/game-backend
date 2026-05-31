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
	db     *gorm.DB
	redis  *redis.Client
	logger *zap.Logger
}

// NewPlayerComponent 创建玩家组件
func NewPlayerComponent(db *gorm.DB, redis *redis.Client, logger *zap.Logger) *PlayerComponent {
	return &PlayerComponent{
		db:     db,
		redis:  redis,
		logger: logger,
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

// Register 注册新玩家
func (p *PlayerComponent) Register(username, password string) (*common.Player, error) {
	// 检查用户名是否存在
	var existing common.Player
	err := p.db.Where("username = ?", username).First(&existing).Error
	if err == nil {
		return nil, errors.New("用户名已存在")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// 加密密码
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	player := &common.Player{
		ID:           generatePlayerID(),
		Username:     username,
		PasswordHash: string(hash),
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
		return nil, err
	}

	p.logger.Info("玩家注册成功", zap.String("player_id", player.ID))

	return player, nil
}

// Login 登录
func (p *PlayerComponent) Login(username, password string) (*common.Player, string, error) {
	var player common.Player
	err := p.db.Where("username = ?", username).First(&player).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errors.New("用户不存在")
		}
		return nil, "", err
	}

	// 验证密码
	err = bcrypt.CompareHashAndPassword([]byte(player.PasswordHash), []byte(password))
	if err != nil {
		return nil, "", errors.New("密码错误")
	}

	// 生成JWT token
	token, err := common.GenerateToken(player.ID, player.Username)
	if err != nil {
		p.logger.Error("生成JWT token失败", zap.Error(err))
		return nil, "", fmt.Errorf("生成token失败: %w", err)
	}

	// 将token存储到Redis（用于注销功能）
	err = p.redis.Set(ctx, fmt.Sprintf("jwt_token:%s", player.ID), token, 24*time.Hour).Err()
	if err != nil {
		p.logger.Warn("保存JWT token到Redis失败", zap.Error(err))
	}

	// 更新最后登录时间
	player.LastLoginAt = time.Now()
	p.db.Save(&player)

	// 设置在线状态
	p.redis.Set(ctx, fmt.Sprintf("online:%s", player.ID), "1", 1*time.Hour)

	p.logger.Info("玩家登录成功", zap.String("player_id", player.ID))

	return &player, token, nil
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

	// 加密新密码
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	player.PasswordHash = string(hash)
	return p.db.Save(&player).Error
}

// DeleteRedisKeys 删除Redis中的token和在线状态（登出时使用）
func (p *PlayerComponent) DeleteRedisKeys(ctx context.Context, playerID string) {
	p.redis.Del(ctx, fmt.Sprintf("jwt_token:%s", playerID))
	p.redis.Del(ctx, fmt.Sprintf("online:%s", playerID))
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
