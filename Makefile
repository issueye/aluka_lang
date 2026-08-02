# Aluka Makefile
# Phase 0 工程基座
# 目标：build / test / install / clean / release

VERSION ?= 0.1.0-dev
BINARY  ?= aluka
MODULE  := github.com/aluka-lang/aluka
PKG     := ./cmd/aluka
LDFLAGS := -X main.version=$(VERSION)
CGO     := CGO_ENABLED=0

# 跨平台目标
TARGETS := \
	bin/aluka-linux-amd64    \
	bin/aluka-linux-arm64     \
	bin/aluka-darwin-amd64   \
	bin/aluka-darwin-arm64   \
	bin/aluka-windows-amd64.exe

.PHONY: all build test cover lint clean install release help

all: build

# 本机构建（默认二进制）
build:
	$(CGO) go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)$(EXT) $(PKG)

# 跨平台构建
release: $(TARGETS)

bin/aluka-linux-amd64:
	GOOS=linux   GOARCH=amd64 $(CGO) go build -ldflags "$(LDFLAGS)" -o $@ $(PKG)
bin/aluka-linux-arm64:
	GOOS=linux   GOARCH=arm64 $(CGO) go build -ldflags "$(LDFLAGS)" -o $@ $(PKG)
bin/aluka-darwin-amd64:
	GOOS=darwin  GOARCH=amd64 $(CGO) go build -ldflags "$(LDFLAGS)" -o $@ $(PKG)
bin/aluka-darwin-arm64:
	GOOS=darwin  GOARCH=arm64 $(CGO) go build -ldflags "$(LDFLAGS)" -o $@ $(PKG)
bin/aluka-windows-amd64.exe:
	GOOS=windows GOARCH=amd64 $(CGO) go build -ldflags "$(LDFLAGS)" -o $@ $(PKG)

# 单元测试
test:
	$(CGO) go test ./...

# 覆盖率
cover:
	$(CGO) go test ./... -cover -coverprofile=coverage.out
	$(CGO) go tool cover -func=coverage.out | tail -1

# Lint（需要 golangci-lint）
lint:
	golangci-lint run ./...

# 安装到 GOBIN
install:
	$(CGO) go install -ldflags "$(LDFLAGS)" $(PKG)

# 清理产物
clean:
	rm -rf bin/ coverage.out coverage.html

help:
	@echo "Aluka Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  build     构建本机二进制到 bin/aluka"
	@echo "  release   跨平台构建 5 个目标到 bin/"
	@echo "  test      运行所有单元测试"
	@echo "  cover     运行测试并生成覆盖率报告"
	@echo "  lint      运行 golangci-lint"
	@echo "  install   安装到 GOBIN"
	@echo "  clean     清理构建产物"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION=$(VERSION)"
	@echo "  BINARY=$(BINARY)"
