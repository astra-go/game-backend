package eventbus

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

// Priority 事件优先级
type Priority int

const (
	PriorityLow    Priority = 0 // 低优先级
	PriorityNormal Priority = 1 // 普通优先级（默认）
	PriorityHigh   Priority = 2 // 高优先级
	PriorityUrgent Priority = 3 // 紧急优先级
)

// prioritizedEvent 带优先级的事件
type prioritizedEvent struct {
	subject   string
	data      []byte
	priority  Priority
	timestamp time.Time
	index     int // heap索引
}

// priorityQueue 优先级队列（最小堆）
type priorityQueue []*prioritizedEvent

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	// 优先级高的先出队（Priority值大的）
	if pq[i].priority == pq[j].priority {
		return pq[i].timestamp.Before(pq[j].timestamp)
	}
	return pq[i].priority > pq[j].priority
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*prioritizedEvent)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

// Conn NATS连接接口（用于解耦和测试）
type Conn interface {
	Publish(subj string, data []byte) error
	Subscribe(subj string, cb nats.MsgHandler) (*nats.Subscription, error)
	QueueSubscribe(subj, queue string, cb nats.MsgHandler) (*nats.Subscription, error)
	Request(subj string, data []byte, timeout time.Duration) (*nats.Msg, error)
	Close()
}

// EventBus 事件总线封装（基于NATS）
type EventBus struct {
	conn     Conn
	logger   *zap.Logger
	subs     sync.Map     // subject -> subscription
	priQueue *priorityQueue // 优先级队列
	queueMu  sync.Mutex    // 队列操作锁
}

// NewEventBus 创建事件总线
func NewEventBus(url string, logger *zap.Logger) (*EventBus, error) {
	opts := []nats.Option{
		nats.Name("AstraGame-EventBus"),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(10),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			logger.Error("NATS断开连接", zap.Error(err))
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Info("NATS重新连接成功")
		}),
	}
	
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("连接NATS失败: %w", err)
	}
	
	return &EventBus{
		conn:     nc,
		logger:   logger,
		priQueue: &priorityQueue{},
	}, nil
}

// Publish 发布事件
func (eb *EventBus) Publish(subject string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}
	
	err = eb.conn.Publish(subject, payload)
	if err != nil {
		eb.logger.Error("发布事件失败",
			zap.String("subject", subject),
			zap.Error(err),
		)
		return err
	}
	
	eb.logger.Debug("事件发布成功",
		zap.String("subject", subject),
		zap.Int("size", len(payload)),
	)
	
	return nil
}

// PublishWithPriority 发布带优先级的事件
func (eb *EventBus) PublishWithPriority(subject string, data interface{}, priority Priority) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}
	
	event := &prioritizedEvent{
		subject:   subject,
		data:      payload,
		priority:  priority,
		timestamp: time.Now(),
	}
	
	eb.queueMu.Lock()
	heap.Push(eb.priQueue, event)
	eb.queueMu.Unlock()
	
	eb.logger.Debug("优先级事件入队",
		zap.String("subject", subject),
		zap.Int("priority", int(priority)),
		zap.Int("queue_size", eb.priQueue.Len()),
	)
	
	// 异步处理优先级队列
	go eb.processPriorityQueue()
	
	return nil
}

// processPriorityQueue 处理优先级队列
func (eb *EventBus) processPriorityQueue() {
	eb.queueMu.Lock()
	defer eb.queueMu.Unlock()
	
	for eb.priQueue.Len() > 0 {
		event := heap.Pop(eb.priQueue).(*prioritizedEvent)
		
		err := eb.conn.Publish(event.subject, event.data)
		if err != nil {
			eb.logger.Error("优先级事件发布失败",
				zap.String("subject", event.subject),
				zap.Error(err),
			)
		}
	}
}

// Subscribe 订阅事件
func (eb *EventBus) Subscribe(subject string, handler func(msg *nats.Msg)) error {
	_, err := eb.conn.Subscribe(subject, func(msg *nats.Msg) {
		defer func() {
			if r := recover(); r != nil {
				eb.logger.Error("消息处理panic",
					zap.String("subject", subject),
					zap.Any("panic", r),
				)
			}
		}()
		
		handler(msg)
	})
	
	if err != nil {
		return fmt.Errorf("订阅失败: %w", err)
	}
	
	eb.logger.Info("订阅成功", zap.String("subject", subject))
	
	return nil
}

// QueueSubscribe 队列订阅（负载均衡）
func (eb *EventBus) QueueSubscribe(subject, queue string, handler func(msg *nats.Msg)) error {
	_, err := eb.conn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		defer func() {
			if r := recover(); r != nil {
				eb.logger.Error("消息处理panic",
					zap.String("subject", subject),
					zap.String("queue", queue),
					zap.Any("panic", r),
				)
			}
		}()
		
		handler(msg)
	})
	
	if err != nil {
		return fmt.Errorf("队列订阅失败: %w", err)
	}
	
	eb.logger.Info("队列订阅成功",
		zap.String("subject", subject),
		zap.String("queue", queue),
	)
	
	return nil
}

// Request 请求-响应模式
func (eb *EventBus) Request(subject string, data interface{}, timeout time.Duration) (*nats.Msg, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("序列化失败: %w", err)
	}
	
	msg, err := eb.conn.Request(subject, payload, timeout)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	
	return msg, nil
}

// CrossServiceEvent 跨服务事件
type CrossServiceEvent struct {
	SourceService string      `json:"source_service"`
	TargetService string      `json:"target_service"`
	EventType    string      `json:"event_type"`
	Payload      interface{} `json:"payload"`
	Timestamp     time.Time   `json:"timestamp"`
}

// PublishCrossService 发布跨服务事件
func (eb *EventBus) PublishCrossService(targetService, eventType string, payload interface{}) error {
	event := CrossServiceEvent{
		SourceService: "game-backend",
		TargetService: targetService,
		EventType:    eventType,
		Payload:      payload,
		Timestamp:     time.Now(),
	}
	
	subject := fmt.Sprintf("cross.%s.%s", targetService, eventType)
	return eb.Publish(subject, event)
}

// SubscribeCrossService 订阅跨服务事件
func (eb *EventBus) SubscribeCrossService(targetService, eventType string, handler func(msg *nats.Msg)) error {
	subject := fmt.Sprintf("cross.%s.%s", targetService, eventType)
	return eb.Subscribe(subject, handler)
}

// Close 关闭连接
func (eb *EventBus) Close() {
	eb.conn.Close()
	eb.logger.Info("EventBus连接关闭")
}

// ========== 预定义主题 ==========

// 房间事件
const (
	SubjectRoomCreated   = "room.*.created"
	SubjectRoomDestroyed = "room.*.destroyed"
	SubjectRoomJoin      = "room.*.player_join"
	SubjectRoomLeave     = "room.*.player_leave"
	SubjectRoomBroadcast = "room.*.broadcast"
	SubjectRoomInput     = "room.*.input"
	SubjectRoomFrame     = "room.*.frame.*"
)

// 匹配事件
const (
	SubjectMatchEnqueue  = "match.enqueue"
	SubjectMatchDequeue  = "match.dequeue"
	SubjectMatchSuccess  = "match.success"
	SubjectMatchTimeout  = "match.timeout"
)

// 玩家事件
const (
	SubjectPlayerLogin    = "player.login"
	SubjectPlayerLogout   = "player.logout"
	SubjectPlayerUpdate   = "player.update"
)
