BINARY   := bugbot
BIN_DIR  := bin
IMAGE    ?= bugbot:local
COMPOSE  ?= docker compose
PKGS     := ./...

.DEFAULT_GOAL := help
.PHONY: help tidy fmt fmt-check vet test test-verbose cover build run clean \
        check docker-build docker-run docker-up docker-down docker-logs k8s-apply

help: ## نمایش فهرست دستورها
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

tidy: ## مرتب‌سازی go.mod و go.sum
	go mod tidy

fmt: ## فرمت‌بندی کد
	gofmt -w .

fmt-check: ## بررسی فرمت (مثل CI)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "فایل‌های فرمت‌نشده:"; echo "$$out"; exit 1; fi

vet: ## اجرای go vet
	go vet $(PKGS)

test: ## اجرای تست‌ها (بدون race detector)
	go test -count=1 $(PKGS)

test-verbose: ## اجرای تست‌ها با خروجی کامل
	go test -race -count=1 -v $(PKGS)

cover: ## گزارش پوشش تست
	go test -count=1 -coverprofile=coverage.out $(PKGS)
	go tool cover -func=coverage.out | tail -1

check: tidy fmt-check vet test ## همان مراحل job تست در CI
	git diff --exit-code go.mod go.sum

build: ## ساخت باینری در bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/$(BINARY) ./cmd/bot

run: ## اجرای ربات به‌صورت محلی (نیازمند BOT_TOKEN)
	go run ./cmd/bot

clean: ## حذف خروجی‌های Build و Test
	rm -rf $(BIN_DIR) coverage.out

docker-build: ## ساخت Image داکر
	docker build -t $(IMAGE) .

docker-run: ## اجرای Image با متغیرهای .env
	docker run --rm --env-file .env -v bugbot-data:/data $(IMAGE)

docker-up: ## اجرای سرویس با docker compose
	$(COMPOSE) up -d --build

docker-down: ## توقف سرویس docker compose
	$(COMPOSE) down

docker-logs: ## مشاهده لاگ‌های سرویس
	$(COMPOSE) logs -f bugbot

k8s-apply: ## اعمال Manifestهای Kubernetes
	kubectl apply -f k8s.yaml
	kubectl -n bugbot rollout status deployment/bugbot --timeout=180s
