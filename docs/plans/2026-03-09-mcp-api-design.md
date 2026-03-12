# MCP Server + API 双后端设计

**日期**：2026-03-09  
**项目**：`github.com/toby1991/pplx-cli`  
**前置**：[CLI 实现](2026-03-08-pplx-cli.md)、[Headless 修复](2026-03-08-headless-fixes.md)  
**状态**：已完成

---

## 动机

CLI 模式（`pplx "query"`）解决了人类直接使用的场景。但还需要让 LLM（Claude、OpenCode 等）能调用 Perplexity 作为搜索工具。

两种搜索后端各有优势：

| | UI Backend | API Backend |
|---|---|---|
| 方式 | 控制 Desktop App (AX 自动化) | Sonar REST API |
| 成本 | 免费（用现有订阅） | $5/月免费额度 |
| 速度 | 10-30 秒 | 2-5 秒 |
| 功能 | 完整（research、deep search、来源选择） | 基础搜索 |
| 依赖 | Perplexity Desktop + 显示器 | 仅需 API key |
| 并发 | 串行 | 并行 |

最佳策略：UI 为主（功能全、免费），API 为备（快速、简单）。

---

## 架构决策

### MCP 而非 ACP

pplx 是简单的工具调用（发送查询 → 返回结果），不是对话式 agent。MCP 的 tool call 模型完全匹配。

### 共享二进制

CLI 和 MCP 共存于同一 `pplx` 二进制。`pplx mcp` 子命令启动 MCP server，其余命令保持 CLI 行为。共享 `driver/` 层。

### Dispatcher 模式

`driver/search.go` 中的 `Dispatcher` 封装了 primary/fallback 逻辑：

```go
type Dispatcher struct {
    Primary       Backend         // "ui" 或 "api"
    Fallback      Backend         // 空字符串 = 无 fallback
    APIKey        string
    PrimaryModel  string          // primary 后端默认模型
    FallbackModel string          // fallback 后端默认模型
    UISources     map[string]bool // UI 后端默认来源
}
```

搜索流程：
1. 用 primary 后端搜索
2. 成功 → 返回结果
3. 失败且有 fallback → 用 fallback 后端搜索
4. 都失败 → 返回两个错误

### 模型命名分离

UI 和 API 后端使用不同的模型命名：

| UI Backend | API Backend |
|---|---|
| GPT-5 | sonar |
| Claude Sonnet | sonar-pro |
| — | sonar-reasoning |
| — | sonar-reasoning-pro |

因此使用两个独立 flag：`--primary-model` 和 `--fallback-model`。

---

## MCP Server 实现

### 依赖

- `github.com/mark3labs/mcp-go v0.45.0` — MCP SDK，处理 JSON-RPC 2.0

### Tools

**search** — 搜索 Perplexity

参数：
- `query` (string, required) — 搜索查询
- `model` (string, optional) — 覆盖默认模型
- `sources` (string, optional) — 逗号分隔的来源列表（仅 UI 后端）

**list_models** — 列出可用模型（根据当前 primary 后端返回对应列表）

**list_sources** — 列出内容来源类别（仅 UI 后端）

### Search Prompt Suffix

MCP 模式下，每次查询自动附加 system prompt，引导 Perplexity：
- 用英文搜索、中文回答
- 至少 5 次 `search_web` + 3 次 `fetch_url`
- 优先权威信源（Wikipedia、学术网站、Reddit）
- 深挖问题，避免泛泛而谈

CLI 模式不附加此后缀。

### 防休眠：caffeinate LaunchAgent

MCP 模式使用 UI 后端时，启动时检查是否有 `caffeinate` 进程正在运行，未运行则打印警告并建议执行 `pplx setup-caffeinate`。

防休眠本身由独立的 LaunchAgent 承担（不绑定 MCP 进程生命周期）：

- `pplx setup-caffeinate`：安装 `~/Library/LaunchAgents/com.pplx.caffeinate.plist`，以 `caffeinate -dimsu` 启动，登录时自动启动、意外退出后自动重启；同时通过 `sysadminctl -screenLock off` 关闭屏幕锁定。
- `pplx remove-caffeinate`：停止并卸载 LaunchAgent，并提示如需恢复锁屏请手动执行 `sysadminctl -screenLock immediate -password -`。

**之前的实现**（已废弃）：在 `automation/ax.m` 中通过 CGo 直接调用 IOKit 创建 `kIOPMAssertionTypePreventUserIdleDisplaySleep`，仅相当于 `caffeinate -d`，缺少 `-s`（防系统休眠）且绑定 MCP 进程，进程退出即失效。

---

## NavigateToHome

### 问题

连续搜索时，Perplexity 停留在上次结果页。此时：
- `world`（来源选择器）按钮不存在 → `SetSources()` 失败
- 通过 URL scheme 发起搜索可能追加到现有对话而非新建线程

### 解决

`NavigateToHome()` 在每次 UI 搜索前调用：
1. 检测 `world` 按钮（首页标志）→ 已在首页，直接返回
2. 检测 `chevron-left` 按钮（结果页标志）→ 点击返回
3. 循环最多 5 次，处理动画延迟
4. 最终确认 `world` 按钮出现

### 首页 vs 结果页 AX 差异

| 元素 | 首页 | 结果页 |
|------|:---:|:---:|
| `world` (来源选择器) | 有 | 无 |
| `logo-stroke` (logo) | 有 | 无 |
| `chevron-left` (返回按钮) | 无 | 有 |
| `bookmark` | 无 | 有 |
| `model` (模型按钮) | 有 | 有 |
| `AXTextArea` (输入框) | 有 | 有 |

---

## Auto-launch

`EnsureAppRunning()` 在每次需要窗口时调用：
1. 尝试 `ActivateApp()` — 如果 App 已运行，直接激活
2. 失败则 `open -b ai.perplexity.mac` 启动
3. 轮询等待 AX 可访问（10 秒超时）

---

## CLI 扩展

### `pplx api` 子命令

```bash
pplx api "query"                    # 用 API 搜索
pplx api --model sonar-reasoning    # 指定模型
pplx api models                     # 列出 API 模型
```

读取 `PERPLEXITY_API_KEY` 环境变量。

### Server Flags

```
--primary ui|api           主后端（默认 ui）
--fallback ui|api          降级后端（空 = 不降级）
--primary-model MODEL      主后端默认模型
--fallback-model MODEL     降级后端默认模型
--sources web,academic,... UI 后端默认来源
```

---

## 实现历史

| Commit | 内容 |
|--------|------|
| `18004f9` | Phase 1: MCP Server + IOPMAssertion（已废弃，见下） |
| `6879bd2` | Phase 2: API 后端 + Dispatcher |
| `57faea7` | Phase 3: --primary-model/--fallback-model/--sources flags |
| `72da055` | Phase 4: Auto-launch App |
| `b935d55` | Phase 5: NavigateToHome — 连续搜索修复 |
| `25358d0` | refactor: 以 caffeinate LaunchAgent 替换 IOPMAssertion |
| `f0774f7` | feat: setup-caffeinate 新增关闭屏幕锁定（sysadminctl） |
| `3d8e8c7` | fix: get_first_window 优先选 AXStandardWindow，跳过 AXDialog |

---

## 已知限制

1. **API 未实际验证**：PERPLEXITY_API_KEY 配额耗尽（401 insufficient_quota），编译和 MCP 协议测试通过，但实际 API 调用未验证
2. **搜索 prompt suffix 为中文优化**：当前 suffix 针对中文用户优化（英文搜索、中文回答），其他语言用户需修改
3. **UI 后端串行**：MCP server 同时只能处理一个 UI 搜索请求
4. **get_first_window 依赖 AXStandardWindow subrole**：macOS 若改变 subrole 命名可能需要更新匹配逻辑
