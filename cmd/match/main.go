package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/astra-go/astra"
	"github.com/astra-go/astra/log"
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/config"
	"github.com/astra-go/game-backend/pkg/match"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 加载配置（优先Nacos，降级到本地）
	cfg, err := config.LoadConfig("configs/nacos.yaml")
	if err != nil {
		slog.Error("加载配置失败", "error", err)
		os.Exit(1)
	}
	
	logger:= log.Default()
	
	slog.Info("启动匹配服务...")
	
	app := astra.New()
	
	// Redis（从配置读取）
	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.GetRedisAddr(),
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		PoolSize:     cfg.Redis.PoolSize,
		MinIdleConns: cfg.Redis.MinIdleConns,
	})
	
	// NATS（从配置读取）
	natsClient := &stubNATSClient{}
	_ = cfg.GetNATSAddr()
	
	// 匹配组件
	matchCfg := match.DefaultMatchConfig()
	matchComp := match.NewMatchComponent(redisClient, natsClient, logger, matchCfg)
	matchComp.Init()
	
	// 路由
	app.POST("/match/enqueue", func(c *astra.Ctx) error {
		var req struct {
			PlayerID string `json:"player_id"`
			Mode     string `json:"mode"`
			MMR      int32  `json:"mmr"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		
		err := matchComp.Enqueue(req.PlayerID, common.GameMode(req.Mode), req.MMR)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		
		return c.JSON(http.StatusOK, map[string]string{"status": "enqueued"})
	})
	
	app.POST("/match/dequeue", func(c *astra.Ctx) error {
		var req struct {
			PlayerID string `json:"player_id"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		
		err := matchComp.Dequeue(req.PlayerID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		
		return c.JSON(http.StatusOK, map[string]string{"status": "dequeued"})
	})
	
	app.GET("/match/status/:player_id", func(c *astra.Ctx) error {
		playerID := c.Param("player_id")
		
		// 查询玩家匹配状态
		processingKey := "match:processing"
		mode, err := redisClient.HGet(c.Request().Context(), processingKey, playerID).Result()
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not in queue"})
		}
		
		return c.JSON(http.StatusOK, map[string]interface{}{
			"player_id": playerID,
			"mode":      mode,
			"status":    "waiting",
		})
	})
	
	app.GET("/health", func(c *astra.Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	
	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		<-quit
		slog.Info("正在关闭匹配服务...")
		// Astra 没有 Shutdown 方法，直接退出
		os.Exit(0)
	}()
	
	// 启动HTTP服务（从配置读取地址）
	addr := cfg.GetHTTPAddr("match")
	slog.Info("匹配服务启动", "addr", addr)
	if err := app.Run(addr); err != nil {
		slog.Error("服务启动失败", "error", err)
		os.Exit(1)
	}
}

type stubNATSClient struct{}
func (s *stubNATSClient) Publish(subject string, data []byte) error { return nil }
func (s *stubNATSClient) Subscribe(subject string, cb func(msg []byte)) error { return nil }
func (s *stubNATSClient) Request(subject string, data []byte, timeout time.Duration) ([]byte, error) { return nil, nil }
func (s *stubNATSClient) Close() error { return nil }
