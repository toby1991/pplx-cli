BINARY := pplx
BUILD_DIR := build
GO := /opt/homebrew/bin/go

.PHONY: all build run clean tidy install

all: build

build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(BINARY) .

run: build
	$(BUILD_DIR)/$(BINARY)

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BUILD_DIR)

# 安装到 /usr/local/bin，方便全局使用
install: build
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	@echo "Installed: /usr/local/bin/$(BINARY)"

# 快速测试搜索
test-search: build
	$(BUILD_DIR)/$(BINARY) "what is the latest Go version"

# 测试 JSON 输出
test-json: build
	$(BUILD_DIR)/$(BINARY) "what is Go programming language" --json
