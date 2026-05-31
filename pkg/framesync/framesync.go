package framesync

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/astra-go/game-backend/pkg/common"
	"go.uber.org/zap"
)

// FrameSync 帧同步核心组件
type FrameSync struct {
	mu             sync.Mutex
	session        common.RoomSessionInterface // 使用接口，解耦循环依赖
	tickMs         int                        // 帧间隔(ms)
	frame          int64                      // 当前帧号
	inputs         map[int64]map[string]common.InputCommand // frame -> playerID -> input
	history        map[int64][]common.InputCommand          // 历史帧输入（用于追帧）
	historyMax     int                        // 最大历史帧数
	broadcastCh    chan *common.WSMessage
	inputCh        chan common.InputCommand
	quitCh         chan struct{}
	logger         *zap.Logger
	
	// 帧率控制
	ticker         *time.Ticker
	running        bool
	paused         bool // 暂停状态（用于混合同步模式切换）
}

// NewFrameSync 创建帧同步器
func NewFrameSync(session common.RoomSessionInterface, tickMs int) *FrameSync {
	return &FrameSync{
		session:       session,
		tickMs:        tickMs,
		frame:         0,
		inputs:        make(map[int64]map[string]common.InputCommand),
		history:       make(map[int64][]common.InputCommand),
		historyMax:    600, // 保存最近600帧(10秒@60fps)
		broadcastCh:   make(chan *common.WSMessage, 256),
		inputCh:       make(chan common.InputCommand, 1024),
		quitCh:        make(chan struct{}),
		logger:        zap.L().With(zap.String("component", "framesync")),
		paused:        false,
	}
}

// Run 启动帧同步循环
func (fs *FrameSync) Run() {
	fs.running = true
	fs.ticker = time.NewTicker(time.Duration(fs.tickMs) * time.Millisecond)
	
	fs.logger.Info("帧同步启动", zap.Int("tick_ms", fs.tickMs))
	
	defer func() {
		fs.ticker.Stop()
		fs.running = false
		fs.logger.Info("帧同步停止")
	}()
	
	// 启动输入处理协程
	go fs.processInputs()
	
	// 帧循环
	for {
		select {
		case <-fs.ticker.C:
			if !fs.paused {
				fs.tick()
			}
			
		case input := <-fs.inputCh:
			fs.collectInput(input)
			
		case <-fs.quitCh:
			return
		}
	}
}

// tick 每帧执行
func (fs *FrameSync) tick() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	frame := fs.frame
	
	// 收集本帧所有玩家输入
	inputs := fs.inputs[frame]
	if inputs == nil {
		inputs = make(map[string]common.InputCommand)
		fs.inputs[frame] = inputs
	}
	
	// 保存历史
	historyItem := make([]common.InputCommand, 0, len(inputs))
	for _, input := range inputs {
		historyItem = append(historyItem, input)
	}
	fs.history[frame] = historyItem
	
	// 清理旧历史
	if frame > int64(fs.historyMax) {
		delete(fs.history, frame-int64(fs.historyMax))
	}
	delete(fs.inputs, frame-int64(fs.historyMax))
	
	// 广播帧数据
	fs.broadcastFrame(frame, historyItem)
	
	// 推进帧号
	fs.frame++
	
	// 更新会话帧号（通过接口）
	// fs.session.SetFrame(fs.frame) // 需要在接口中定义
}

// collectInput 收集输入
func (fs *FrameSync) collectInput(input common.InputCommand) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	frame := input.Frame
	if _, ok := fs.inputs[frame]; !ok {
		fs.inputs[frame] = make(map[string]common.InputCommand)
	}
	
	// 同一玩家同一帧只保留最新输入
	fs.inputs[frame][input.PlayerID] = input
}

// processInputs 处理输入通道
func (fs *FrameSync) processInputs() {
	for {
		select {
		case input := <-fs.inputCh:
			fs.collectInput(input)
			
		case <-fs.quitCh:
			return
		}
	}
}

// broadcastFrame 广播帧数据
func (fs *FrameSync) broadcastFrame(frame int64, inputs []common.InputCommand) {
	msg := &common.WSMessage{
		Type:   common.WSMsgFrame,
		RoomID: fs.session.GetRoomID(),
		Frame:  frame,
		Data: map[string]interface{}{
			"frame":  frame,
			"inputs": inputs,
			"count":  len(inputs),
		},
	}
	
	// 序列化
	data, err := json.Marshal(msg)
	if err != nil {
		fs.logger.Error("帧数据序列化失败", zap.Error(err))
		return
	}
	
	_ = data
	// 通过NATS广播（实际项目中应使用事件总线）
	// nats.Publish(fmt.Sprintf("room.%s.frame.%d", fs.session.GetRoomID(), frame), data)
	
	fs.logger.Debug("帧广播",
		zap.Int64("frame", frame),
		zap.Int("input_count", len(inputs)),
	)
}

// SubmitInput 提交玩家输入
func (fs *FrameSync) SubmitInput(input common.InputCommand) error {
	if !fs.running {
		return fmt.Errorf("帧同步未运行")
	}
	
	// 输入帧号校正（防止作弊）
	if input.Frame < fs.frame-10 {
		// 太旧的帧，丢弃
		fs.logger.Warn("丢弃过期输入",
			zap.String("player_id", input.PlayerID),
			zap.Int64("input_frame", input.Frame),
			zap.Int64("current_frame", fs.frame),
		)
		return nil
	}
	
	if input.Frame > fs.frame+10 {
		// 太未来的帧，调整
		input.Frame = fs.frame
	}
	
	select {
	case fs.inputCh <- input:
		return nil
	case <-time.After(50 * time.Millisecond):
		return fmt.Errorf("输入通道阻塞")
	}
}

// GetHistory 获取历史帧（用于追帧/重连）
func (fs *FrameSync) GetHistory(fromFrame, toFrame int64) [][]common.InputCommand {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	count := toFrame - fromFrame + 1
	result := make([][]common.InputCommand, 0, count)
	
	for f := fromFrame; f <= toFrame; f++ {
		if history, ok := fs.history[f]; ok {
			result = append(result, history)
		} else {
			result = append(result, []common.InputCommand{})
		}
	}
	
	return result
}

// GetSnapshot 获取当前帧快照（用于模式切换）
func (fs *FrameSync) GetSnapshot() *common.GameSnapshot {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	return &common.GameSnapshot{
		Frame:    fs.frame,
		Inputs:   fs.inputs,
		Timestamp: time.Now().UnixNano(),
	}
}

// GetCurrentFrame 获取当前帧号
func (fs *FrameSync) GetCurrentFrame() int64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.frame
}

// Pause 暂停帧同步
func (fs *FrameSync) Pause() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.paused = true
	fs.logger.Info("帧同步暂停")
}

// Resume 恢复帧同步
func (fs *FrameSync) Resume() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.paused = false
	fs.logger.Info("帧同步恢复")
}

// Stop 停止帧同步
func (fs *FrameSync) Stop() {
	close(fs.quitCh)
}
