# Popover Dismiss Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复 `NavigateToHome()` 在 sources/model popover 处于打开状态时因 `world` 按钮在 AX 树中不可见而死循环失败的问题。

**Architecture:** 在 `NavigateToHome()` 循环前新增 popover 检测（`ax_has_popover`）+  Escape 键关闭（`ax_press_escape`）；两个 C 函数通过 CGO 暴露为 Go wrapper。不改变现有函数签名，不影响 `SetSources`/`SetModel` 流程。

**Tech Stack:** Objective-C / CGo / macOS AXUIElement / CGEvent

---

### Task 1: 在 ax.m 新增 ax_has_popover 和 ax_press_escape

**Files:**
- Modify: `automation/ax.m` (在文件末尾 `ax_has_button` 之后追加)

**Step 1: 追加两个 C 函数到 ax.m**

在 `ax.m` 末尾（1076 行之后）添加：

```objc
// 检查 App 主窗口直接子节点中是否存在 AXPopover
// 只扫描一层深度（popover 始终是 window 的直接子节点）
// 返回 1 表示有 popover，0 表示没有
int ax_has_popover(const char *bundle_id) {
    AXUIElementRef win = get_first_window(bundle_id);
    if (!win) return 0;

    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(win, kAXChildrenAttribute, (CFTypeRef *)&children);
    CFRelease(win);
    if (!children) return 0;

    int found = 0;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex i = 0; i < count && !found; i++) {
        AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
        char subrole[64];
        get_subrole(child, subrole, sizeof(subrole));
        if (strcmp(subrole, "AXPopover") == 0) {
            found = 1;
        }
    }
    CFRelease(children);
    return found;
}

// 向前台 App 发送 Escape 键（关闭 popover / 取消弹出层）
void ax_press_escape(void) {
    // kVK_Escape = 53
    CGEventRef keyDown = CGEventCreateKeyboardEvent(NULL, 53, true);
    CGEventRef keyUp   = CGEventCreateKeyboardEvent(NULL, 53, false);
    CGEventPost(kCGHIDEventTap, keyDown);
    CGEventPost(kCGHIDEventTap, keyUp);
    CFRelease(keyDown);
    CFRelease(keyUp);
}
```

**Step 2: 编译验证**

```bash
make build
```

期望：无编译错误，生成 `build/pplx`。

**Step 3: Commit**

```bash
git add automation/ax.m
git commit -m "feat(ax): add ax_has_popover and ax_press_escape"
```

---

### Task 2: 在 ax.h 声明新函数

**Files:**
- Modify: `automation/ax.h`（在 `ax_has_button` 声明之后追加）

**Step 1: 追加声明**

在 `ax.h` 末尾现有 `ax_has_button` 行之后追加：

```c
int ax_has_popover(const char *bundle_id);
void ax_press_escape(void);
```

**Step 2: 编译验证**

```bash
make build
```

期望：无警告无错误。

---

### Task 3: 在 ax.go 添加 Go wrapper

**Files:**
- Modify: `automation/ax.go`（在 `HasButton` 函数之后追加）

**Step 1: 追加 Go wrapper**

```go
// HasPopover 检查 App 主窗口直接子节点中是否存在 AXPopover（sources/model 选择器等）
func HasPopover(bundleID string) bool {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	return C.ax_has_popover(cBundleID) == 1
}

// PressEscape 向前台 App 发送 Escape 键，用于关闭 popover / 弹出层
func PressEscape() {
	C.ax_press_escape()
}
```

**Step 2: 编译验证**

```bash
make build
```

期望：无编译错误。

---

### Task 4: 在 NavigateToHome() 加 popover dismiss 逻辑

**Files:**
- Modify: `driver/perplexity.go`，`NavigateToHome()` 函数，`ensureWindow()` 调用之后、循环之前

**Step 1: 在 ensureWindow() 之后插入以下代码**

定位位置：`perplexity.go:77`（`ensureWindow()` 调用之后）

插入内容：

```go
	// 如果 sources/model popover 处于打开状态，world 按钮在 AX 树中不可见。
	// 检测到 popover 时发 Escape 键关闭它，再继续首页检测。
	if automation.HasPopover(BundleID) {
		fmt.Fprintf(os.Stderr, "[nav] popover open, pressing Escape to dismiss\n")
		automation.PressEscape()
		time.Sleep(uiDelay)
	}
```

**Step 2: 编译验证**

```bash
make build
```

**Step 3: 手动测试**

1. 打开 Perplexity Desktop，点击 world 按钮打开 sources popover，保持打开状态
2. 执行：`pplx search "hello"`
3. 预期 stderr 中出现：`[nav] popover open, pressing Escape to dismiss`
4. 预期搜索正常完成，无 "world button not found" 错误

**Step 4: Commit**

```bash
git add driver/perplexity.go
git commit -m "fix: dismiss popover before NavigateToHome world button check"
```

---

### Task 5: 合并整体 Commit（可选，若前面已分步提交则跳过）

```bash
git log --oneline -5
```

确认所有变更均已提交。

---

## 验证 Checklist

- [ ] `make build` 零错误零警告
- [ ] sources popover 打开时 `pplx search` 能自动关闭 popover 并完成搜索
- [ ] model popover 打开时同上
- [ ] 正常情况（无 popover）行为不变
- [ ] `pplx dump` 不受影响
