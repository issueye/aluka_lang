# Aluka Makefile
# Phase 0 工程基座
# 目标：build / test / install / clean / release

VERSION ?= 0.2.0-dev
BINARY  ?= aluka
MODULE  := github.com/aluka-lang/aluka
PKG     := ./cmd/aluka
LDFLAGS := -s -w -X main.version=$(VERSION)
CGO     := CGO_ENABLED=0
# 根目录 ./... 不会进入带 go.mod 的子目录；按 workspace 模块逐个测。
WORKPKGS := $(shell go list -f '{{.Dir}}/...' -m)

# 跨平台目标
TARGETS := \
	bin/aluka-linux-amd64    \
	bin/aluka-linux-arm64     \
	bin/aluka-darwin-amd64   \
	bin/aluka-darwin-arm64   \
	bin/aluka-windows-amd64.exe

.PHONY: all build test test-engine test-pkgmanager cover lint clean install release icon help rust-build rust-test rust-lint rust-check

all: build

# 生成品牌多尺寸图标与 Windows 资源文件
icon:
	go run ./internal/cli/genicon/main.go
	windres -i cmd/aluka/aluka.rc -O coff -o cmd/aluka/aluka_windows_amd64.syso -F pe-x86-64

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

# 单元测试（覆盖 go.work 内全部 module）
test:
	$(CGO) go test $(WORKPKGS)

# 叶子模块在 GOWORK=off 下自测（校验 replace，不依赖 workspace）
test-engine:
	cd internal/engine && GOWORK=off $(CGO) go test ./...

test-pkgmanager:
	cd internal/pkgmanager && GOWORK=off $(CGO) go test ./...

# 覆盖率
cover:
	$(CGO) go test $(WORKPKGS) -cover -coverprofile=coverage.out
	$(CGO) go tool cover -func=coverage.out | tail -1

# Lint（需要 golangci-lint）
lint:
	golangci-lint run --timeout 5m $(WORKPKGS)

# === Rust 重构工作区（rust/，见 docs/rust-reimplementation-devplan.md）=====
# 与 Go 版并存：Rust 侧自成 cargo workspace，不参与 go.work。

rust-build:
	cd rust && cargo build

rust-test:
	cd rust && cargo test

rust-lint:
	cd rust && cargo clippy --all-targets && cargo fmt --all --check

# Rust 侧提交前自查（构建 + 测试 + lint）
rust-check: rust-build rust-test rust-lint

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
	@echo "  test      运行所有单元测试（go.work）"
	@echo "  test-engine / test-pkgmanager  GOWORK=off 叶子模块自测"
	@echo "  cover     运行测试并生成覆盖率报告"
	@echo "  lint      运行 golangci-lint"
	@echo "  install   安装到 GOBIN"
	@echo "  clean     清理构建产物"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION=$(VERSION)"
	@echo "  BINARY=$(BINARY)"
