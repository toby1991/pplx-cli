# pplx

> 把你的 Perplexity Pro 订阅变成 MCP 工具——由 Desktop App 执行搜索，零额外费用。

[English](README.md)

`pplx` 是一个 macOS CLI 工具兼 MCP 服务，通过 Accessibility 自动化直接驱动 **Perplexity Desktop App**，将你现有的 Pro 订阅变成 Claude、OpenCode 等任意 MCP 客户端可调用的搜索后端。同时内置 Sonar REST API 后端作为备选。

---

## 快速上手

```bash
# 1. 编译安装
git clone https://github.com/toby1991/pplx-cli
cd pplx-cli
make install          # 编译 → /usr/local/bin/pplx

# 2. 授权 Accessibility（一次性）
#    系统设置 → 隐私与安全性 → 辅助功能 → 添加你的终端 App

# 3. 验证是否正常工作
pplx "什么是 MCP？"

# 4. 配置到 Claude Desktop 或 OpenCode（见下方 MCP 章节）
```

**Headless Mac（Mac mini 通过 SSH 访问）？** 额外一步：

```bash
pplx setup-caffeinate   # 防休眠 + 关闭锁屏——执行一次，重启后自动生效
```

---

## 工作原理

两个后端，一个二进制：

| 后端 | 方式 | 费用 | 速度 |
|------|------|------|------|
| **UI**（默认） | 通过 macOS AXUIElement API 控制 Perplexity Desktop App | 免费——使用你的 Pro 订阅 | 10–30 秒 |
| **API** | 直接调用 Perplexity Sonar REST API | $5/月免费额度 + API Key | 2–5 秒 |

两种运行模式：

| 模式 | 命令 | 场景 |
|------|------|------|
| **CLI** | `pplx "query"` | 终端直接使用、脚本、管道 |
| **MCP Server** | `pplx mcp` | Claude、OpenCode 等发起的工具调用 |

---

## CLI 使用

```bash
# 基础搜索
pplx "Go 错误处理最佳实践"

# 指定模型（名称前缀匹配 UI 模型切换器）
pplx --model "Claude Sonnet" "解释 Monad"

# 指定内容来源
pplx --sources web,academic "RAG 最新论文"

# 管道输入/输出
echo "什么是熵？" | pplx
pplx "最好的 Go CLI 框架" --json | jq '.answer'

# 静默模式——只输出答案，不显示来源
pplx -q "法国首都"

# 交互式 REPL
pplx
```

### 子命令

```bash
pplx status               # 当前模式/模型（读取 Perplexity UserDefaults）
pplx models               # 列出可用的 UI 模型
pplx sources              # 列出内容来源类别
pplx dump                 # 导出 AX 树（诊断用）
pplx version

# Headless Mac 管理
pplx setup-caffeinate     # 安装 caffeinate LaunchAgent + 关闭锁屏
pplx remove-caffeinate    # 卸载

# API 后端
pplx api "query"          # 通过 REST API 搜索
pplx api --model sonar-reasoning "解释 P=NP"
pplx api models           # 列出 API 模型
```

### 输出格式

**TTY 模式**——带颜色和 spinner：
```
⠋ 正在搜索...

────────────────────────────────────────────────────
Go 错误处理最佳实践包括对错误进行包装...

来源：
  [1] Effective Go — https://go.dev/doc/effective_go
  [2] Go Blog: 错误处理 — https://go.dev/blog/error-handling-and-go
────────────────────────────────────────────────────
```

**Pipe 模式**——纯文本，无颜色，无 spinner。

**JSON 模式**（`--json`）：
```json
{
  "answer": "Go 错误处理最佳实践...",
  "citations": [
    {"index": 1, "title": "Effective Go", "url": "https://go.dev/doc/effective_go"}
  ],
  "mode": "search",
  "model": "sonar_pro"
}
```

---

## MCP Server

`pplx mcp` 通过 stdio 将 Perplexity 暴露为 MCP 工具。

### 工具列表

| 工具 | 说明 |
|------|------|
| `search` | 搜索 Perplexity——可覆盖 `model` 和 `sources` |
| `list_models` | 列出当前后端的可用模型 |
| `list_sources` | 列出内容来源类别（仅 UI 后端） |

### 后端配置

```bash
pplx mcp                                    # 仅 UI（默认）
pplx mcp --primary api                      # 仅 API
pplx mcp --primary ui --fallback api        # UI 主，API 备
pplx mcp --primary ui --fallback api \
  --primary-model "GPT-5" \
  --fallback-model sonar-pro
pplx mcp --sources web,academic             # UI 后端默认来源
```

### 配置 OpenClaw

[OpenClaw](https://openclaw.ai) 通过 [mcporter](https://github.com/steipete/mcporter) 管理 MCP 服务器。将以下配置添加到 `~/.mcporter/mcporter.json`：

```json
{
  "mcpServers": {
    "pplx": {
      "command": "/usr/local/bin/pplx",
      "args": ["mcp", "--primary", "api", "--primary-model", "sonar",
               "--fallback", "ui", "--fallback-model", "GPT-5",
               "--sources", "web,academic,social"],
      "env": {
        "PERPLEXITY_API_KEY": "pplx-...",
        "PPLX_PROMPT_SUFFIX": "..."
      }
    }
  }
}
```

### 配置 Claude Desktop

`~/Library/Application Support/Claude/claude_desktop_config.json`：

```json
{
  "mcpServers": {
    "perplexity": {
      "type": "stdio",
      "command": "/usr/local/bin/pplx",
      "args": ["mcp", "--primary", "ui", "--fallback", "api"],
      "env": {
        "PERPLEXITY_API_KEY": "pplx-..."
      }
    }
  }
}
```

### 配置 OpenCode

`~/.config/opencode/opencode.json`：

```json
{
  "mcp": {
    "perplexity": {
      "type": "stdio",
      "command": "/usr/local/bin/pplx",
      "args": ["mcp", "--primary", "ui", "--fallback", "api", "--primary-model", "GPT-5"],
      "env": {
        "PERPLEXITY_API_KEY": "pplx-..."
      }
    }
  }
}
```

### MCP 特有行为

- **搜索提示后缀**：MCP 模式下，每次查询自动附加系统提示，引导 Perplexity 深度搜索、引用权威来源。
- **自动启动**：如果 Perplexity Desktop 未运行，会自动启动。
- **返回首页**：每次 UI 搜索前，App 会自动返回首页，确保新线程、干净状态。
- **Caffeinate 检查**：使用 UI 后端时，启动时检查 `caffeinate` 是否运行，未运行则提示执行 `pplx setup-caffeinate`。

---

## Headless Mac 配置

在无显示器的 Mac mini 上通过 SSH 运行？

```bash
# 一条命令搞定防休眠 + 关闭锁屏：
pplx setup-caffeinate
```

这条命令做了两件事：
1. 安装 LaunchAgent，登录时自动运行 `caffeinate -dimsu`（自动重启，重启后持续生效）
2. 通过 `sysadminctl` 关闭锁屏（需要输入你的登录密码）

**为什么锁屏很关键**：远程会话（VNC/SSH）断开时，macOS 会触发锁屏，将 WindowServer 降级为「应用程序」模式——所有 AX 自动化调用都会静默返回空数据或过期数据。关闭锁屏可以防止这种情况。

额外的加固步骤：

```bash
sudo pmset -a displaysleep 0      # 通过 pmset 关闭显示器休眠（双重保险）
```

### 显示器设置

在「系统设置」中调整以下两项，防止显示器在无人值守时休眠或锁屏：

| 设置项 | 路径 | 值 |
|--------|------|----|
| 非活跃时关闭显示器 | 系统设置 → 显示器 | **永不** |
| 屏幕保护程序/显示器关闭后需要密码 | 系统设置 → 锁定屏幕 | **永不** |

### 虚拟显示器

Perplexity 需要至少一个显示器（物理或虚拟）。可选方案：

1. **Apple 远程管理** *(优先尝试)* — 在「系统设置 → 共享」中启用「远程管理」后，Apple Silicon Mac mini 会自动创建虚拟帧缓冲，无需额外硬件。
2. **HDMI 假负载插头** — 将廉价的 HDMI 假负载插头接入 Mac mini 的 HDMI 口，macOS 会将其识别为真实显示器。在远程管理方案不奏效时，这是最可靠的选择。
3. **BetterDisplay** — [BetterDisplay](https://github.com/waydabber/BetterDisplay) 可以纯软件创建虚拟显示器，无需任何硬件，适用于没有 HDMI 口的 Mac mini（如仅有 USB-C 接口的型号）。

- **Accessibility 权限**：在「系统设置 → 隐私与安全性 → 辅助功能」中授权运行 `pplx` 的进程。

---

## 架构

```
┌─────────────────────────────────────────────────┐
│                    cmd/                          │
│  root.go  mcp.go  api.go  caffeinate.go  ...    │
│            Cobra CLI + MCP Server                │
└───────────────────┬─────────────────────────────┘
                    │
          ┌─────────┴──────────┐
          │      driver/        │
          │  search.go          │  ← Dispatcher（主/备路由）
          │  perplexity.go      │  ← UI 后端（AX 自动化）
          │  api.go             │  ← API 后端（Sonar REST）
          └─────────┬───────────┘
                    │
          ┌─────────┴───────────┐
          │    automation/       │
          │  ax.go  （CGo）      │
          │  ax.h   （C 头文件） │
          │  ax.m   （ObjC 实现）│
          │  macOS AXUIElement   │
          └──────────────────────┘
```

### 搜索流程——UI 后端

```
NavigateToHome()       → 如果在结果页则点击 chevron-left 返回首页
SetModel(model)        → 打开模型弹窗，按前缀选中
SetSources(sources)    → 打开来源弹窗，切换复选框
Search(query)
  → 打开 perplexity-app://search?q=...   URL scheme 触发搜索
  → waitForButtonWithScroll("copy")      轮询直到生成完成
      → HasButton("checkbox")            生成中时出现
      → GetContentLength()               跟踪答案增长
      → ScrollToBottom()                 处理虚拟化视口
  → Click(copyBtn) + ReadClipboard()     提取答案
  → parseClipboardContent()              解析答案 + 来源
```

### 搜索流程——API 后端

```
APISearch(apiKey, model, query)
  → POST https://api.perplexity.ai/chat/completions
  → 解析响应 + 来源
```

---

## 项目结构

```
pplx-cli/
├── main.go
├── go.mod / go.sum
├── Makefile
├── README.md / README.zh.md
├── cmd/
│   ├── root.go          # 根命令、flags、搜索分发
│   ├── mcp.go           # MCP Server 子命令
│   ├── api.go           # API 搜索子命令
│   ├── caffeinate.go    # setup-caffeinate / remove-caffeinate
│   ├── status.go
│   ├── models.go
│   ├── sources.go
│   ├── dump.go          # AX 树导出（诊断）
│   └── version.go
├── driver/
│   ├── perplexity.go    # UI 后端：Search、SetModel、SetSources、NavigateToHome
│   ├── search.go        # Dispatcher：主/备路由
│   └── api.go           # API 后端：Sonar REST 客户端
├── automation/
│   ├── ax.go            # Go/CGo 绑定
│   ├── ax.h             # C 头文件
│   └── ax.m             # Objective-C：AXUIElement 实现
├── output/
│   └── format.go        # TTY 检测、spinner、颜色、JSON
└── docs/plans/          # 设计文档
```

---

## 环境要求

- macOS（Apple Silicon 或 Intel）
- Go 1.23+
- Perplexity Desktop App（UI 后端必需）
- 已授权 Accessibility 权限（UI 后端必需）
- `PERPLEXITY_API_KEY` 环境变量（API 后端，可选）

---

## 已知限制

| 限制 | 说明 |
|------|------|
| 仅 macOS | 使用 AXUIElement API，不支持跨平台 |
| UI 后端串行 | 每次只能执行一个搜索（Perplexity App 本身的限制） |
| 剪贴板短暂被覆盖 | UI 后端通过剪贴板读取结果，搜索完成后恢复原始内容 |
| 需要显示器 | UI 后端至少需要一个物理或虚拟显示器 |
| 需要 Accessibility 权限 | 在「系统设置 → 隐私与安全性 → 辅助功能」中授权 |

---

## License

MIT
