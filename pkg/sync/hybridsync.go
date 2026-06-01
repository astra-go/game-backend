package sync

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/astra-go/astra/log"
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/framesync"
	"github.com/astra-go/game-backend/pkg/statesync"
)

// HybridSync 混合同步管理器
// 用于 MOBA 游戏：Lockstep（帧同步）+ 状态同步
type HybridSync struct {
	mu              sync.Mutex
	session         common.RoomSessionInterface
	frameSync       *framesync.FrameSync
	stateSync       *statesync.StateSync
	currentMode     common.SyncMode
	transitioning   bool
	lastTransition  time.Time
	logger          *log.Logger
	
	// 混合同步配置
	lockstepFrames  int64  // Lockstep 持续帧数
	stateSyncFrames int64  // 状态同步持续帧数
	frameCount      int64  // 当前模式下的帧计数
}

// NewHybridSync 创建混合同步管理器
func NewHybridSync(session common.RoomSessionInterface, logger *log.Logger) *HybridSync {
	return &HybridSync{
		session:         session,
		currentMode:     common.SyncModeFrame, // 默认从帧同步开始
		lockstepFrames:  300,  // 5秒@60fps
		stateSyncFrames: 60,   // 3秒@20fps
		frameCount:      0,
		logger:          logger.WithFields("component", "hybridsync"),
	}
}

// Start 启动混合同步
func (hs *HybridSync) Start() error {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	
	hs.logger.Info("启动混合同步", "initial_mode", string(hs.currentMode))
	
	// 启动帧同步
	hs.frameSync = framesync.NewFrameSync(hs.session, 16) // 60fps
	go hs.frameSync.Run()
	
	// 启动状态同步（低频率）
	hs.stateSync = statesync.NewStateSync(hs.session, 20) // 20Hz
	go hs.stateSync.Run()
	
	// 启动模式切换协程
	go hs.modeSwitchLoop()
	
	return nil
}

// modeSwitchLoop 模式切换循环
func (hs *HybridSync) modeSwitchLoop() {
	ticker := time.NewTicker(100 * time.Millisecond) // 10Hz 检查
	defer ticker.Stop()
	
	for range ticker.C {
		hs.mu.Lock()
		
		if hs.transitioning {
			hs.mu.Unlock()
			continue
		}
		
		hs.frameCount++
		
		// 检查是否需要切换模式
		shouldSwitch := false
		if hs.currentMode == common.SyncModeFrame && hs.frameCount >= hs.lockstepFrames {
			shouldSwitch = true
		} else if hs.currentMode == common.SyncModeState && hs.frameCount >= hs.stateSyncFrames {
			shouldSwitch = true
		}
		
		hs.mu.Unlock()
		
		if shouldSwitch {
			hs.switchMode()
		}
	}
}

// switchMode 切换同步模式
func (hs *HybridSync) switchMode() error {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	
	hs.transitioning = true
	defer func() {
		hs.transitioning = false
	}()
	
	oldMode := hs.currentMode
	
	// 切换模式
	if hs.currentMode == common.SyncModeFrame {
		hs.currentMode = common.SyncModeState
		hs.frameCount = 0
		
		// 从帧同步切换到状态同步
		// 1. 暂停帧同步广播
		hs.frameSync.Pause()
		
		// 2. 发送全量状态快照
		snapshot := hs.frameSync.GetSnapshot()
		hs.stateSync.ApplySnapshot(snapshot)
		
		// 3. 恢复状态同步
		hs.stateSync.Resume()
		
		hs.logger.Info("切换到状态同步",
			"from", string(oldMode),
			"to", string(hs.currentMode),
		)
		
	} else {
		hs.currentMode = common.SyncModeFrame
		hs.frameCount = 0
		
		// 从状态同步切换到帧同步
		// 1. 暂停状态同步
		hs.stateSync.Pause()
		
		// 2. 发送模式切换消息
		hs.broadcastModeSwitch(common.SyncModeFrame)
		
		// 3. 恢复帧同步
		hs.frameSync.Resume()
		
		hs.logger.Info("切换到帧同步",
			"from", string(oldMode),
			"to", string(hs.currentMode),
		)
	}
	
	return nil
}

// broadcastModeSwitch 广播模式切换消息
func (hs *HybridSync) broadcastModeSwitch(newMode common.SyncMode) {
	msg := map[string]interface{}{
		"type":      "mode_switch",
		"new_mode":  string(newMode),
		"frame":     hs.frameSync.GetCurrentFrame(),
		"timestamp": time.Now().UnixNano(),
	}
	
	_, _ = json.Marshal(msg)
	// 通过 NATS 广播
	// nats.Publish(fmt.Sprintf("room.%s.mode_switch", hs.session.GetRoomID()), data)
	
	hs.logger.Debug("广播模式切换", "new_mode", string(newMode))
}

// SubmitInput 提交玩家输入（根据当前模式路由）
func (hs *HybridSync) SubmitInput(input common.InputCommand) error {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	
	if hs.currentMode == common.SyncModeFrame {
		return hs.frameSync.SubmitInput(input)
	} else {
		// 状态同步模式下，输入直接发送给状态同步组件
		delta := common.EntityDelta{
			EntityID: 1, // 简化：假设只有一个实体
			Frame:    hs.session.GetFrame(),
		}
		return hs.stateSync.SubmitDelta(delta)
	}
}

// GetCurrentMode 获取当前同步模式
func (hs *HybridSync) GetCurrentMode() common.SyncMode {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return hs.currentMode
}

// Stop 停止混合同步
func (hs *HybridSync) Stop() {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	
	if hs.frameSync != nil {
		hs.frameSync.Stop()
	}
	
	if hs.stateSync != nil {
		hs.stateSync.Stop()
	}
	
	hs.logger.Info("混合同步停止")
}
