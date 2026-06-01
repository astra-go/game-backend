package friend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/astra-go/game-backend/pkg/natsclient"
	"github.com/redis/go-redis/v9"
	"github.com/willf/bloom"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var ctx = context.Background()

const (
	defaultFriendLimit   = 500
	requestExpireDays    = 7
	distributedLockTTL   = 10 * time.Second
	cacheTTL             = 5 * time.Minute
	localCacheSize       = 10000
	asyncWriteQueueSize  = 10000
	bloomFilterSize      = 1000000
	bloomFilterHashCount = 5
)

// FriendComponent 好友服务组件
type FriendComponent struct {
	db              *gorm.DB
	redis           *redis.Client
	nc              natsclient.Client
	logger          *zap.Logger
	localCache      *lru.Cache[string, []common.FriendInfo]
	bloomFilter     *bloom.BloomFilter
	asyncWriteQueue chan *asyncWriteTask
	mu              sync.RWMutex
}

type asyncWriteTask struct {
	taskType string
	data     any
}

// NewFriendComponent 创建好友组件
func NewFriendComponent(db *gorm.DB, redis *redis.Client, nc natsclient.Client, logger *zap.Logger) *FriendComponent {
	localCache, _ := lru.New[string, []common.FriendInfo](localCacheSize)

	return &FriendComponent{
		db:              db,
		redis:           redis,
		nc:              nc,
		logger:          logger,
		localCache:      localCache,
		bloomFilter:     bloom.New(bloomFilterSize, bloomFilterHashCount),
		asyncWriteQueue: make(chan *asyncWriteTask, asyncWriteQueueSize),
	}
}

// Init 初始化
func (f *FriendComponent) Init() error {
	f.logger.Info("FriendComponent 初始化")

	// 自动迁移数据库
	err := f.db.AutoMigrate(&common.Friend{}, &common.FriendRequest{})
	if err != nil {
		return err
	}

	// 加载已存在的玩家ID到布隆过滤器
	var players []common.Player
	f.db.Select("id").Find(&players)
	for _, p := range players {
		f.bloomFilter.AddString(p.ID)
	}

	// 启动异步写入协程
	go f.asyncWriteToMySQL()

	f.logger.Info("FriendComponent 初始化完成",
		zap.Int("bloom_filter_players", len(players)),
	)

	return nil
}

// SendRequest 发送好友请求
func (f *FriendComponent) SendRequest(playerID, targetID, message string) error {
	if playerID == targetID {
		return errors.New("不能添加自己为好友")
	}

	// 布隆过滤器快速检查目标玩家是否存在
	if !f.bloomFilter.TestString(targetID) {
		return errors.New("目标玩家不存在")
	}

	// 获取分布式锁（防止并发重复请求）
	lockKey := f.getRequestLockKey(playerID, targetID)
	locked, err := f.acquireLock(lockKey, distributedLockTTL)
	if err != nil {
		return fmt.Errorf("获取锁失败: %w", err)
	}
	if !locked {
		return errors.New("请求处理中，请稍后重试")
	}
	defer f.releaseLock(lockKey)

	// 检查是否已经是好友
	var existingFriend common.Friend
	err = f.db.Where("player_id = ? AND friend_id = ?", playerID, targetID).First(&existingFriend).Error
	if err == nil {
		return errors.New("已经是好友关系")
	}

	// 检查是否有待处理的请求
	var existingRequest common.FriendRequest
	err = f.db.Where(
		"(player_id = ? AND target_id = ? OR player_id = ? AND target_id = ?) AND status = 'pending' AND expires_at > NOW()",
		playerID, targetID, targetID, playerID,
	).First(&existingRequest).Error
	if err == nil {
		return errors.New("已有待处理的好友请求")
	}

	// 创建好友请求
	request := &common.FriendRequest{
		ID:        f.generateRequestID(),
		PlayerID:  playerID,
		TargetID:  targetID,
		Status:    "pending",
		Message:   message,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(requestExpireDays * 24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err = f.db.Create(request).Error
	if err != nil {
		return fmt.Errorf("创建好友请求失败: %w", err)
	}

	f.logger.Info("好友请求已发送",
		zap.String("request_id", request.ID),
		zap.String("player_id", playerID),
		zap.String("target_id", targetID),
	)

	// NATS通知目标玩家
	f.publishFriendRequestNotification(targetID, request)

	return nil
}

// AcceptRequest 接受好友请求
func (f *FriendComponent) AcceptRequest(requestID, targetID string) error {
	// 查询请求
	var request common.FriendRequest
	err := f.db.Where("id = ? AND target_id = ? AND status = 'pending'", requestID, targetID).First(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("好友请求不存在或已处理")
		}
		return err
	}

	// 检查是否过期
	if time.Now().After(request.ExpiresAt) {
		return errors.New("好友请求已过期")
	}

	// 检查好友数量限制
	var count int64
	f.db.Model(&common.Friend{}).Where("player_id = ?", targetID).Count(&count)
	if count >= defaultFriendLimit {
		return fmt.Errorf("好友数量已达上限（%d）", defaultFriendLimit)
	}

	// 开启事务：更新请求状态 + 创建双向好友关系
	err = f.db.Transaction(func(tx *gorm.DB) error {
		// 更新请求状态
		request.Status = "accepted"
		request.UpdatedAt = time.Now()
		if err := tx.Save(&request).Error; err != nil {
			return err
		}

		// 创建双向好友关系（A→B 和 B→A）
		now := time.Now()
		friends := []common.Friend{
			{
				PlayerID:  request.PlayerID,
				FriendID:  request.TargetID,
				CreatedAt: now,
			},
			{
				PlayerID:  request.TargetID,
				FriendID:  request.PlayerID,
				CreatedAt: now,
			},
		}

		for _, friend := range friends {
			if err := tx.Create(&friend).Error; err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("接受好友请求失败: %w", err)
	}

	// 清除双方的好友列表缓存
	f.invalidateCache(request.PlayerID)
	f.invalidateCache(request.TargetID)

	f.logger.Info("好友请求已接受",
		zap.String("request_id", requestID),
		zap.String("player_id", request.PlayerID),
		zap.String("target_id", request.TargetID),
	)

	// NATS通知双方
	f.publishFriendAddedNotification(request.PlayerID, request.TargetID)
	f.publishFriendAddedNotification(request.TargetID, request.PlayerID)

	return nil
}

// RejectRequest 拒绝好友请求
func (f *FriendComponent) RejectRequest(requestID, targetID string) error {
	// 查询请求（仅目标玩家可以拒绝）
	var request common.FriendRequest
	err := f.db.Where("id = ? AND target_id = ? AND status = 'pending'", requestID, targetID).First(&request).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("好友请求不存在或已处理")
		}
		return err
	}

	// 更新状态
	request.Status = "rejected"
	request.UpdatedAt = time.Now()
	err = f.db.Save(&request).Error
	if err != nil {
		return fmt.Errorf("拒绝好友请求失败: %w", err)
	}

	f.logger.Info("好友请求已拒绝",
		zap.String("request_id", requestID),
		zap.String("player_id", request.PlayerID),
		zap.String("target_id", request.TargetID),
	)

	return nil
}

// DeleteFriend 删除好友
func (f *FriendComponent) DeleteFriend(playerID, friendID string) error {
	// 开启事务：删除双向好友关系
	err := f.db.Transaction(func(tx *gorm.DB) error {
		// 删除 A→B
		if err := tx.Where("player_id = ? AND friend_id = ?", playerID, friendID).Delete(&common.Friend{}).Error; err != nil {
			return err
		}

		// 删除 A→B
		if err := tx.Where("player_id = ? AND friend_id = ?", friendID, playerID).Delete(&common.Friend{}).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("删除好友失败: %w", err)
	}

	// 清除双方的好友列表缓存
	f.invalidateCache(playerID)
	f.invalidateCache(friendID)

	f.logger.Info("好友已删除",
		zap.String("player_id", playerID),
		zap.String("friend_id", friendID),
	)

	// NATS通知双方
	f.publishFriendRemovedNotification(playerID, friendID)
	f.publishFriendRemovedNotification(friendID, playerID)

	return nil
}

// GetFriendList 获取好友列表（两层缓存：本地LRU → Redis → MySQL）
func (f *FriendComponent) GetFriendList(playerID string) ([]common.FriendInfo, error) {
	// 1. 本地LRU缓存查询
	if cached, ok := f.localCache.Get(playerID); ok {
		f.logger.Debug("好友列表命中本地缓存", zap.String("player_id", playerID))
		return cached, nil
	}

	// 2. Redis缓存查询
	cacheKey := f.getFriendListCacheKey(playerID)
	var friendInfos []common.FriendInfo
	val, err := f.redis.Get(ctx, cacheKey).Result()
	if err == nil {
		// Redis缓存命中，反序列化
		if err := common.JSONUnmarshal([]byte(val), &friendInfos); err == nil {
			// 写入本地缓存
			f.localCache.Add(playerID, friendInfos)
			f.logger.Debug("好友列表命中Redis缓存", zap.String("player_id", playerID))
			return friendInfos, nil
		}
	}

	// 3. MySQL查询（使用Preload避免N+1问题）
	var friends []common.Friend
	err = f.db.Where("player_id = ?", playerID).Find(&friends).Error
	if err != nil {
		return nil, fmt.Errorf("查询好友列表失败: %w", err)
	}

	// 批量查询好友详细信息
	if len(friends) == 0 {
		return []common.FriendInfo{}, nil
	}

	friendIDs := make([]string, len(friends))
	for i, f := range friends {
		friendIDs[i] = f.FriendID
	}

	var players []common.Player
	err = f.db.Where("id IN ?", friendIDs).Find(&players).Error
	if err != nil {
		return nil, fmt.Errorf("查询好友信息失败: %w", err)
	}

	// 批量查询在线状态
	onlineStatus, _ := f.batchIsOnline(friendIDs)

	// 组装FriendInfo
	for _, player := range players {
		friendInfos = append(friendInfos, common.FriendInfo{
			PlayerID:    player.ID,
			Username:    player.Username,
			Nickname:    player.Nickname,
			Avatar:      player.Avatar,
			Level:       player.Level,
			Online:      onlineStatus[player.ID],
			LastLoginAt: player.LastLoginAt,
			CreatedAt:   player.CreatedAt,
		})
	}

	// 写入Redis缓存
	if data, err := common.JSONMarshal(friendInfos); err == nil {
		f.redis.Set(ctx, cacheKey, data, cacheTTL)
	}

	// 写入本地缓存
	f.localCache.Add(playerID, friendInfos)

	f.logger.Debug("好友列表从MySQL加载",
		zap.String("player_id", playerID),
		zap.Int("count", len(friendInfos)),
	)

	return friendInfos, nil
}

// GetPendingRequests 获取待处理的好友请求
func (f *FriendComponent) GetPendingRequests(playerID string) ([]common.FriendRequest, error) {
	var requests []common.FriendRequest
	err := f.db.Where(
		"target_id = ? AND status = 'pending' AND expires_at > NOW()",
		playerID,
	).Order("created_at DESC").Find(&requests).Error

	if err != nil {
		return nil, fmt.Errorf("查询好友请求失败: %w", err)
	}

	return requests, nil
}

// NotifyFriendsOnline 通知好友玩家上线
func (f *FriendComponent) NotifyFriendsOnline(playerID string) {
	// 获取好友列表
	friends, err := f.GetFriendList(playerID)
	if err != nil {
		f.logger.Warn("获取好友列表失败", zap.Error(err))
		return
	}

	// 向每个好友发送上线通知
	for _, friend := range friends {
		subject := fmt.Sprintf("friend.online.%s", friend.PlayerID)
		data := map[string]string{
			"player_id": playerID,
			"timestamp": time.Now().Format(time.RFC3339),
		}
		if payload, err := common.JSONMarshal(data); err == nil {
			f.nc.Publish(subject, payload)
		}
	}

	f.logger.Debug("已通知好友上线",
		zap.String("player_id", playerID),
		zap.Int("friend_count", len(friends)),
	)
}

// NotifyFriendsOffline 通知好友玩家下线
func (f *FriendComponent) NotifyFriendsOffline(playerID string) {
	// 获取好友列表
	friends, err := f.GetFriendList(playerID)
	if err != nil {
		f.logger.Warn("获取好友列表失败", zap.Error(err))
		return
	}

	// 向每个好友发送下线通知
	for _, friend := range friends {
		subject := fmt.Sprintf("friend.offline.%s", friend.PlayerID)
		data := map[string]string{
			"player_id": playerID,
			"timestamp": time.Now().Format(time.RFC3339),
		}
		if payload, err := common.JSONMarshal(data); err == nil {
			f.nc.Publish(subject, payload)
		}
	}

	f.logger.Debug("已通知好友下线",
		zap.String("player_id", playerID),
		zap.Int("friend_count", len(friends)),
	)
}

// batchIsOnline 批量查询在线状态（Redis MGET）
func (f *FriendComponent) batchIsOnline(playerIDs []string) (map[string]bool, error) {
	if len(playerIDs) == 0 {
		return map[string]bool{}, nil
	}

	// 构造Redis keys
	keys := make([]string, len(playerIDs))
	for i, id := range playerIDs {
		keys[i] = fmt.Sprintf("online:%s", id)
	}

	// 使用Pipeline批量查询
	pipe := f.redis.Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.Get(ctx, key)
	}
	_, err := pipe.Exec(ctx)

	// 组装结果
	result := make(map[string]bool)
	for i, cmd := range cmds {
		val, err := cmd.Result()
		result[playerIDs[i]] = (err == nil && val == "1")
	}

	return result, err
}

// invalidateCache 清除缓存
func (f *FriendComponent) invalidateCache(playerID string) {
	// 清除本地缓存
	f.localCache.Remove(playerID)

	// 清除Redis缓存
	cacheKey := f.getFriendListCacheKey(playerID)
	f.redis.Del(ctx, cacheKey)
}

// asyncWriteToMySQL 异步写入MySQL（从缓冲队列消费）
func (f *FriendComponent) asyncWriteToMySQL() {
	for task := range f.asyncWriteQueue {
		switch task.taskType {
		case "create_friend":
			if friend, ok := task.data.(*common.Friend); ok {
				if err := f.db.Create(friend).Error; err != nil {
					f.logger.Error("异步写入好友关系失败", zap.Error(err))
				}
			}
		case "delete_friend":
			if data, ok := task.data.(map[string]string); ok {
				playerID := data["player_id"]
				friendID := data["friend_id"]
				if err := f.db.Where("player_id = ? AND friend_id = ?", playerID, friendID).Delete(&common.Friend{}).Error; err != nil {
					f.logger.Error("异步删除好友关系失败", zap.Error(err))
				}
			}
		}
	}
}

// ========== 辅助函数 ==========

func (f *FriendComponent) generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}

func (f *FriendComponent) getRequestLockKey(playerID, targetID string) string {
	// 使用LEAST/GREATEST确保锁的唯一性
	if playerID < targetID {
		return fmt.Sprintf("lock:friend_request:%s:%s", playerID, targetID)
	}
	return fmt.Sprintf("lock:friend_request:%s:%s", targetID, playerID)
}

func (f *FriendComponent) getFriendListCacheKey(playerID string) string {
	return fmt.Sprintf("friend_list:%s", playerID)
}

func (f *FriendComponent) acquireLock(key string, ttl time.Duration) (bool, error) {
	return f.redis.SetNX(ctx, key, "1", ttl).Result()
}

func (f *FriendComponent) releaseLock(key string) {
	f.redis.Del(ctx, key)
}

func (f *FriendComponent) publishFriendRequestNotification(targetID string, request *common.FriendRequest) {
	subject := fmt.Sprintf("friend.request.%s", targetID)
	data := map[string]any{
		"request_id": request.ID,
		"player_id":  request.PlayerID,
		"message":    request.Message,
		"timestamp":  time.Now().Format(time.RFC3339),
	}
	if payload, err := common.JSONMarshal(data); err == nil {
		f.nc.Publish(subject, payload)
	}
}

func (f *FriendComponent) publishFriendAddedNotification(playerID, friendID string) {
	subject := fmt.Sprintf("friend.added.%s", playerID)
	data := map[string]string{
		"friend_id": friendID,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	if payload, err := common.JSONMarshal(data); err == nil {
		f.nc.Publish(subject, payload)
	}
}

func (f *FriendComponent) publishFriendRemovedNotification(playerID, friendID string) {
	subject := fmt.Sprintf("friend.removed.%s", playerID)
	data := map[string]string{
		"friend_id": friendID,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	if payload, err := common.JSONMarshal(data); err == nil {
		f.nc.Publish(subject, payload)
	}
}
