# Astra Game Backend

基于 Astra 框架的高性能游戏后端系统，支持帧同步和状态同步两种模式。

## 项目结构

```
astra-game-backend/
├── cmd/                    # 服务入口
│   ├── gateway/            # WebSocket 网关
│   ├── room/               # 房间管理服务
│   ├── match/              # 匹配服务
│   └── player/             # 玩家服务
├── pkg/                    # 核心组件
│   ├── gateway/            # 网关组件
│   ├── match/              # 匹配算法
│   ├── room/               # 房间管理
│   ├── framesync/          # 帧同步
│   ├── statesync/          # 状态同步
│   ├── player/             # 玩家服务
│   ├── eventbus/           # 事件总线
│   └── common/             # 公共类型
├── configs/                # 配置文件
├── scripts/                # 脚本
├── deploy/                 # 部署配置
│   └── k8s/               # Kubernetes
├── docker-compose.yml      # 本地开发环境
├── Makefile                # 构建脚本
└── README.md
```

## 快速开始

### 1. 启动基础设施

```bash
# 启动 Redis + MySQL + NATS
make docker-up

# 等待服务就绪
docker-compose ps
```

### 2. 初始化数据库

```bash
# 执行初始化脚本
mysql -h 127.0.0.1 -u root -p < scripts/init.sql
```

### 3. 编译运行

```bash
# 安装依赖
make deps

# 编译所有服务
make build

# 启动所有服务
make run
```

### 4. 验证

```bash
# 健康检查
curl <http://localhost:8080/health>  # 网关
curl <http://localhost:8081/health>  # 房间
curl <http://localhost:8082/health>  # 匹配
curl <http://localhost:8083/health>  # 玩家
```

## 核心功能

### 1. 帧同步 (Lockstep)

适用于 MOBA、格斗游戏等需要确定性计算的场景。

**特点：**
- 所有玩家每帧上报操作
- 服务器收集所有输入后广播
- 客户端根据输入模拟游戏逻辑
- 支持追帧（掉线重连）

**API 示例：**

```javascript
// 客户端连接
const ws = new WebSocket('ws://localhost:8080/ws?player_id=xxx&token=yyy&room_id=room_xxx');

// 发送操作
ws.send(JSON.stringify({
  type: 'input',
  room_id: 'room_xxx',
  frame: 1234,
  data: {
    player_id: 'player_xxx',
    type: 1,  // 1=移动, 2=技能, 3=攻击
    data: {...}
  }
}));

// 接收帧广播
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.type === 'frame') {
    // 处理帧数据
    applyFrame(msg.frame, msg.data.inputs);
  }
};
```

### 2. 状态同步

适用于 MMO、FPS 等大地图、多实体场景。

**特点：**
- 服务器权威，定期广播状态增量
- 只同步变化的实体（Delta Compression）
- 支持全量同步（防止累积误差）
- 带宽占用低

**API 示例：**

```javascript
// 接收状态增量
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.type === 'state_delta') {
    // 应用状态增量
    applyDelta(msg.frame, msg.delta);
  }
};
```

### 3. 匹配系统

基于 MMR/ELO 的范围匹配算法。

**特点：**
- 初始搜索范围：±100 MMR
- 每次扩大：+100 MMR
- 最大搜索范围：±800 MMR
- 超时时间：30 秒
- 原子性操作（Lua 脚本）

**API 示例：**

```bash
# 加入匹配队列
curl -X POST <http://localhost:8082/match/enqueue> \\
  -H "Content-Type: application/json" \\
  -d '{"player_id":"player_xxx","mode":"1v1","mmr":1200}'

# 查询匹配状态
curl <http://localhost:8082/match/status/player_xxx>

# 退出队列
curl -X POST <http://localhost:8082/match/dequeue> \\
  -H "Content-Type: application/json" \\
  -d '{"player_id":"player_xxx"}'
```

### 4. 房间管理

支持创建、加入、离开、销毁房间。

**API 示例：**

```bash
# 创建房间
curl -X POST <http://localhost:8081/rooms> \\
  -H "Content-Type: application/json" \\
  -d '{"owner_id":"player_xxx","mode":"1v1","max_players":2,"map_id":1}'

# 加入房间
curl -X POST <http://localhost:8081/rooms/room_xxx/players> \\
  -H "Content-Type: application/json" \\
  -d '{"player_id":"player_yyy","team_id":1,"hero_id":101}'

# 离开房间
curl -X DELETE <http://localhost:8081/rooms/room_xxx/players/player_yyy>
```

## 配置说明

配置文件：`configs/config.yaml`

```yaml
# 帧同步配置
game:
  frame_sync_tick_ms: 16    # 60fps
  frame_history_max: 600     # 保存最近600帧

# 状态同步配置
  state_sync_hz: 20         # 20Hz
  full_sync_interval: 300    # 每300帧全量同步

# 匹配配置
  match:
    mmr_delta_initial: 100  # 初始搜索范围
    mmr_delta_max: 800       # 最大搜索范围
    match_timeout: 30        # 匹配超时(秒)
```

## 监控

### Prometheus 指标

```
# 网关
ws_connections_active{}          # 当前活跃连接数
ws_messages_total{direction,msg_type}  # 消息总数
ws_connection_duration_seconds{reason} # 连接持续时间

# 匹配
match_queue_size{mode}          # 队列大小
match_success_total{mode}       # 匹配成功次数
match_timeout_total{mode}       # 匹配超时次数

# 房间
room_count{}                    # 房间数量
room_players_count{room_id}     # 房间玩家数
```

访问 `<http://localhost:9090>` 查看 Prometheus 控制台。

## 部署

### Docker Compose (本地开发)

```bash
make docker-up
```

### Kubernetes (生产环境)

```bash
# 构建镜像
make docker-build

# 部署到 K8s
make deploy
```

## 性能优化

### 1. 帧同步优化
- 输入合并：多个操作合并到同一帧
- 状态哈希：定期计算游戏状态哈希，检测不同步
- 断线重连：保存最近 600 帧历史

### 2. 状态同步优化
- 增量压缩：只同步变化的属性
- 兴趣管理：只同步视野内的实体
- 插值预测：客户端插值 + 服务器校正

### 3. 匹配优化
- 异步处理：后台协程处理匹配队列
- Lua 脚本：原子性操作，避免竞态
- 分片队列：按 MMR 分片，减少锁竞争

## 测试

```bash
# 单元测试
make test

# 性能测试
make bench

# 代码检查
make lint
```

## 技术栈

- **语言：** Go 1.21+
- **框架：** Astra (高性能 HTTP/WebSocket)
- **数据库：** MySQL 8.0 (持久化) + Redis (缓存/会话)
- **消息队列：** NATS (事件总线)
- **监控：** Prometheus + Grafana
- **部署：** Docker + Kubernetes

## 许可证

MIT License

## 贡献

欢迎提交 Issue 和 Pull Request！

---

**注意：** 本项目是框架代码，部分功能需要进一步完善：
- [ ] 完善 JWT 认证
- [ ] 实现完整的匹配算法（MMR 查询）
- [ ] 添加单元测试
- [ ] 完善错误处理
- [ ] 优化 WebSocket 连接管理（支持集群）
- [ ] 实现配置中心集成 (Nacos)
