package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/astra-go/astra"
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ========== Prometheus指标 ==========

var (
	wsConnectionsActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ws_connections_active",
			Help: "当前活跃WebSocket连接数",
		},
	)
	wsMessagesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ws_messages_total",
			Help: "WebSocket消息总数",
		},
		[]string{"direction", "msg_type"},
	)
	wsConnectionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ws_connection_duration_seconds",
			Help:    "WebSocket连接持续时间",
			Buckets: []float64{10, 30, 60, 300, 600, 1800, 3600},
		},
		[]string{"reason"},
	)
)

// ========== GatewayComponent ==========

// GatewayComponent 游戏网关组件
type GatewayComponent struct {
	app         *astra.App
	hashRing    *ConsistentHash
	router      *MessageRouter
	nodeID      string
	upgrader    websocket.Upgrader
	rooms       sync.Map // roomID -> *RoomSession
	connections sync.Map // connID -> *WSConnection
	redis       RedisClient
	nats        NATSClient
	config      GatewayConfig
	quitCh      chan struct{}  // 退出信号
	wg          sync.WaitGroup // 等待 goroutine 退出
}

// GatewayConfig 网关配置
type GatewayConfig struct {
	ReadBufferSize    int
	WriteBufferSize   int
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	MaxMessageSize    int64
	ReconnectWindow   time.Duration
}

// DefaultGatewayConfig 默认配置
func DefaultGatewayConfig() GatewayConfig {
	return GatewayConfig{
		ReadBufferSize:    1024,
		WriteBufferSize:   1024,
		HeartbeatInterval: 30 * time.Second,
		HeartbeatTimeout:  90 * time.Second,
		MaxMessageSize:    4096,
		ReconnectWindow:   5 * time.Minute,
	}
}

// RedisClient Redis接口
type RedisClient interface {
	Get(key string) (string, error)
	Set(key string, value interface{}, expiry time.Duration) error
	Del(key string) error
	HGet(key, field string) (string, error)
	HSet(key, field string, value interface{}) error
	HDel(key, field string) error
	Expire(key string, expiry time.Duration) error
}

// NATSClient NATS接口
type NATSClient interface {
	Publish(subject string, data []byte) error
	Subscribe(subject string, cb func(msg []byte)) error
	Close() error
}

// NewGatewayComponent 创建网关组件
func NewGatewayComponent(redis RedisClient, nats NATSClient, cfg GatewayConfig, nodeID string) *GatewayComponent {
	g := &GatewayComponent{
		redis:  redis,
		nats:   nats,
		config: cfg,
		nodeID: nodeID,
		app:    astra.New(),
		quitCh: make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  cfg.ReadBufferSize,
			WriteBufferSize: cfg.WriteBufferSize,
			CheckOrigin: func(r *http.Request) bool {
				return true // 生产环境应校验Origin
			},
		},
	}

	// 初始化一致性哈希环（150个虚拟节点）
	g.hashRing = NewConsistentHash(150, nil)
	// 注册当前网关节点
	g.hashRing.Add(nodeID)

	// 初始化消息路由器
	g.router = NewMessageRouter(g, nodeID)

	return g
}

// Init 初始化组件
func (g *GatewayComponent) Init() error {
	slog.Info("GatewayComponent 初始化")

	// 注册路由
	g.app.GET("/ws", g.HandleWS)

	// 启动消息路由器
	g.router.Start()

	// 订阅NATS房间消息
	if g.nats != nil {
		g.nats.Subscribe("room.*.broadcast", g.handleRoomBroadcast)
		g.nats.Subscribe("room.*.player_leave", g.handlePlayerLeave)
		// 订阅跨节点转发消息
		g.nats.Subscribe(fmt.Sprintf("gateway.%s.forward", g.nodeID), g.router.HandleForwardedMessage)
	}

	// 启动连接超时清理协程
	g.wg.Add(1)
	go g.cleanupStaleConnections()

	return nil
}

// Close 关闭组件（优雅退出）
func (g *GatewayComponent) Close() error {
	slog.Info("GatewayComponent 开始关闭...")

	// 通知 goroutine 退出
	close(g.quitCh)

	// 等待所有 goroutine 退出（最多等待 10 秒）
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("GatewayComponent 所有 goroutine 已退出")
	case <-time.After(10 * time.Second):
		slog.Warn("GatewayComponent 等待 goroutine 退出超时")
	}

	// 关闭所有 WebSocket 连接
	g.connections.Range(func(key, value interface{}) bool {
		wsc := value.(*WSConnection)
		g.closeConnection(wsc)
		return true
	})

	// 关闭 NATS
	if g.nats != nil {
		g.nats.Close()
	}

	return nil
}

// cleanupStaleConnections 定期清理超时连接
func (g *GatewayComponent) cleanupStaleConnections() {
	defer g.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			g.connections.Range(func(key, value interface{}) bool {
				wsc := value.(*WSConnection)
				wsc.mu.Lock()
				if now.Sub(wsc.lastPong) > 3*time.Minute {
					wsc.mu.Unlock()
					g.closeConnection(wsc)
				} else {
					wsc.mu.Unlock()
				}
				return true
			})
		case <-g.quitCh:
			slog.Info("cleanupStaleConnections goroutine 已退出")
			return
		}
	}
}

// Run 启动网关服务
func (g *GatewayComponent) Run(addr string) error {
	slog.Info("网关服务启动", "addr", addr)
	return g.app.Run(addr)
}

// ========== WebSocket连接管理 ==========

// WSConnection WebSocket连接包装
type WSConnection struct {
	conn     *websocket.Conn
	connID   string
	playerID string
	roomID   string
	sendCh   chan []byte
	quitCh   chan struct{}
	lastPing time.Time
	lastPong time.Time
	mu       sync.Mutex
}

// HandleWS WebSocket升级处理
func (g *GatewayComponent) HandleWS(c *astra.Ctx) error {
	// 从URL参数获取token和playerID（使用真实的 Astra API）
	playerID := c.Query("player_id")
	token := c.Query("token")
	roomID := c.Query("room_id")

	// 验证JWT token
	if token == "" || playerID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token or player_id"})
	}

	// 使用JWT验证token
	claims, err := common.ValidateToken(token)
	if err != nil {
		slog.Error("JWT验证失败", "error", err, "player_id", playerID)
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token", "details": err.Error()})
	}

	// 验证token中的player_id是否匹配
	if claims.PlayerID != playerID {
		slog.Error("token中的player_id不匹配", "token_player_id", claims.PlayerID, "query_player_id", playerID)
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "token player_id mismatch"})
	}

	slog.Info("JWT验证成功", "player_id", playerID, "username", claims.Username)

	// 验证reconnect token
	reconnectToken := c.Request().URL.Query().Get("reconnect_token")
	if reconnectToken != "" {
		valid, err := g.validateReconnectToken(playerID, reconnectToken)
		if err == nil && valid {
			slog.Info("玩家重连", "player_id", playerID, "room_id", roomID)
			// 恢复会话逻辑在OnConnect中处理
		}
	}

	// 升级WebSocket（使用真实的 Astra API）
	conn, err := g.upgrader.Upgrade(c.Writer(), c.Request(), nil)
	if err != nil {
		slog.Error("WebSocket升级失败", "error", err)
		return err
	}

	// 创建连接对象
	connID := generateConnID()
	wsc := &WSConnection{
		conn:     conn,
		connID:   connID,
		playerID: playerID,
		roomID:   roomID,
		sendCh:   make(chan []byte, 256),
		quitCh:   make(chan struct{}),
		lastPing: time.Now(),
		lastPong: time.Now(),
	}

	// 注册连接
	g.connections.Store(connID, wsc)
	wsConnectionsActive.Inc()

	// 设置读取限制
	conn.SetReadLimit(g.config.MaxMessageSize)
	conn.SetReadDeadline(time.Now().Add(g.config.HeartbeatTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(g.config.HeartbeatTimeout))
		wsc.lastPong = time.Now()
		wsMessagesTotal.WithLabelValues("in", "pong").Inc()
		return nil
	})

	// 启动读写goroutine
	go g.readPump(wsc)
	go g.writePump(wsc)

	// 发送连接成功消息
	welcome := common.WSMessage{
		Type:   common.WSMsgReconnect,
		RoomID: roomID,
		Data:   map[string]interface{}{"conn_id": connID},
	}
	welcomeBytes, _ := json.Marshal(welcome)
	wsc.sendCh <- welcomeBytes

	slog.Info("WebSocket连接建立", "conn_id", connID, "player_id", playerID)
	return nil
}

// readPump 读取消息
func (g *GatewayComponent) readPump(wsc *WSConnection) {
	defer func() {
		g.closeConnection(wsc)
	}()

	for {
		_, msg, err := wsc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Error("WebSocket读取错误", "conn_id", wsc.connID, "error", err)
			}
			break
		}

		wsMessagesTotal.WithLabelValues("in", "text").Inc()

		// 解析消息
		var wsMsg common.WSMessage
		if err := json.Unmarshal(msg, &wsMsg); err != nil {
			slog.Warn("消息解析失败", "conn_id", wsc.connID, "error", err)
			g.sendError(wsc, "invalid message format")
			continue
		}

		// 处理消息
		g.handleWSMessage(wsc, &wsMsg)
	}
}

// writePump 写入消息
func (g *GatewayComponent) writePump(wsc *WSConnection) {
	ticker := time.NewTicker(g.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case msg := <-wsc.sendCh:
			wsc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := wsc.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				slog.Error("WebSocket写入失败", "conn_id", wsc.connID, "error", err)
				return
			}
			wsMessagesTotal.WithLabelValues("out", "text").Inc()

		case <-ticker.C:
			// 心跳
			wsc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := wsc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				slog.Error("心跳发送失败", "conn_id", wsc.connID, "error", err)
				return
			}
			wsMessagesTotal.WithLabelValues("out", "ping").Inc()

		case <-wsc.quitCh:
			return
		}
	}
}

// handleWSMessage 处理WebSocket消息
func (g *GatewayComponent) handleWSMessage(wsc *WSConnection, msg *common.WSMessage) {
	switch msg.Type {
	case common.WSMsgJoin:
		g.handleJoin(wsc, msg)
	case common.WSMsgLeave:
		g.handleLeave(wsc, msg)
	case common.WSMsgInput:
		g.handleInput(wsc, msg)
	case common.WSMsgHeartbeat:
		g.handleHeartbeat(wsc, msg)
	case common.WSMsgReconnect:
		g.handleReconnect(wsc, msg)
	default:
		slog.Warn("未知消息类型", "type", msg.Type, "conn_id", wsc.connID)
		g.sendError(wsc, "unknown message type")
	}
}

// handleJoin 处理加入房间
func (g *GatewayComponent) handleJoin(wsc *WSConnection, msg *common.WSMessage) {
	if msg.RoomID == "" {
		g.sendError(wsc, "room_id is required")
		return
	}

	// 使用消息路由器确定房间应该路由到哪个网关节点
	result, err := g.router.RouteMessage(msg.RoomID, msg)
	if err != nil {
		g.sendError(wsc, err.Error())
		return
	}

	// 如果需要转发到其他节点，路由器会自动处理
	// 当前节点仍然处理本地连接的加入逻辑
	slog.Info("房间路由", "room_id", msg.RoomID, "target_node", result.TargetNode, "is_local", result.IsLocal)

	wsc.roomID = msg.RoomID
	wsc.lastPing = time.Now()

	// 更新Redis玩家状态
	if g.redis != nil {
		g.redis.HSet(fmt.Sprintf("room:%s:members", msg.RoomID), wsc.playerID, "online")
		g.redis.Expire(fmt.Sprintf("room:%s", msg.RoomID), 1*time.Hour)
	}

	// 通知房间服务
	if g.nats != nil {
		data, _ := json.Marshal(map[string]string{
			"player_id": wsc.playerID,
			"room_id":   msg.RoomID,
			"conn_id":   wsc.connID,
		})
		g.nats.Publish(fmt.Sprintf("room.%s.player_join", msg.RoomID), data)
	}

	// 回复客户端
	resp := common.WSMessage{
		Type:   common.WSMsgJoin,
		RoomID: msg.RoomID,
		Data:   map[string]interface{}{"status": "joined"},
	}
	g.sendMessage(wsc, &resp)

	slog.Info("玩家加入房间", "player_id", wsc.playerID, "room_id", msg.RoomID)
}

// handleLeave 处理离开房间
func (g *GatewayComponent) handleLeave(wsc *WSConnection, msg *common.WSMessage) {
	if wsc.roomID == "" {
		return
	}

	// 更新Redis
	if g.redis != nil {
		g.redis.HDel(fmt.Sprintf("room:%s:members", wsc.roomID), wsc.playerID)
	}

	// 通知房间服务
	if g.nats != nil {
		data, _ := json.Marshal(map[string]string{
			"player_id": wsc.playerID,
			"room_id":   wsc.roomID,
		})
		g.nats.Publish(fmt.Sprintf("room.%s.player_leave", wsc.roomID), data)
	}

	wsc.roomID = ""

	slog.Info("玩家离开房间", "player_id", wsc.playerID, "room_id", msg.RoomID)
}

// handleInput 处理玩家输入
func (g *GatewayComponent) handleInput(wsc *WSConnection, msg *common.WSMessage) {
	if wsc.roomID == "" {
		g.sendError(wsc, "not in a room")
		return
	}

	// 转发给房间服务
	if g.nats != nil {
		data, _ := json.Marshal(msg)
		g.nats.Publish(fmt.Sprintf("room.%s.input", wsc.roomID), data)
	}

	wsMessagesTotal.WithLabelValues("in", "input").Inc()
}

// handleHeartbeat 处理心跳
func (g *GatewayComponent) handleHeartbeat(wsc *WSConnection, msg *common.WSMessage) {
	wsc.lastPing = time.Now()

	// 更新Redis心跳时间
	if g.redis != nil {
		g.redis.HSet("player:heartbeat", wsc.playerID, time.Now().Unix())
	}

	resp := common.WSMessage{
		Type: common.WSMsgHeartbeat,
		Data: map[string]interface{}{"timestamp": time.Now().UnixMilli()},
	}
	g.sendMessage(wsc, &resp)
}

// handleReconnect 处理重连
func (g *GatewayComponent) handleReconnect(wsc *WSConnection, msg *common.WSMessage) {
	// 查询Redis中的会话快照
	if g.redis != nil {
		snapshot, err := g.redis.Get(fmt.Sprintf("session:%s", wsc.playerID))
		if err == nil && snapshot != "" {
			// 恢复会话
			resp := common.WSMessage{
				Type:   common.WSMsgReconnect,
				RoomID: wsc.roomID,
				Data:   json.RawMessage(snapshot),
			}
			g.sendMessage(wsc, &resp)
			return
		}
	}

	g.sendError(wsc, "no session found for reconnection")
}

// ========== 辅助方法 ==========

func (g *GatewayComponent) sendMessage(wsc *WSConnection, msg *common.WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("消息序列化失败", "error", err)
		return
	}
	select {
	case wsc.sendCh <- data:
	default:
		slog.Warn("发送队列满", "conn_id", wsc.connID)
	}
}

func (g *GatewayComponent) sendError(wsc *WSConnection, errMsg string) {
	msg := common.WSMessage{
		Type: common.WSMsgError,
		Data: map[string]string{"error": errMsg},
	}
	g.sendMessage(wsc, &msg)
}

func (g *GatewayComponent) closeConnection(wsc *WSConnection) {
	wsc.conn.Close()
	close(wsc.quitCh)
	g.connections.Delete(wsc.connID)
	wsConnectionsActive.Dec()

	// 通知房间玩家离开
	if wsc.roomID != "" {
		g.handleLeave(wsc, &common.WSMessage{RoomID: wsc.roomID})
	}

	slog.Info("WebSocket连接关闭", "conn_id", wsc.connID, "player_id", wsc.playerID)
}

func (g *GatewayComponent) validateReconnectToken(playerID, token string) (bool, error) {
	if g.redis == nil {
		return false, fmt.Errorf("redis not available")
	}
	storedToken, err := g.redis.Get(fmt.Sprintf("reconnect:%s", playerID))
	if err != nil {
		return false, err
	}
	return storedToken == token, nil
}

func (g *GatewayComponent) handleRoomBroadcast(data []byte) {
	// NATS回调：广播房间消息给所有连接的客户端
	var msg common.WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		slog.Error("广播消息解析失败", "error", err)
		return
	}

	// 查找房间内所有连接并发送
	g.connections.Range(func(key, value interface{}) bool {
		wsc := value.(*WSConnection)
		if wsc.roomID == msg.RoomID {
			g.sendMessage(wsc, &msg)
		}
		return true
	})
}

func (g *GatewayComponent) handlePlayerLeave(data []byte) {
	// NATS回调：通知指定玩家离开
	var payload map[string]string
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	playerID := payload["player_id"]

	g.connections.Range(func(key, value interface{}) bool {
		wsc := value.(*WSConnection)
		if wsc.playerID == playerID {
			msg := common.WSMessage{
				Type:   common.WSMsgLeave,
				RoomID: wsc.roomID,
			}
			g.sendMessage(wsc, &msg)
		}
		return true
	})
}

func generateConnID() string {
	return fmt.Sprintf("conn_%d", time.Now().UnixNano())
}

// ========== 节点管理方法 ==========

// AddGatewayNode 添加网关节点到哈希环
func (g *GatewayComponent) AddGatewayNode(nodeID string) {
	g.hashRing.Add(nodeID)
	slog.Info("网关节点已添加", "node_id", nodeID, "total_nodes", g.hashRing.Size())
}

// RemoveGatewayNode 从哈希环移除网关节点
func (g *GatewayComponent) RemoveGatewayNode(nodeID string) {
	g.hashRing.Remove(nodeID)
	slog.Info("网关节点已移除", "node_id", nodeID, "total_nodes", g.hashRing.Size())
}

// GetRoomNode 获取房间对应的网关节点
func (g *GatewayComponent) GetRoomNode(roomID string) string {
	return g.hashRing.Get(roomID)
}

// GetRoomNodes 获取房间对应的N个副本节点（用于高可用）
func (g *GatewayComponent) GetRoomNodes(roomID string, n int) []string {
	return g.hashRing.GetN(roomID, n)
}

// GetAllNodes 获取所有网关节点列表
func (g *GatewayComponent) GetAllNodes() []string {
	return g.hashRing.Nodes()
}
