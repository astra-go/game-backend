package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/astra-go/game-backend/internal/models"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// ActivityService 活动服务
type ActivityService struct {
	db        *gorm.DB
	redis     *redis.Client
	inventory any // 背包服务
}

// NewActivityService 创建活动服务
func NewActivityService(db *gorm.DB, redis *redis.Client, inv any) *ActivityService {
	return &ActivityService{
		db:        db,
		redis:     redis,
		inventory: inv,
	}
}

// ListActiveActivities 获取进行中的活动列表
func (s *ActivityService) ListActiveActivities(ctx context.Context) ([]*models.Activity, error) {
	var activities []*models.Activity
	now := time.Now()
	
	err := s.db.WithContext(ctx).
		Where("status = ? AND start_time <= ? AND end_time >= ?", 
			models.ActivityStatusActive, now, now).
		Order("sort_order ASC, created_at DESC").
		Find(&activities).Error
	
	return activities, err
}

// ListUpcomingActivities 获取即将开始的活动
func (s *ActivityService) ListUpcomingActivities(ctx context.Context, limit int) ([]*models.Activity, error) {
	var activities []*models.Activity
	now := time.Now()
	
	err := s.db.WithContext(ctx).
		Where("status = ? AND start_time > ?", models.ActivityStatusUpcoming, now).
		Order("start_time ASC").
		Limit(limit).
		Find(&activities).Error
	
	return activities, err
}

// GetActivity 获取活动详情
func (s *ActivityService) GetActivity(ctx context.Context, activityID string) (*models.Activity, error) {
	var activity models.Activity
	err := s.db.WithContext(ctx).Where("id = ?", activityID).First(&activity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("活动不存在")
		}
		return nil, err
	}
	return &activity, nil
}

// GetPlayerActivityRecord 获取玩家活动记录
func (s *ActivityService) GetPlayerActivityRecord(ctx context.Context, activityID, playerID string) (*models.ActivityRecord, error) {
	var record models.ActivityRecord
	err := s.db.WithContext(ctx).
		Where("activity_id = ? AND player_id = ?", activityID, playerID).
		First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &record, nil
}

// GetPlayerAllRecords 获取玩家所有活动记录
func (s *ActivityService) GetPlayerAllRecords(ctx context.Context, playerID string) ([]*models.ActivityRecord, error) {
	var records []*models.ActivityRecord
	err := s.db.WithContext(ctx).
		Where("player_id = ?", playerID).
		Order("updated_at DESC").
		Find(&records).Error
	return records, err
}

// Checkin 签到
func (s *ActivityService) Checkin(ctx context.Context, playerID string) (*models.DailyCheckinRecord, error) {
	today := time.Now().Format("2006-01-02")
	
	// 检查今日是否已签到
	var existing models.DailyCheckinRecord
	err := s.db.WithContext(ctx).
		Where("player_id = ? AND checkin_date = ?", playerID, today).
		First(&existing).Error
	if err == nil {
		return nil, errors.New("今日已签到")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	
	// 获取签到活动
	activity, err := s.getDailyCheckinActivity(ctx)
	if err != nil {
		return nil, err
	}
	
	// 创建签到记录
	record := &models.DailyCheckinRecord{
		ID:            fmt.Sprintf("DC%s%s%d", playerID, today, time.Now().UnixNano()%10000),
		PlayerID:      playerID,
		CheckinDate:   today,
		RewardType:    activity.RewardType,
		RewardID:      activity.RewardID,
		RewardAmount:  activity.RewardAmount,
	}
	
	err = s.db.WithContext(ctx).Create(record).Error
	if err != nil {
		return nil, err
	}
	
	// 发放奖励
	err = s.grantReward(ctx, playerID, activity)
	if err != nil {
		// 回滚签到记录
		s.db.WithContext(ctx).Delete(record)
		return nil, err
	}
	
	return record, nil
}

// GetCheckinInfo 获取签到信息
func (s *ActivityService) GetCheckinInfo(ctx context.Context, playerID string) (*CheckinInfo, error) {
	today := time.Now().Format("2006-01-02")
	
	// 检查今日是否已签到
	var todayRecord models.DailyCheckinRecord
	err := s.db.WithContext(ctx).
		Where("player_id = ? AND checkin_date = ?", playerID, today).
		First(&todayRecord).Error
	checkedInToday := err == nil
	
	// 获取连续签到天数
	consecutiveDays, err := s.getConsecutiveCheckinDays(ctx, playerID)
	if err != nil {
		consecutiveDays = 0
	}
	
	return &CheckinInfo{
		CheckedInToday:   checkedInToday,
		ConsecutiveDays: consecutiveDays,
		TodayDate:        today,
	}, nil
}

// CheckinInfo 签到信息
type CheckinInfo struct {
	CheckedInToday   bool   `json:"checked_in_today"`
	ConsecutiveDays  int    `json:"consecutive_days"`
	TodayDate        string `json:"today_date"`
}

// RedeemGiftCode 兑换码兑换
func (s *ActivityService) RedeemGiftCode(ctx context.Context, playerID, code string) (*models.GiftCodeUsage, error) {
	// 查找兑换码
	var giftCode models.GiftCode
	err := s.db.WithContext(ctx).Where("code = ?", code).First(&giftCode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("兑换码不存在")
		}
		return nil, err
	}
	
	// 验证有效性
	if !giftCode.IsValid() {
		return nil, errors.New("兑换码已失效")
	}
	
	// 检查玩家是否已使用（一次性兑换码）
	var existingUsage models.GiftCodeUsage
	err = s.db.WithContext(ctx).
		Where("code_id = ? AND player_id = ?", giftCode.ID, playerID).
		First(&existingUsage).Error
	if err == nil {
		return nil, errors.New("您已使用过此兑换码")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	
	// 创建使用记录
	usage := &models.GiftCodeUsage{
		ID:           fmt.Sprintf("GCU%s%s%d", playerID, code, time.Now().UnixNano()%10000),
		CodeID:       giftCode.ID,
		Code:         code,
		PlayerID:     playerID,
		RewardType:   giftCode.RewardType,
		RewardID:     giftCode.RewardID,
		RewardAmount: giftCode.RewardAmount,
		UsedAt:       time.Now(),
	}
	
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 创建使用记录
		if err := tx.Create(usage).Error; err != nil {
			return err
		}
		
		// 更新使用次数
		if err := tx.Model(&models.GiftCode{}).Where("id = ?", giftCode.ID).
			Update("used_count", gorm.Expr("used_count + 1")).Error; err != nil {
			return err
		}
		
		return nil
	})
	if err != nil {
		return nil, err
	}
	
	// 发放奖励
	err = s.grantRewardByType(ctx, playerID, giftCode.RewardType, giftCode.RewardID, giftCode.RewardAmount)
	if err != nil {
		return nil, err
	}
	
	return usage, nil
}

// ClaimRechargeRebate 领取充值返利
func (s *ActivityService) ClaimRechargeRebate(ctx context.Context, playerID, activityID string) (*models.ActivityRecord, error) {
	// 获取活动
	activity, err := s.GetActivity(ctx, activityID)
	if err != nil {
		return nil, err
	}
	
	if activity.Type != models.ActivityTypeRechargeRebate {
		return nil, errors.New("活动类型不匹配")
	}
	
	// 检查活动状态
	if !activity.IsActive() {
		return nil, errors.New("活动已结束")
	}
	
	// 获取玩家记录
	record, err := s.GetPlayerActivityRecord(ctx, activityID, playerID)
	if err != nil {
		return nil, err
	}
	
	if record != nil && record.Status == "claimed" {
		return nil, errors.New("已领取过此奖励")
	}
	
	// 检查是否满足领取条件
	// TODO: 这里应该检查玩家的充值金额是否达到阈值
	
	// 更新记录
	if record == nil {
		record = &models.ActivityRecord{
			ID:         fmt.Sprintf("AR%s%s%d", activityID, playerID, time.Now().UnixNano()%10000),
			ActivityID: activityID,
			PlayerID:   playerID,
			Status:     "claimed",
			Progress:   0,
			Target:     activity.Threshold,
		}
		record.ClaimedAt = new(time.Time)
		*record.ClaimedAt = time.Now()
		
		err = s.db.WithContext(ctx).Create(record).Error
	} else {
		now := time.Now()
		record.Status = "claimed"
		record.ClaimedAt = &now
		
		err = s.db.WithContext(ctx).Save(record).Error
	}
	if err != nil {
		return nil, err
	}
	
	// 发放奖励
	err = s.grantReward(ctx, playerID, activity)
	if err != nil {
		return nil, err
	}
	
	return record, nil
}

// ProcessRecharge 处理充值触发活动
func (s *ActivityService) ProcessRecharge(ctx context.Context, playerID string, amount int64) error {
	// 查找所有进行中的充值返利活动
	var activities []*models.Activity
	now := time.Now()
	err := s.db.WithContext(ctx).
		Where("type = ? AND status = ? AND start_time <= ? AND end_time >= ? AND threshold <= ?",
			models.ActivityTypeRechargeRebate, models.ActivityStatusActive, now, now, amount).
		Find(&activities).Error
	if err != nil {
		return err
	}
	
	for _, activity := range activities {
		// 创建或更新活动记录
		record, err := s.GetPlayerActivityRecord(ctx, activity.ID, playerID)
		if err != nil {
			continue
		}
		
		if record == nil {
			record = &models.ActivityRecord{
				ID:         fmt.Sprintf("AR%s%s%d", activity.ID, playerID, time.Now().UnixNano()%10000),
				ActivityID: activity.ID,
				PlayerID:   playerID,
				Status:     "pending",
				Progress:   amount,
				Target:     activity.Threshold,
			}
			s.db.WithContext(ctx).Create(record)
		} else {
			if record.Status != "claimed" {
				s.db.WithContext(ctx).Model(record).Updates(map[string]interface{}{
					"progress": gorm.Expr("progress + ?", amount),
				})
			}
		}
	}
	
	return nil
}

// CreateActivity 创建活动（后台管理）
func (s *ActivityService) CreateActivity(ctx context.Context, activity *models.Activity) error {
	if activity.ID == "" {
		activity.ID = fmt.Sprintf("ACT%d%s%d", time.Now().Unix(), randString(8), time.Now().UnixNano()%10000)
	}
	activity.CreatedAt = time.Now()
	activity.UpdatedAt = time.Now()
	
	return s.db.WithContext(ctx).Create(activity).Error
}

// UpdateActivity 更新活动
func (s *ActivityService) UpdateActivity(ctx context.Context, activity *models.Activity) error {
	activity.UpdatedAt = time.Now()
	return s.db.WithContext(ctx).Save(activity).Error
}

// CreateGiftCode 创建兑换码
func (s *ActivityService) CreateGiftCode(ctx context.Context, code *models.GiftCode) error {
	if code.ID == "" {
		code.ID = fmt.Sprintf("GC%d%s%d", time.Now().Unix(), randString(8), time.Now().UnixNano()%10000)
	}
	code.CreatedAt = time.Now()
	
	return s.db.WithContext(ctx).Create(code).Error
}

// BatchCreateGiftCodes 批量创建兑换码
func (s *ActivityService) BatchCreateGiftCodes(ctx context.Context, codes []*models.GiftCode) error {
	now := time.Now()
	for _, code := range codes {
		if code.ID == "" {
			code.ID = fmt.Sprintf("GC%d%s%d", now.Unix(), randString(8), now.Nanosecond()%10000)
		}
		code.CreatedAt = now
	}
	
	return s.db.WithContext(ctx).CreateInBatches(codes, 100).Error
}

// 私有方法

func (s *ActivityService) getDailyCheckinActivity(ctx context.Context) (*models.Activity, error) {
	var activity models.Activity
	now := time.Now()
	err := s.db.WithContext(ctx).
		Where("type = ? AND status = ? AND start_time <= ? AND end_time >= ?",
			models.ActivityTypeDailyCheckin, models.ActivityStatusActive, now, now).
		First(&activity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 返回默认签到奖励
			return &models.Activity{
				ID:            "default_checkin",
				RewardType:    "diamond",
				RewardAmount:  100,
			}, nil
		}
		return nil, err
	}
	return &activity, nil
}

func (s *ActivityService) getConsecutiveCheckinDays(ctx context.Context, playerID string) (int, error) {
	var records []models.DailyCheckinRecord
	err := s.db.WithContext(ctx).
		Where("player_id = ?", playerID).
		Order("checkin_date DESC").
		Limit(30).
		Find(&records).Error
	if err != nil {
		return 0, err
	}
	
	if len(records) == 0 {
		return 0, nil
	}
	
	consecutive := 0
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	
	for i, record := range records {
		expectedDate := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		if record.CheckinDate == expectedDate || (i == 0 && record.CheckinDate == yesterday) {
			consecutive++
		} else if i == 0 && record.CheckinDate == today {
			// 今天已签到但记录还没更新
			consecutive++
		} else {
			break
		}
	}
	
	return consecutive, nil
}

func (s *ActivityService) grantReward(ctx context.Context, playerID string, activity *models.Activity) error {
	return s.grantRewardByType(ctx, playerID, activity.RewardType, activity.RewardID, activity.RewardAmount)
}

func (s *ActivityService) grantRewardByType(ctx context.Context, playerID, rewardType, rewardID string, amount int64) error {
	switch rewardType {
	case "diamond":
		return nil // TODO: 调用背包服务添加钻石
	case "gold":
		return nil // TODO: 调用背包服务添加金币
	case "item":
		return nil // TODO: 调用背包服务添加道具
	default:
		return errors.New("未知的奖励类型: " + rewardType)
	}
}

// GetActivityExtData 获取活动扩展配置
func GetActivityExtData[T any](activity *models.Activity) (*T, error) {
	if activity.ExtData == "" {
		return nil, nil
	}
	var data T
	err := json.Unmarshal([]byte(activity.ExtData), &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

func randString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
