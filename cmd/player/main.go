package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/astra-go/astra"
	"github.com/astra-go/astra/log"
	"github.com/astra-go/game-backend/pkg/api"
	"github.com/astra-go/game-backend/pkg/config"
	"github.com/astra-go/game-backend/pkg/friend"
	"github.com/astra-go/game-backend/pkg/middleware"
	"github.com/astra-go/game-backend/pkg/player"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	// 加载配置（优先Nacos，降级到本地）
	cfg, err := config.LoadConfig("configs/nacos.yaml")
	if err != nil {
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}

	logger := log.Default()

	slog.Info("启动玩家服务...")

	// 初始化数据库（从配置读取DSN）
	db, err := gorm.Open(mysql.Open(cfg.GetDSN()), &gorm.Config{})
	if err != nil {
		slog.Error("数据库连接失败", "error", err)
		os.Exit(1)
	}

	// Redis（从配置读取）
	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.GetRedisAddr(),
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		PoolSize:     cfg.Redis.PoolSize,
		MinIdleConns: cfg.Redis.MinIdleConns,
	})

	// 好友组件（暂时传nil给NATS，后续可扩展）
	friendComp := friend.NewFriendComponent(db, redisClient, nil, logger)
	err = friendComp.Init()
	if err != nil {
		slog.Error("好友组件初始化失败", "error", err)
		os.Exit(1)
	}

	// 玩家组件
	playerComp := player.NewPlayerComponent(db, redisClient, logger, friendComp)
	err = playerComp.Init()
	if err != nil {
		slog.Error("玩家组件初始化失败", "error", err)
		os.Exit(1)
	}

	// Astra应用
	app := astra.New()

	// 注册JWT认证中间件（全局，但只有需认证的路由会校验）
	// 注意：这里不在全局注册，由各路由按需引入

	// 创建玩家API并注册路由
	playerAPI := api.NewPlayerAPI(playerComp, logger)
	playerAPI.RegisterRoutes(app)

	// 健康检查
	app.GET("/health", func(c *astra.Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		slog.Info("正在关闭玩家服务...")
		os.Exit(0)
	}()

	// 启动HTTP服务（从配置读取地址）
	addr := cfg.GetHTTPAddr("player")
	slog.Info("玩家服务启动", "addr", addr)
	if err := app.Run(addr); err != nil {
		slog.Error("服务启动失败", "error", err)
		os.Exit(1)
	}

	// 保持引用（避免未使用导入错误）
	_ = middleware.AuthMiddleware
}
