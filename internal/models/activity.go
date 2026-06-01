package models

import (
	"time"
)

// ActivityType 活动类型
type ActivityType string

const (
	ActivityTypeDailyCheckin   ActivityType = "daily_checkin"    // 每日签到
	ActivityTypeFirstRecharge  ActivityType = "first_recharge"   // 首充礼包
	ActivityTypeRechargeRebate  ActivityType = "recharge_rebate"  // 充值返利
	ActivityTypeConsumeRebate   ActivityType = "consume_rebate"   // 消费返利
	ActivityTypeLimitedDiscount ActivityType = "limited_discount" // 限时折扣
	ActivityTypeHolidayEvent    ActivityType = "holiday_event"    // 节日活动
	ActivityTypeDailyTask       ActivityType = "daily_task"       // 每日任务
	ActivityTypeWeeklyTask      ActivityType = "weekly_task"     // 每周任务
	ActivityTypeGiftCode        ActivityType = "gift_code"        // 兑换码
	ActivityTypeLoginBonus      ActivityType = "login_bonus"      // 登录奖励
)

// ActivityStatus 活动状态
type ActivityStatus string

const (
	ActivityStatusActive   ActivityStatus = "active"   // 进行中
	ActivityStatusUpcoming ActivityStatus = "upcoming" // 即将开始
	ActivityStatusEnded    ActivityStatus = "ended"    // 已结束
)

// Activity 活动配置
type Activity struct {
	ID             string        `json:"id" gorm:"primaryKey;size:64"`
	Name           string        `json:"name" gorm:"size:128;not null"`
	Description    string        `json:"description" gorm:"type:text"`
	Type           ActivityType  `json:"type" gorm:"size:32;not null;index"`
	Status         ActivityStatus `json:"status" gorm:"size:16;default:active"`
	StartTime      time.Time     `json:"start_time" gorm:"index"`
	EndTime        time.Time     `json:"end_time" gorm:"index"`
	RewardType     string        `json:"reward_type" gorm:"size:32"` // diamond/gold/item
	RewardID       string        `json:"reward_id" gorm:"size:64"`
	RewardAmount   int64         `json:"reward_amount"`
	Threshold      int64         `json:"threshold"`      // 触发阈值（如充值金额）
	MaxRewardCount int           `json:"max_reward_count"` // 总奖励数量上限
	ClaimedCount   int           `json:"claimed_count"`   // 已领取数量
	PlayerLimit    int           `json:"player_limit"`    // 每人参与次数上限，0表示无限
	SortOrder      int           `json:"sort_order"`       // 排序
	Icon           string        `json:"icon" gorm:"size:256"`
	ExtData        string        `json:"ext_data" gorm:"type:text"` // 扩展配置JSON
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// TableName 表名
func (Activity) TableName() string {
	return "activities"
}

// CheckStatus 检查活动状态
func (a *Activity) CheckStatus() ActivityStatus {
	now := time.Now()
	if now.Before(a.StartTime) {
		return ActivityStatusUpcoming
	}
	if now.After(a.EndTime) {
		return ActivityStatusEnded
	}
	return ActivityStatusActive
}

// IsActive 检查活动是否进行中
func (a *Activity) IsActive() bool {
	return a.CheckStatus() == ActivityStatusActive
}

// ActivityRecord 玩家活动记录
type ActivityRecord struct {
	ID            string    `json:"id" gorm:"primaryKey;size:64"`
	ActivityID    string    `json:"activity_id" gorm:"size:64;index;not null"`
	PlayerID      string    `json:"player_id" gorm:"size:64;index;not null"`
	Status        string    `json:"status" gorm:"size:16;default:pending"` // pending/claimed/expired
	Progress      int64     `json:"progress"`     // 当前进度
	Target        int64     `json:"target"`       // 目标值
	ClaimedAt     *time.Time `json:"claimed_at"`
	ClaimedReward string    `json:"claimed_reward" gorm:"type:text"` // 已领取奖励详情
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TableName 表名
func (ActivityRecord) TableName() string {
	return "activity_records"
}

// UniqueKey 生成唯一键
func (r *ActivityRecord) UniqueKey() string {
	return r.ActivityID + "_" + r.PlayerID
}

// DailyCheckinRecord 每日签到记录
type DailyCheckinRecord struct {
	ID        string    `json:"id" gorm:"primaryKey;size:64"`
	PlayerID  string    `json:"player_id" gorm:"size:64;index;not null"`
	CheckinDate string  `json:"checkin_date" gorm:"size:10;not null"` // YYYY-MM-DD
	RewardType string   `json:"reward_type" gorm:"size:32"`
	RewardID   string   `json:"reward_id" gorm:"size:64"`
	RewardAmount int64  `json:"reward_amount"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 表名
func (DailyCheckinRecord) TableName() string {
	return "daily_checkin_records"
}

// GiftCode 兑换码
type GiftCode struct {
	ID          string    `json:"id" gorm:"primaryKey;size:64"`
	Code        string    `json:"code" gorm:"uniqueIndex;size:32;not null"`
	Name        string    `json:"name" gorm:"size:128"`
	Type        string    `json:"type" gorm:"size:32"` // one_time/multi_use/date_range
	RewardType  string    `json:"reward_type" gorm:"size:32"`
	RewardID    string    `json:"reward_id" gorm:"size:64"`
	RewardAmount int64    `json:"reward_amount"`
	UseLimit    int       `json:"use_limit"`    // 使用次数上限
	UsedCount   int       `json:"used_count"`   // 已使用次数
	ValidFrom   time.Time `json:"valid_from"`
	ValidUntil  time.Time `json:"valid_until"`
	MinLevel    int       `json:"min_level"`    // 最低等级要求
	Status      string    `json:"status" gorm:"size:16;default:active"`
	CreatedAt   time.Time `json:"created_at"`
}

// TableName 表名
func (GiftCode) TableName() string {
	return "gift_codes"
}

// IsValid 检查兑换码是否有效
func (g *GiftCode) IsValid() bool {
	now := time.Now()
	if g.Status != "active" {
		return false
	}
	if now.Before(g.ValidFrom) || now.After(g.ValidUntil) {
		return false
	}
	if g.Type == "one_time" && g.UsedCount >= 1 {
		return false
	}
	if g.UseLimit > 0 && g.UsedCount >= g.UseLimit {
		return false
	}
	return true
}

// GiftCodeUsage 兑换码使用记录
type GiftCodeUsage struct {
	ID        string    `json:"id" gorm:"primaryKey;size:64"`
	CodeID    string    `json:"code_id" gorm:"size:64;index;not null"`
	Code      string    `json:"code" gorm:"size:32;index;not null"`
	PlayerID  string    `json:"player_id" gorm:"size:64;index;not null"`
	RewardType string   `json:"reward_type" gorm:"size:32"`
	RewardID  string    `json:"reward_id" gorm:"size:64"`
	RewardAmount int64  `json:"reward_amount"`
	UsedAt    time.Time `json:"used_at"`
}

// TableName 表名
func (GiftCodeUsage) TableName() string {
	return "gift_code_usages"
}

// RechargeRebateConfig 充值返利配置
type RechargeRebateConfig struct {
	ID          string `json:"id" gorm:"primaryKey;size:64"`
	ActivityID  string `json:"activity_id" gorm:"size:64;index;not null"`
	Threshold   int64  `json:"threshold"`    // 充值金额阈值(分)
	RebateRate  int    `json:"rebate_rate"`  // 返利比例(百分比)
	MaxRebate   int64  `json:"max_rebate"`   // 最大返利金额
	RewardType  string `json:"reward_type" gorm:"size:32"`
	RewardID    string `json:"reward_id" gorm:"size:64"`
	RewardAmount int64 `json:"reward_amount"`
}

// TableName 表名
func (RechargeRebateConfig) TableName() string {
	return "recharge_rebate_configs"
}
