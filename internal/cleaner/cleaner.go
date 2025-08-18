package cleaner

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tiggoins/zombie-cleaner/internal/config"
	"github.com/tiggoins/zombie-cleaner/internal/detector"
	"github.com/tiggoins/zombie-cleaner/internal/logger"
	"github.com/tiggoins/zombie-cleaner/internal/metrics"
	"github.com/tiggoins/zombie-cleaner/internal/runtime"
)

// 容器状态跟踪
type ContainerState struct {
	ContainerID  string
	ZombieCount  int
	LastDetected time.Time
	InProgress   bool
	PodName      string
	Namespace    string
}

type Cleaner struct {
	config          *config.Config
	logger          *logger.Logger
	detector        *detector.Detector
	containerRuntime runtime.ContainerRuntimeInterface

	// 状态跟踪
	containerStates map[string]*ContainerState
	stateMutex      sync.RWMutex

	// 白名单正则表达式
	whitelistRegexes []*regexp.Regexp

	// 控制通道
	stopChan chan struct{}
}

func New(cfg *config.Config, log *logger.Logger) (*Cleaner, error) {
	det, err := detector.New(cfg.Cleaner.ProcessTimeout, cfg.Cleaner.ContainerRuntime, log)
	if err != nil {
		return nil, fmt.Errorf("创建检测器失败: %w", err)
	}

	// 创建容器运行时实现
	var containerRuntime runtime.ContainerRuntimeInterface
	switch cfg.Cleaner.ContainerRuntime {
	case config.RuntimeDocker:
		containerRuntime, err = runtime.NewDockerRuntime(log)
		if err != nil {
			log.Warn("无法创建Docker运行时", "error", err)
		}
	case config.RuntimeContainerd:
		containerRuntime, err = runtime.NewContainerdRuntime(log)
		if err != nil {
			log.Warn("无法创建Containerd运行时", "error", err)
		}
	}

	if containerRuntime == nil {
		return nil, fmt.Errorf("%s", "无法创建容器运行时实现")
	}

	c := &Cleaner{
		config:           cfg,
		logger:           log.WithComponent("cleaner"),
		detector:         det,
		containerRuntime: containerRuntime,
		containerStates:  make(map[string]*ContainerState),
		stopChan:         make(chan struct{}),
	}
	for _, pattern := range cfg.Cleaner.WhitelistPatterns {
		regex, err := regexp.Compile(pattern)
		if err != nil {
			log.Warn("白名单模式编译失败", "pattern", pattern, "error", err)
			continue
		}
		c.whitelistRegexes = append(c.whitelistRegexes, regex)
	}

	return c, nil
}

func (c *Cleaner) Start(ctx context.Context) {
	c.logger.Info("启动僵尸进程清理器",
		"check_interval", c.config.Cleaner.CheckInterval,
		"confirm_count", c.config.Cleaner.ConfirmCount,
		"dry_run", c.config.Cleaner.DryRun)

	ticker := time.NewTicker(c.config.Cleaner.CheckInterval)
	defer ticker.Stop()

	// 立即执行一次检测
	c.runCheck(ctx)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("收到上下文取消信号，停止清理器")
			return
		case <-c.stopChan:
			c.logger.Info("收到停止信号，停止清理器")
			return
		case <-ticker.C:
			c.runCheck(ctx)
		}
	}
}

func (c *Cleaner) Stop(ctx context.Context) {
	c.logger.Info("正在停止清理器...")
	close(c.stopChan)

	// 等待当前操作完成，使用RWMutex的RLock来避免阻塞其他读操作
	done := make(chan struct{})
	go func() {
		c.stateMutex.RLock()
		defer c.stateMutex.RUnlock()
		close(done)
	}()

	select {
	case <-done:
		c.logger.Info("清理器已停止")
	case <-ctx.Done():
		c.logger.Warn("停止清理器超时")
	}

	// 关闭容器运行时连接
	if c.containerRuntime != nil {
		if err := c.containerRuntime.Close(); err != nil {
			c.logger.Warn("关闭容器运行时连接失败", "error", err)
		}
	}

	// 关闭检测器
	if c.detector != nil {
		if err := c.detector.Close(); err != nil {
			c.logger.Warn("关闭检测器失败", "error", err)
		}
	}
}

func (c *Cleaner) runCheck(ctx context.Context) {
	c.logger.Debug("开始检测周期")

	zombies, err := c.detector.DetectZombies(ctx)
	if err != nil {
		c.logger.Error("检测僵尸进程失败", "error", err)
		metrics.CleanupFailures.WithLabelValues(metrics.GetNodeName(), "detection_failed").Inc()
		return
	}

	if len(zombies) == 0 {
		c.logger.Debug("未发现僵尸进程")
		c.cleanupOldStates()
		return
	}

	// 按容器分组处理僵尸进程
	containerZombies := make(map[string][]detector.ZombieInfo)
	for _, zombie := range zombies {
		if zombie.IsInContainer {
			containerID := zombie.Container.ID
			containerZombies[containerID] = append(containerZombies[containerID], zombie)
		}
	}

	c.processContainerZombies(ctx, containerZombies)
	c.cleanupOldStates()
}

func (c *Cleaner) processContainerZombies(ctx context.Context, containerZombies map[string][]detector.ZombieInfo) {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	for containerID, zombies := range containerZombies {
		if len(zombies) == 0 {
			continue
		}

		// 获取容器信息
		container := zombies[0].Container

		// 检查白名单
		if c.isWhitelisted(container.PodName) {
			c.logger.Debug("容器在白名单中，跳过清理",
				"container_id", containerID,
				"pod_name", container.PodName,
				"namespace", container.PodNS)
			continue
		}

		// 更新状态
		state, exists := c.containerStates[containerID]
		if !exists {
			state = &ContainerState{
				ContainerID: containerID,
				PodName:     container.PodName,
				Namespace:   container.PodNS,
			}
			c.containerStates[containerID] = state
		}

		// 防止重复处理
		if state.InProgress {
			c.logger.Debug("容器正在处理中，跳过", "container_id", containerID)
			continue
		}

		state.LastDetected = time.Now()
		state.ZombieCount++

		c.logger.Info("更新容器僵尸进程状态",
			"container_id", containerID,
			"pod_name", container.PodName,
			"namespace", container.PodNS,
			"zombie_count", state.ZombieCount,
			"confirm_threshold", c.config.Cleaner.ConfirmCount,
			"zombie_pids", c.getZombiePIDs(zombies))

		// 检查是否达到确认次数
		if state.ZombieCount >= c.config.Cleaner.ConfirmCount {
			// 检查是否有PPID为1的僵尸进程
			hasOrphanZombies := false
			for _, zombie := range zombies {
				if zombie.PPID == 1 {
					hasOrphanZombies = true
					c.logger.Warn("发现PPID为1的孤儿僵尸进程，无法直接清理",
						"container_id", containerID,
						"pod_name", container.PodName,
						"namespace", container.PodNS,
						"zombie_pid", zombie.PID,
						"zombie_count", state.ZombieCount)
				}
			}

			if hasOrphanZombies {
				// 对于包含孤儿僵尸进程的容器，只记录日志，不执行清理操作
				c.logger.Warn("容器包含孤儿僵尸进程，跳过清理操作",
					"container_id", containerID,
					"pod_name", container.PodName,
					"namespace", container.PodNS,
					"zombie_count", state.ZombieCount)
				// 重置计数器，避免重复报告
				state.ZombieCount = 0
			} else {
				c.logger.Warn("容器僵尸进程确认次数达到阈值，开始清理",
					"container_id", containerID,
					"pod_name", container.PodName,
					"namespace", container.PodNS,
					"zombie_count", state.ZombieCount)

				// 异步清理，避免阻塞其他容器的处理
				go c.cleanupContainer(ctx, containerID, state, zombies)
			}
		}
	}
}

func (c *Cleaner) cleanupContainer(ctx context.Context, containerID string, state *ContainerState, zombies []detector.ZombieInfo) {
	c.stateMutex.Lock()
	state.InProgress = true
	c.stateMutex.Unlock()

	defer func() {
		c.stateMutex.Lock()
		delete(c.containerStates, containerID)
		c.stateMutex.Unlock()
	}()

	containerLog := c.logger.WithContainer(containerID, state.PodName, state.Namespace)

	if c.config.Cleaner.DryRun {
		containerLog.Info("干跑模式：模拟清理容器", "zombie_count", len(zombies))
		return
	}

	containerLog.Info("开始清理容器", "zombie_count", len(zombies))

	// 首先尝试删除容器
	if err := c.removeContainer(ctx, containerID); err != nil {
		containerLog.Error("删除容器失败，尝试强制清理", "error", err)

		// 如果删除失败，尝试kill container-shim或containerd-shim
		if err := c.killContainerShim(containerID); err != nil {
			containerLog.Error("清理container-shim失败", "error", err)
			metrics.CleanupFailures.WithLabelValues(metrics.GetNodeName(), "cleanup_failed").Inc()
			return
		}
	}

	metrics.ContainersCleaned.WithLabelValues(metrics.GetNodeName(), state.Namespace, state.PodName).Inc()
	containerLog.Info("容器清理完成")
}

func (c *Cleaner) removeContainer(ctx context.Context, containerID string) error {
	// 设置超时
	timeoutCtx, cancel := context.WithTimeout(ctx, c.config.Cleaner.ContainerTimeout)
	defer cancel()

	c.logger.Info("尝试删除容器", "container_id", containerID)

	// 使用容器运行时接口删除容器
	if c.containerRuntime != nil {
		if err := c.containerRuntime.RemoveContainer(timeoutCtx, containerID, c.config.Cleaner.ContainerTimeout); err != nil {
			if timeoutCtx.Err() == context.DeadlineExceeded {
				metrics.ContainerOperationTimeouts.WithLabelValues(metrics.GetNodeName(), "remove").Inc()
			}
			return fmt.Errorf("删除容器失败: %w", err)
		}
		c.logger.Info("成功删除容器", "container_id", containerID)
		return nil
	}

	return fmt.Errorf("没有可用的容器运行时")
}

func (c *Cleaner) killContainerShim(containerID string) error {
	c.logger.Info("尝试kill container-shim", "container_id", containerID)

	// 查找containerd-shim和container-shim进程
	// containerd使用containerd-shim，而Docker可能使用docker-containerd-shim
	patterns := []string{
		fmt.Sprintf("containerd-shim.*%s", containerID[:12]),
		fmt.Sprintf("docker-containerd-shim.*%s", containerID[:12]),
	}

	var allPids []string
	for _, pattern := range patterns {
		cmd := exec.Command("pgrep", "-f", pattern)
		output, err := cmd.Output()
		if err != nil {
			// 如果找不到进程，继续查找其他模式
			c.logger.Debug("查找shim进程模式失败", "pattern", pattern, "error", err)
			continue
		}

		pids := strings.Fields(strings.TrimSpace(string(output)))
		allPids = append(allPids, pids...)
	}

	if len(allPids) == 0 {
		return fmt.Errorf("未找到docker-containerd-shim或containerd-shim进程")
	}

	// Kill所有相关的shim进程
	for _, pid := range allPids {
		killCmd := exec.Command("kill", "-9", pid)
		if err := killCmd.Run(); err != nil {
			c.logger.Warn("kill shim进程失败", "pid", pid, "error", err)
		} else {
			c.logger.Info("成功kill shim进程", "pid", pid)
		}
	}

	return nil
}

func (c *Cleaner) isWhitelisted(podName string) bool {
	for _, regex := range c.whitelistRegexes {
		if regex.MatchString(podName) {
			return true
		}
	}
	return false
}

func (c *Cleaner) getZombiePIDs(zombies []detector.ZombieInfo) []int {
	pids := make([]int, len(zombies))
	for i, zombie := range zombies {
		pids[i] = zombie.PID
	}
	return pids
}

func (c *Cleaner) cleanupOldStates() {
	c.stateMutex.Lock()
	defer c.stateMutex.Unlock()

	now := time.Now()
	cleanupThreshold := c.config.Cleaner.CheckInterval * 3 // 3个检测周期后清理

	for containerID, state := range c.containerStates {
		if !state.InProgress && now.Sub(state.LastDetected) > cleanupThreshold {
			c.logger.Debug("清理过期的容器状态", "container_id", containerID)
			delete(c.containerStates, containerID)
		}
	}
}