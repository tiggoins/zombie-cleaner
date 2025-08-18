package detector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/procfs"
	"github.com/tiggoins/zombie-cleaner/internal/config"
	"github.com/tiggoins/zombie-cleaner/internal/logger"
	"github.com/tiggoins/zombie-cleaner/internal/metrics"
	"github.com/tiggoins/zombie-cleaner/internal/runtime"
)

type ContainerMeta = runtime.ContainerMeta

type ZombieInfo struct {
	PID           int
	PPID          int
	Cmdline       string
	Container     *ContainerMeta
	IsInContainer bool
}

type Detector struct {
	logger           *logger.Logger
	ContainerRuntime runtime.ContainerRuntimeInterface
	processTimeout   time.Duration
	containerTimeout time.Duration

	// 超时容器跟踪
	timeoutContainers struct {
		mu sync.Mutex
		m  map[string]time.Time // containerID -> timeout time
	}

	// 全局缓存避免重复构建同一PID子树
	pidTreeCache struct {
		mu sync.Mutex
		m  map[int]map[int]bool
	}
}

func New(processTimeout time.Duration, containerTimeout time.Duration, containerRuntime config.ContainerRuntime, log *logger.Logger) (*Detector, error) {
	d := &Detector{
		logger:           log.WithComponent("detector"),
		processTimeout:   processTimeout,
		containerTimeout: containerTimeout,
	}
	d.pidTreeCache.m = make(map[int]map[int]bool)
	d.timeoutContainers.m = make(map[string]time.Time)

	var runtimeImpl runtime.ContainerRuntimeInterface
	var err error

	// 根据配置创建容器运行时实现
	switch containerRuntime {
	case config.RuntimeDocker:
		runtimeImpl, err = runtime.NewDockerRuntime(log, containerTimeout, d)
		if err != nil {
			log.Warn("无法创建Docker运行时", "error", err)
		}
	case config.RuntimeContainerd:
		runtimeImpl, err = runtime.NewContainerdRuntime(log, containerTimeout, d)
		if err != nil {
			log.Warn("无法创建Containerd运行时", "error", err)
		}
	}

	if runtimeImpl == nil {
		return nil, fmt.Errorf("%s", "无法创建容器运行时实现")
	}

	d.ContainerRuntime = runtimeImpl

	return d, nil
}

func (d *Detector) DetectZombies(ctx context.Context) ([]ZombieInfo, error) {
	start := time.Now()
	nodeName := metrics.GetNodeName()
	defer func() {
		metrics.CheckDuration.WithLabelValues(nodeName).Observe(time.Since(start).Seconds())
	}()

	d.logger.Info("开始检测僵尸进程")
	
	// 清理旧的超时记录
	d.CleanupOldTimeouts()

	// 获取所有进程信息
	fs, err := procfs.NewFS("/proc")
	if err != nil {
		return nil, fmt.Errorf("无法打开 /proc: %w", err)
	}

	allProcs, err := fs.AllProcs()
	if err != nil {
		return nil, fmt.Errorf("获取进程信息失败: %w", err)
	}

	// 构建父进程映射和收集僵尸进程
	parentMap := make(map[int][]int)
	zombies := make(map[int]procfs.Proc)

	for _, proc := range allProcs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		stat, err := proc.Stat()
		if err != nil {
			continue
		}
		parentMap[stat.PPID] = append(parentMap[stat.PPID], stat.PID)
		if stat.State == "Z" {
			zombies[stat.PID] = proc
		}
	}

	zombieCount := len(zombies)
	d.logger.Info("发现僵尸进程", "count", zombieCount)
	metrics.ZombieProcessesFound.WithLabelValues(nodeName).Set(float64(zombieCount))

	if zombieCount == 0 {
		return nil, nil
	}

	// 获取容器PID树
	containers, err := d.getContainerPIDTrees(ctx, parentMap)
	if err != nil {
		d.logger.Error("获取容器PID树失败", "error", err)
		return nil, err
	}

	// 构建PID到容器的映射
	pidToContainer := make(map[int]*ContainerMeta)
	for i := range containers {
		for pid := range containers[i].PIDSet {
			pidToContainer[pid] = &containers[i]
		}
	}

	// 分析僵尸进程归属
	var zombieInfos []ZombieInfo
	for zpid, proc := range zombies {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		stat, err := proc.Stat()
		if err != nil {
			continue
		}

		// 获取完整的命令行参数
		cmdline, err := proc.CmdLine()
		if err != nil {
			d.logger.Warn("无法获取进程命令行参数", "pid", zpid, "error", err)
			// 如果无法获取命令行参数，回退到使用Comm
			cmdline = []string{stat.Comm}
		}

		cmdlineStr := strings.Join(cmdline, " ")
		zombieInfo := ZombieInfo{
			PID:     zpid,
			PPID:    stat.PPID,
			Cmdline: cmdlineStr,
		}

		// 检查僵尸进程是否属于容器
		if container, ok := pidToContainer[zpid]; ok {
			zombieInfo.Container = container
			zombieInfo.IsInContainer = true
		} else if container, ok := pidToContainer[stat.PPID]; ok {
			zombieInfo.Container = container
			zombieInfo.IsInContainer = true
		}

		zombieInfos = append(zombieInfos, zombieInfo)

		// 记录详细日志
		if zombieInfo.IsInContainer {
			d.logger.WithZombie(zpid, stat.PPID, cmdlineStr).
				WithContainer(zombieInfo.Container.ID, zombieInfo.Container.PodName, zombieInfo.Container.PodNS).
				Info("发现容器内僵尸进程")
		} else {
			d.logger.WithZombie(zpid, stat.PPID, cmdlineStr).
				Info("发现宿主机僵尸进程")
		}
	}

	return zombieInfos, nil
}

func (d *Detector) getContainerPIDTrees(ctx context.Context, parentMap map[int][]int) ([]ContainerMeta, error) {
	var (
		resultMu  sync.Mutex
		result    []ContainerMeta
		wg        sync.WaitGroup
		semaphore = make(chan struct{}, 20) // 限制并发数量
	)

	// 获取容器列表
	containers, err := d.ContainerRuntime.ListContainers(ctx)
	if err != nil {
		d.logger.Error("获取容器列表失败", "error", err)
		return result, err
	}

	// 构建PID树并填充容器信息
	for i := range containers {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case semaphore <- struct{}{}:
		}

		wg.Add(1)
		go func(container runtime.ContainerMeta) {
			defer func() {
				<-semaphore
				wg.Done()
			}()

			// 构建PID树
			container.PIDSet = d.buildPIDTree(container.PID, parentMap)

			resultMu.Lock()
			result = append(result, container)
			resultMu.Unlock()
		}(containers[i])
	}

	wg.Wait()
	metrics.TrackedContainers.WithLabelValues(metrics.GetNodeName()).Set(float64(len(result)))

	return result, nil
}

func (d *Detector) buildPIDTree(root int, parentMap map[int][]int) map[int]bool {
	// 检查缓存
	d.pidTreeCache.mu.Lock()
	if cached, ok := d.pidTreeCache.m[root]; ok {
		d.pidTreeCache.mu.Unlock()
		return cached
	}
	d.pidTreeCache.mu.Unlock()

	// 构建PID树
	tree := make(map[int]bool)
	visited := make(map[int]bool)

	var walk func(pid int)
	walk = func(pid int) {
		if visited[pid] {
			return
		}
		visited[pid] = true
		tree[pid] = true
		// 添加边界检查，防止无限递归
		if pid < 0 || pid > 4194304 { // kernel.pid_max = 4194304
			return
		}
		for _, child := range parentMap[pid] {
			// 添加子PID的边界检查
			if child >= 0 && child <= 4194304 {
				walk(child)
			}
		}
	}
	walk(root)

	// 缓存结果，但限制缓存大小以防止内存泄漏
	d.pidTreeCache.mu.Lock()
	// 如果缓存太大，清空它以避免内存泄漏
	if len(d.pidTreeCache.m) > 1000 {
		d.pidTreeCache.m = make(map[int]map[int]bool)
	}
	d.pidTreeCache.m[root] = tree
	d.pidTreeCache.mu.Unlock()

	return tree
}

func (d *Detector) RecordTimeoutContainer(containerID string) {
	d.timeoutContainers.mu.Lock()
	defer d.timeoutContainers.mu.Unlock()
	d.timeoutContainers.m[containerID] = time.Now()
}

func (d *Detector) HasTimeoutContainer(containerID string) bool {
	d.timeoutContainers.mu.Lock()
	defer d.timeoutContainers.mu.Unlock()
	_, exists := d.timeoutContainers.m[containerID]
	return exists
}

func (d *Detector) CleanupOldTimeouts() {
	d.timeoutContainers.mu.Lock()
	defer d.timeoutContainers.mu.Unlock()
	
	// 清理超过1小时的超时记录
	threshold := time.Now().Add(-1 * time.Hour)
	for containerID, timeoutTime := range d.timeoutContainers.m {
		if timeoutTime.Before(threshold) {
			delete(d.timeoutContainers.m, containerID)
		}
	}
}

func (d *Detector) GetTimeoutContainers() []string {
	d.timeoutContainers.mu.Lock()
	defer d.timeoutContainers.mu.Unlock()
	
	var containers []string
	for containerID := range d.timeoutContainers.m {
		containers = append(containers, containerID)
	}
	return containers
}

// Close 关闭容器运行时连接
func (d *Detector) Close() error {
	if d.ContainerRuntime != nil {
		return d.ContainerRuntime.Close()
	}
	return nil
}
