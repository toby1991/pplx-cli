# 陌生 macOS App 的 UI 自动化探索方法论

**日期**：2026-03-08  
**背景**：在为 Perplexity Desktop App 开发 UI 自动化时，总结出的完整探索流程

---

## 核心原则

在为陌生 macOS App 开发 UI 自动化之前，**不要假设任何东西**——一定要先探索清楚：

1. App 是什么技术栈（Electron / 原生 AppKit / SwiftUI / Qt / Tauri）
2. AX 树的实际结构和元素属性
3. 各功能的可用交互路径（AX 操作 vs URL scheme vs AppleScript vs CLI）
4. SwiftUI 的特殊限制（哪些属性永远为空）

---

## Step 1: 确认技术栈

```bash
# 查看 Info.plist
defaults read /Applications/YourApp.app/Contents/Info

# 检查是否是 Electron（有 Electron 框架 = web 内容）
ls /Applications/YourApp.app/Contents/Frameworks/ | grep -i electron

# 检查主二进制的动态库依赖
otool -L /Applications/YourApp.app/Contents/MacOS/AppBinary | head -20

# 检查是否链接了 WebKit（Electron/Tauri 的标志）
otool -L /Applications/YourApp.app/Contents/MacOS/AppBinary | grep -i webkit
```

**SwiftUI vs AppKit 的区别对 AX 的影响**：
- **AppKit**：AX 树结构丰富，`AXStaticText.value` 可读，`AXSelected` 有效
- **SwiftUI**：AX 树扁平，`AXStaticText.value` 经常为 `missing value`，状态属性不可靠
- **Electron**：走 WebKit AX 路径，DOM 直接可读

---

## Step 2: 探索 URL Scheme

```bash
# 查看 Info.plist 中注册的 URL schemes
/usr/libexec/PlistBuddy -c "Print :CFBundleURLTypes" \
  /Applications/YourApp.app/Contents/Info.plist

# 测试 URL scheme（用 open 命令）
open "yourapp://action?param=value"

# 监控 App 收到的 URL（需要 App 有日志输出）
# 或用 Instruments 的 URL scheme 跟踪
```

**注意**：不要假设 URL scheme 参数有效——必须逐一验证！

---

## Step 3: 探索 AX 树结构

### 工具一：Accessibility Inspector（推荐，Apple 官方工具）

位置：`/Applications/Xcode.app/Contents/Applications/Accessibility Inspector.app`
或 Xcode → Open Developer Tool → Accessibility Inspector

用法：
1. 打开 App，选择目标窗口
2. 在 Accessibility Inspector 中选择 App
3. 点击「Hierarchy」查看完整 AX 树
4. 点击界面元素，查看其属性（AXRole, AXDescription, AXValue 等）

### 工具二：用 Python 脚本批量导出 AX 树

```python
#!/usr/bin/env python3
"""dump_ax.py - 导出指定 App 的 AX 树"""
import subprocess
import sys

def dump_ax(bundle_id):
    script = f'''
tell application "System Events"
    set theApp to first process whose bundle identifier is "{bundle_id}"
    set output to ""
    repeat with w in windows of theApp
        set output to output & "WINDOW: " & name of w & "\\n"
        repeat with b in buttons of w
            set desc to description of b
            set output to output & "  BUTTON: " & desc & "\\n"
        end repeat
    end repeat
    return output
end tell
'''
    result = subprocess.run(['osascript', '-e', script], capture_output=True, text=True)
    print(result.stdout)
    if result.stderr:
        print("STDERR:", result.stderr, file=sys.stderr)

dump_ax(sys.argv[1])
```

```bash
python3 dump_ax.py ai.perplexity.mac
```

### 工具三：CGo/C 程序直接枚举 AX 树

最精确，可以看到所有属性：

```objc
void enumerate_ax_tree(AXUIElementRef element, int depth) {
    // 获取 AXRole
    CFTypeRef role;
    AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &role);
    
    // 获取 AXDescription  
    CFTypeRef desc;
    AXUIElementCopyAttributeValue(element, kAXDescriptionAttribute, &desc);
    
    // 获取 AXValue
    CFTypeRef value;
    AXUIElementCopyAttributeValue(element, kAXValueAttribute, &value);
    
    printf("%*s[%s] desc=%s value=%s\n", depth*2, "",
           role ? CFStringGetCStringPtr(role, kCFStringEncodingUTF8) : "?",
           desc ? CFStringGetCStringPtr(desc, kCFStringEncodingUTF8) : "?",
           value ? CFStringGetCStringPtr(value, kCFStringEncodingUTF8) : "?");
    
    // 递归子元素
    CFArrayRef children;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef*)&children);
    if (children) {
        for (CFIndex i = 0; i < CFArrayGetCount(children); i++) {
            enumerate_ax_tree(CFArrayGetValueAtIndex(children, i), depth + 1);
        }
        CFRelease(children);
    }
}
```

---

## Step 4: 验证每个功能的可行性

对每个需要自动化的功能，按以下顺序尝试：

### 优先级顺序（从最稳定到最脆弱）

1. **CLI / 命令行**（最稳定）
   - `defaults read/write` 读写 UserDefaults
   - 专有命令行工具
   
2. **URL Scheme**（较稳定，但参数可能无效）
   - `open "app://action?param=value"`
   - 必须逐一测试参数有效性
   
3. **AppleScript**（中等稳定）
   - 查看 App 是否有 sdef：`sdef /Applications/YourApp.app`
   - `tell application "YourApp" to ...`
   
4. **AX UI 自动化**（最灵活但最脆弱）
   - 依赖 UI 结构不变
   - SwiftUI App 的 AX 属性可能不可靠
   
5. **鼠标坐标点击**（最后手段，完全不可靠）
   - 依赖窗口大小和位置固定
   - 绝对不推荐用于生产代码

### 验证矩阵（以 Perplexity 为例）

| 功能 | CLI | URL Scheme | AppleScript | AX 点击 |
|------|-----|-----------|-------------|---------|
| 触发搜索 | ❌ | ✅ `?q=` | ❌ | ✅ |
| 切换模式 | ❌ | ❌ `?mode=` 无效 | ❌ | ✅ description 按钮 |
| 切换模型 | ❌ | ❌ `?model=` 无效 | ❌ | ✅ popover + 按钮 |
| 读取状态 | ✅ `defaults read` | - | - | ❌ AXSelected 失效 |
| 读取结果 | ❌ | - | ❌ | ✅ copy 按钮 + 剪贴板 |

---

## Step 5: 处理 SwiftUI 特殊限制

### 已知 SwiftUI AX 限制

**`AXStaticText.value` 不可读**：
- 症状：读取文本内容时返回 `missing value` 或空字符串
- 原因：SwiftUI 的 `Text` 组件默认不向 AX API 暴露文本值
- **解法**：找 copy/share 按钮，通过剪贴板读取内容

**`AXSelected` 对 toggle/tab 按钮无效**：
- 症状：选中的按钮 `AXSelected = false`，未选中的也是 `false`
- 原因：SwiftUI 不映射选中状态到 AX
- **解法**：`defaults read` 读取 UserDefaults 中的状态

**AXCheckBox `value` 为 `0`/`1` 字符串而非布尔**：
- 可以读写，但类型是 `CFString` 而非 `CFBoolean`

**Popover 元素只有在弹出后才存在于 AX 树中**：
- 必须先点击触发按钮，等待 popover 出现，再查找其中的元素
- 可通过短暂 `usleep(300000)` 等待 popover 动画完成

---

## Step 6: 结果读取策略

当 `AXStaticText.value` 不可用时，可用的替代方案：

### 方案 A：Copy 按钮 + 剪贴板（推荐）
```
1. 等待 copy 按钮出现（表示内容加载完成）
2. 记录当前剪贴板内容（用于之后恢复）
3. AX 点击 copy 按钮
4. usleep(200000) 等待剪贴板写入
5. 读取 NSPasteboard.generalPasteboard 的字符串内容
6. 恢复原始剪贴板内容（可选）
```

### 方案 B：Share Sheet + 导出文件
如果有 share 功能，可以导出到文件后读取（较复杂，不推荐）

### 方案 C：OCR（截图 + 文字识别）
使用 macOS Vision 框架对截图做 OCR（精度有限，不推荐）

---

## 常用调试命令速查

```bash
# 查看 App 的 Bundle ID
mdls -name kMDItemCFBundleIdentifier /Applications/YourApp.app

# 查看 App 的 PID
pgrep -f "YourApp"

# 监控 UserDefaults 变化
defaults read ai.perplexity.mac

# 实时监控 defaults 变化（需要 fswatch）
fswatch ~/Library/Preferences/ai.perplexity.mac.plist | \
  xargs -I{} defaults read ai.perplexity.mac

# 检查 App 是否有 Accessibility 权限
# （在被自动化的程序中运行）
AXIsProcessTrusted()  # returns bool

# 查看 App 的 URL scheme handler
/usr/libexec/PlistBuddy -c "Print :CFBundleURLTypes:0:CFBundleURLSchemes:0" \
  /Applications/YourApp.app/Contents/Info.plist
```

---

## 经验教训（来自 Perplexity 探索过程）

1. **不要假设 URL scheme 参数有效**：Perplexity 的 `?mode=` `?model=` 参数静默忽略，URL scheme 只是打开 App 并填充搜索框的快捷方式，实际的功能切换仍需 AX 操作

2. **SwiftUI App 的 AX 树比 AppKit 稀疏**：不要期待 `AXStaticText` 能读到内容，要找 copy/export 按钮

3. **先找最稳定的路径**：`defaults read` 比 AX 属性更可靠地读取状态

4. **Popover 是懒加载的**：点击触发按钮后需要等待动画，才能在 AX 树中找到 popover 内的元素

5. **`AXDescription` 是按钮识别的最可靠字段**：比 `AXTitle` 或位置更稳定；Perplexity 用 SF Symbols 名作为 description

6. **搜索完成的信号是 copy 按钮出现**：不要用 `AXValue` 或文本长度判断是否加载完成
