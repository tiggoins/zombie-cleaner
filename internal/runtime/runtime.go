package runtime

import (
	"context"
	"time"
)

// ContainerMeta 容器元信息
type ContainerMeta struct {
	ID        string
	PID       int
	PodName   string
	PodNS     string
	Comm      string
	PIDSet    map[int]bool
	CreatedAt time.Time
}

// ContainerRuntimeInterface 定义容器运行时接口
type ContainerRuntimeInterface interface {
	// ListContainers 列出所有容器
	ListContainers(ctx context.Context) ([]ContainerMeta, error)
	
	// RemoveContainer 删除容器
	RemoveContainer(ctx context.Context, containerID string, timeout time.Duration) error
	
	// RecordTimeoutContainer 记录超时容器
	RecordTimeoutContainer(containerID string)
	
	// KillContainerShim 杀死容器的shim进程
	KillContainerShim(containerID string) error
	
	// Close 关闭运行时客户端连接
	Close() error
}