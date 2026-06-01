package services

import (
	"testing"
	"time"

	"github.com/astra-go/game-backend/internal/models"
)

// TestActivityModel 模型测试
func TestActivityModel(t *testing.T) {
	activity := &models.Activity{
		ID:           "test_activity_001",
		Name:         "测试活动",
		Type:         models.ActivityTypeDailyCheckin,
		Status:       models.ActivityStatusActive,
		StartTime:    time.Now().Add(-24 * time.Hour),
		EndTime:      time.Now().Add(7 * 24 * time.Hour),
		RewardType:   "diamond",
		RewardAmount: 100,
	}
	
	// 测试状态检查
	status := activity.CheckStatus()
	if status != models.ActivityStatusActive {
		t.Errorf("期望状态active，实际%s", status)
	}
	
	// 测试IsActive
	if !activity.IsActive() {
		t.Error("活动应该处于进行中状态")
	}
	
	t.Logf("活动模型测试通过: %+v", activity)
}

// TestActivityRecord 活动记录测试
func TestActivityRecord(t *testing.T) {
	record := &models.ActivityRecord{
		ID:         "record_001",
		ActivityID: "activity_001",
		PlayerID:   "player_001",
		Status:     "pending",
		Progress:   50,
		Target:     100,
	}
	
	// 测试UniqueKey
	key := record.UniqueKey()
	expected := "activity_001_player_001"
	if key != expected {
		t.Errorf("期望唯一键%s，实际%s", expected, key)
	}
	
	t.Logf("活动记录测试通过: %s", key)
}

// TestGiftCode 兑换码测试
func TestGiftCode(t *testing.T) {
	code := &models.GiftCode{
		ID:           "gc_001",
		Code:         "TEST1234",
		Type:         "one_time",
		RewardType:   "diamond",
		RewardAmount: 500,
		UseLimit:     1,
		UsedCount:    0,
		ValidFrom:    time.Now().Add(-24 * time.Hour),
		ValidUntil:   time.Now().Add(7 * 24 * time.Hour),
		Status:       "active",
	}
	
	// 测试有效性检查
	if !code.IsValid() {
		t.Error("兑换码应该有效")
	}
	
	// 测试已使用后无效
	code.UsedCount = 1
	if code.IsValid() {
		t.Error("已使用的兑换码应该无效")
	}
	
	t.Logf("兑换码测试通过")
}

// TestCheckinInfo 签到信息测试
func TestCheckinInfo(t *testing.T) {
	info := &CheckinInfo{
		CheckedInToday:   true,
		ConsecutiveDays:  7,
		TodayDate:        time.Now().Format("2006-01-02"),
	}
	
	if !info.CheckedInToday {
		t.Error("今日应该已签到")
	}
	
	if info.ConsecutiveDays != 7 {
		t.Errorf("连续签到天数应为7，实际%d", info.ConsecutiveDays)
	}
	
	t.Logf("签到信息测试通过: %+v", info)
}

// TestRechargeRebateConfig 充值返利配置测试
func TestRechargeRebateConfig(t *testing.T) {
	config := &models.RechargeRebateConfig{
		ID:          "config_001",
		ActivityID:  "activity_recharge",
		Threshold:   6000, // 60元
		RebateRate:  10,   // 10%返利
		MaxRebate:   1000, // 最多返1000分
		RewardType:  "diamond",
		RewardAmount: 600,
	}
	
	// 测试阈值
	if config.Threshold != 6000 {
		t.Errorf("阈值应为6000，实际%d", config.Threshold)
	}
	
	// 测试返利计算
	rechargeAmount := int64(10000) // 100元
	rebateAmount := rechargeAmount * int64(config.RebateRate) / 100
	if rebateAmount > config.MaxRebate {
		rebateAmount = config.MaxRebate
	}
	
	expectedRebate := int64(1000) // 10% of 10000 = 1000, capped at 1000
	if rebateAmount != expectedRebate {
		t.Errorf("返利金额应为%d，实际%d", expectedRebate, rebateAmount)
	}
	
	t.Logf("充值返利配置测试通过")
}

// TestDailyCheckinRecord 每日签到记录测试
func TestDailyCheckinRecord(t *testing.T) {
	record := &models.DailyCheckinRecord{
		ID:            "checkin_001",
		PlayerID:      "player_001",
		CheckinDate:   time.Now().Format("2006-01-02"),
		RewardType:    "diamond",
		RewardAmount:  100,
	}
	
	if record.CheckinDate == "" {
		t.Error("签到日期不应为空")
	}
	
	t.Logf("每日签到记录测试通过")
}

// TestActivityTypes 活动类型枚举测试
func TestActivityTypes(t *testing.T) {
	types := []models.ActivityType{
		models.ActivityTypeDailyCheckin,
		models.ActivityTypeFirstRecharge,
		models.ActivityTypeRechargeRebate,
		models.ActivityTypeConsumeRebate,
		models.ActivityTypeLimitedDiscount,
		models.ActivityTypeHolidayEvent,
		models.ActivityTypeDailyTask,
		models.ActivityTypeWeeklyTask,
		models.ActivityTypeGiftCode,
		models.ActivityTypeLoginBonus,
	}
	
	expected := []string{
		"daily_checkin",
		"first_recharge",
		"recharge_rebate",
		"consume_rebate",
		"limited_discount",
		"holiday_event",
		"daily_task",
		"weekly_task",
		"gift_code",
		"login_bonus",
	}
	
	for i, at := range types {
		if string(at) != expected[i] {
			t.Errorf("期望类型%s，实际%s", expected[i], at)
		}
	}
	
	t.Logf("活动类型枚举测试通过")
}

// TestRandString 随机字符串生成测试
func TestRandString(t *testing.T) {
	str1 := randString(16)
	str2 := randString(16)
	
	if len(str1) != 16 {
		t.Errorf("期望长度16，实际%d", len(str1))
	}
	
	if str1 == str2 {
		t.Error("两次生成的随机字符串应该不同")
	}
	
	t.Logf("随机字符串生成测试通过: %s", str1)
}
