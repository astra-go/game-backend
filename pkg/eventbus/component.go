package eventbus

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/astra-go/astra/log"
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/nats-io/nats.go"
)

// EventBusComponent 游戏事件总线组件（高层封装）
type EventBusComponent struct {
	bus      *EventBus
	logger   *log.Logger
	handlers sync.Map // subject -> []HandlerFunc
	mu       sync.RWMutex
}

// HandlerFunc 事件处理函数
type HandlerFunc func(data []byte) error

// NewEventBusComponent 创建事件总线组件
func NewEventBusComponent(bus *EventBus, logger *log.Logger) *EventBusComponent {
	return &EventBusComponent{
		bus:    bus,
		logger: logger,
	}
}

// Init 初始化组件
func (ebc *EventBusComponent) Init() error {
	ebc.logger.Info("EventBusComponent 初始化")
	return nil
}

// ========== 房间事件发布 ==========

// PublishRoomCreated 发布房间创建事件
func (ebc *EventBusComponent) PublishRoomCreated(roomID string, room *common.Room) error {
	subject := fmt.Sprintf("room.%s.created", roomID)
	return ebc.bus.Publish(subject, room)
}

// PublishRoomDestroyed 发布房间销毁事件
func (ebc *EventBusComponent) PublishRoomDestroyed(roomID string) error {
	subject := fmt.Sprintf("room.%s.destroyed", roomID)
	return ebc.bus.Publish(subject, map[string]string{"room_id": roomID})
}

// PublishPlayerJoin 发布玩家加入房间事件
func (ebc *EventBusComponent) PublishPlayerJoin(roomID, playerID string, teamID, heroID int32) error {
	subject := fmt.Sprintf("room.%s.player_join", roomID)
	data := map[string]any{
		"room_id":   roomID,
		"player_id": playerID,
		"team_id":   teamID,
		"hero_id":   heroID,
	}
	return ebc.bus.Publish(subject, data)
}

// PublishPlayerLeave 发布玩家离开房间事件
func (ebc *EventBusComponent) PublishPlayerLeave(roomID, playerID string) error {
	subject := fmt.Sprintf("room.%s.player_leave", roomID)
	data := map[string]string{
		"room_id":   roomID,
		"player_id": playerID,
	}
	return ebc.bus.Publish(subject, data)
}

// PublishRoomBroadcast 发布房间广播消息
func (ebc *EventBusComponent) PublishRoomBroadcast(roomID string, msg *common.WSMessage) error {
	subject := fmt.Sprintf("room.%s.broadcast", roomID)
	return ebc.bus.Publish(subject, msg)
}

// PublishRoomInput 发布玩家输入事件
func (ebc *EventBusComponent) PublishRoomInput(roomID string, input *common.InputCommand) error {
	subject := fmt.Sprintf("room.%s.input", roomID)
	return ebc.bus.Publish(subject, input)
}

// PublishRoomFrame 发布帧同步数据
func (ebc *EventBusComponent) PublishRoomFrame(roomID string, frame int64, inputs map[string]common.InputCommand) error {
	subject := fmt.Sprintf("room.%s.frame.%d", roomID, frame)
	data := map[string]any{
		"frame":  frame,
		"inputs": inputs,
	}
	return ebc.bus.Publish(subject, data)
}

// ========== 匹配事件发布 ==========

// PublishMatchEnqueue 发布匹配入队事件
func (ebc *EventBusComponent) PublishMatchEnqueue(ticket *common.MatchTicket) error {
	return ebc.bus.Publish(SubjectMatchEnqueue, ticket)
}

// PublishMatchDequeue 发布匹配出队事件
func (ebc *EventBusComponent) PublishMatchDequeue(playerID string) error {
	data := map[string]string{"player_id": playerID}
	return ebc.bus.Publish(SubjectMatchDequeue, data)
}

// PublishMatchSuccess 发布匹配成功事件
func (ebc *EventBusComponent) PublishMatchSuccess(result *common.MatchResult) error {
	return ebc.bus.Publish(SubjectMatchSuccess, result)
}

// PublishMatchTimeout 发布匹配超时事件
func (ebc *EventBusComponent) PublishMatchTimeout(playerID string) error {
	data := map[string]string{"player_id": playerID}
	return ebc.bus.Publish(SubjectMatchTimeout, data)
}

// ========== 玩家事件发布 ==========

// PublishPlayerLogin 发布玩家登录事件
func (ebc *EventBusComponent) PublishPlayerLogin(playerID string, player *common.Player) error {
	return ebc.bus.Publish(SubjectPlayerLogin, player)
}

// PublishPlayerLogout 发布玩家登出事件
func (ebc *EventBusComponent) PublishPlayerLogout(playerID string) error {
	data := map[string]string{"player_id": playerID}
	return ebc.bus.Publish(SubjectPlayerLogout, data)
}

// PublishPlayerUpdate 发布玩家信息更新事件
func (ebc *EventBusComponent) PublishPlayerUpdate(player *common.Player) error {
	return ebc.bus.Publish(SubjectPlayerUpdate, player)
}

// ========== 事件订阅 ==========

// SubscribeRoomCreated 订阅房间创建事件
func (ebc *EventBusComponent) SubscribeRoomCreated(handler func(roomID string, room *common.Room) error) error {
	return ebc.bus.Subscribe(SubjectRoomCreated, func(msg *nats.Msg) {
		var room common.Room
		if err := json.Unmarshal(msg.Data, &room); err != nil {
			ebc.logger.Error("解析房间创建事件失败", "error", err)
			return
		}
		if err := handler(room.ID, &room); err != nil {
			ebc.logger.Error("处理房间创建事件失败",
				"room_id", room.ID,
				"error", err,
			)
		}
	})
}

// SubscribeRoomDestroyed 订阅房间销毁事件
func (ebc *EventBusComponent) SubscribeRoomDestroyed(handler func(roomID string) error) error {
	return ebc.bus.Subscribe(SubjectRoomDestroyed, func(msg *nats.Msg) {
		var data map[string]string
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			ebc.logger.Error("解析房间销毁事件失败", "error", err)
			return
		}
		roomID := data["room_id"]
		if err := handler(roomID); err != nil {
			ebc.logger.Error("处理房间销毁事件失败",
				"room_id", roomID,
				"error", err,
			)
		}
	})
}

// SubscribePlayerJoin 订阅玩家加入房间事件
func (ebc *EventBusComponent) SubscribePlayerJoin(handler func(roomID, playerID string, teamID, heroID int32) error) error {
	return ebc.bus.Subscribe(SubjectRoomJoin, func(msg *nats.Msg) {
		var data map[string]any
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			ebc.logger.Error("解析玩家加入事件失败", "error", err)
			return
		}
		roomID, ok := data["room_id"].(string)
		if !ok {
			ebc.logger.Error("room_id类型错误")
			return
		}
		playerID, ok := data["player_id"].(string)
		if !ok {
			ebc.logger.Error("player_id类型错误")
			return
		}
		teamIDFloat, ok := data["team_id"].(float64)
		if !ok {
			ebc.logger.Error("team_id类型错误")
			return
		}
		heroIDFloat, ok := data["hero_id"].(float64)
		if !ok {
			ebc.logger.Error("hero_id类型错误")
			return
		}
		teamID := int32(teamIDFloat)
		heroID := int32(heroIDFloat)
		if err := handler(roomID, playerID, teamID, heroID); err != nil {
			ebc.logger.Error("处理玩家加入事件失败",
				"room_id", roomID,
				"player_id", playerID,
				"error", err,
			)
		}
	})
}

// SubscribePlayerLeave 订阅玩家离开房间事件
func (ebc *EventBusComponent) SubscribePlayerLeave(handler func(roomID, playerID string) error) error {
	return ebc.bus.Subscribe(SubjectRoomLeave, func(msg *nats.Msg) {
		var data map[string]string
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			ebc.logger.Error("解析玩家离开事件失败", "error", err)
			return
		}
		roomID := data["room_id"]
		playerID := data["player_id"]
		if err := handler(roomID, playerID); err != nil {
			ebc.logger.Error("处理玩家离开事件失败",
				"room_id", roomID,
				"player_id", playerID,
				"error", err,
			)
		}
	})
}

// SubscribeRoomBroadcast 订阅房间广播消息
func (ebc *EventBusComponent) SubscribeRoomBroadcast(handler func(msg *common.WSMessage) error) error {
	return ebc.bus.Subscribe(SubjectRoomBroadcast, func(natsMsg *nats.Msg) {
		var msg common.WSMessage
		if err := json.Unmarshal(natsMsg.Data, &msg); err != nil {
			ebc.logger.Error("解析房间广播消息失败", "error", err)
			return
		}
		if err := handler(&msg); err != nil {
			ebc.logger.Error("处理房间广播消息失败",
				"room_id", msg.RoomID,
				"type", msg.Type,
				"error", err,
			)
		}
	})
}

// SubscribeRoomInput 订阅玩家输入事件
func (ebc *EventBusComponent) SubscribeRoomInput(handler func(roomID string, input *common.InputCommand) error) error {
	return ebc.bus.Subscribe(SubjectRoomInput, func(msg *nats.Msg) {
		var input common.InputCommand
		if err := json.Unmarshal(msg.Data, &input); err != nil {
			ebc.logger.Error("解析玩家输入事件失败", "error", err)
			return
		}
		// 从subject中提取roomID
		roomID := extractRoomIDFromSubject(msg.Subject)
		if err := handler(roomID, &input); err != nil {
			ebc.logger.Error("处理玩家输入事件失败",
				"room_id", roomID,
				"player_id", input.PlayerID,
				"error", err,
			)
		}
	})
}

// SubscribeRoomFrame 订阅帧同步数据
func (ebc *EventBusComponent) SubscribeRoomFrame(roomID string, handler func(frame int64, inputs map[string]common.InputCommand) error) error {
	subject := fmt.Sprintf("room.%s.frame.*", roomID)
	return ebc.bus.Subscribe(subject, func(msg *nats.Msg) {
		var data map[string]any
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			ebc.logger.Error("解析帧同步数据失败", "error", err)
			return
		}
		frame := int64(data["frame"].(float64))
		inputsRaw := data["inputs"].(map[string]any)
		inputs := make(map[string]common.InputCommand)
		for playerID, inputData := range inputsRaw {
			inputBytes, _ := json.Marshal(inputData)
			var input common.InputCommand
			json.Unmarshal(inputBytes, &input)
			inputs[playerID] = input
		}
		if err := handler(frame, inputs); err != nil {
			ebc.logger.Error("处理帧同步数据失败",
				"room_id", roomID,
				"frame", frame,
				"error", err,
			)
		}
	})
}

// ========== 匹配事件订阅 ==========

// SubscribeMatchEnqueue 订阅匹配入队事件
func (ebc *EventBusComponent) SubscribeMatchEnqueue(handler func(ticket *common.MatchTicket) error) error {
	return ebc.bus.Subscribe(SubjectMatchEnqueue, func(msg *nats.Msg) {
		var ticket common.MatchTicket
		if err := json.Unmarshal(msg.Data, &ticket); err != nil {
			ebc.logger.Error("解析匹配入队事件失败", "error", err)
			return
		}
		if err := handler(&ticket); err != nil {
			ebc.logger.Error("处理匹配入队事件失败",
				"player_id", ticket.PlayerID,
				"error", err,
			)
		}
	})
}

// QueueSubscribeMatchEnqueue 队列订阅匹配入队事件（负载均衡）
func (ebc *EventBusComponent) QueueSubscribeMatchEnqueue(queue string, handler func(ticket *common.MatchTicket) error) error {
	return ebc.bus.QueueSubscribe(SubjectMatchEnqueue, queue, func(msg *nats.Msg) {
		var ticket common.MatchTicket
		if err := json.Unmarshal(msg.Data, &ticket); err != nil {
			ebc.logger.Error("解析匹配入队事件失败", "error", err)
			return
		}
		if err := handler(&ticket); err != nil {
			ebc.logger.Error("处理匹配入队事件失败",
				"player_id", ticket.PlayerID,
				"error", err,
			)
		}
	})
}

// SubscribeMatchSuccess 订阅匹配成功事件
func (ebc *EventBusComponent) SubscribeMatchSuccess(handler func(result *common.MatchResult) error) error {
	return ebc.bus.Subscribe(SubjectMatchSuccess, func(msg *nats.Msg) {
		var result common.MatchResult
		if err := json.Unmarshal(msg.Data, &result); err != nil {
			ebc.logger.Error("解析匹配成功事件失败", "error", err)
			return
		}
		if err := handler(&result); err != nil {
			ebc.logger.Error("处理匹配成功事件失败",
				"room_id", result.RoomID,
				"error", err,
			)
		}
	})
}

// ========== 玩家事件订阅 ==========

// SubscribePlayerLogin 订阅玩家登录事件
func (ebc *EventBusComponent) SubscribePlayerLogin(handler func(player *common.Player) error) error {
	return ebc.bus.Subscribe(SubjectPlayerLogin, func(msg *nats.Msg) {
		var player common.Player
		if err := json.Unmarshal(msg.Data, &player); err != nil {
			ebc.logger.Error("解析玩家登录事件失败", "error", err)
			return
		}
		if err := handler(&player); err != nil {
			ebc.logger.Error("处理玩家登录事件失败",
				"player_id", player.ID,
				"error", err,
			)
		}
	})
}

// SubscribePlayerLogout 订阅玩家登出事件
func (ebc *EventBusComponent) SubscribePlayerLogout(handler func(playerID string) error) error {
	return ebc.bus.Subscribe(SubjectPlayerLogout, func(msg *nats.Msg) {
		var data map[string]string
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			ebc.logger.Error("解析玩家登出事件失败", "error", err)
			return
		}
		playerID := data["player_id"]
		if err := handler(playerID); err != nil {
			ebc.logger.Error("处理玩家登出事件失败",
				"player_id", playerID,
				"error", err,
			)
		}
	})
}

// SubscribePlayerUpdate 订阅玩家信息更新事件
func (ebc *EventBusComponent) SubscribePlayerUpdate(handler func(player *common.Player) error) error {
	return ebc.bus.Subscribe(SubjectPlayerUpdate, func(msg *nats.Msg) {
		var player common.Player
		if err := json.Unmarshal(msg.Data, &player); err != nil {
			ebc.logger.Error("解析玩家更新事件失败", "error", err)
			return
		}
		if err := handler(&player); err != nil {
			ebc.logger.Error("处理玩家更新事件失败",
				"player_id", player.ID,
				"error", err,
			)
		}
	})
}

// ========== 请求-响应模式 ==========

// RequestRoomInfo 请求房间信息
func (ebc *EventBusComponent) RequestRoomInfo(roomID string, timeout time.Duration) (*common.Room, error) {
	subject := fmt.Sprintf("room.%s.info.request", roomID)
	data := map[string]string{"room_id": roomID}
	msg, err := ebc.bus.Request(subject, data, timeout)
	if err != nil {
		return nil, err
	}
	var room common.Room
	if err := json.Unmarshal(msg.Data, &room); err != nil {
		return nil, fmt.Errorf("解析房间信息失败: %w", err)
	}
	return &room, nil
}

// RequestPlayerInfo 请求玩家信息
func (ebc *EventBusComponent) RequestPlayerInfo(playerID string, timeout time.Duration) (*common.Player, error) {
	subject := fmt.Sprintf("player.%s.info.request", playerID)
	data := map[string]string{"player_id": playerID}
	msg, err := ebc.bus.Request(subject, data, timeout)
	if err != nil {
		return nil, err
	}
	var player common.Player
	if err := json.Unmarshal(msg.Data, &player); err != nil {
		return nil, fmt.Errorf("解析玩家信息失败: %w", err)
	}
	return &player, nil
}

// ========== 辅助方法 ==========

// extractRoomIDFromSubject 从subject中提取roomID
// 例如: "room.room_123.input" -> "room_123"
func extractRoomIDFromSubject(subject string) string {
	parts := splitSubject(subject)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func splitSubject(subject string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(subject); i++ {
		if subject[i] == '.' {
			parts = append(parts, subject[start:i])
			start = i + 1
		}
	}
	if start < len(subject) {
		parts = append(parts, subject[start:])
	}
	return parts
}

// Close 关闭事件总线
func (ebc *EventBusComponent) Close() {
	ebc.bus.Close()
}
