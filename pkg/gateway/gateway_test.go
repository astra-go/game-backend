package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

// ========== Mock 实现 ==========

// mockRedisClient Mock Redis客户端
type mockRedisClient struct {
	mu     sync.RWMutex
	data   map[string]string
	hashes map[string]map[string]string
	err    error // 可设置以模拟错误
}

func newMockRedisClient() *mockRedisClient {
	return &mockRedisClient{
		data:   make(map[string]string),
		hashes: make(map[string]map[string]string),
	}
}

func (m *mockRedisClient) Get(key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return "", m.err
	}
	v, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("key not found: %s", key)
	}
	return v, nil
}

func (m *mockRedisClient) Set(key string, value interface{}, expiry time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.data[key] = fmt.Sprintf("%v", value)
	return nil
}

func (m *mockRedisClient) Del(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	delete(m.data, key)
	return nil
}

func (m *mockRedisClient) HGet(key, field string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return "", m.err
	}
	h, ok := m.hashes[key]
	if !ok {
		return "", fmt.Errorf("hash key not found: %s", key)
	}
	v, ok := h[field]
	if !ok {
		return "", fmt.Errorf("field not found: %s", field)
	}
	return v, nil
}

func (m *mockRedisClient) HSet(key, field string, value interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if _, ok := m.hashes[key]; !ok {
		m.hashes[key] = make(map[string]string)
	}
	m.hashes[key][field] = fmt.Sprintf("%v", value)
	return nil
}

func (m *mockRedisClient) HDel(key, field string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if h, ok := m.hashes[key]; ok {
		delete(h, field)
	}
	return nil
}

func (m *mockRedisClient) Expire(key string, expiry time.Duration) error {
	return nil // mock不需要实际过期
}

// mockNATSClient Mock NATS客户端
type mockNATSClient struct {
	mu         sync.RWMutex
	published  []publishedMsg
	subscribed map[string]func(msg []byte)
	closed     bool
}

type publishedMsg struct {
	subject string
	data    []byte
}

func newMockNATSClient() *mockNATSClient {
	return &mockNATSClient{
		subscribed: make(map[string]func(msg []byte)),
	}
}

func (m *mockNATSClient) Publish(subject string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, publishedMsg{subject: subject, data: data})
	return nil
}

func (m *mockNATSClient) Subscribe(subject string, cb func(msg []byte)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribed[subject] = cb
	return nil
}

func (m *mockNATSClient) Close() error {
	m.closed = true
	return nil
}

func (m *mockNATSClient) getPublished() []publishedMsg {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]publishedMsg, len(m.published))
	copy(cp, m.published)
	return cp
}

// ========== 辅助函数 ==========

// 生成有效的JWT token用于测试
func generateTestToken(playerID, username string) string {
	token, _ := common.GenerateToken(playerID, username)
	return token
}

// 创建带WebSocket升级的httptest服务器
func setupTestServer(g *GatewayComponent) *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟astra.Ctx行为：直接用upgrader升级
		playerID := r.URL.Query().Get("player_id")
		token := r.URL.Query().Get("token")
		roomID := r.URL.Query().Get("room_id")

		if token == "" || playerID == "" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing token or player_id"})
			return
		}

		claims, err := common.ValidateToken(token)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid token", "details": err.Error()})
			return
		}

		if claims.PlayerID != playerID {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "token player_id mismatch"})
			return
		}

		// 升级WebSocket
		conn, err := g.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

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

		g.connections.Store(connID, wsc)
		wsConnectionsActive.Inc()

		conn.SetReadLimit(g.config.MaxMessageSize)
		conn.SetReadDeadline(time.Now().Add(g.config.HeartbeatTimeout))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(g.config.HeartbeatTimeout))
			wsc.lastPong = time.Now()
			return nil
		})

		go g.readPump(wsc)
		go g.writePump(wsc)

		// 发送欢迎消息
		welcome := common.WSMessage{
			Type:   common.WSMsgReconnect,
			RoomID: roomID,
			Data:   map[string]interface{}{"conn_id": connID},
		}
		welcomeBytes, _ := json.Marshal(welcome)
		wsc.sendCh <- welcomeBytes
	})

	return httptest.NewServer(handler)
}

// 连接WebSocket并返回连接
func connectWS(t *testing.T, server *httptest.Server, playerID, token, roomID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	u := fmt.Sprintf("%s/ws?player_id=%s&token=%s&room_id=%s", wsURL, playerID, token, roomID)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("WebSocket连接失败: %v", err)
	}
	return conn
}

// 读取JSON消息
func readWSMessage(t *testing.T, conn *websocket.Conn) common.WSMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	var wsMsg common.WSMessage
	if err := json.Unmarshal(msg, &wsMsg); err != nil {
		t.Fatalf("解析消息失败: %v, raw: %s", err, string(msg))
	}
	return wsMsg
}

// ========== 测试用例 ==========

func TestNewGatewayComponent(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	cfg := DefaultGatewayConfig()

	g := NewGatewayComponent(redis, nats, cfg, "test-node-1")

	assert.NotNil(t, g)
	assert.Equal(t, redis, g.redis)
	assert.Equal(t, nats, g.nats)
	assert.Equal(t, cfg.HeartbeatInterval, g.config.HeartbeatInterval)
	assert.Equal(t, cfg.HeartbeatTimeout, g.config.HeartbeatTimeout)
	assert.Equal(t, cfg.MaxMessageSize, g.config.MaxMessageSize)
	assert.NotNil(t, g.upgrader)
}

func TestDefaultGatewayConfig(t *testing.T) {
	cfg := DefaultGatewayConfig()

	assert.Equal(t, 1024, cfg.ReadBufferSize)
	assert.Equal(t, 1024, cfg.WriteBufferSize)
	assert.Equal(t, 30*time.Second, cfg.HeartbeatInterval)
	assert.Equal(t, 90*time.Second, cfg.HeartbeatTimeout)
	assert.Equal(t, int64(4096), cfg.MaxMessageSize)
	assert.Equal(t, 5*time.Minute, cfg.ReconnectWindow)
}

func TestGatewayConfig_Custom(t *testing.T) {
	tests := []struct {
		name              string
		config            GatewayConfig
		expectInterval    time.Duration
		expectTimeout     time.Duration
		expectMaxMsgSize  int64
	}{
		{
			name:             "short_heartbeat",
			config:           GatewayConfig{HeartbeatInterval: 10 * time.Second, HeartbeatTimeout: 30 * time.Second, MaxMessageSize: 2048},
			expectInterval:   10 * time.Second,
			expectTimeout:    30 * time.Second,
			expectMaxMsgSize: 2048,
		},
		{
			name:             "long_heartbeat",
			config:           GatewayConfig{HeartbeatInterval: 60 * time.Second, HeartbeatTimeout: 180 * time.Second, MaxMessageSize: 8192},
			expectInterval:   60 * time.Second,
			expectTimeout:    180 * time.Second,
			expectMaxMsgSize: 8192,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redis := newMockRedisClient()
			nats := newMockNATSClient()
			g := NewGatewayComponent(redis, nats, tt.config, "test-node-1")
			assert.Equal(t, tt.expectInterval, g.config.HeartbeatInterval)
			assert.Equal(t, tt.expectTimeout, g.config.HeartbeatTimeout)
			assert.Equal(t, tt.expectMaxMsgSize, g.config.MaxMessageSize)
		})
	}
}

func TestHandleWS_MissingTokenOrPlayerID(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantCode int
		wantErr  string
	}{
		{
			name:     "missing_both",
			url:      "/ws",
			wantCode: http.StatusUnauthorized,
			wantErr:  "missing token or player_id",
		},
		{
			name:     "missing_token",
			url:      "/ws?player_id=p1",
			wantCode: http.StatusUnauthorized,
			wantErr:  "missing token or player_id",
		},
		{
			name:     "missing_player_id",
			url:      "/ws?token=abc",
			wantCode: http.StatusUnauthorized,
			wantErr:  "missing token or player_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redis := newMockRedisClient()
			nats := newMockNATSClient()
			g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
			server := setupTestServer(g)
			defer server.Close()

			resp, err := http.Get(server.URL + tt.url)
			assert.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, tt.wantCode, resp.StatusCode)

			var body map[string]string
			json.NewDecoder(resp.Body).Decode(&body)
			assert.Equal(t, tt.wantErr, body["error"])
		})
	}
}

func TestHandleWS_InvalidJWT(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		wantCode int
		wantErr  string
	}{
		{
			name:     "invalid_token",
			token:    "invalid.jwt.token",
			wantCode: http.StatusUnauthorized,
			wantErr:  "invalid token",
		},
		{
			name:     "empty_token",
			token:    "",
			wantCode: http.StatusUnauthorized,
			wantErr:  "missing token or player_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redis := newMockRedisClient()
			nats := newMockNATSClient()
			g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
			server := setupTestServer(g)
			defer server.Close()

			url := fmt.Sprintf("%s/ws?player_id=p1&token=%s", server.URL, tt.token)
			resp, err := http.Get(url)
			assert.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, tt.wantCode, resp.StatusCode)
		})
	}
}

func TestHandleWS_TokenPlayerIDMismatch(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	// 生成player1的token，但用player2的ID连接
	token := generateTestToken("player1", "user1")
	url := fmt.Sprintf("%s/ws?player_id=player2&token=%s", server.URL, token)
	resp, err := http.Get(url)
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "token player_id mismatch", body["error"])
}

func TestHandleWS_SuccessfulConnection(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")
	defer conn.Close()

	// 应该收到欢迎消息
	msg := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgReconnect, msg.Type)
	assert.Equal(t, "room1", msg.RoomID)

	// 验证连接已注册
	count := 0
	g.connections.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 1, count)
}

func TestHandleWS_MultipleConnections(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	conns := make([]*websocket.Conn, 3)
	for i := 0; i < 3; i++ {
		pid := fmt.Sprintf("player%d", i)
		token := generateTestToken(pid, fmt.Sprintf("user%d", i))
		conns[i] = connectWS(t, server, pid, token, "room1")
		defer conns[i].Close()

		// 消费欢迎消息
		readWSMessage(t, conns[i])
	}

	count := 0
	g.connections.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 3, count)
}

func TestHandleJoin(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "")
	defer conn.Close()

	// 消费欢迎消息
	readWSMessage(t, conn)

	// 发送join消息
	joinMsg := common.WSMessage{
		Type:   common.WSMsgJoin,
		RoomID: "room123",
	}
	data, _ := json.Marshal(joinMsg)
	err := conn.WriteMessage(websocket.TextMessage, data)
	assert.NoError(t, err)

	// 读取响应
	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgJoin, resp.Type)
	assert.Equal(t, "room123", resp.RoomID)

	// 验证Redis写入
	redis.mu.RLock()
	_, ok := redis.hashes["room:room123:members"]
	redis.mu.RUnlock()
	assert.True(t, ok)

	// 验证NATS发布
	pubMsgs := nats.getPublished()
	assert.True(t, len(pubMsgs) > 0)
	found := false
	for _, m := range pubMsgs {
		if strings.HasPrefix(m.subject, "room.room123.player_join") {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestHandleJoin_MissingRoomID(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "")
	defer conn.Close()

	// 消费欢迎消息
	readWSMessage(t, conn)

	// 发送join消息无room_id
	joinMsg := common.WSMessage{
		Type: common.WSMsgJoin,
	}
	data, _ := json.Marshal(joinMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	// 应收到error
	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgError, resp.Type)
}

func TestHandleLeave(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")
	defer conn.Close()

	// 消费欢迎消息
	readWSMessage(t, conn)

	// 先join
	joinMsg := common.WSMessage{Type: common.WSMsgJoin, RoomID: "room1"}
	data, _ := json.Marshal(joinMsg)
	conn.WriteMessage(websocket.TextMessage, data)
	readWSMessage(t, conn) // join响应

	// 发送leave
	leaveMsg := common.WSMessage{Type: common.WSMsgLeave, RoomID: "room1"}
	data, _ = json.Marshal(leaveMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	time.Sleep(100 * time.Millisecond)

	// 验证NATS发布了player_leave
	pubMsgs := nats.getPublished()
	found := false
	for _, m := range pubMsgs {
		if strings.Contains(m.subject, "player_leave") {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestHandleHeartbeat(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")
	defer conn.Close()

	// 消费欢迎消息
	readWSMessage(t, conn)

	// 发送心跳
	hbMsg := common.WSMessage{Type: common.WSMsgHeartbeat}
	data, _ := json.Marshal(hbMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	// 读取心跳响应
	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgHeartbeat, resp.Type)

	// 验证Redis更新了心跳
	redis.mu.RLock()
	_, ok := redis.hashes["player:heartbeat"]
	redis.mu.RUnlock()
	assert.True(t, ok)
}

func TestHandleInput(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")
	defer conn.Close()

	// 消费欢迎消息
	readWSMessage(t, conn)

	// 先join房间
	joinMsg := common.WSMessage{Type: common.WSMsgJoin, RoomID: "room1"}
	data, _ := json.Marshal(joinMsg)
	conn.WriteMessage(websocket.TextMessage, data)
	readWSMessage(t, conn)

	// 发送input
	inputMsg := common.WSMessage{
		Type:   common.WSMsgInput,
		RoomID: "room1",
		Data:   map[string]interface{}{"action": "move", "x": 100},
	}
	data, _ = json.Marshal(inputMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	time.Sleep(100 * time.Millisecond)

	// 验证NATS发布了input
	pubMsgs := nats.getPublished()
	found := false
	for _, m := range pubMsgs {
		if strings.Contains(m.subject, "input") {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestHandleInput_NotInRoom(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	// 连接时不指定room
	conn := connectWS(t, server, "player1", token, "")
	defer conn.Close()

	readWSMessage(t, conn) // 欢迎消息

	// 发送input但不在房间
	inputMsg := common.WSMessage{
		Type: common.WSMsgInput,
		Data: map[string]interface{}{"action": "move"},
	}
	data, _ := json.Marshal(inputMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgError, resp.Type)
}

func TestHandleUnknownMessageType(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "")
	defer conn.Close()

	readWSMessage(t, conn)

	// 发送未知类型
	unknownMsg := common.WSMessage{Type: "unknown_type"}
	data, _ := json.Marshal(unknownMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgError, resp.Type)
}

func TestHandleInvalidJSON(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "")
	defer conn.Close()

	readWSMessage(t, conn)

	// 发送无效JSON
	conn.WriteMessage(websocket.TextMessage, []byte("not json"))

	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgError, resp.Type)
}

func TestHandleReconnect(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")
	defer conn.Close()

	readWSMessage(t, conn)

	// 无session数据时重连应返回error
	reconnMsg := common.WSMessage{Type: common.WSMsgReconnect, RoomID: "room1"}
	data, _ := json.Marshal(reconnMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgError, resp.Type)
}

func TestHandleReconnect_WithSession(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	// 预设session数据
	redis.Set("session:player1", `{"state":"active"}`, 0)

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")
	defer conn.Close()

	readWSMessage(t, conn)

	reconnMsg := common.WSMessage{Type: common.WSMsgReconnect, RoomID: "room1"}
	data, _ := json.Marshal(reconnMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgReconnect, resp.Type)
}

func TestValidateReconnectToken(t *testing.T) {
	tests := []struct {
		name        string
		storedToken string
		inputToken  string
		redisAvail  bool
		expectValid bool
		expectErr   bool
	}{
		{
			name:        "valid_token",
			storedToken: "token123",
			inputToken:  "token123",
			redisAvail:  true,
			expectValid: true,
			expectErr:   false,
		},
		{
			name:        "invalid_token",
			storedToken: "token123",
			inputToken:  "wrong_token",
			redisAvail:  true,
			expectValid: false,
			expectErr:   false,
		},
		{
			name:        "no_redis",
			storedToken: "",
			inputToken:  "token123",
			redisAvail:  false,
			expectValid: false,
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redis := newMockRedisClient()
			nats := newMockNATSClient()
			g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")

			if tt.redisAvail {
				redis.Set("reconnect:player1", tt.storedToken, 0)
			} else {
				g.redis = nil
			}

			valid, err := g.validateReconnectToken("player1", tt.inputToken)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectValid, valid)
			}
		})
	}
}

func TestHandleRoomBroadcast(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")
	defer conn.Close()

	readWSMessage(t, conn) // 欢迎消息

	// 模拟NATS广播
	broadcastData, _ := json.Marshal(common.WSMessage{
		Type:   common.WSMsgFrame,
		RoomID: "room1",
		Data:   map[string]interface{}{"frame": 1},
	})
	g.handleRoomBroadcast(broadcastData)

	// 应收到广播消息
	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgFrame, resp.Type)
}

func TestHandlePlayerLeave_NATS(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")
	defer conn.Close()

	readWSMessage(t, conn) // 欢迎消息

	// 模拟NATS player_leave消息
	leaveData, _ := json.Marshal(map[string]string{"player_id": "player1"})
	g.handlePlayerLeave(leaveData)

	// 应收到leave消息
	resp := readWSMessage(t, conn)
	assert.Equal(t, common.WSMsgLeave, resp.Type)
}

func TestSendMessage_ChannelFull(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")

	// 创建一个缓冲区很小的sendCh
	wsc := &WSConnection{
		connID:   "test_conn",
		playerID: "player1",
		roomID:   "room1",
		sendCh:   make(chan []byte, 1), // 缓冲区只有1
		quitCh:   make(chan struct{}),
	}

	// 填满缓冲区
	wsc.sendCh <- []byte("first")

	// 再发送不应阻塞，只是被丢弃
	msg := &common.WSMessage{Type: common.WSMsgHeartbeat}
	g.sendMessage(wsc, msg) // 不应死锁
}

func TestCloseConnection(t *testing.T) {
	redis := newMockRedisClient()
	nats := newMockNATSClient()
	g := NewGatewayComponent(redis, nats, DefaultGatewayConfig(), "test-node-1")
	server := setupTestServer(g)
	defer server.Close()

	token := generateTestToken("player1", "user1")
	conn := connectWS(t, server, "player1", token, "room1")

	readWSMessage(t, conn) // 欢迎消息

	// 查找连接
	var wsc *WSConnection
	g.connections.Range(func(_, v interface{}) bool {
		wsc = v.(*WSConnection)
		return false
	})
	assert.NotNil(t, wsc)

	// 关闭客户端连接，触发closeConnection
	conn.Close()
	time.Sleep(200 * time.Millisecond)

	// 验证连接已清理
	count := 0
	g.connections.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 0, count)
}

func TestGenerateConnID(t *testing.T) {
	id1 := generateConnID()
	assert.True(t, strings.HasPrefix(id1, "conn_"))
	// 生成两个ID验证格式
	id2 := generateConnID()
	assert.True(t, strings.HasPrefix(id2, "conn_"))
}

func TestGateway_NilRedisNATS(t *testing.T) {
	// 确保redis/nats为nil时不panic
	g := NewGatewayComponent(nil, nil, DefaultGatewayConfig(), "test-node-1")

	// handleJoin不应panic
	wsc := &WSConnection{
		playerID: "p1",
		roomID:   "",
		sendCh:   make(chan []byte, 256),
		quitCh:   make(chan struct{}),
	}
	g.handleJoin(wsc, &common.WSMessage{RoomID: "room1"})
	assert.Equal(t, "room1", wsc.roomID)

	// handleLeave不应panic
	g.handleLeave(wsc, &common.WSMessage{})
	assert.Equal(t, "", wsc.roomID)

	// handleHeartbeat不应panic
	g.handleHeartbeat(wsc, &common.WSMessage{})

	// handleInput不应panic（但不在房间中）
	wsc.roomID = "room1"
	g.handleInput(wsc, &common.WSMessage{})
}

func TestMockRedis_AllOperations(t *testing.T) {
	m := newMockRedisClient()

	// Set & Get
	err := m.Set("key1", "value1", 0)
	assert.NoError(t, err)
	val, err := m.Get("key1")
	assert.NoError(t, err)
	assert.Equal(t, "value1", val)

	// Del
	err = m.Del("key1")
	assert.NoError(t, err)
	_, err = m.Get("key1")
	assert.Error(t, err)

	// HSet & HGet
	err = m.HSet("hash1", "field1", "hvalue1")
	assert.NoError(t, err)
	val, err = m.HGet("hash1", "field1")
	assert.NoError(t, err)
	assert.Equal(t, "hvalue1", val)

	// HDel
	err = m.HDel("hash1", "field1")
	assert.NoError(t, err)
	_, err = m.HGet("hash1", "field1")
	assert.Error(t, err)

	// Error mode
	m.err = fmt.Errorf("redis error")
	err = m.Set("key2", "value2", 0)
	assert.Error(t, err)
	m.err = nil
}

func TestMockNATS_AllOperations(t *testing.T) {
	m := newMockNATSClient()

	// Publish
	err := m.Publish("test.subject", []byte("data"))
	assert.NoError(t, err)
	msgs := m.getPublished()
	assert.Len(t, msgs, 1)
	assert.Equal(t, "test.subject", msgs[0].subject)

	// Subscribe
	_ = m.Subscribe("test.subject2", func(msg []byte) {})
	assert.NoError(t, err)
	assert.NotNil(t, m.subscribed["test.subject2"])

	// Close
	err = m.Close()
	assert.NoError(t, err)
	assert.True(t, m.closed)
}
