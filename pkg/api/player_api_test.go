package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/middleware"
	"github.com/astra-go/game-backend/pkg/player"
	"github.com/astra-go/astra"
	"github.com/astra-go/astra/testutil"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// testEnv 测试环境，包含所有依赖
type testEnv struct {
	app        *astra.App
	server     *testutil.Server
	playerComp *player.PlayerComponent
	logger     *zap.Logger
	db         *gorm.DB
	mr         *miniredis.Miniredis
}

// setupTestEnv 初始化测试环境
func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	logger := zap.NewNop()

	// 使用MySQL测试数据库
	db, err := gorm.Open(mysql.Open("root:@tcp(127.0.0.1:3306)/astra_game_test?parseTime=true&charset=utf8mb4"), &gorm.Config{})
	if err != nil {
		t.Skip("跳过：需要MySQL测试数据库")
	}

	// 自动迁移
	db.AutoMigrate(&common.Player{})

	// 清空测试数据
	db.Exec("DELETE FROM players")

	// 使用miniredis模拟Redis
	mr, err := miniredis.Run()
	require.NoError(t, err)

	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	// 创建PlayerComponent
	pc := player.NewPlayerComponent(db, redisClient, logger)
	err = pc.Init()
	require.NoError(t, err)

	// 创建Astra应用
	app := testutil.NewTestApp()

	// 创建PlayerAPI并注册路由
	playerAPI := NewPlayerAPI(pc, logger)
	playerAPI.RegisterRoutes(app)

	// 创建测试服务器
	server := testutil.NewServer(t, app)

	return &testEnv{
		app:        app,
		server:     server,
		playerComp: pc,
		logger:     logger,
		db:         db,
		mr:         mr,
	}
}

// tearDownTestEnv 清理测试环境
func tearDownTestEnv(env *testEnv) {
	if env.mr != nil {
		env.mr.Close()
	}
}

// ========== 注册测试 ==========

func TestRegisterSuccess(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	resp := env.server.POST("/api/v1/player/register", map[string]string{
		"username": "testuser",
		"password": "testpass123",
	})

	resp.AssertStatus(http.StatusCreated)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.Equal(t, 0, result.Code)
	assert.Equal(t, "ok", result.Msg)
}

func TestRegisterDuplicateUsername(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 先注册一个用户
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "dupuser",
		"password": "testpass123",
	})

	// 再用相同用户名注册
	resp := env.server.POST("/api/v1/player/register", map[string]string{
		"username": "dupuser",
		"password": "testpass456",
	})

	resp.AssertStatus(http.StatusBadRequest)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.NotEqual(t, 0, result.Code)
}

func TestRegisterMissingParams(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 缺少密码
	resp := env.server.POST("/api/v1/player/register", map[string]string{
		"username": "nopass",
	})

	resp.AssertStatus(http.StatusBadRequest)

	// 缺少用户名
	resp = env.server.POST("/api/v1/player/register", map[string]string{
		"password": "nouser",
	})

	resp.AssertStatus(http.StatusBadRequest)
}

// ========== 登录测试 ==========

func TestLoginSuccess(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 先注册
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "loginuser",
		"password": "loginpass",
	})

	// 登录
	resp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "loginuser",
		"password": "loginpass",
	})

	resp.AssertStatus(http.StatusOK)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.Equal(t, 0, result.Code)
}

func TestLoginWrongPassword(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 先注册
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "wrongpw",
		"password": "correctpass",
	})

	// 用错误密码登录
	resp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "wrongpw",
		"password": "wrongpass",
	})

	resp.AssertStatus(http.StatusUnauthorized)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.NotEqual(t, 0, result.Code)
}

func TestLoginUserNotExist(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	resp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "nonexistent",
		"password": "somepass",
	})

	resp.AssertStatus(http.StatusUnauthorized)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.NotEqual(t, 0, result.Code)
}

// ========== 认证中间件测试（不依赖数据库） ==========

func TestAuthMiddlewareNoTokenUnit(t *testing.T) {
	logger := zap.NewNop()
	mw := middleware.AuthMiddleware(logger)

	app := testutil.NewTestApp()
	app.GET("/protected", mw, func(c *astra.Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{"msg": "ok"})
	})
	server := testutil.NewServer(t, app)

	// 无token访问
	resp := server.GET("/protected")
	resp.AssertStatus(http.StatusUnauthorized)

	var result map[string]interface{}
	json.Unmarshal(resp.Body(), &result)
	assert.Equal(t, float64(401), result["code"])
	assert.Equal(t, "未授权", result["msg"])
}

func TestAuthMiddlewareValidTokenUnit(t *testing.T) {
	logger := zap.NewNop()
	mw := middleware.AuthMiddleware(logger)

	app := testutil.NewTestApp()
	app.GET("/protected", mw, func(c *astra.Ctx) error {
		playerID, _ := c.Get(middleware.ContextKeyPlayerID)
		username, _ := c.Get(middleware.ContextKeyUsername)
		return c.JSON(http.StatusOK, map[string]string{
			"player_id": playerID.(string),
			"username":  username.(string),
		})
	})
	server := testutil.NewServer(t, app)

	// 生成有效token
	token, err := common.GenerateToken("player123", "testuser")
	require.NoError(t, err)

	resp := server.GET("/protected", map[string]string{
		"Authorization": "Bearer " + token,
	})

	resp.AssertStatus(http.StatusOK)

	var result map[string]interface{}
	json.Unmarshal(resp.Body(), &result)
	assert.Equal(t, "player123", result["player_id"])
	assert.Equal(t, "testuser", result["username"])
}

func TestAuthMiddlewareQueryToken(t *testing.T) {
	logger := zap.NewNop()
	mw := middleware.AuthMiddleware(logger)

	app := testutil.NewTestApp()
	app.GET("/ws", mw, func(c *astra.Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{"msg": "ok"})
	})
	server := testutil.NewServer(t, app)

	// 通过Query参数传递token（WebSocket场景）
	token, err := common.GenerateToken("player456", "wsuser")
	require.NoError(t, err)

	resp := server.GET("/ws?token=" + token)
	resp.AssertStatus(http.StatusOK)
}

func TestAuthMiddlewareInvalidToken(t *testing.T) {
	logger := zap.NewNop()
	mw := middleware.AuthMiddleware(logger)

	app := testutil.NewTestApp()
	app.GET("/protected", mw, func(c *astra.Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{"msg": "ok"})
	})
	server := testutil.NewServer(t, app)

	resp := server.GET("/protected", map[string]string{
		"Authorization": "Bearer invalidtoken123",
	})

	resp.AssertStatus(http.StatusUnauthorized)

	var result map[string]interface{}
	json.Unmarshal(resp.Body(), &result)
	assert.Equal(t, float64(401), result["code"])
}

func TestAuthMiddlewareExpiredToken(t *testing.T) {
	logger := zap.NewNop()
	mw := middleware.AuthMiddleware(logger)

	app := testutil.NewTestApp()
	app.GET("/protected", mw, func(c *astra.Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{"msg": "ok"})
	})
	server := testutil.NewServer(t, app)

	// 使用不同secret生成的token（签名不匹配，等同于无效）
	originalSecret := common.JWTSecret
	common.JWTSecret = []byte("temp-secret-for-test")
	token, _ := common.GenerateToken("player789", "expireduser")
	common.JWTSecret = originalSecret

	resp := server.GET("/protected", map[string]string{
		"Authorization": "Bearer " + token,
	})

	resp.AssertStatus(http.StatusUnauthorized)
}

// ========== 获取个人信息测试 ==========

func TestGetProfile(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 注册并登录
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "profileuser",
		"password": "profilepass",
	})

	loginResp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "profileuser",
		"password": "profilepass",
	})

	var loginResult struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	loginResp.AssertJSON(&loginResult)
	require.Equal(t, 0, loginResult.Code)

	// 获取个人信息
	resp := env.server.GET("/api/v1/player/profile", map[string]string{
		"Authorization": "Bearer " + loginResult.Data.Token,
	})

	resp.AssertStatus(http.StatusOK)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.Equal(t, 0, result.Code)
}

// ========== 修改密码测试 ==========

func TestChangePassword(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 注册并登录
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "changepw",
		"password": "oldpass",
	})

	loginResp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "changepw",
		"password": "oldpass",
	})

	var loginResult struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	loginResp.AssertJSON(&loginResult)
	require.Equal(t, 0, loginResult.Code)

	// 修改密码
	resp := env.server.POST("/api/v1/player/change-password", map[string]string{
		"old_password": "oldpass",
		"new_password": "newpass123",
	}, map[string]string{
		"Authorization": "Bearer " + loginResult.Data.Token,
	})

	resp.AssertStatus(http.StatusOK)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.Equal(t, 0, result.Code)
}

func TestChangePasswordWrongOld(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 注册并登录
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "wrongold",
		"password": "correctold",
	})

	loginResp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "wrongold",
		"password": "correctold",
	})

	var loginResult struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	loginResp.AssertJSON(&loginResult)

	// 用错误的旧密码修改
	resp := env.server.POST("/api/v1/player/change-password", map[string]string{
		"old_password": "wrongold",
		"new_password": "newpass123",
	}, map[string]string{
		"Authorization": "Bearer " + loginResult.Data.Token,
	})

	resp.AssertStatus(http.StatusBadRequest)
}

// ========== 排行榜测试 ==========

func TestLeaderboard(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	resp := env.server.GET("/api/v1/player/leaderboard")
	resp.AssertStatus(http.StatusOK)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.Equal(t, 0, result.Code)
}

// ========== 登出测试 ==========

func TestLogout(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 注册并登录
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "logoutuser",
		"password": "logoutpass",
	})

	loginResp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "logoutuser",
		"password": "logoutpass",
	})

	var loginResult struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	loginResp.AssertJSON(&loginResult)
	require.Equal(t, 0, loginResult.Code)

	// 登出
	resp := env.server.POST("/api/v1/player/logout", nil, map[string]string{
		"Authorization": "Bearer " + loginResult.Data.Token,
	})

	resp.AssertStatus(http.StatusOK)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.Equal(t, 0, result.Code)
}

// ========== 响应格式测试 ==========

func TestResponseFormat(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	resp := env.server.POST("/api/v1/player/register", map[string]string{
		"username": "formatuser",
		"password": "formatpass",
	})

	resp.AssertStatus(http.StatusCreated)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Body(), &result))

	// 验证统一响应格式包含code和msg字段
	assert.Contains(t, result, "code")
	assert.Contains(t, result, "msg")
	assert.Equal(t, float64(0), result["code"])
	assert.Equal(t, "ok", result["msg"])
	assert.Contains(t, result, "data")
}

// ========== 获取其他玩家信息测试 ==========

func TestGetPlayerByID(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 注册两个用户
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "user_a",
		"password": "pass_a",
	})

	regResp := env.server.POST("/api/v1/player/register", map[string]string{
		"username": "user_b",
		"password": "pass_b",
	})

	var regResult struct {
		Code int `json:"code"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	regResp.AssertJSON(&regResult)
	require.Equal(t, 0, regResult.Code)

	// 登录user_a
	loginResp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "user_a",
		"password": "pass_a",
	})

	var loginResult struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	loginResp.AssertJSON(&loginResult)

	// 获取user_b的信息
	resp := env.server.GET("/api/v1/player/"+regResult.Data.ID, map[string]string{
		"Authorization": "Bearer " + loginResult.Data.Token,
	})

	resp.AssertStatus(http.StatusOK)
}

// ========== 上下文键常量测试 ==========

func TestContextKeyConstants(t *testing.T) {
	assert.Equal(t, "player_id", middleware.ContextKeyPlayerID)
	assert.Equal(t, "username", middleware.ContextKeyUsername)
}

// ========== DeleteRedisKeys单元测试 ==========

func TestDeleteRedisKeys(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	logger := zap.NewNop()
	redisClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	pc := player.NewPlayerComponent(nil, redisClient, logger)

	// 设置一些键
	ctx := context.Background()
	redisClient.Set(ctx, "jwt_token:test_player", "token123", time.Hour)
	redisClient.Set(ctx, "online:test_player", "1", time.Hour)

	// 删除
	pc.DeleteRedisKeys(ctx, "test_player")

	// 验证已删除
	val, err := redisClient.Get(ctx, "jwt_token:test_player").Result()
	assert.Error(t, err) // 应该不存在
	assert.Empty(t, val)
}

// ========== 更新资料测试 ==========

func TestUpdateProfile(t *testing.T) {
	env := setupTestEnv(t)
	defer tearDownTestEnv(env)

	// 注册并登录
	env.server.POST("/api/v1/player/register", map[string]string{
		"username": "updateuser",
		"password": "updatepass",
	})

	loginResp := env.server.POST("/api/v1/player/login", map[string]string{
		"username": "updateuser",
		"password": "updatepass",
	})

	var loginResult struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	loginResp.AssertJSON(&loginResult)
	require.Equal(t, 0, loginResult.Code)

	// 更新资料
	resp := env.server.PUT("/api/v1/player/profile", map[string]string{
		"avatar":   "https://example.com/avatar.png",
		"nickname": "新昵称",
	}, map[string]string{
		"Authorization": "Bearer " + loginResult.Data.Token,
	})

	resp.AssertStatus(http.StatusOK)

	var result apiResponse
	resp.AssertJSON(&result)
	assert.Equal(t, 0, result.Code)
}
