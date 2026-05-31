.PHONY: help build run clean test docker-up docker-down dev

# 默认目标
help: ## 显示帮助信息
	@echo "Astra Game Backend - Makefile"
	@echo "=========================="
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# 构建
build: ## 编译所有服务
	@echo "编译网关服务..."
	@cd cmd/gateway && go build -o ../../bin/gateway .
	@echo "编译房间服务..."
	@cd cmd/room && go build -o ../../bin/room .
	@echo "编译匹配服务..."
	@cd cmd/match && go build -o ../../bin/match .
	@echo "编译玩家服务..."
	@cd cmd/player && go build -o ../../bin/player .
	@echo "编译完成！输出目录: bin/"

# 运行
run: ## 启动所有服务（本地开发）
	@echo "启动网关服务 (端口 8080)..."
	@./bin/gateway &
	@echo "启动房间服务 (端口 8081)..."
	@./bin/room &
	@echo "启动匹配服务 (端口 8082)..."
	@./bin/match &
	@echo "启动玩家服务 (端口 8083)..."
	@./bin/player &
	@echo "所有服务已启动！"

# 清理
clean: ## 清理编译产物
	@echo "清理编译产物..."
	@rm -rf bin/
	@echo "清理完成！"

# 测试
test: ## 运行单元测试
	@echo "运行测试..."
	@go test -v ./...

# Docker
docker-up: ## 启动 Docker 容器（Redis + MySQL + NATS）
	@echo "启动 Docker 容器..."
	@docker-compose up -d
	@echo "容器启动完成！"
	@docker-compose ps

docker-down: ## 停止 Docker 容器
	@echo "停止 Docker 容器..."
	@docker-compose down
	@echo "容器已停止！"

docker-restart: ## 重启 Docker 容器
	@make docker-down
	@make docker-up

# 开发
dev: ## 以开发模式启动（热重载）
	@echo "开发模式启动..."
	@which air > /dev/null || go install github.com/cosmtrek/air@latest
	@air

# 依赖
deps: ## 安装依赖
	@echo "安装 Go 依赖..."
	@go mod download
	@go mod tidy
	@echo "依赖安装完成！"

# 代码检查
lint: ## 运行代码检查
	@echo "运行 golangci-lint..."
	@which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@golangci-lint run ./...

# 数据库
db-migrate: ## 运行数据库迁移
	@echo "运行数据库迁移..."
	@go run scripts/migrate.go
	@echo "迁移完成！"

db-seed: ## 填充测试数据
	@echo "填充测试数据..."
	@go run scripts/seed.go
	@echo "数据填充完成！"

# 生成 Proto
proto: ## 生成 Protobuf 代码
	@echo "生成 Protobuf 代码..."
	@protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		pkg/proto/*.proto
	@echo "Protobuf 代码生成完成！"

# 压测
bench: ## 运行性能测试
	@echo "运行性能测试..."
	@go test -bench=. -benchmem ./...

# 镜像
docker-build: ## 构建 Docker 镜像
	@echo "构建 Docker 镜像..."
	@docker build -t astra-game-backend:latest .
	@echo "镜像构建完成！"

# 部署
deploy: ## 部署到 Kubernetes
	@echo "部署到 Kubernetes..."
	@kubectl apply -f deploy/k8s/
	@echo "部署完成！"

# 帮助信息
default: help
