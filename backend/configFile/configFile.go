package configFile

import (
	"bytes"
	"os"
	"sync"

	"github.com/spf13/viper"
)

// Config 全局配置结构体
type Config struct {
	Server ServerConfig `destructure:"server"`
	Jwt    JwtConfig    `destructure:"jwt"`
	Log    LogConfig    `destructure:"log"`
	SQLite SQLiteConfig `destructure:"sqlite"`
}

// SQLiteConfig SQLite 配置（与 Postgres 隔离，独立配置）
type SQLiteConfig struct {
	Path string `destructure:"path"` // 数据库文件路径，如 "data/iot.db"
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port int `destructure:"port"`
}

// JwtConfig JWT 配置
type JwtConfig struct {
	Secret     string `destructure:"secret"`
	ExpireHour int    `destructure:"expireHour"`
	Issuer     string `destructure:"issuer"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level     string `destructure:"level"`
	OutputDir string `destructure:"outputDir"`
	MaxSizeMB int    `destructure:"maxSizeMB"` // 单日志文件大小上限（MB），超过则滚动当日下一个文件；<=0 不按大小滚动
	MaxDays   int    `destructure:"maxDays"`   // 日志文件最大保留天数，超过则删除旧文件；<=0 不清理
}

var (
	cfg    *Config
	rwLock sync.RWMutex
	once   sync.Once

	// defaultYAML 嵌入二进制的默认配置内容（由 main 包通过 go:embed default.yaml 注入）
	defaultYAML []byte
)

// SetDefaultConfig 注入嵌入的默认配置内容（main 包 go:embed default.yaml 提供），须在 InitConfig 前调用
func SetDefaultConfig(data []byte) {
	defaultYAML = data
}

// InitConfig 显式初始化配置（推荐在 main 中调用）
// 加载策略（优先级由低到高）：
//  1. 嵌入二进制的 default.yaml：基础配置，单二进制部署无需外部配置文件
//  2. 运行目录下的 default.yaml：若存在则加载并合并，覆盖嵌入默认值，便于部署调参
//  3. APP_ENV 指定的 dev.yaml / prod.yaml：若设置则加载并合并，环境配置覆盖默认值
func InitConfig() *Config {
	once.Do(func() {
		v := viper.New()
		v.SetConfigType("yaml")

		// 1. 加载嵌入的 default.yaml 作为基础配置
		if defaultYAML == nil {
			panic("embedded default config is nil, call configFile.SetDefaultConfig first")
		}
		if err := v.ReadConfig(bytes.NewReader(defaultYAML)); err != nil {
			panic("failed to read embedded default config: " + err.Error())
		}

		// 2. 运行目录存在 default.yaml 时加载并合并（覆盖嵌入默认值）
		if _, err := os.Stat("default.yaml"); err == nil {
			diskV := viper.New()
			diskV.SetConfigType("yaml")
			diskV.SetConfigName("default")
			diskV.AddConfigPath(".")
			if err := diskV.ReadInConfig(); err != nil {
				panic("failed to read external default config: " + err.Error())
			}
			for _, key := range diskV.AllKeys() {
				v.Set(key, diskV.Get(key))
			}
		}

		// 3. 根据 APP_ENV 加载 dev.yaml / prod.yaml 并合并（环境配置覆盖默认值）
		env := os.Getenv("APP_ENV")
		if env != "" {
			envV := viper.New()
			envV.SetConfigType("yaml")
			envV.AddConfigPath(".")
			envV.SetConfigName(env)
			if err := envV.ReadInConfig(); err != nil {
				panic("failed to read " + env + " config: " + err.Error())
			}
			for _, key := range envV.AllKeys() {
				v.Set(key, envV.Get(key))
			}
		}

		// 4. 反序列化到全局配置
		cfg = &Config{}
		if err := v.Unmarshal(cfg); err != nil {
			panic("failed to unmarshal config: " + err.Error())
		}
	})
	return cfg
}

// GetConfig 线程安全读取配置
func GetConfig() (*Config, error) {
	rwLock.RLock()
	defer rwLock.RUnlock()
	if cfg == nil {
		return nil, nil
	}
	return cfg, nil
}

// MustGetConfig 读取失败则 panic
func MustGetConfig() *Config {
	cfg, err := GetConfig()
	if err != nil {
		panic("config not loaded: " + err.Error())
	}
	return cfg
}

// ReloadConfig 热重载配置
func ReloadConfig(dir, fileName string) error {
	rwLock.Lock()
	defer rwLock.Unlock()

	v := viper.New()
	v.SetConfigName(fileName)
	v.SetConfigType("yaml")
	if dir != "" {
		v.AddConfigPath(dir)
	}
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		return err
	}

	newCfg := &Config{}
	if err := v.Unmarshal(newCfg); err != nil {
		return err
	}

	cfg = newCfg
	return nil
}
