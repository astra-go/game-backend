package api

import (
	"time"

	"github.com/astra-go/astra"
	"github.com/astra-go/game-backend/internal/models"
	"github.com/astra-go/game-backend/internal/services"
	"github.com/redis/go-redis/v9"
	"github.com/astra-go/astra/log"
	"gorm.io/gorm"
)

// ActivityAPI 活动API
type ActivityAPI struct {
	db              *gorm.DB
	redis           *redis.Client
	activityService *services.ActivityService
	logger          *log.Logger
}

// NewActivityAPI 创建活动API
func NewActivityAPI(db *gorm.DB, redisClient *redis.Client, logger *log.Logger) *ActivityAPI {
	activitySvc := services.NewActivityService(db, redisClient, nil)
	
	return &ActivityAPI{
		db:             db,
		redis:          redisClient,
		activityService: activitySvc,
		logger:         logger,
	}
}

// RegisterRoutes 注册路由
func (a *ActivityAPI) RegisterRoutes(router any) {
	a.logger.Info("ActivityAPI routes registered")
}

// GetActiveActivities 获取进行中的活动
// GET /api/v1/activities
func (a *ActivityAPI) GetActiveActivities(c *astra.Ctx) error {
	activities, err := a.activityService.ListActiveActivities(c.Request().Context())
	if err != nil {
		a.logger.Error("获取活动列表失败", "error", err)
		return ResponseError(c, 500, "获取活动列表失败")
	}
	return ResponseOK(c, activities)
}

// GetActivityDetail 获取活动详情
// GET /api/v1/activities/:id
func (a *ActivityAPI) GetActivityDetail(c *astra.Ctx) error {
	id := c.Param("id")
	activity, err := a.activityService.GetActivity(c.Request().Context(), id)
	if err != nil {
		return ResponseError(c, 404, err.Error())
	}
	return ResponseOK(c, activity)
}

// Checkin 签到
// POST /api/v1/activities/checkin
func (a *ActivityAPI) Checkin(c *astra.Ctx) error {
	playerID := c.Query("player_id")
	if playerID == "" {
		return ResponseError(c, 400, "缺少player_id参数")
	}
	
	record, err := a.activityService.Checkin(c.Request().Context(), playerID)
	if err != nil {
		return ResponseError(c, 400, err.Error())
	}
	return ResponseOK(c, record)
}

// GetCheckinInfo 获取签到信息
// GET /api/v1/activities/checkin/info
func (a *ActivityAPI) GetCheckinInfo(c *astra.Ctx) error {
	playerID := c.Query("player_id")
	if playerID == "" {
		return ResponseError(c, 400, "缺少player_id参数")
	}
	
	info, err := a.activityService.GetCheckinInfo(c.Request().Context(), playerID)
	if err != nil {
		return ResponseError(c, 400, err.Error())
	}
	return ResponseOK(c, info)
}

// RedeemGiftCode 兑换码兑换
// POST /api/v1/activities/giftcode/redeem
func (a *ActivityAPI) RedeemGiftCode(c *astra.Ctx) error {
	var req struct {
		PlayerID string `json:"player_id"`
		Code     string `json:"code"`
	}
	
	if err := c.Bind(&req); err != nil {
		return ResponseError(c, 400, "参数解析失败")
	}
	
	if req.PlayerID == "" || req.Code == "" {
		return ResponseError(c, 400, "参数不完整")
	}
	
	usage, err := a.activityService.RedeemGiftCode(c.Request().Context(), req.PlayerID, req.Code)
	if err != nil {
		return ResponseError(c, 400, err.Error())
	}
	return ResponseOK(c, usage)
}

// ClaimRechargeRebate 领取充值返利
// POST /api/v1/activities/:activity_id/claim
func (a *ActivityAPI) ClaimRechargeRebate(c *astra.Ctx) error {
	activityID := c.Param("activity_id")
	playerID := c.Query("player_id")
	
	if playerID == "" {
		return ResponseError(c, 400, "缺少player_id参数")
	}
	
	record, err := a.activityService.ClaimRechargeRebate(c.Request().Context(), playerID, activityID)
	if err != nil {
		return ResponseError(c, 400, err.Error())
	}
	return ResponseOK(c, record)
}

// GetPlayerActivityRecords 获取玩家活动记录
// GET /api/v1/activities/records
func (a *ActivityAPI) GetPlayerActivityRecords(c *astra.Ctx) error {
	playerID := c.Query("player_id")
	if playerID == "" {
		return ResponseError(c, 400, "缺少player_id参数")
	}
	
	records, err := a.activityService.GetPlayerAllRecords(c.Request().Context(), playerID)
	if err != nil {
		return ResponseError(c, 400, err.Error())
	}
	return ResponseOK(c, records)
}

// CreateActivity 创建活动（后台）
// POST /api/v1/admin/activities
func (a *ActivityAPI) CreateActivity(c *astra.Ctx) error {
	var activity models.Activity
	if err := c.Bind(&activity); err != nil {
		return ResponseError(c, 400, "参数解析失败")
	}
	
	err := a.activityService.CreateActivity(c.Request().Context(), &activity)
	if err != nil {
		return ResponseError(c, 500, err.Error())
	}
	return ResponseOK(c, activity)
}

// UpdateActivity 更新活动
// PUT /api/v1/admin/activities/:id
func (a *ActivityAPI) UpdateActivity(c *astra.Ctx) error {
	var activity models.Activity
	if err := c.Bind(&activity); err != nil {
		return ResponseError(c, 400, "参数解析失败")
	}
	
	err := a.activityService.UpdateActivity(c.Request().Context(), &activity)
	if err != nil {
		return ResponseError(c, 500, err.Error())
	}
	return ResponseOK(c, activity)
}

// CreateGiftCode 创建兑换码
// POST /api/v1/admin/giftcodes
func (a *ActivityAPI) CreateGiftCode(c *astra.Ctx) error {
	var req struct {
		Code         string    `json:"code"`
		Name         string    `json:"name"`
		Type         string    `json:"type"`
		RewardType   string    `json:"reward_type"`
		RewardID     string    `json:"reward_id"`
		RewardAmount int64     `json:"reward_amount"`
		UseLimit     int       `json:"use_limit"`
		ValidFrom    time.Time `json:"valid_from"`
		ValidUntil   time.Time `json:"valid_until"`
		MinLevel     int       `json:"min_level"`
	}
	
	if err := c.Bind(&req); err != nil {
		return ResponseError(c, 400, "参数解析失败")
	}
	
	code := &models.GiftCode{
		Code:         req.Code,
		Name:         req.Name,
		Type:         req.Type,
		RewardType:   req.RewardType,
		RewardID:     req.RewardID,
		RewardAmount: req.RewardAmount,
		UseLimit:     req.UseLimit,
		ValidFrom:    req.ValidFrom,
		ValidUntil:   req.ValidUntil,
		MinLevel:     req.MinLevel,
		Status:       "active",
	}
	
	err := a.activityService.CreateGiftCode(c.Request().Context(), code)
	if err != nil {
		return ResponseError(c, 500, err.Error())
	}
	return ResponseOK(c, code)
}

// BatchCreateGiftCodes 批量创建兑换码
// POST /api/v1/admin/giftcodes/batch
func (a *ActivityAPI) BatchCreateGiftCodes(c *astra.Ctx) error {
	var req struct {
		Name         string    `json:"name"`
		Count        int       `json:"count"`
		Type         string    `json:"type"`
		RewardType   string    `json:"reward_type"`
		RewardID     string    `json:"reward_id"`
		RewardAmount int64     `json:"reward_amount"`
		UseLimit     int       `json:"use_limit"`
		ValidFrom    time.Time `json:"valid_from"`
		ValidUntil   time.Time `json:"valid_until"`
		MinLevel     int       `json:"min_level"`
	}
	
	if err := c.Bind(&req); err != nil {
		return ResponseError(c, 400, "参数解析失败")
	}
	
	if req.Count <= 0 || req.Count > 1000 {
		return ResponseError(c, 400, "数量必须在1-1000之间")
	}
	
	codes := make([]*models.GiftCode, req.Count)
	for i := 0; i < req.Count; i++ {
		codes[i] = &models.GiftCode{
			Code:         generateGiftCode(),
			Name:         req.Name,
			Type:         req.Type,
			RewardType:   req.RewardType,
			RewardID:     req.RewardID,
			RewardAmount: req.RewardAmount,
			UseLimit:     req.UseLimit,
			ValidFrom:    req.ValidFrom,
			ValidUntil:   req.ValidUntil,
			MinLevel:     req.MinLevel,
			Status:       "active",
		}
	}
	
	err := a.activityService.BatchCreateGiftCodes(c.Request().Context(), codes)
	if err != nil {
		return ResponseError(c, 500, err.Error())
	}
	return ResponseOK(c, map[string]any{
		"created_count": len(codes),
		"codes":         codes,
	})
}

// generateGiftCode 生成随机兑换码
func generateGiftCode() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 16)
	for i := range result {
		result[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(result)
}

// ListUpcomingActivities 获取即将开始的活动
// GET /api/v1/activities/upcoming
func (a *ActivityAPI) ListUpcomingActivities(c *astra.Ctx) error {
	activities, err := a.activityService.ListUpcomingActivities(c.Request().Context(), 10)
	if err != nil {
		return ResponseError(c, 500, err.Error())
	}
	return ResponseOK(c, activities)
}

