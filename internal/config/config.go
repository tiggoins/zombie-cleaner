package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type ContainerRuntime string

const (
	RuntimeDocker     ContainerRuntime = "docker"
	RuntimeContainerd ContainerRuntime = "containerd"
)

type Config struct {
	Cleaner CleanerConfig `yaml:"cleaner"`
	Metrics MetricsConfig `yaml:"metrics"`
	Logger  LoggerConfig  `yaml:"logger"`
}

type MetricsConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type LoggerConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type CleanerConfig struct {
	// 检测间隔
	CheckInterval time.Duration `yaml:"check_interval"`
	// 确认次数 - 连续几次发现僵尸进程才执行清理
	ConfirmCount int `yaml:"confirm_count"`
	// 容器操作超时时间
	ContainerTimeout time.Duration `yaml:"container_timeout"`
	// 最大并发处理容器数量
	MaxConcurrentContainers int `yaml:"max_concurrent_containers"`
	// 白名单容器名称模式
	WhitelistPatterns []string `yaml:"whitelist_patterns"`
	// 是否启用干跑模式（只检测不清理）
	DryRun bool `yaml:"dry_run"`
	// 容器运行时类型 ("docker", "containerd",默认为"docker")
	ContainerRuntime ContainerRuntime `yaml:"container_runtime"`
}

func Load(configFile string) *Config {
	cfg := &Config{
		Cleaner: CleanerConfig{
			CheckInterval:           5 * time.Minute,
			ConfirmCount:            3,
			ContainerTimeout:        10 * time.Second,
			MaxConcurrentContainers: 10,
			WhitelistPatterns:       []string{},
			DryRun:                  false,
			ContainerRuntime:        RuntimeDocker,
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Port:    9090,
		},
		Logger: LoggerConfig{
			Level:  "info",
			Format: "json",
		},
	}

	// 如果配置文件存在，则加载
	if _, err := os.Stat(configFile); err == nil {
		data, err := os.ReadFile(configFile)
		if err != nil {
			panic("读取配置文件失败")
		}

		if err := yaml.Unmarshal(data, cfg); err != nil {
			panic("解析配置文件失败")
		}
	}

	cfg.validate()
	return cfg
}

func (c *Config) validate() {
	if c.Cleaner.CheckInterval <= 0 {
		panic("检测间隔必须大于0")
	}
	if c.Cleaner.ConfirmCount <= 0 {
		panic("确认次数必须大于0")
	}
	if c.Cleaner.ContainerTimeout <= 0 {
		panic("容器超时时间必须大于0")
	}
	if c.Cleaner.MaxConcurrentContainers <= 0 {
		c.Cleaner.MaxConcurrentContainers = 10
	}
	if c.Cleaner.ContainerRuntime != RuntimeContainerd && c.Cleaner.ContainerRuntime != RuntimeDocker {
		panic("容器运行时必须是docker或containerd")
	}
}
