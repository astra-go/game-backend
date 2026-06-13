package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/astra-go/astra/config"
)

const (
	DefaultDataID = "astra-game.yaml"
	DefaultGroup  = "DEFAULT_GROUP"
)

// Config 应用配置结构体
type Config struct {
	HTTP struct {
		GatewayAddr string `yaml:"gateway_addr"`
		RoomAddr    string `yaml:"room_addr"`
		MatchAddr   string `yaml:"match_addr"`
		PlayerAddr  string `yaml:"player_addr"`
	} `yaml:"http"`

	Redis struct {
		Addr         string `yaml:"addr"`
		Password     string `yaml:"password"`
		DB           int    `yaml:"db"`
		PoolSize     int    `yaml:"pool_size"`
		MinIdleConns int    `yaml:"min_idle_conns"`
	} `yaml:"redis"`

	MySQL struct {
		Host         string `yaml:"host"`
		Port         int    `yaml:"port"`
		User         string `yaml:"user"`
		Password     string `yaml:"password"`
		Database     string `yaml:"database"`
		Charset      string `yaml:"charset"`
		ParseTime    bool   `yaml:"parse_time"`
		MaxIdleConns int    `yaml:"max_idle_conns"`
		MaxOpenConns int    `yaml:"max_open_conns"`
	} `yaml:"mysql"`

	NATS struct {
		URL           string `yaml:"url"`
		MaxReconnect  int    `yaml:"max_reconnect"`
		ReconnectWait int    `yaml:"reconnect_wait"`
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
		Enabled           bool `yaml:"enabled"`
		RequestsPerSecond int  `yaml:"requests_per_second"`
		Burst             int  `yaml:"burst"`
	} `yaml:"ratelimit"`
}

var (
	globalConfig *Config
	configMutex  sync.RWMutex
	astraConfig  *config.Config
)

// LoadConfig 加载配置（优先从Nacos，失败则使用本地文件）
func LoadConfig(nacosConfigPath string) (*Config, error) {
	cfg, err := loadWithAstra(nacosConfigPath)
	if err != nil {
		slog.Warn("从Nacos加载配置失败，使用本地配置", "error", err)
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

// loadWithAstra 使用 astra 配置系统加载
func loadWithAstra(nacosConfigPath string) (*Config, error) {
	nacosCfg, err := readNacosConnectionConfig(nacosConfigPath)
	if err != nil {
		return nil, fmt.Errorf("读取Nacos配置失败: %w", err)
	}

	// 使用 astra config 的 NacosSource
	nacosSrc, err := config.NewNacosSource(config.NacosSourceConfig{
		ServerAddr: nacosCfg.ServerAddr,
		Namespace:  nacosCfg.Namespace,
		Group:      nacosCfg.Group,
		DataID:     nacosCfg.DataID,
		Format:     config.YAMLFormat,
		Username:   nacosCfg.Username,
		Password:   nacosCfg.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("创建Nacos源失败: %w", err)
	}

	// 使用本地配置作为 fallback
	localSrc := &localConfigSource{path: findLocalConfigPath()}

	// 使用 astra 配置系统加载
	astraCfg, err := config.New(localSrc, nacosSrc)
	if err != nil {
		return nil, fmt.Errorf("创建astra配置失败: %w", err)
	}

	// 解析到 Config 结构体
	appCfg := &Config{}
	if err := astraCfg.Scan(appCfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// 启动热更新
	ctx, cancel := context.WithCancel(context.Background())
	astraCfg.StartWatch(ctx)

	// 注册热更新回调
	astraConfig = astraCfg
	astraConfig.Watch(func() {
		newCfg := &Config{}
		if err := astraConfig.Scan(newCfg); err != nil {
			slog.Error("热更新配置解析失败", "error", err)
			return
		}
		configMutex.Lock()
		globalConfig = newCfg
		configMutex.Unlock()
		slog.Info("配置热更新成功")
	})

	// 保持 cancel 引用（当前版本忽略，context 由系统管理）
	_ = cancel

	return appCfg, nil
}

// localConfigSource 本地配置文件源，实现 config.Source 接口
type localConfigSource struct {
	path string
}

func (s *localConfigSource) Name() string { return fmt.Sprintf("local:%s", s.path) }

func (s *localConfigSource) Load() (map[string]any, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := yaml.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func findLocalConfigPath() string {
	paths := []string{
		"configs/config.yaml",
		"../configs/config.yaml",
		filepath.Join(".", "configs", "config.yaml"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "configs/config.yaml"
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

// loadFromLocal 从本地配置文件加载（降级方案）
func loadFromLocal() (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(findLocalConfigPath())
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	slog.Info("从本地配置文件加载成功")
	return cfg, nil
}

// GetConfig 获取当前配置（线程安全）
func GetConfig() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return globalConfig
}

// GetString 获取字符串配置
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
func (c *Config) GetRedisAddr() string { return c.Redis.Addr }

// GetNATSAddr 获取NATS地址
func (c *Config) GetNATSAddr() string { return c.NATS.URL }

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

// WatchConfigChanges 手动监听配置变更
func WatchConfigChanges(callback func(*Config)) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if callback != nil {
				callback(GetConfig())
			}
		}
	}()
}
