# Zombie Process Cleaner for Kubernetes

一个专门为 Kubernetes 平台设计的僵尸进程清理工具，以 DaemonSet 形式部署在每个节点上，自动检测和清理长期存在的僵尸进程。

## 主要特性

- 🔍 **智能检测**：准确识别僵尸进程并关联到对应的容器和Pod
- ⏰ **多次确认**：连续多次检测到僵尸进程后才执行清理，避免误操作
- 🛡️ **安全保护**：支持白名单机制，保护关键系统容器
- ⏱️ **超时控制**：容器操作超时后自动终止 container-shim 进程
- 📊 **完整监控**：提供详细的 Prometheus 指标和结构化日志
- 🚀 **高效运行**：轻量级设计，最小化对节点性能的影响

## 架构设计

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Node 1        │    │   Node 2        │    │   Node N        │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │ Zombie      │ │    │ │ Zombie      │ │    │ │ Zombie      │ │
│ │ Cleaner     │ │    │ │ Cleaner     │ │    │ │ Cleaner     │ │
│ │ Pod         │ │    │ │ Pod         │ │    │ │ Pod         │ │
│ └─────────────┘ │    │ └─────────────┘ │    │ └─────────────┘ │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 │
                    ┌─────────────────┐
                    │   Prometheus    │
                    │   Monitoring    │
                    └─────────────────┘
```

## 工作流程

1. **定时检测**：每5分钟扫描节点上的所有进程
2. **僵尸识别**：识别状态为 'Z' 的僵尸进程
3. **容器关联**：通过进程树分析将僵尸进程关联到具体容器
4. **多次确认**：连续3次检测到同一容器的僵尸进程
5. **安全检查**：验证容器不在白名单中
6. **执行清理**：
   - 首先尝试优雅重启容器
   - 失败时强制终止 container-shim 进程
7. **记录监控**：记录详细日志并更新监控指标

## 快速开始

### 1. 部署到 Kubernetes

```bash
# 克隆项目
git clone https://github.com/your-org/zombie-cleaner.git
cd zombie-cleaner

# 部署到集群
make deploy

# 检查状态
make status
```

### 2. 验证部署

```bash
# 查看DaemonSet状态
kubectl get daemonset zombie-cleaner -n kube-system

# 查看Pod运行状态
kubectl get pods -n kube-system -l app=zombie-cleaner

# 查看日志
make logs
```

### 3. 查看指标

```bash
# 获取指标地址
make metrics

# 或直接访问
kubectl port-forward -n kube-system svc/zombie-cleaner-metrics 9090:9090
# 然后访问 http://localhost:9090/metrics
```

## 配置说明

### 主要配置参数

```yaml
cleaner:
  # 检测间隔（默认：5分钟）
  check_interval: 5m
  
  # 确认次数（默认：3次）
  confirm_count: 3
  
  # 容器操作超时（默认：30秒）
  container_timeout: 30s
  
  # 进程检查超时（默认：10秒）
  process_timeout: 10s
  
  # 最大并发处理容器数（默认：10）
  max_concurrent_containers: 10
  
  # 白名单模式（正则表达式）
  whitelist_patterns:
    - "^kube-system-.*"
    - "^monitoring-.*"
    - "^logging-.*"
  
  # 干跑模式（默认：false）
  dry_run: false
```

### 环境变量覆盖

```bash
# 检测间隔
CHECK_INTERVAL=3m

# 确认次数
CONFIRM_COUNT=2

# 干跑模式
DRY_RUN=true

# 日志级别
LOG_LEVEL=debug
```

## 监控指标

系统提供以下 Prometheus 指标：

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `zombie_cleaner_zombie_processes_found` | Gauge | 当前发现的僵尸进程数量 |
| `zombie_cleaner_containers_cleaned_total` | Counter | 清理的容器总数 |
| `zombie_cleaner_cleanup_failures_total` | Counter | 清理失败的总次数 |
| `zombie_cleaner_check_duration_seconds` | Histogram | 检测周期耗时 |
| `zombie_cleaner_container_operation_timeouts_total` | Counter | 容器操作超时次数 |
| `zombie_cleaner_tracked_containers` | Gauge | 当前跟踪的容器数量 |

### Grafana 仪表盘示例查询

```promql
# 僵尸进程趋势
sum(zombie_cleaner_zombie_processes_found) by (node)

# 清理成功率
rate(zombie_cleaner_containers_cleaned_total[5m]) / 
(rate(zombie_cleaner_containers_cleaned_total[5m]) + rate(zombie_cleaner_cleanup_failures_total[5m]))

# 检测耗时分位数
histogram_quantile(0.95, rate(zombie_cleaner_check_duration_seconds_bucket[5m]))
```

## 日志格式

系统使用结构化 JSON 日志格式：

```json
{
  "time": "2024-08-18T10:30:00Z",
  "level": "INFO",
  "msg": "发现容器内僵尸进程",
  "component": "detector",
  "container_id": "abc123456789",
  "pod_name": "my-app-7d4f8b9c6-x8k9m",
  "namespace": "default",
  "zombie_pid": 12345,
  "zombie_ppid": 12340,
  "zombie_cmdline": "sleep 3600"
}
```

## 常见问题

### Q: 如何启用干跑模式进行测试？

```bash
# 方法1：通过环境变量
make dry-run

# 方法2：修改配置文件
kubectl patch configmap zombie-cleaner-config -n kube-system \
  --patch '{"data":{"config.yaml":"...dry_run: true..."}}'
make restart
```

### Q: 如何调整检测频率？

```bash
# 设置为3分钟检测一次
kubectl set env daemonset/zombie-cleaner CHECK_INTERVAL=3m -n kube-system
```

### Q: 如何查看详细的调试信息？

```bash
# 启用调试模式
make debug

# 查看日志
kubectl logs -n kube-system -l app=zombie-cleaner --tail=100 -f
```

### Q: 如何添加新的白名单模式？

编辑配置文件，添加新的正则表达式模式：

```bash
kubectl edit configmap zombie-cleaner-config -n kube-system
# 在 whitelist_patterns 中添加新模式
make config-update
```

### Q: 系统对节点性能的影响如何？

- **CPU使用**：通常 < 100m，峰值 < 500m
- **内存使用**：通常 < 64Mi，峰值 < 256Mi
- **网络**：最小（仅指标暴露）
- **磁盘I/O**：仅读取 /proc 文件系统

## 安全考虑

### 权限说明

DaemonSet 需要以下权限：

- **特权模式**：访问宿主机进程信息
- **hostPID: true**：查看宿主机进程
- **Docker Socket**：执行容器操作
- **Kubernetes API**：获取节点和Pod信息

### 安全措施

1. **白名单保护**：关键系统容器不会被清理
2. **多次确认**：避免误杀短暂的僵尸进程
3. **超时控制**：防止长时间阻塞
4. **详细审计**：完整的操作日志记录

## 开发指南

### 项目结构

```
zombie-cleaner/
├── main.go                 # 主程序入口
├── internal/
│   ├── config/            # 配置管理
│   ├── logger/            # 日志处理
│   ├── metrics/           # 监控指标
│   ├── detector/          # 僵尸进程检测
│   └── cleaner/           # 清理逻辑
├── config/                # 配置文件
├── deploy/                # Kubernetes部署文件
├── Dockerfile            # 容器构建
├── Makefile              # 构建脚本
└── README.md             # 说明文档
```

### 本地开发

```bash
# 安装依赖
go mod download

# 运行测试
make test

# 本地构建
make build

# 本地运行（需要Docker权限）
sudo ./bin/zombie-cleaner -config config/config.yaml -log-level debug
```

### 贡献代码

1. Fork 项目仓库
2. 创建功能分支：`git checkout -b feature/amazing-feature`
3. 提交更改：`git commit -m 'Add some amazing feature'`
4. 推送分支：`git push origin feature/amazing-feature`
5. 创建 Pull Request

## 故障排查

### 常见问题诊断

1. **Pod 无法启动**
   ```bash
   kubectl describe pod -n kube-system -l app=zombie-cleaner
   ```

2. **权限不足**
   ```bash
   kubectl get clusterrolebinding zombie-cleaner
   ```

3. **指标不可用**
   ```bash
   kubectl port-forward -n kube-system svc/zombie-cleaner-metrics 9090:9090
   curl http://localhost:9090/health
   ```

4. **清理失败**
   ```bash
   # 查看失败指标
   kubectl exec -n kube-system deploy/prometheus -- \
     promtool query instant 'zombie_cleaner_cleanup_failures_total'
   ```

## 版本历史

- **v1.0.0**：初始版本，支持基本的僵尸进程检测和清理
- 规划中功能：
  - 支持 containerd 运行时
  - 自定义清理策略
  - 更细粒度的监控指标

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件

## 联系方式

- 项目主页：https://github.com/your-org/zombie-cleaner
- 问题报告：https://github.com/your-org/zombie-cleaner/issues
- 邮件联系：admin@your-org.com
