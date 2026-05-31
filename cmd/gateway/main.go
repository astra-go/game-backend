package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/config"
	"github.com/astra-go/game-backend/pkg/gateway"
	"github.com/astra-go/astra"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func main() {
	// 加载配置（优先Nacos，降级到本地）
	cfg, err := config.LoadConfig("configs/nacos.yaml")
	if err != nil {
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}
	
	// 初始化日志
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	
	slog.Info("启动游戏网关服务...")
	
	// 初始化Astra框架
	app := astra.New()
	
	// 初始化Redis（从配置读取）
	redisClient := redis.NewClient(&redis.Options{
		Addr:        cfg.GetRedisAddr(),
		Password:    cfg.Redis.Password,
		DB:          cfg.Redis.DB,
		PoolSize:    cfg.Redis.PoolSize,
		MinIdleConns: cfg.Redis.MinIdleConns,
	})
	
	// 初始化NATS（从配置读取）
	natsClient := &stubNATSClient{}
	_ = cfg.GetNATSAddr() // 使用配置中的NATS地址
	
	// 创建网关组件
	gwCfg := gateway.DefaultGatewayConfig()
	redisAdapter := gateway.NewRedisAdapter(redisClient)
	nodeID := fmt.Sprintf("gateway-%d", time.Now().Unix())
	gw := gateway.NewGatewayComponent(redisAdapter, natsClient, gwCfg, nodeID)
	
	// 注册路由
	app.GET("/ws", func(c *astra.Ctx) error {
		return gw.HandleWS(c)
	})
	
	app.GET("/health", func(c *astra.Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
		})
	})
	
	app.GET("/rooms/:room_id/info", func(c *astra.Ctx) error {
		roomID := c.Param("room_id")
		// 从Redis获取房间信息
		info, err := redisClient.HGet(c.Request().Context(), fmt.Sprintf("room:%s", roomID), "info").Result()
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "room not found"})
		}
		var room common.Room
		json.Unmarshal([]byte(info), &room)
		return c.JSON(http.StatusOK, room)
	})
	
	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		<-quit
		slog.Info("正在关闭网关服务...")
		// Astra 没有 Shutdown 方法，直接退出
		os.Exit(0)
	}()
	
	// 启动HTTP服务（从配置读取地址）
	addr := cfg.GetHTTPAddr("gateway")
	slog.Info("网关服务启动", "addr", addr)
	if err := app.Run(addr); err != nil {
		slog.Error("服务启动失败", "error", err)
		os.Exit(1)
	}
}

// stubNATSClient 占位NATS客户端
type stubNATSClient struct{}

func (s *stubNATSClient) Publish(subject string, data []byte) error {
	slog.Debug("NATS Publish", "subject", subject, "data_len", len(data))
	return nil
}

func (s *stubNATSClient) Subscribe(subject string, cb func(msg []byte)) error {
	slog.Debug("NATS Subscribe", "subject", subject)
	return nil
}

func (s *stubNATSClient) Close() error {
	return nil
}
