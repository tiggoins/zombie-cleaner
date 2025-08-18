package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Cleaner CleanerConfig `yaml:"cleaner"`
	Metrics MetricsConfig `yaml:"metrics"`
	Logger  LoggerConfig  `yaml:"logger"`
}

type ContainerRuntime string

const (
	RuntimeDocker     ContainerRuntime = "docker"
	RuntimeContainerd ContainerRuntime = "containerd"
)

type CleanerConfig struct {
	// 检测间隔
	CheckInterval time.Duration `yaml:"check_interval"`
	// 确认次数 - 连续几次发现僵尸进程才执行清理
	ConfirmCount int `yaml:"confirm_count"`
	// 容器操作超时时间
	ContainerTimeout time.Duration `yaml:"container_timeout"`
	// 进程检查超时时间
	ProcessTimeout time.Duration `yaml:"process_timeout"`
	// 最大并发处理容器数量
	MaxConcurrentContainers int `yaml:"max_concurrent_containers"`
	// 白名单容器名称模式
	WhitelistPatterns []string `yaml:"whitelist_patterns"`
	// 是否启用干跑模式（只检测不清理）
	DryRun bool `yaml:"dry_run"`
	// 容器运行时类型 ("docker", "containerd",默认为"docker")
	ContainerRuntime ContainerRuntime `yaml:"container_runtime"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Port    int    `yaml:"port"`
	Path    string `yaml:"path"`
}

type LoggerConfig struct {
	Level      string `yaml:"level"`
	Format     string `yaml:"format"`
	OutputFile string `yaml:"output_file"`
}

func Load(configFile string) (*Config, error) {
	cfg := &Config{
		Cleaner: CleanerConfig{
			CheckInterval:           5 * time.Minute,
			ConfirmCount:            3,
			ContainerTimeout:        30 * time.Second,
			ProcessTimeout:          10 * time.Second,
			MaxConcurrentContainers: 10,
			WhitelistPatterns:       []string{},
			DryRun:                  false,
			ContainerRuntime:        RuntimeDocker,
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Port:    9090,
			Path:    "/metrics",
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
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}

		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件失败: %w", err)
		}
	}

	return cfg, cfg.validate()
}

func (c *Config) validate() error {
	if c.Cleaner.CheckInterval <= 0 {
		return fmt.Errorf("检测间隔必须大于0")
	}
	if c.Cleaner.ConfirmCount <= 0 {
		return fmt.Errorf("确认次数必须大于0")
	}
	if c.Cleaner.ContainerTimeout <= 0 {
		return fmt.Errorf("容器超时时间必须大于0")
	}
	if c.Cleaner.MaxConcurrentContainers <= 0 {
		c.Cleaner.MaxConcurrentContainers = 10
	}
	if c.Cleaner.ContainerRuntime != RuntimeContainerd && c.Cleaner.ContainerRuntime != RuntimeDocker {
		return fmt.Errorf("容器运行时必须是'docker'或'containerd'")
	}
	return nil
}
