package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"os"

	"github.com/tiggoins/zombie-cleaner/internal/logger"
)

var (
	// 僵尸进程数量
	ZombieProcessesFound = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "zombie_cleaner_zombie_processes_found",
			Help: "当前发现的僵尸进程数量",
		},
		[]string{"node"},
	)

	// 容器清理次数
	ContainersCleaned = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zombie_cleaner_containers_cleaned_total",
			Help: "清理的容器总数",
		},
		[]string{"node", "namespace", "pod_name"},
	)

	// 清理失败次数
	CleanupFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zombie_cleaner_cleanup_failures_total",
			Help: "清理失败的总次数",
		},
		[]string{"node", "reason"},
	)

	// 检测周期耗时
	CheckDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "zombie_cleaner_check_duration_seconds",
			Help:    "检测周期耗时",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"node"},
	)

	// 容器操作超时次数
	ContainerOperationTimeouts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zombie_cleaner_container_operation_timeouts_total",
			Help: "容器操作超时次数",
		},
		[]string{"node", "operation"},
	)

	// 当前正在跟踪的容器数量
	TrackedContainers = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "zombie_cleaner_tracked_containers",
			Help: "当前正在跟踪的容器数量",
		},
		[]string{"node"},
	)
)

type Server struct {
	port   int
	logger *logger.Logger
}

func NewServer(port int, log *logger.Logger) *Server {
	// 注册指标
	prometheus.MustRegister(
		ZombieProcessesFound,
		ContainersCleaned,
		CleanupFailures,
		CheckDuration,
		ContainerOperationTimeouts,
		TrackedContainers,
	)

	return &Server{
		port:   port,
		logger: log.WithComponent("metrics"),
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return server.ListenAndServe()
}

func GetNodeName() string {
	// 从环境变量获取节点名称，这在DaemonSet中会自动设置
	if nodeName := os.Getenv("NODE_NAME"); nodeName != "" {
		return nodeName
	}
	return "unknown"
}
