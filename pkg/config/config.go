package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"gopkg.in/yaml.v3"
)

const (
	DefaultDataID = "astra-game.yaml"
	DefaultGroup  = "DEFAULT_GROUP"
)

// Config 应用配置结构体
type Config struct {
	HTTP struct {
		GatewayAddr string `yaml:"gateway_addr"`
		RoomAddr   string `yaml:"room_addr"`
		MatchAddr  string `yaml:"match_addr"`
		PlayerAddr string `yaml:"player_addr"`
	} `yaml:"http"`

	Redis struct {
		Addr         string `yaml:"addr"`
		Password     string `yaml:"password"`
		DB           int    `yaml:"db"`
		PoolSize     int    `yaml:"pool_size"`
		MinIdleConns int    `yaml:"min_idle_conns"`
	} `yaml:"redis"`

	MySQL struct {
		Host       string `yaml:"host"`
		Port       int    `yaml:"port"`
		User       string `yaml:"user"`
		Password   string `yaml:"password"`
		Database   string `yaml:"database"`
		Charset    string `yaml:"charset"`
		ParseTime  bool   `yaml:"parse_time"`
		MaxIdleConns int  `yaml:"max_idle_conns"`
		MaxOpenConns int  `yaml:"max_open_conns"`
	} `yaml:"mysql"`

	NATS struct {
		URL          string `yaml:"url"`
		MaxReconnect int    `yaml:"max_reconnect"`
		ReconnectWait int   `yaml:"reconnect_wait"`
	} `yaml:"nats"`

	Game struct {
		FrameSyncTickMs  int `yaml:"frame_sync_tick_ms"`
		FrameHistoryMax  int `yaml:"frame_history_max"`
		StateSyncHz      int `yaml:"state_sync_hz"`
		FullSyncInterval int `yaml:"full_sync_interval"`
		Match            struct {
			MMRDeltaInitial int `yaml:"mmr_delta_initial"`
			MMRDeltaMax     int `yaml:"mmr_delta_max"`
			MMRDeltaGrowth  int `yaml:"mmr_delta_growth"`
			MatchTimeout    int `yaml:"match_timeout"`
			QueueTTL        int `yaml:"queue_ttl"`
		} `yaml:"match"`
		Room struct {
			MaxPlayersPerRoom int `yaml:"max_players_per_room"`
			MaxRoomsPerNode   int `yaml:"max_rooms_per_node"`
			RoomTTL           int `yaml:"room_ttl"`
			ReconnectWindow   int `yaml:"reconnect_window"`
		} `yaml:"room"`
		Heartbeat struct {
			Interval int `yaml:"interval"`
			Timeout  int `yaml:"timeout"`
		} `yaml:"heartbeat"`
	} `yaml:"game"`

	Metrics struct {
		Enabled bool   `yaml:"enabled"`
		Port    int    `yaml:"port"`
		Path    string `yaml:"path"`
	} `yaml:"metrics"`

	Log struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
		Output string `yaml:"output"`
	} `yaml:"log"`

	Ratelimit struct {
		Enabled            bool `yaml:"enabled"`
		RequestsPerSecond  int  `yaml:"requests_per_second"`
		Burst              int  `yaml:"burst"`
	} `yaml:"ratelimit"`
}

var (
	globalConfig *Config
	configMutex  sync.RWMutex
	nacosClient  config_client.IConfigClient
	dataID       string
	group        string
)

// LoadConfig 加载配置（优先从Nacos，失败则使用本地文件）
func LoadConfig(nacosConfigPath string) (*Config, error) {
	// 尝试从Nacos加载
	cfg, err := loadFromNacos(nacosConfigPath)
	if err != nil {
		slog.Warn("从Nacos加载配置失败，使用本地配置", "error", err)
		// 降级：使用本地配置
		cfg, err = loadFromLocal()
		if err != nil {
			return nil, fmt.Errorf("加载本地配置失败: %w", err)
		}
	} else {
		slog.Info("从Nacos加载配置成功")
	}

	configMutex.Lock()
	globalConfig = cfg
	configMutex.Unlock()

	return cfg, nil
}

// loadFromNacos 从Nacos加载配置
func loadFromNacos(nacosConfigPath string) (*Config, error) {
	// 读取Nacos连接配置
	nacosConfig, err := readNacosConnectionConfig(nacosConfigPath)
	if err != nil {
		return nil, fmt.Errorf("读取Nacos配置失败: %w", err)
	}

	// 创建Nacos客户端
	client, err := createNacosClient(nacosConfig)
	if err != nil {
		return nil, fmt.Errorf("创建Nacos客户端失败: %w", err)
	}

	nacosClient = client
	dataID = nacosConfig.DataID
	group = nacosConfig.Group

	// 获取配置
	content, err := client.GetConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
	})
	if err != nil {
		return nil, fmt.Errorf("获取Nacos配置失败: %w", err)
	}

	// 解析配置
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(content), cfg); err != nil {
		return nil, fmt.Errorf("解析Nacos配置失败: %w", err)
	}

	// 启动配置监听（热更新）
	go watchConfig(client, dataID, group)

	return cfg, nil
}

// readNacosConnectionConfig 读取Nacos连接配置
func readNacosConnectionConfig(path string) (*NacosConnectionConfig, error) {
	if path == "" {
		path = "configs/nacos.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &NacosConnectionConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// 设置默认值
	if cfg.DataID == "" {
		cfg.DataID = DefaultDataID
	}
	if cfg.Group == "" {
		cfg.Group = DefaultGroup
	}

	return cfg, nil
}

// NacosConnectionConfig Nacos连接配置
type NacosConnectionConfig struct {
	ServerAddr string `yaml:"server_addr"`
	Namespace  string `yaml:"namespace"`
	Group      string `yaml:"group"`
	DataID     string `yaml:"data_id"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
}

// createNacosClient 创建Nacos客户端
func createNacosClient(cfg *NacosConnectionConfig) (config_client.IConfigClient, error) {
	// 创建ServerConfig
	serverConfig := []constant.ServerConfig{
		*constant.NewServerConfig(
			cfg.ServerAddr,
			8848, // 默认端口
		),
	}

	// 创建ClientConfig
	clientConfig := *constant.NewClientConfig(
		constant.WithNamespaceId(cfg.Namespace),
		constant.WithUsername(cfg.Username),
		constant.WithPassword(cfg.Password),
		constant.WithTimeoutMs(5000),
		constant.WithNotLoadCacheAtStart(true),
		constant.WithLogDir("/tmp/nacos/log"),
		constant.WithCacheDir("/tmp/nacos/cache"),
		constant.WithLogLevel("info"),
	)

	// 创建客户端
	client, err := clients.NewConfigClient(
		vo.NacosClientParam{
			ClientConfig:  &clientConfig,
			ServerConfigs: serverConfig,
		},
	)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// watchConfig 监听配置变更
func watchConfig(client config_client.IConfigClient, dataID, group string) {
	err := client.ListenConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
		OnChange: func(namespace, group, dataId, data string) {
			slog.Info("检测到配置变更，正在重新加载...", 
				"data_id", dataId,
				"group", group,
			)

			cfg := &Config{}
			if err := yaml.Unmarshal([]byte(data), cfg); err != nil {
				slog.Error("解析新配置失败", "error", err)
				return
			}

			configMutex.Lock()
			globalConfig = cfg
			configMutex.Unlock()

			slog.Info("配置热更新成功")
		},
	})

	if err != nil {
		slog.Error("监听Nacos配置失败", "error", err)
	}
}

// loadFromLocal 从本地配置文件加载
func loadFromLocal() (*Config, error) {
	// 尝试多个可能的路径
	paths := []string{
		"configs/config.yaml",
		"../configs/config.yaml",
		filepath.Join(".", "configs", "config.yaml"),
	}

	var configPath string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			configPath = p
			break
		}
	}

	if configPath == "" {
		return nil, fmt.Errorf("未找到配置文件 configs/config.yaml")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	slog.Info("从本地配置文件加载成功", "path", configPath)
	return cfg, nil
}

// GetConfig 获取当前配置（线程安全）
func GetConfig() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return globalConfig
}

// GetString 获取字符串配置（支持点号路径，如 "redis.addr"）
func GetString(path string) string {
	cfg := GetConfig()
	if cfg == nil {
		return ""
	}

	switch path {
	case "redis.addr":
		return cfg.Redis.Addr
	case "mysql.host":
		return cfg.MySQL.Host
	case "nats.url":
		return cfg.NATS.URL
	default:
		return ""
	}
}

// GetInt 获取整数配置
func GetInt(path string) int {
	cfg := GetConfig()
	if cfg == nil {
		return 0
	}

	switch path {
	case "redis.db":
		return cfg.Redis.DB
	case "mysql.port":
		return cfg.MySQL.Port
	case "game.frame_sync_tick_ms":
		return cfg.Game.FrameSyncTickMs
	default:
		return 0
	}
}

// GetBool 获取布尔配置
func GetBool(path string) bool {
	cfg := GetConfig()
	if cfg == nil {
		return false
	}

	switch path {
	case "metrics.enabled":
		return cfg.Metrics.Enabled
	case "ratelimit.enabled":
		return cfg.Ratelimit.Enabled
	default:
		return false
	}
}

// GetDSN 获取MySQL DSN
func (c *Config) GetDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=%t",
		c.MySQL.User,
		c.MySQL.Password,
		c.MySQL.Host,
		c.MySQL.Port,
		c.MySQL.Database,
		c.MySQL.Charset,
		c.MySQL.ParseTime,
	)
}

// GetRedisAddr 获取Redis地址
func (c *Config) GetRedisAddr() string {
	return c.Redis.Addr
}

// GetNATSAddr 获取NATS地址
func (c *Config) GetNATSAddr() string {
	return c.NATS.URL
}

// GetHTTPAddr 获取HTTP服务地址
func (c *Config) GetHTTPAddr(service string) string {
	switch service {
	case "gateway":
		return c.HTTP.GatewayAddr
	case "room":
		return c.HTTP.RoomAddr
	case "match":
		return c.HTTP.MatchAddr
	case "player":
		return c.HTTP.PlayerAddr
	default:
		return ":8080"
	}
}

// WatchConfigChanges 手动监听配置变更（可选）
func WatchConfigChanges(callback func(*Config)) {
	// 定期检查配置是否变更（简单实现）
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// 这里可以添加自定义的检查逻辑
			if callback != nil {
				callback(GetConfig())
			}
		}
	}()
}
