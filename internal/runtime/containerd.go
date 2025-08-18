package runtime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"github.com/tiggoins/zombie-cleaner/internal/logger"
)

// ContainerdRuntime Containerd运行时实现
type ContainerdRuntime struct {
	client   *containerd.Client
	logger   *logger.Logger
	timeout  time.Duration
	detector interface {
		RecordTimeoutContainer(containerID string)
	}
}

// NewContainerdRuntime 创建Containerd运行时实例
func NewContainerdRuntime(log *logger.Logger, timeout time.Duration, detector interface {
	RecordTimeoutContainer(containerID string)
}) (*ContainerdRuntime, error) {
	cli, err := containerd.New("/run/containerd/containerd.sock")
	if err != nil {
		return nil, fmt.Errorf("无法连接containerd守护进程: %w", err)
	}

	return &ContainerdRuntime{
		client:   cli,
		logger:   log.WithComponent("containerd-runtime"),
		timeout:  timeout,
		detector: detector,
	}, nil
}

// ListContainers 列出所有Containerd容器
func (c *ContainerdRuntime) ListContainers(ctx context.Context) ([]ContainerMeta, error) {
	// 使用k8s.io命名空间
	nsCtx := namespaces.WithNamespace(ctx, "k8s.io")

	containers, err := c.client.Containers(nsCtx)
	if err != nil {
		return nil, fmt.Errorf("获取Containerd容器列表失败: %w", err)
	}

	var result []ContainerMeta
	for _, container := range containers {
		// 为每个容器设置超时
		inspectCtx, cancel := context.WithTimeout(nsCtx, c.timeout)
		info, err := container.Info(inspectCtx)
		cancel()
		
		if err != nil {
			// 检查是否是超时错误
			if errors.Is(err, context.DeadlineExceeded) {
				c.logger.Warn("Containerd容器检查超时", "container_id", container.ID())
				// 记录超时容器
				if c.detector != nil {
					c.detector.RecordTimeoutContainer(container.ID())
				}
			} else {
				c.logger.Warn("Containerd容器检查失败", "container_id", container.ID(), "error", err)
			}
			continue
		}

		// 获取容器任务以获取PID
		task, err := container.Task(inspectCtx, nil)
		if err != nil {
			// 容器可能没有运行的任务
			continue
		}

		containerPID := int(task.Pid())
		if containerPID <= 0 {
			continue // 容器未运行
		}

		// 获取容器命令
		var comm string
		spec, err := container.Spec(inspectCtx)
		if err == nil && spec != nil && spec.Process != nil {
			comm = strings.Join(spec.Process.Args, " ")
		}

		// 注意：这里不构建PID树，因为这部分逻辑在detector中处理
		
		containerMeta := ContainerMeta{
			ID:        container.ID()[:12], // 短ID
			PID:       containerPID,
			Comm:      comm,
			PIDSet:    make(map[int]bool), // 在detector中填充
			CreatedAt: info.CreatedAt,
		}

		// 解析Pod信息
		labels := info.Labels
		if podName, ok := labels["io.kubernetes.pod.name"]; ok {
			containerMeta.PodName = podName
		} else {
			containerMeta.PodName = container.ID()
		}

		if podNS, ok := labels["io.kubernetes.pod.namespace"]; ok {
			containerMeta.PodNS = podNS
		} else {
			containerMeta.PodNS = "default"
		}

		result = append(result, containerMeta)
	}

	return result, nil
}

// RemoveContainer 删除Containerd容器
func (c *ContainerdRuntime) RemoveContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	// 设置超时
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c.logger.Info("尝试删除Containerd容器", "container_id", containerID)

	// 使用k8s.io命名空间
	nsCtx := namespaces.WithNamespace(timeoutCtx, "k8s.io")

	// 获取容器
	container, err := c.client.LoadContainer(nsCtx, containerID)
	if err != nil {
		return fmt.Errorf("无法加载Containerd容器: %w", err)
	}

	// 获取任务
	task, err := container.Task(nsCtx, nil)
	if err != nil {
		c.logger.Warn("无法获取Containerd容器任务", "container_id", containerID, "error", err)
	} else {
		// 停止任务
		_, err = task.Delete(nsCtx, containerd.WithProcessKill)
		if err != nil {
			c.logger.Warn("无法停止Containerd容器任务", "container_id", containerID, "error", err)
		}
	}

	// 删除容器
	if err := container.Delete(nsCtx, containerd.WithSnapshotCleanup); err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("删除Containerd容器超时: %w", err)
		}
		return fmt.Errorf("删除Containerd容器失败: %w", err)
	}

	c.logger.Info("成功删除Containerd容器", "container_id", containerID)
	return nil
}

// RecordTimeoutContainer 记录超时容器
func (c *ContainerdRuntime) RecordTimeoutContainer(containerID string) {
	if c.detector != nil {
		c.detector.RecordTimeoutContainer(containerID)
	}
}

// HasTimeoutContainer 检查容器是否超时
func (c *ContainerdRuntime) HasTimeoutContainer(containerID string) bool {
	// ContainerdRuntime doesn't track timeouts directly, this is handled by the detector
	return false
}

// KillContainerShim 杀死容器的shim进程
func (c *ContainerdRuntime) KillContainerShim(containerID string) error {
	c.logger.Info("尝试kill containerd-shim", "container_id", containerID)

	// 查找containerd-shim进程
	pattern := fmt.Sprintf("containerd-shim.*%s", containerID[:12])
	cmd := exec.Command("pgrep", "-f", pattern)
	output, err := cmd.Output()
	if err != nil {
		// 如果找不到进程，记录日志但不返回错误
		c.logger.Debug("查找containerd-shim进程失败", "pattern", pattern, "error", err)
		return nil
	}

	pids := strings.Fields(strings.TrimSpace(string(output)))
	for _, pid := range pids {
		killCmd := exec.Command("kill", "-9", pid)
		if err := killCmd.Run(); err != nil {
			c.logger.Warn("kill containerd-shim进程失败", "pid", pid, "error", err)
		} else {
			c.logger.Info("成功kill containerd-shim进程", "pid", pid)
		}
	}

	return nil
}

// Close 关闭Containerd客户端连接
func (c *ContainerdRuntime) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}