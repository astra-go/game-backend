package eventbus

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

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
	conn   Conn
	logger *zap.Logger
	subs   sync.Map // subject -> subscription
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
		conn:   nc,
		logger: logger,
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
