package eventbus

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// ========== Mock Conn ==========

type mockConn struct {
	mu           sync.Mutex
	subscriptions []mockSub
	published    []publishedMsg
	closed       bool
	publishErr   error
	subscribeErr error
	requestMsg   *nats.Msg
	requestErr   error
}

type mockSub struct {
	subject string
	queue   string
	handler nats.MsgHandler
}

type publishedMsg struct {
	subject string
	data    []byte
}

func (m *mockConn) Publish(subj string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.publishErr != nil {
		return m.publishErr
	}
	m.published = append(m.published, publishedMsg{subject: subj, data: data})
	return nil
}

func (m *mockConn) Subscribe(subj string, cb nats.MsgHandler) (*nats.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subscribeErr != nil {
		return nil, m.subscribeErr
	}
	m.subscriptions = append(m.subscriptions, mockSub{subject: subj, handler: cb})
	return &nats.Subscription{}, nil
}

func (m *mockConn) QueueSubscribe(subj, queue string, cb nats.MsgHandler) (*nats.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subscribeErr != nil {
		return nil, m.subscribeErr
	}
	m.subscriptions = append(m.subscriptions, mockSub{subject: subj, queue: queue, handler: cb})
	return &nats.Subscription{}, nil
}

func (m *mockConn) Request(subj string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	if m.requestErr != nil {
		return nil, m.requestErr
	}
	if m.requestMsg != nil {
		return m.requestMsg, nil
	}
	return &nats.Msg{Data: []byte(`{"ok":true}`)}, nil
}

func (m *mockConn) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *mockConn) getSubscriptions() []mockSub {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockSub, len(m.subscriptions))
	copy(out, m.subscriptions)
	return out
}

func (m *mockConn) getPublished() []publishedMsg {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]publishedMsg, len(m.published))
	copy(out, m.published)
	return out
}

func newEventBusWithMock(mc *mockConn) *EventBus {
	return &EventBus{
		conn:   mc,
		logger: zap.NewNop(),
	}
}

// ========== Tests ==========

func TestNewEventBus_BadURL(t *testing.T) {
	logger := zap.NewNop()
	_, err := NewEventBus("nats://invalid-host:4222", logger)
	assert.Error(t, err, "无效的NATS地址应该返回错误")
}

func TestEventBus_Publish_MarshalError(t *testing.T) {
	eb := &EventBus{logger: zap.NewNop()}
	err := eb.Publish("test", make(chan int))
	assert.Error(t, err, "不可序列化的数据应该返回错误")
}

func TestEventBus_Publish_Success(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	data := map[string]string{"key": "value"}
	err := eb.Publish("test.subject", data)
	assert.NoError(t, err)

	pub := mc.getPublished()
	assert.Len(t, pub, 1)
	assert.Equal(t, "test.subject", pub[0].subject)

	var got map[string]string
	assert.NoError(t, json.Unmarshal(pub[0].data, &got))
	assert.Equal(t, "value", got["key"])
}

func TestEventBus_Publish_PublishError(t *testing.T) {
	mc := &mockConn{publishErr: errors.New("publish failed")}
	eb := newEventBusWithMock(mc)

	err := eb.Publish("test.subject", map[string]string{"k": "v"})
	assert.Error(t, err)
}

func TestEventBus_Subscribe_Success(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	err := eb.Subscribe("test.subject", func(msg *nats.Msg) {})
	assert.NoError(t, err)

	subs := mc.getSubscriptions()
	assert.Len(t, subs, 1)
	assert.Equal(t, "test.subject", subs[0].subject)
}

func TestEventBus_Subscribe_Error(t *testing.T) {
	mc := &mockConn{subscribeErr: errors.New("subscribe failed")}
	eb := newEventBusWithMock(mc)

	err := eb.Subscribe("test.subject", func(msg *nats.Msg) {})
	assert.Error(t, err)
}

func TestEventBus_Subscribe_PanicRecovery(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	err := eb.Subscribe("test.subject", func(msg *nats.Msg) {
		panic("test panic")
	})
	assert.NoError(t, err)

	subs := mc.getSubscriptions()
	assert.Len(t, subs, 1)
	assert.NotPanics(t, func() {
		subs[0].handler(&nats.Msg{Subject: "test.subject", Data: []byte("test")})
	})
}

func TestEventBus_QueueSubscribe_Success(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	err := eb.QueueSubscribe("test.subject", "queue1", func(msg *nats.Msg) {})
	assert.NoError(t, err)

	subs := mc.getSubscriptions()
	assert.Len(t, subs, 1)
	assert.Equal(t, "queue1", subs[0].queue)
}

func TestEventBus_QueueSubscribe_Error(t *testing.T) {
	mc := &mockConn{subscribeErr: errors.New("queue subscribe failed")}
	eb := newEventBusWithMock(mc)

	err := eb.QueueSubscribe("test.subject", "queue1", func(msg *nats.Msg) {})
	assert.Error(t, err)
}

func TestEventBus_QueueSubscribe_PanicRecovery(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	err := eb.QueueSubscribe("test.subject", "queue1", func(msg *nats.Msg) {
		panic("queue panic")
	})
	assert.NoError(t, err)

	subs := mc.getSubscriptions()
	assert.NotPanics(t, func() {
		subs[0].handler(&nats.Msg{Subject: "test.subject", Data: []byte("test")})
	})
}

func TestEventBus_Request_Success(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	msg, err := eb.Request("test.subject", map[string]string{"k": "v"}, time.Second)
	assert.NoError(t, err)
	assert.NotNil(t, msg)
}

func TestEventBus_Request_MarshalError(t *testing.T) {
	eb := &EventBus{logger: zap.NewNop()}
	msg, err := eb.Request("test.subject", make(chan int), time.Second)
	assert.Error(t, err)
	assert.Nil(t, msg)
}

func TestEventBus_Request_Error(t *testing.T) {
	mc := &mockConn{requestErr: errors.New("request failed")}
	eb := newEventBusWithMock(mc)

	msg, err := eb.Request("test.subject", map[string]string{"k": "v"}, time.Second)
	assert.Error(t, err)
	assert.Nil(t, msg)
}

func TestEventBus_Close(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	eb.Close()
	assert.True(t, mc.closed)
}

// ========== 多订阅者测试 ==========

func TestEventBus_MultipleSubscribers(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	var count int32
	for i := 0; i < 5; i++ {
		err := eb.Subscribe("multi.sub", func(msg *nats.Msg) {
			atomic.AddInt32(&count, 1)
		})
		assert.NoError(t, err)
	}

	subs := mc.getSubscriptions()
	assert.Len(t, subs, 5)

	msg := &nats.Msg{Subject: "multi.sub", Data: []byte("hello")}
	for _, sub := range subs {
		sub.handler(msg)
	}
	assert.Equal(t, int32(5), atomic.LoadInt32(&count))
}

// ========== 并发安全测试 ==========

func TestEventBus_ConcurrentPublish(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = eb.Publish("concurrent.test", map[string]int{"i": i})
		}(i)
	}
	wg.Wait()

	assert.Len(t, mc.getPublished(), 100)
}

func TestEventBus_ConcurrentSubscribe(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = eb.Subscribe("concurrent.sub", func(msg *nats.Msg) {})
		}()
	}
	wg.Wait()

	assert.Len(t, mc.getSubscriptions(), 50)
}

func TestEventBus_ConcurrentMixed(t *testing.T) {
	mc := &mockConn{}
	eb := newEventBusWithMock(mc)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = eb.Publish("mixed.test", map[string]string{"k": "v"})
		}()
		go func() {
			defer wg.Done()
			_ = eb.Subscribe("mixed.test", func(msg *nats.Msg) {})
		}()
	}
	wg.Wait()
}

// ========== Table-driven 测试 ==========

func TestEventBus_Publish_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		data    interface{}
		wantErr bool
	}{
		{"正常map数据", "room.1.created", map[string]string{"room_id": "1"}, false},
		{"字符串数据", "player.login", "hello", false},
		{"nil数据", "match.enqueue", nil, false},
		{"整数数据", "player.update", 42, false},
		{"不可序列化数据", "test.error", make(chan int), true},
		{"空subject", "", map[string]string{"k": "v"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &mockConn{}
			eb := newEventBusWithMock(mc)
			err := eb.Publish(tt.subject, tt.data)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEventBus_Subscribe_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		subscribe  func(*EventBus, *mockConn) error
		wantErr    bool
		checkQueue string
	}{
		{
			name: "普通订阅",
			subscribe: func(eb *EventBus, _ *mockConn) error {
				return eb.Subscribe("room.*.created", func(msg *nats.Msg) {})
			},
			wantErr: false,
		},
		{
			name: "队列订阅",
			subscribe: func(eb *EventBus, _ *mockConn) error {
				return eb.QueueSubscribe("room.*.input", "workers", func(msg *nats.Msg) {})
			},
			wantErr:    false,
			checkQueue: "workers",
		},
		{
			name: "订阅失败",
			subscribe: func(eb *EventBus, mc *mockConn) error {
				mc.subscribeErr = errors.New("fail")
				return eb.Subscribe("fail.sub", func(msg *nats.Msg) {})
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &mockConn{}
			eb := newEventBusWithMock(mc)
			err := tt.subscribe(eb, mc)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				subs := mc.getSubscriptions()
				assert.NotEmpty(t, subs)
				if tt.checkQueue != "" {
					assert.Equal(t, tt.checkQueue, subs[0].queue)
				}
			}
		})
	}
}

func TestEventBus_PredefinedSubjects(t *testing.T) {
	subjects := map[string]string{
		"RoomCreated":   SubjectRoomCreated,
		"RoomDestroyed": SubjectRoomDestroyed,
		"RoomJoin":      SubjectRoomJoin,
		"RoomLeave":     SubjectRoomLeave,
		"RoomBroadcast": SubjectRoomBroadcast,
		"RoomInput":     SubjectRoomInput,
		"RoomFrame":     SubjectRoomFrame,
		"MatchEnqueue":  SubjectMatchEnqueue,
		"MatchDequeue":  SubjectMatchDequeue,
		"MatchSuccess":  SubjectMatchSuccess,
		"MatchTimeout":  SubjectMatchTimeout,
		"PlayerLogin":   SubjectPlayerLogin,
		"PlayerLogout":  SubjectPlayerLogout,
		"PlayerUpdate":  SubjectPlayerUpdate,
	}

	for name, subj := range subjects {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, subj)
		})
	}
}
