# 变量定义
APP_NAME := zombie-cleaner
VERSION := v1.0.0
REGISTRY := docker.io
IMAGE_NAME := $(REGISTRY)/$(APP_NAME)
FULL_IMAGE := $(IMAGE_NAME):$(VERSION)

# Go 相关变量
GOMOD := $(shell head -1 go.mod | awk '{print $$2}')
LDFLAGS := -w -s -X main.version=$(VERSION)

.PHONY: help build test clean docker-build docker-push deploy undeploy logs

help: ## 显示帮助信息
	@echo "可用的命令:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## 构建二进制文件
	@echo "构建 $(APP_NAME)..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-ldflags="$(LDFLAGS)" \
		-o bin/$(APP_NAME) \
		./main.go
	@echo "构建完成: bin/$(APP_NAME)"

test: ## 运行测试
	@echo "运行测试..."
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "测试完成，覆盖率报告: coverage.html"

clean: ## 清理构建产物
	@echo "清理构建产物..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	docker rmi $(FULL_IMAGE) 2>/dev/null || true

docker-build: ## 构建Docker镜像
	@echo "构建Docker镜像: $(FULL_IMAGE)"
	docker build -t $(FULL_IMAGE) .
	docker tag $(FULL_IMAGE) $(IMAGE_NAME):latest

docker-push: docker-build ## 推送Docker镜像
	@echo "推送Docker镜像: $(FULL_IMAGE)"
	docker push $(FULL_IMAGE)
	docker push $(IMAGE_NAME):latest

deploy: ## 部署到Kubernetes
	@echo "部署 $(APP_NAME) 到Kubernetes..."
	kubectl apply -f deploy/daemonset.yaml
	@echo "等待DaemonSet就绪..."
	kubectl rollout status daemonset/$(APP_NAME) -n kube-system

undeploy: ## 从Kubernetes卸载
	@echo "从Kubernetes卸载 $(APP_NAME)..."
	kubectl delete -f deploy/daemonset.yaml --ignore-not-found=true

logs: ## 查看日志
	@echo "查看 $(APP_NAME) 日志..."
	kubectl logs -n kube-system -l app=$(APP_NAME) --tail=100 -f

status: ## 查看部署状态
	@echo "检查 $(APP_NAME) 状态..."
	kubectl get daemonset $(APP_NAME) -n kube-system
	kubectl get pods -n kube-system -l app=$(APP_NAME)

metrics: ## 查看指标
	@echo "获取指标端点..."
	@kubectl get pods -n kube-system -l app=$(APP_NAME) -o jsonpath='{.items[0].status.podIP}' | xargs -I {} echo "指标地址: http://{}:9090/metrics"

restart: ## 重启DaemonSet
	@echo "重启 $(APP_NAME)..."
	kubectl rollout restart daemonset/$(APP_NAME) -n kube-system
	kubectl rollout status daemonset/$(APP_NAME) -n kube-system

config-update: ## 更新配置
	@echo "更新配置..."
	kubectl create configmap $(APP_NAME)-config \
		--from-file=config/config.yaml \
		--dry-run=client -o yaml | kubectl apply -f -
	$(MAKE) restart

dry-run: ## 启用干跑模式
	@echo "启用干跑模式..."
	kubectl set env daemonset/$(APP_NAME) DRY_RUN=true -n kube-system

production: ## 禁用干跑模式
	@echo "禁用干跑模式（生产模式）..."
	kubectl set env daemonset/$(APP_NAME) DRY_RUN=false -n kube-system

debug: ## 调试模式（详细日志）
	@echo "启用调试模式..."
	kubectl set env daemonset/$(APP_NAME) LOG_LEVEL=debug -n kube-system

# 本地开发相关
dev-run: build ## 本地运行（需要Docker）
	@echo "本地运行 $(APP_NAME)..."
	sudo ./bin/$(APP_NAME) -config config/config.yaml -log-level debug

mod-tidy: ## 整理依赖
	go mod tidy
	go mod verify

lint: ## 代码检查
	golangci-lint run

# 版本管理
version: ## 显示当前版本
	@echo "当前版本: $(VERSION)"

tag: ## 创建Git标签
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push origin $(VERSION)
