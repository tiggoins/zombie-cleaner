package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tiggoins/zombie-cleaner/internal/cleaner"
	"github.com/tiggoins/zombie-cleaner/internal/config"
	"github.com/tiggoins/zombie-cleaner/internal/logger"
	"github.com/tiggoins/zombie-cleaner/internal/metrics"
)

var (
	configFile = flag.String("config", "/etc/zombie-cleaner/config.yaml", "配置文件路径")
)

func main() {
	flag.Parse()

	// 加载配置
	cfg := config.Load(*configFile)
	// 初始化日志
	log := logger.New(cfg.Logger.Level, cfg.Logger.Format)
	log.Info("启动僵尸进程清理器")

	// 初始化指标监控
	if cfg.Metrics.Enabled {
		metricsServer := metrics.NewServer(cfg.Metrics.Port, log)
		go func() {
			if err := metricsServer.Start(); err != nil {
				log.Error("指标服务器启动失败", "error", err)
			}
		}()
		log.Info("指标监控已启用", "port", cfg.Metrics.Port)
	}

	// 创建清理器
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	zombieCleaner, err := cleaner.New(cfg, log)
	if err != nil {
		log.Fatal("创建清理器失败", "error", err)
	}

	// 启动清理器
	go zombieCleaner.Start(ctx)

	// 优雅关闭
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Info("收到关闭信号，开始优雅关闭...")

	// 给清理器一些时间完成当前操作
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	cancel() // 取消主上下文
	zombieCleaner.Stop(shutdownCtx)

	log.Info("僵尸进程清理器已关闭")
}
