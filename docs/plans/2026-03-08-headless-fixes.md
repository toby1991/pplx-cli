# pplx-cli Headless Mac 兼容性修复

**日期**：2026-03-08
**问题**：pplx 在无显示器的 Mac mini（headless）上崩溃或功能异常

---

## 根因分析

### 问题 1: AX 树循环自引用（SIGSEGV）

**现象**：headless 时 `pplx search` 立即 segfault
**原因**：macOS AX server 在无显示器时，`AXApplication` 的 `kAXChildrenAttribute` 包含自身作为子节点，导致递归遍历无限循环 → 栈溢出
**证据**：`debug/hasnot.txt` 中 AXApplication 的 children 列表包含 AXApplication

### 问题 2: 无 AXWindow

**现象**：headless 时所有按钮查找返回 NULL
**原因**：没有显示器时 macOS 不创建 AXWindow，所有 UI 元素不在 AX 树中
**解决前提**：需要配置虚拟显示器（如 BetterDisplay dummy display）

### 问题 3: `ax_is_trusted()` 退出时崩溃

**现象**：pplx 成功执行后在进程退出时 stack overflow
**原因**：`AXIsProcessTrustedWithOptions(@{kAXTrustedCheckOptionPrompt: @YES})` 触发 AppKit UI 基础设施初始化，headless 时没有 window server 支撑，退出时清理代码崩溃

### 问题 4: CGEvent 滚动在 headless 下失效

**现象**：内容生成完成后找不到 copy 按钮
**原因**：`CGEventPost(kCGHIDEventTap, scrollEvent)` 依赖 window server 将事件路由到目标窗口；headless 时 window server 功能受限，事件被丢弃

### 问题 5: 研究阶段卡死无检测

**现象**：Perplexity 偶尔在搜索资料阶段卡住不前进，pplx 白等 120 秒
**原因**：waitForButtonWithScroll 没有针对"内容持续为 0"的独立超时

---

## 修复方案

### Fix 1: 从 Window 开始遍历（绕过自引用）

**文件**：`automation/ax.m`

- 新增 `get_first_window()` helper：通过 `kAXWindowsAttribute` 获取第一个窗口，绕过 AXApplication 的 children 属性
- 修改所有 public 函数（`ax_find_button`, `ax_find_button_prefix`, `ax_find_checkbox_prefix`, `ax_wait_for_button`, `ax_get_content_length`）从 window 开始遍历
- `ax_dump_elements` 保留从 app root 开始（诊断用途，已有 depth=50 保护）

### Fix 2: 递归深度限制

**文件**：`automation/ax.m`

- `find_button_recursive` 添加 `int depth` 参数，`depth > 30` 返回 NULL
- `find_button_prefix_recursive` 同上
- `find_checkbox_prefix_recursive` 同上
- 即使 Fix 1 已绕过自引用，depth limit 作为兜底保护仍然必要

### Fix 3: 窗口等待函数

**文件**：`automation/ax.m`, `automation/ax.h`, `automation/ax.go`

- 新增 C 函数 `ax_wait_for_window(bundle_id, timeout_ms, poll_ms)`：轮询直到 `get_first_window()` 返回非 NULL
- 新增 Go wrapper `WaitForWindow(bundleID, timeout, poll)`
- headless + 虚拟显示器场景下，窗口可能延迟出现

### Fix 4: ensureWindow() 统一入口

**文件**：`driver/perplexity.go`

- 新增 `ensureWindow()` helper：`ActivateApp()` + `WaitForWindow(10s)`
- 在 `Search()`, `SetModel()`, `SetSources()` 开头调用
- 错误消息提示用户检查显示器或虚拟显示器配置

### Fix 5: 纯 AX API 滚动（替代 CGEvent）

**文件**：`automation/ax.m`, `driver/perplexity.go`

C 层：
- 删除所有 CGEvent 鼠标移动 + 滚轮代码
- 新增 `find_scrollable_group()` 递归查找带 `AXScrollDownByPage` action 的 AXGroup
- `ax_scroll_to_bottom` 改为调用 `AXUIElementPerformAction(scrollable, "AXScrollDownByPage")`
- 纯 AX 调用，不经过 window server，headless 完全可用

Go 层：
- `waitForButtonWithScroll` 逻辑重构：先等内容稳定再滚动（避免生成中滚动）
- 添加 premature stability reset：如果判定稳定后内容又变了（研究阶段暂停），自动重置
- 添加研究阶段卡死检测：`currentLen == 0` 持续超过 90 秒提前退出

**关键发现**：Perplexity（SwiftUI）的滚动容器是 AXGroup（非 AXScrollArea），支持 `AXScrollDownByPage` / `AXScrollUpByPage` action。整个 AX 树中只有 12 种 role，不存在 AXScrollArea。

### Fix 6: ax_is_trusted() 智能弹窗

**文件**：`automation/ax.m`

- 有显示器时（`[NSScreen screens].count > 0`）：使用 `AXIsProcessTrustedWithOptions(@{prompt: @YES})` 弹出授权引导
- headless 时：使用 `AXIsProcessTrusted()` 静默检查，避免触发 AppKit UI 崩溃

### Fix 7: 自动打开系统设置

**文件**：`cmd/root.go`

- 未授权时自动执行 `open x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility`
- 错误消息引导用户在辅助功能列表中勾选终端应用

---

## 滚动机制详解

### 为什么不能用 CGEvent？

headless Mac 的 window server 功能受限：
- `CGEventPost(kCGHIDEventTap, ...)` 需要 window server 路由事件到目标窗口
- `CGEventPostToPid(pid, ...)` SwiftUI 应用直接忽略，不处理
- 键盘事件（PageDown）焦点在文本输入框而非滚动区域

### 为什么 AXScrollArea 不存在？

Perplexity 使用 SwiftUI 的 `ScrollView`，SwiftUI 不映射为标准的 AXScrollArea role。实际结构：

```
AXWindow
  AXGroup (size ≈ window size, 有 AXScrollDownByPage action) ← 这是滚动容器
    AXGroup
      AXTextArea (对话内容)
      AXButton (copy, share 等)
```

### waitForButtonWithScroll 状态机

```
[开始] → 轮询 GetContentLength
  ↓
len=0 持续 → 研究阶段（zeroSince 计时）
  ↓                    ↓
len>0 出现           超过 90s → 报错退出
  ↓
len 变化中 → 生成中（stableCount=0）
  ↓
len 稳定 3 轮 → contentStable=true → ScrollToBottom → FindButton("copy")
  ↓                                                      ↓
len 又变了 → contentStable=false（自纠正）              找到 → 返回
```

---

## 测试验证

```bash
# 短回答测试
./build/pplx search "2+2等于几"

# 长回答测试（触发研究阶段 + 多轮内容生成）
./build/pplx search "详细介绍Linux内核的进程调度器演进历史"

# 验证 headless（需要在无远程桌面的 Mac mini 上测试）
# 前提：配置虚拟显示器（BetterDisplay 等）
ssh macmini './build/pplx search "hello"'
```

---

## 已知限制

1. **仍需显示器**：Fix 1-3 解决了 AX 崩溃，但 Perplexity 作为 GUI App 仍需至少一个显示器（物理或虚拟）才能创建窗口。Apple Silicon Mac mini 启用"远程管理"后通常自动创建虚拟显示器，可能不需要 HDMI 假负载。
2. **CGEvent 依赖未完全消除**：`ax_open_file_dialog_and_input()`（附件上传）仍使用 CGEvent 键盘模拟，headless 下可能失败
3. **研究阶段超时**：~~旧的 90s 固定超时~~ 已被 Fix 12 的 UI 状态检测替代。现在通过 `checkbox` 按钮动态判断生成状态，总超时 300s，实际测试中深度研究查询在 2-3 分钟内完成

---

## Fix 8: WindowServer Compositor 修复（根因：屏幕锁定）

**根因**：screen lock 导致 WindowServer 停止合成窗口，`kAXWindowsAttribute` 返回 AXApplication 自引用而非 AXWindow。

**重要更正**：根因是屏幕锁定（screen lock），不是 VNC 断开连接。

**解决方案**：系统设置，无需代码修改：

```bash
# 禁用显示器休眠
sudo pmset -a displaysleep 0

# 禁用屏保密码（防止自动锁屏）
defaults write com.apple.screensaver askForPassword -int 0
```

**Apple Silicon Mac mini 注意**：启用"远程管理"后可能自动创建虚拟显示器，不一定需要 HDMI 假负载（dummy plug）。`pplx setup-headless` 命令会检测显示器状态并给出适当建议。

---

## Fix 9: SSH PID 查找 — 三层降级策略

**文件**：`automation/ax.m`

**问题**：从 SSH 会话调用时，Cocoa 的 `NSRunningApplication` API 可能返回 nil（因为 SSH session 在不同的 bootstrap domain）。

**解决方案**：三层降级查找：

1. **Layer 1**: `[NSRunningApplication runningApplicationsWithBundleIdentifier:]`
2. **Layer 2**: `[[NSWorkspace sharedWorkspace] runningApplications]` 遍历全部
3. **Layer 3**: `proc_listpids(PROC_ALL_PIDS)` + `proc_pidpath()` + 读取 `Info.plist` 文件中的 `CFBundleIdentifier` — 纯 syscall，完全绕过 Cocoa

**激活 App 的降级**：`ax_activate_app()` 在 Cocoa 激活失败时降级为 `system("open -b <bundle_id>")`。

关键 C 函数：
- `find_pid_by_bundle_id_syscall()` — 纯 syscall PID 查找
- `get_running_app()` — 整合三层策略

---

## Fix 10: Copy 按钮剪贴板重试机制

**文件**：`automation/ax.m`, `automation/ax.go`, `driver/perplexity.go`

**问题**：间歇性 bug，点击 copy 按钮后剪贴板没有更新。

**解决方案**：

C 层新增：
- `ax_clipboard_change_count()` — 返回剪贴板 changeCount（每次内容变化单调递增）
- `ax_read_all_textarea_values()` — 收集所有 AXTextArea 的文本值
- `collect_textarea_values_recursive()` — 递归遍历 AX 树收集文本

Go 层（`Search()` 函数）：
1. 记录 copy 前的 changeCount
2. 点击 copy 按钮
3. 检查 changeCount 是否变化
4. 未变化 → 重试最多 3 次（每次间隔 500ms）
5. 3 次重试都失败 → fallback：直接读取 AXTextArea values

---

## Fix 11: 滚动到底部 — 连续快速滚动修复

**文件**：`driver/perplexity.go` `waitForButtonWithScroll()`

**问题**：AX 树是**视口虚拟化**的 — 只有屏幕上可见的元素存在于 AX 树中。copy 按钮只在滚动到回答最底部时才出现。

旧逻辑：`contentStable` 后每次主循环只滚动 1 页，然后 sleep 800ms + 可能被内容长度波动重置 `contentStable`。每页滚动实际耗时约 3.5 秒，15-20 页的长回答轻松超过 120s timeout。

**修复**：`contentStable` 后进入连续快速滚动子循环：

```go
if contentStable {
    for scrollAttempt := 0; scrollAttempt < 50; scrollAttempt++ {
        automation.ScrollToBottom(bundleID)
        time.Sleep(300 * time.Millisecond)
        elem, err := automation.FindButton(bundleID, desc)
        if err == nil { return elem, nil }
    }
    // 50 次滚动未找到 → 误判稳定，重置
    contentStable = false
    stableCount = 0
}
```

50 scrolls × 300ms = 15 秒，足以滚过 50 页内容。不会被主循环的 contentLen 波动打断。

---

## `pplx setup-headless` 命令

**文件**：`cmd/setup_headless.go`

自动配置 headless Mac 的环境：

1. `sudo pmset -a displaysleep 0` — 禁用显示器休眠（失败时打印手动命令）
2. `defaults write com.apple.screensaver askForPassword -int 0` — 禁用屏保密码
3. 检测显示器数量（通过 `NSScreen` API）：
   - 有显示器 → 报告正常
   - 无显示器 → 建议检查远程管理设置或插入 HDMI 假负载
4. 检查 Accessibility 权限状态
5. 显示当前 pmset displaysleep 设置

---

## Fix 12: UI 状态检测 — 替代固定超时的生成完成判断

**文件**：`automation/ax.m`, `automation/ax.h`, `automation/ax.go`, `driver/perplexity.go`

**问题**：研究/深度搜索查询在 Perplexity 的 research 阶段内容长度持续为 0（可达 1-3+ 分钟），旧的固定 90 秒 `researchTimeout` 不可靠 — 要么过早超时，要么无法区分"正在研究"和"真正卡住"。

**关键发现**（通过 AX 树 dump 对比）：

| UI 状态 | `checkbox` 按钮 | `bookmark` 按钮 | `microphone` 按钮 |
|----------|:-:|:-:|:-:|
| **生成中**（包括研究阶段） | ✅ 存在 | ❌ 不存在 | ❌ 不存在 |
| **生成完成** | ❌ 不存在 | ✅ 存在 | ✅ 存在 |

`checkbox` AXButton（"停止生成"控件）在整个 research + streaming 阶段**始终存在**，生成完成后**立即消失**，是 100% 可靠的状态指示器。

**解决方案**：

C 层新增：
- `ax_has_button(bundle_id, description)` — 轻量级按钮存在检查，不返回元素只返回 bool

Go 层新增：
- `HasButton(bundleID, desc)` — Go wrapper

Driver 层重写 `waitForButtonWithScroll()` 三态逻辑：

```
[开始] → 轮询 HasButton("checkbox") + GetContentLength
  ↓
generating=true (checkbox 存在)
  → 跳过稳定性检查，继续等待（即使 content=0 持续数分钟）
  ↓
generating=false (checkbox 消失)
  → contentLen > 0 且稳定 3 轮 → ScrollToBottom → FindButton("copy")
  → contentLen == 0 超过 30s → 报告卡住
  ↓
找到 copy 按钮 → 返回成功
```

**参数调整**：
- `searchTimeout`: 120s → 300s（允许最长 5 分钟的深度研究）
- `generatingIndicator`: `"checkbox"` — 用于检测生成状态的按钮描述
- `noActivityTimeout`: 30s — 非生成状态下内容为 0 的最大等待时间

**测试结果**（5 个查询全部通过）：

| 查询 | 类型 | 结果 | 关键数据 |
|-------|------|------|----------|
| "1+1等于几" | 简单 | ✅ | 11 轮, generating=true 持续 8 轮 |
| "介绍Rust所有权系统" | 中等 | ✅ | 正常流程 |
| "2026年3月最新AI大模型排行榜" | 研究型 | ✅ | 36 轮, **24 轮在研究阶段**, generating=true 全程保持 |
| "2026年全球半导体行业趋势" | 研究型 | ✅ | 39 轮, 内容在 791 处暂停 7 轮但 generating=true 防止了误判 |

---

## Headless Mac mini 快速配置指南

```bash
# 1. 构建最新版本
make build  # 或: go build -o build/pplx .

# 2. 复制到 Mac mini
scp build/pplx macmini:~/pplx

# 3. SSH 到 Mac mini 运行 setup
ssh macmini '~/pplx setup-headless'

# 4. 确认系统设置
#    - 系统设置 → 通用 → 共享 → 远程管理: 开启
#    - 系统设置 → 隐私与安全性 → 辅助功能: 授权终端

# 5. 测试
ssh macmini '~/pplx "hello"'
```
