package runtime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/tiggoins/zombie-cleaner/internal/logger"
)

// DockerRuntime Docker运行时实现
type DockerRuntime struct {
	client   *client.Client
	logger   *logger.Logger
	timeout  time.Duration
	detector interface {
		RecordTimeoutContainer(containerID string)
	}
}

// NewDockerRuntime 创建Docker运行时实例
func NewDockerRuntime(log *logger.Logger, timeout time.Duration, detector interface {
	RecordTimeoutContainer(containerID string)
}) (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion("1.38"))
	if err != nil {
		return nil, fmt.Errorf("无法连接Docker守护进程: %w", err)
	}

	return &DockerRuntime{
		client:   cli,
		logger:   log.WithComponent("docker-runtime"),
		timeout:  timeout,
		detector: detector,
	}, nil
}

// ListContainers 列出所有Docker容器
func (d *DockerRuntime) ListContainers(ctx context.Context) ([]ContainerMeta, error) {
	containers, err := d.client.ContainerList(ctx, types.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Docker容器列表失败: %w", err)
	}

	var result []ContainerMeta
	for _, container := range containers {
		// 为每个容器设置超时
		inspectCtx, cancel := context.WithTimeout(ctx, d.timeout)
		inspect, err := d.client.ContainerInspect(inspectCtx, container.ID)
		cancel()
		
		if err != nil {
			// 检查是否是超时错误
			if errors.Is(err, context.DeadlineExceeded) {
				d.logger.Warn("Docker容器检查超时", "container_id", container.ID)
				// 记录超时容器
				if d.detector != nil {
					d.detector.RecordTimeoutContainer(container.ID)
				}
			} else {
				d.logger.Warn("Docker容器检查失败", "container_id", container.ID, "error", err)
			}
			continue
		}

		containerPID := inspect.State.Pid
		if containerPID <= 0 {
			continue // 容器未运行
		}

		comm := d.getContainerProcess(inspect)
		// 注意：这里不构建PID树，因为这部分逻辑在detector中处理
		
		c := ContainerMeta{
			ID:     container.ID[:12], // 短ID
			PID:    containerPID,
			Comm:   comm,
			PIDSet: make(map[int]bool), // 在detector中填充
			CreatedAt: func() time.Time {
				t, err := time.Parse(time.RFC3339Nano, inspect.Created)
				if err != nil {
					return time.Now()
				}
				return t
			}(),
		}

		// 解析Pod信息
		name := strings.Trim(inspect.Name, "/")
		parts := strings.Split(name, "_")
		if len(parts) >= 5 {
			c.PodName = parts[2]
			c.PodNS = parts[3]
		} else {
			c.PodName = name
			c.PodNS = "-"
		}

		result = append(result, c)
	}

	return result, nil
}

// RemoveContainer 删除Docker容器
func (d *DockerRuntime) RemoveContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	// 设置超时
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	d.logger.Info("尝试删除Docker容器", "container_id", containerID)

	// 先停止容器再删除
	timeoutSeconds := int(timeout.Seconds())
	if err := d.client.ContainerStop(timeoutCtx, containerID, container.StopOptions{Timeout: &timeoutSeconds}); err != nil {
		d.logger.Debug("停止Docker容器失败", "container_id", containerID, "error", err)
	}

	// 删除容器
	if err := d.client.ContainerRemove(timeoutCtx, containerID, types.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}); err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("删除Docker容器超时: %w", err)
		}
		return fmt.Errorf("删除Docker容器失败: %w", err)
	}
	
	d.logger.Info("成功删除Docker容器", "container_id", containerID)
	return nil
}

// RecordTimeoutContainer 记录超时容器
func (d *DockerRuntime) RecordTimeoutContainer(containerID string) {
	if d.detector != nil {
		d.detector.RecordTimeoutContainer(containerID)
	}
}

// KillContainerShim 杀死容器的shim进程
func (d *DockerRuntime) KillContainerShim(containerID string) error {
	d.logger.Info("尝试kill docker-containerd-shim", "container_id", containerID)

	// 查找docker-containerd-shim进程
	pattern := fmt.Sprintf("docker-containerd-shim.*%s", containerID[:12])
	cmd := exec.Command("pgrep", "-f", pattern)
	output, err := cmd.Output()
	if err != nil {
		// 如果找不到进程，记录日志但不返回错误
		d.logger.Debug("查找docker-containerd-shim进程失败", "pattern", pattern, "error", err)
		return nil
	}

	pids := strings.Fields(strings.TrimSpace(string(output)))
	for _, pid := range pids {
		killCmd := exec.Command("kill", "-9", pid)
		if err := killCmd.Run(); err != nil {
			d.logger.Warn("kill docker-containerd-shim进程失败", "pid", pid, "error", err)
		} else {
			d.logger.Info("成功kill docker-containerd-shim进程", "pid", pid)
		}
	}

	return nil
}

// Close 关闭Docker客户端连接
func (d *DockerRuntime) Close() error {
	if d.client != nil {
		return d.client.Close()
	}
	return nil
}

// getContainerProcess 获取容器进程信息
func (d *DockerRuntime) getContainerProcess(inspect types.ContainerJSON) string {
	var cmdParts []string
	if ep := inspect.Config.Entrypoint; len(ep) > 0 {
		cmdParts = append(cmdParts, ep...)
	}
	if args := inspect.Config.Cmd; len(args) > 0 {
		argsStr := strings.Join(args, " ")
		if len(argsStr) > 100 {
			argsStr = argsStr[:100] + "..."
		}
		cmdParts = append(cmdParts, argsStr)
	}
	return strings.Join(cmdParts, " ")
}