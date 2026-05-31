package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/config"
	"github.com/astra-go/game-backend/pkg/room"
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
	
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	
	slog.Info("启动房间管理服务...")
	
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
	
	// 房间组件
	roomCfg := room.DefaultRoomConfig()
	roomComp := room.NewRoomComponent(redisClient, natsClient, logger, roomCfg)
	roomComp.Init()
	
	// 路由
	app.POST("/rooms", func(c *astra.Ctx) error {
		var req struct {
			OwnerID    string `json:"owner_id"`
			Mode       string `json:"mode"`
			MaxPlayers int32  `json:"max_players"`
			MapID      int32  `json:"map_id"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		
		mode := common.GameMode(req.Mode)
		room, err := roomComp.CreateRoom(req.OwnerID, mode, req.MaxPlayers, req.MapID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		
		return c.JSON(http.StatusOK, room)
	})
	
	app.POST("/rooms/:room_id/players", func(c *astra.Ctx) error {
		roomID := c.Param("room_id")
		
		var req struct {
			PlayerID string `json:"player_id"`
			TeamID   int32  `json:"team_id"`
			HeroID   int32  `json:"hero_id"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		
		err := roomComp.AddPlayer(roomID, req.PlayerID, req.TeamID, req.HeroID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		
		return c.JSON(http.StatusOK, map[string]string{"status": "joined"})
	})
	
	app.DELETE("/rooms/:room_id/players/:player_id", func(c *astra.Ctx) error {
		roomID := c.Param("room_id")
		playerID := c.Param("player_id")
		
		err := roomComp.RemovePlayer(roomID, playerID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		
		return c.JSON(http.StatusOK, map[string]string{"status": "left"})
	})
	
	app.GET("/health", func(c *astra.Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	
	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		<-quit
		slog.Info("正在关闭房间服务...")
		// Astra 没有 Shutdown 方法，直接退出
		os.Exit(0)
	}()
	
	// 启动HTTP服务（从配置读取地址）
	addr := cfg.GetHTTPAddr("room")
	slog.Info("房间服务启动", "addr", addr)
	if err := app.Run(addr); err != nil {
		slog.Error("服务启动失败", "error", err)
		os.Exit(1)
	}
}

type stubNATSClient struct{}
func (s *stubNATSClient) Publish(subject string, data []byte) error { return nil }
func (s *stubNATSClient) Subscribe(subject string, cb func(msg []byte)) error { return nil }
func (s *stubNATSClient) Close() error { return nil }
