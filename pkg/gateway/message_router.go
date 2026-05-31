package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
)

// MessageRouter 消息路由器
type MessageRouter struct {
	gateway      *GatewayComponent
	nodeID       string
	routeCache   sync.Map // roomID -> nodeID 缓存
	cacheTTL     time.Duration
	forwardQueue chan *ForwardMessage
	quitCh       chan struct{}
}

// ForwardMessage 转发消息
type ForwardMessage struct {
	TargetNode string
	RoomID     string
	Message    *common.WSMessage
	RetryCount int
	Timestamp  int64
}

// RouteResult 路由结果
type RouteResult struct {
	TargetNode string
	IsLocal    bool
	NeedForward bool
}

// NewMessageRouter 创建消息路由器
func NewMessageRouter(gateway *GatewayComponent, nodeID string) *MessageRouter {
	return &MessageRouter{
		gateway:      gateway,
		nodeID:       nodeID,
		cacheTTL:     5 * time.Minute,
		forwardQueue: make(chan *ForwardMessage, 1000),
		quitCh:       make(chan struct{}),
	}
}

// Start 启动路由器
func (r *MessageRouter) Start() {
	go r.forwardWorker()
	slog.Info("消息路由器已启动", "node_id", r.nodeID)
}

// Stop 停止路由器
func (r *MessageRouter) Stop() {
	close(r.quitCh)
	slog.Info("消息路由器已停止", "node_id", r.nodeID)
}

// RouteMessage 路由消息到目标节点
func (r *MessageRouter) RouteMessage(roomID string, msg *common.WSMessage) (*RouteResult, error) {
	if roomID == "" {
		return nil, fmt.Errorf("room_id is empty")
	}

	// 1. 检查缓存
	if cached, ok := r.routeCache.Load(roomID); ok {
		cacheEntry := cached.(*routeCacheEntry)
		if time.Since(cacheEntry.timestamp) < r.cacheTTL {
			return &RouteResult{
				TargetNode:  cacheEntry.nodeID,
				IsLocal:     cacheEntry.nodeID == r.nodeID,
				NeedForward: cacheEntry.nodeID != r.nodeID,
			}, nil
		}
		// 缓存过期，删除
		r.routeCache.Delete(roomID)
	}

	// 2. 使用一致性哈希计算目标节点
	targetNode := r.gateway.hashRing.Get(roomID)
	if targetNode == "" {
		return nil, fmt.Errorf("no available node for room %s", roomID)
	}

	// 3. 更新缓存
	r.routeCache.Store(roomID, &routeCacheEntry{
		nodeID:    targetNode,
		timestamp: time.Now(),
	})

	result := &RouteResult{
		TargetNode:  targetNode,
		IsLocal:     targetNode == r.nodeID,
		NeedForward: targetNode != r.nodeID,
	}

	// 4. 如果需要转发，加入转发队列
	if result.NeedForward {
		r.forwardQueue <- &ForwardMessage{
			TargetNode: targetNode,
			RoomID:     roomID,
			Message:    msg,
			RetryCount: 0,
			Timestamp:  time.Now().UnixMilli(),
		}
	}

	return result, nil
}

// forwardWorker 转发工作协程
func (r *MessageRouter) forwardWorker() {
	for {
		select {
		case fwdMsg := <-r.forwardQueue:
			r.forwardMessage(fwdMsg)
		case <-r.quitCh:
			return
		}
	}
}

// forwardMessage 转发消息到目标节点
func (r *MessageRouter) forwardMessage(fwdMsg *ForwardMessage) {
	if r.gateway.nats == nil {
		slog.Error("NATS客户端未初始化，无法转发消息")
		return
	}

	// 构造转发消息
	envelope := map[string]interface{}{
		"source_node": r.nodeID,
		"target_node": fwdMsg.TargetNode,
		"room_id":     fwdMsg.RoomID,
		"message":     fwdMsg.Message,
		"timestamp":   fwdMsg.Timestamp,
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		slog.Error("转发消息序列化失败", "error", err)
		return
	}

	// 发送到NATS主题：gateway.{target_node}.forward
	subject := fmt.Sprintf("gateway.%s.forward", fwdMsg.TargetNode)
	if err := r.gateway.nats.Publish(subject, data); err != nil {
		slog.Error("转发消息失败", "target_node", fwdMsg.TargetNode, "error", err)

		// 重试逻辑
		if fwdMsg.RetryCount < 3 {
			fwdMsg.RetryCount++
			time.AfterFunc(time.Second*time.Duration(fwdMsg.RetryCount), func() {
				r.forwardQueue <- fwdMsg
			})
		}
		return
	}

	slog.Debug("消息已转发", "target_node", fwdMsg.TargetNode, "room_id", fwdMsg.RoomID)
}

// HandleForwardedMessage 处理转发来的消息
func (r *MessageRouter) HandleForwardedMessage(data []byte) {
	var envelope map[string]interface{}
	if err := json.Unmarshal(data, &envelope); err != nil {
		slog.Error("转发消息解析失败", "error", err)
		return
	}

	// 验证目标节点
	targetNode, _ := envelope["target_node"].(string)
	if targetNode != r.nodeID {
		slog.Warn("收到非本节点的转发消息", "target_node", targetNode, "current_node", r.nodeID)
		return
	}

	roomID, _ := envelope["room_id"].(string)
	msgData, _ := envelope["message"].(map[string]interface{})

	// 重构为 WSMessage
	msg := &common.WSMessage{
		Type:   msgData["type"].(string),
		RoomID: roomID,
	}
	if frame, ok := msgData["frame"].(float64); ok {
		msg.Frame = int64(frame)
	}
	if data, ok := msgData["data"]; ok {
		msg.Data = data
	}

	// 广播给本节点的房间连接
	r.gateway.connections.Range(func(key, value interface{}) bool {
		wsc := value.(*WSConnection)
		if wsc.roomID == roomID {
			r.gateway.sendMessage(wsc, msg)
		}
		return true
	})

	slog.Debug("已处理转发消息", "room_id", roomID, "msg_type", msg.Type)
}

// InvalidateCache 使缓存失效
func (r *MessageRouter) InvalidateCache(roomID string) {
	r.routeCache.Delete(roomID)
	slog.Debug("路由缓存已失效", "room_id", roomID)
}

// GetCachedRoute 获取缓存的路由
func (r *MessageRouter) GetCachedRoute(roomID string) (string, bool) {
	if cached, ok := r.routeCache.Load(roomID); ok {
		entry := cached.(*routeCacheEntry)
		if time.Since(entry.timestamp) < r.cacheTTL {
			return entry.nodeID, true
		}
		r.routeCache.Delete(roomID)
	}
	return "", false
}

// routeCacheEntry 路由缓存条目
type routeCacheEntry struct {
	nodeID    string
	timestamp time.Time
}

// BroadcastToRoom 广播消息到房间（自动处理跨节点）
func (r *MessageRouter) BroadcastToRoom(roomID string, msg *common.WSMessage) error {
	// 1. 路由到目标节点
	result, err := r.RouteMessage(roomID, msg)
	if err != nil {
		return err
	}

	// 2. 如果是本地节点，直接广播
	if result.IsLocal {
		r.gateway.connections.Range(func(key, value interface{}) bool {
			wsc := value.(*WSConnection)
			if wsc.roomID == roomID {
				r.gateway.sendMessage(wsc, msg)
			}
			return true
		})
	}
	// 如果需要转发，forwardWorker 会自动处理

	return nil
}

// MulticastToNodes 多播消息到多个节点（用于副本同步）
func (r *MessageRouter) MulticastToNodes(roomID string, msg *common.WSMessage, replicaCount int) error {
	// 获取N个副本节点
	nodes := r.gateway.hashRing.GetN(roomID, replicaCount)
	if len(nodes) == 0 {
		return fmt.Errorf("no available nodes for room %s", roomID)
	}

	// 向每个节点发送消息
	for _, node := range nodes {
		if node == r.nodeID {
			// 本地节点直接广播
			r.gateway.connections.Range(func(key, value interface{}) bool {
				wsc := value.(*WSConnection)
				if wsc.roomID == roomID {
					r.gateway.sendMessage(wsc, msg)
				}
				return true
			})
		} else {
			// 远程节点加入转发队列
			r.forwardQueue <- &ForwardMessage{
				TargetNode: node,
				RoomID:     roomID,
				Message:    msg,
				RetryCount: 0,
				Timestamp:  time.Now().UnixMilli(),
			}
		}
	}

	slog.Debug("多播消息已发送", "room_id", roomID, "nodes", nodes)
	return nil
}
