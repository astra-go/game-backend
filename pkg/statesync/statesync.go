package statesync

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	"go.uber.org/zap"
)

// StateSync 状态同步组件
type StateSync struct {
	mu            sync.Mutex
	session       common.RoomSessionInterface // 使用接口，解耦循环依赖
	hz            int            // 同步频率(Hz)
	entities      map[int64]*common.EntityState // entityID -> state
	dirtyEntities map[int64]bool  // 脏实体集合
	broadcastCh   chan *common.DeltaMessage
	deltaCh       chan *common.EntityDelta
	quitCh        chan struct{}
	logger        *zap.Logger
	
	// 状态压缩
	lastFullSync  int64
	fullSyncInterval int64  // 每N帧全量同步一次
	sendCh        chan []byte
	
	running       bool
	paused        bool // 暂停状态（用于混合同步模式切换）
}

// NewStateSync 创建状态同步器
func NewStateSync(session common.RoomSessionInterface, hz int) *StateSync {
	return &StateSync{
		session:         session,
		hz:              hz,
		entities:        make(map[int64]*common.EntityState),
		dirtyEntities:   make(map[int64]bool),
		broadcastCh:     make(chan *common.DeltaMessage, 256),
		deltaCh:         make(chan *common.EntityDelta, 1024),
		quitCh:          make(chan struct{}),
		logger:          zap.L().With(zap.String("component", "statesync")),
		fullSyncInterval: 300, // 每300帧全量同步一次
		lastFullSync:    0,
		sendCh:          make(chan []byte, 256),
		running:         false,
		paused:          false,
	}
}

// Run 启动状态同步循环
func (ss *StateSync) Run() {
	ss.running = true
	interval := time.Duration(1000/ss.hz) * time.Millisecond
	ticker := time.NewTicker(interval)
	
	ss.logger.Info("状态同步启动", zap.Int("hz", ss.hz))
	
	defer func() {
		ticker.Stop()
		ss.running = false
		ss.logger.Info("状态同步停止")
	}()
	
	// 启动增量处理协程
	go ss.processDeltas()
	
	// 同步循环
	for {
		select {
		case <-ticker.C:
			if !ss.paused {
				ss.tick()
			}
			
		case <-ss.quitCh:
			return
		}
	}
}

// tick 每帧执行
func (ss *StateSync) tick() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	
	frame := ss.session.GetFrame()
	
	// 检查是否需要全量同步
	if frame-ss.lastFullSync >= ss.fullSyncInterval {
		ss.broadcastFullSync(frame)
		ss.lastFullSync = frame
		return
	}
	
	// 增量同步：只发送脏实体
	dirtyCount := len(ss.dirtyEntities)
	if dirtyCount == 0 {
		return
	}
	
	deltas := make([]common.EntityDelta, 0, dirtyCount)
	for entityID := range ss.dirtyEntities {
		if state, ok := ss.entities[entityID]; ok {
			delta := common.EntityDelta{
				EntityID: entityID,
				Frame:    frame,
			}
			
			// 只发送变化的字段（简化：发送所有）
			delta.Position = &common.Position{
				X: state.Position.X,
				Y: state.Position.Y,
				Z: state.Position.Z,
			}
			delta.Health = &common.Health{
				Current: state.Health.Current,
				Max:     state.Health.Max,
			}
			
			deltas = append(deltas, delta)
		}
	}
	
	// 清空脏集合
	ss.dirtyEntities = make(map[int64]bool)
	
	// 广播增量
	ss.broadcastDelta(frame, deltas)
}

// applyDelta 应用状态增量
func (ss *StateSync) applyDelta(delta *common.EntityDelta) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	
	entityID := delta.EntityID
	
	// 获取或创建实体
	state, ok := ss.entities[entityID]
	if !ok {
		state = &common.EntityState{
			EntityID: entityID,
			Health:   common.Health{Current: 100, Max: 100},
			LastSync: delta.Frame,
		}
		ss.entities[entityID] = state
	}
	
	// 应用增量
	if delta.Position != nil {
		state.Position.X = delta.Position.X
		state.Position.Y = delta.Position.Y
		state.Position.Z = delta.Position.Z
	}
	
	if delta.Health != nil {
		state.Health.Current = delta.Health.Current
		state.Health.Max = delta.Health.Max
	}
	
	state.LastSync = delta.Frame
	state.Dirty = true
	
	// 标记为脏
	ss.dirtyEntities[entityID] = true
}

// processDeltas 处理增量通道
func (ss *StateSync) processDeltas() {
	for {
		select {
		case delta := <-ss.deltaCh:
			ss.applyDelta(delta)
			
		case <-ss.quitCh:
			return
		}
	}
}

// broadcastDelta 广播增量
func (ss *StateSync) broadcastDelta(frame int64, deltas []common.EntityDelta) {
	msg := &common.DeltaMessage{
		Frame: frame,
		Delta: deltas,
	}
	
	data, err := json.Marshal(msg)
	if err != nil {
		ss.logger.Error("增量序列化失败", zap.Error(err))
		return
	}
	
	// 通过NATS/WebSocket广播
	// nats.Publish(fmt.Sprintf("room.%s.state_delta", ss.session.GetRoomID()), data)
	
	select {
	case ss.sendCh <- data:
	default:
		ss.logger.Warn("发送队列满，丢弃增量")
	}
	
	ss.logger.Debug("状态增量广播",
		zap.Int64("frame", frame),
		zap.Int("delta_count", len(deltas)),
	)
}

// broadcastFullSync 广播全量状态
func (ss *StateSync) broadcastFullSync(frame int64) {
	// 注意：调用方（tick）已持有 ss.mu，此处不再加锁
	
	// 构建全量状态
	allStates := make([]common.EntityState, 0, len(ss.entities))
	for _, state := range ss.entities {
		allStates = append(allStates, *state)
	}
	
	msg := map[string]any{
		"type":   "full_sync",
		"frame":  frame,
		"states": allStates,
	}
	
	data, err := json.Marshal(msg)
	if err != nil {
		ss.logger.Error("全量同步序列化失败", zap.Error(err))
		return
	}
	_ = data
	// 广播
	// nats.Publish(fmt.Sprintf("room.%s.full_sync", ss.session.GetRoomID()), data)
	
	ss.logger.Info("全量状态同步",
		zap.Int64("frame", frame),
		zap.Int("entity_count", len(allStates)),
	)
}

// SubmitDelta 提交状态增量
func (ss *StateSync) SubmitDelta(delta common.EntityDelta) error {
	if !ss.running {
		return fmt.Errorf("状态同步未运行")
	}
	
	select {
	case ss.deltaCh <- &delta:
		return nil
	case <-time.After(50 * time.Millisecond):
		return fmt.Errorf("增量通道阻塞")
	}
}

// GetEntityState 获取实体状态
func (ss *StateSync) GetEntityState(entityID int64) (*common.EntityState, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	
	state, ok := ss.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("实体不存在: %d", entityID)
	}
	
	return state, nil
}

// GetAllStates 获取所有实体状态
func (ss *StateSync) GetAllStates() []*common.EntityState {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	
	states := make([]*common.EntityState, 0, len(ss.entities))
	for _, state := range ss.entities {
		states = append(states, state)
	}
	
	return states
}

// ApplySnapshot 应用帧同步快照（模式切换时使用）
func (ss *StateSync) ApplySnapshot(snapshot *common.GameSnapshot) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	
	// 将帧同步快照转换为状态同步初始状态
	for frame, inputs := range snapshot.Inputs {
		// 应用所有输入，计算初始状态
		_ = frame // 实际使用中应处理每一帧
		_ = inputs
	}
	
	ss.logger.Info("应用帧同步快照", zap.Int64("frame", snapshot.Frame))
}

// Pause 暂停状态同步
func (ss *StateSync) Pause() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.paused = true
	ss.logger.Info("状态同步暂停")
}

// Resume 恢复状态同步
func (ss *StateSync) Resume() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.paused = false
	ss.logger.Info("状态同步恢复")
}

// Stop 停止状态同步
func (ss *StateSync) Stop() {
	close(ss.quitCh)
}
