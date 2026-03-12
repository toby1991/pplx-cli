#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#include <libproc.h>
#include "ax.h"

// ─────────────────────────────────────────────
// 内部工具函数
// ─────────────────────────────────────────────

// Layer 3: 纯系统调用 PID 查找（proc_listpids + proc_pidpath + Info.plist 文件 I/O）
// 不依赖任何 Cocoa / LaunchServices API，SSH Background domain 下 100% 可用
// 返回匹配的 PID，失败返回 -1
static pid_t find_pid_by_bundle_id_syscall(const char *bundle_id) {
    int bufSize = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
    if (bufSize <= 0) return -1;

    pid_t *pids = malloc(bufSize);
    if (!pids) return -1;

    int actual = proc_listpids(PROC_ALL_PIDS, 0, pids, bufSize);
    int count = actual / (int)sizeof(pid_t);

    pid_t result = -1;

    for (int i = 0; i < count; i++) {
        if (pids[i] == 0) continue;

        char pathBuf[PROC_PIDPATHINFO_MAXSIZE];
        if (proc_pidpath(pids[i], pathBuf, sizeof(pathBuf)) <= 0) continue;

        // 只关心 .app bundle 中的进程：路径必须包含 "/Contents/MacOS/"
        char *macosMarker = strstr(pathBuf, "/Contents/MacOS/");
        if (!macosMarker) continue;

        // 从路径中提取 .app bundle 目录
        // 例: /Applications/Perplexity.app/Contents/MacOS/Perplexity
        //   → /Applications/Perplexity.app
        // 向前找 ".app/" 或 ".app/Contents"
        char *appSuffix = strstr(pathBuf, ".app/Contents/MacOS/");
        if (!appSuffix) continue;

        // 计算 .app 路径长度（包含 ".app"）
        size_t appPathLen = (appSuffix - pathBuf) + 4; // +4 for ".app"
        char appDir[PROC_PIDPATHINFO_MAXSIZE];
        strncpy(appDir, pathBuf, appPathLen);
        appDir[appPathLen] = '\0';

        // 读取 <app_dir>/Contents/Info.plist
        char plistPath[PROC_PIDPATHINFO_MAXSIZE];
        snprintf(plistPath, sizeof(plistPath), "%s/Contents/Info.plist", appDir);

        NSString *plistPathStr = [NSString stringWithUTF8String:plistPath];
        NSDictionary *plist = [NSDictionary dictionaryWithContentsOfFile:plistPathStr];
        if (!plist) continue;

        NSString *plistBundleID = plist[@"CFBundleIdentifier"];
        if (!plistBundleID) continue;

        if (strcmp([plistBundleID UTF8String], bundle_id) == 0) {
            result = pids[i];
            break;
        }
    }

    free(pids);
    return result;
}

// 通过 Bundle ID 获取 NSRunningApplication（Cocoa API，仅 Layer 1/2）
// GUI/Aqua session 下可用；SSH Background domain 下通常返回 nil
// 返回 retained NSRunningApplication*（ARC 管理）；失败返回 nil
static NSRunningApplication *get_running_app(const char *bundle_id) {
    NSString *bid = [NSString stringWithUTF8String:bundle_id];

    // Layer 1: NSRunningApplication 类方法（不依赖 NSWorkspace 实例）
    NSArray *matching = [NSRunningApplication runningApplicationsWithBundleIdentifier:bid];
    if (matching.count > 0) {
        return matching.firstObject;
    }

    // Layer 2: NSWorkspace（GUI/Aqua session 下可用）
    NSArray *apps = [[NSWorkspace sharedWorkspace] runningApplications];
    for (NSRunningApplication *app in apps) {
        if ([app.bundleIdentifier isEqualToString:bid]) {
            return app;
        }
    }

    // Layer 3: 尝试通过 syscall 找到 PID，再构造 NSRunningApplication
    // 注意：runningApplicationWithProcessIdentifier: 在 SSH 下也可能失败，
    //       但 get_running_app 主要给 ax_activate_app 用，失败时有 open -b 回退
    pid_t pid = find_pid_by_bundle_id_syscall(bundle_id);
    if (pid > 0) {
        NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        if (app) return app;
    }

    return nil;
}

// 通过 Bundle ID 获取 App 的 PID（三层 fallback，SSH session 兼容）
// Layer 1/2: Cocoa API（快速路径）
// Layer 3: 纯系统调用（SSH 下 100% 可用）
static pid_t get_pid_for_bundle(const char *bundle_id) {
    // 先尝试 Cocoa API（快速）
    NSRunningApplication *app = get_running_app(bundle_id);
    if (app) {
        return app.processIdentifier;
    }

    // Cocoa 全部失败 → 纯系统调用（SSH 下的最终保障）
    return find_pid_by_bundle_id_syscall(bundle_id);
}

// 递归在 AX 树中查找第一个匹配 description 的按钮（带深度限制，防止 headless 自引用）
static AXUIElementRef find_button_recursive(AXUIElementRef element, NSString *description, int depth) {
    if (depth > 30) return NULL;

    // 检查当前节点
    CFTypeRef roleVal = NULL;
    AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &roleVal);
    if (roleVal) {
        NSString *role = (__bridge_transfer NSString *)roleVal;
        if ([role isEqualToString:@"AXButton"]) {
            CFTypeRef descVal = NULL;
            AXUIElementCopyAttributeValue(element, kAXDescriptionAttribute, &descVal);
            if (descVal) {
                NSString *desc = (__bridge_transfer NSString *)descVal;
                if ([desc isEqualToString:description]) {
                    CFRetain(element);
                    return element;
                }
            }
        }
    }

    // 递归子节点
    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (!children) return NULL;

    AXUIElementRef found = NULL;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex i = 0; i < count; i++) {
        AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
        found = find_button_recursive(child, description, depth + 1);
        if (found) break;
    }
    CFRelease(children);
    return found;
}

// 递归查找 description 前缀匹配的按钮（带深度限制）
static AXUIElementRef find_button_prefix_recursive(AXUIElementRef element, NSString *prefix, int depth) {
    if (depth > 30) return NULL;

    CFTypeRef roleVal = NULL;
    AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &roleVal);
    if (roleVal) {
        NSString *role = (__bridge_transfer NSString *)roleVal;
        if ([role isEqualToString:@"AXButton"]) {
            CFTypeRef descVal = NULL;
            AXUIElementCopyAttributeValue(element, kAXDescriptionAttribute, &descVal);
            if (descVal) {
                NSString *desc = (__bridge_transfer NSString *)descVal;
                if ([desc hasPrefix:prefix]) {
                    CFRetain(element);
                    return element;
                }
            }
        }
    }

    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (!children) return NULL;

    AXUIElementRef found = NULL;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex i = 0; i < count; i++) {
        AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
        found = find_button_prefix_recursive(child, prefix, depth + 1);
        if (found) break;
    }
    CFRelease(children);
    return found;
}

// 递归查找 AXCheckBox（按 description 前缀，带深度限制）
static AXUIElementRef find_checkbox_prefix_recursive(AXUIElementRef element, NSString *prefix, int depth) {
    if (depth > 30) return NULL;

    CFTypeRef roleVal = NULL;
    AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &roleVal);
    if (roleVal) {
        NSString *role = (__bridge_transfer NSString *)roleVal;
        if ([role isEqualToString:@"AXCheckBox"]) {
            CFTypeRef descVal = NULL;
            AXUIElementCopyAttributeValue(element, kAXDescriptionAttribute, &descVal);
            if (descVal) {
                NSString *desc = (__bridge_transfer NSString *)descVal;
                if ([desc hasPrefix:prefix]) {
                    CFRetain(element);
                    return element;
                }
            }
        }
    }

    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (!children) return NULL;

    AXUIElementRef found = NULL;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex i = 0; i < count; i++) {
        AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
        found = find_checkbox_prefix_recursive(child, prefix, depth + 1);
        if (found) break;
    }
    CFRelease(children);
    return found;
}

// 获取 App 的根 AX 元素
static AXUIElementRef get_app_element(const char *bundle_id) {
    pid_t pid = get_pid_for_bundle(bundle_id);
    if (pid < 0) return NULL;
    return AXUIElementCreateApplication(pid);
}

// 获取 App 的第一个 AXWindow（绕过 AXApplication 的 children，避免 headless 自引用循环）
// 返回 retain 过的 AXUIElementRef，调用方需 CFRelease；无窗口时返回 NULL
// 包含角色验证：拒绝 WindowServer 退化时返回的 AXApplication 自引用
// 获取元素的 subrole 字符串，写入 buf。失败时 buf[0] = '\0'。
static void get_subrole(AXUIElementRef elem, char *buf, size_t bufsize) {
    buf[0] = '\0';
    CFTypeRef val = NULL;
    AXUIElementCopyAttributeValue(elem, kAXSubroleAttribute, &val);
    if (val) {
        if (CFGetTypeID(val) == CFStringGetTypeID()) {
            CFStringGetCString((CFStringRef)val, buf, (CFIndex)bufsize, kCFStringEncodingUTF8);
        }
        CFRelease(val);
    }
}

// 获取元素的 role 字符串，写入 buf。失败时 buf[0] = '\0'。
static void get_role(AXUIElementRef elem, char *buf, size_t bufsize) {
    buf[0] = '\0';
    CFTypeRef val = NULL;
    AXUIElementCopyAttributeValue(elem, kAXRoleAttribute, &val);
    if (val) {
        if (CFGetTypeID(val) == CFStringGetTypeID()) {
            CFStringGetCString((CFStringRef)val, buf, (CFIndex)bufsize, kCFStringEncodingUTF8);
        }
        CFRelease(val);
    }
}

static AXUIElementRef get_first_window(const char *bundle_id) {
    AXUIElementRef app = get_app_element(bundle_id);
    if (!app) return NULL;

    CFTypeRef windowsVal = NULL;
    AXError axErr = AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &windowsVal);
    CFRelease(app);

    if (axErr != kAXErrorSuccess) {
        fprintf(stderr, "[get_first_window] kAXWindowsAttribute AXError=%d "
                "(API disabled=-25211, cannot complete=-25204)\n", (int)axErr);
        fflush(stderr);
        if (windowsVal) CFRelease(windowsVal);
        return NULL;
    }

    if (!windowsVal || CFGetTypeID(windowsVal) != CFArrayGetTypeID()) {
        if (windowsVal) CFRelease(windowsVal);
        return NULL;
    }

    CFArrayRef windows = (CFArrayRef)windowsVal;
    CFIndex count = CFArrayGetCount(windows);
    if (count == 0) {
        CFRelease(windowsVal);
        return NULL;
    }

    // ── 两轮遍历：优先 AXStandardWindow，跳过 AXDialog 和 AXApplication ──

    AXUIElementRef fallback = NULL;  // 非 AXDialog/AXApplication 的备选

    for (CFIndex i = 0; i < count; i++) {
        AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
        char role[64], subrole[64];
        get_role(win, role, sizeof(role));
        get_subrole(win, subrole, sizeof(subrole));

        // 拒绝 AXApplication 自引用（WindowServer compositor 退化）
        if (strcmp(role, "AXApplication") == 0) {
            fprintf(stderr, "[get_first_window] WARNING: window[%ld] is AXApplication "
                    "(self-reference) — WindowServer compositor may be degraded. "
                    "Ensure self-loopback VNC is active (open vnc://localhost).\n", (long)i);
            fflush(stderr);
            continue;
        }

        // 最优：AXStandardWindow — 直接返回
        if (strcmp(subrole, "AXStandardWindow") == 0) {
            CFRetain(win);
            CFRelease(windowsVal);
            return win;
        }

        // 跳过 AXDialog（输入法弹窗等临时窗口）
        if (strcmp(subrole, "AXDialog") == 0) {
            continue;
        }

        // 其他 subrole：记为 fallback（取第一个）
        if (!fallback) {
            fallback = win;
        }
    }

    // 没有 AXStandardWindow，用 fallback（非 Dialog/Application 的第一个窗口）
    if (fallback) {
        CFRetain(fallback);
        CFRelease(windowsVal);
        return fallback;
    }

    // 全部窗口都被跳过
    fprintf(stderr, "[get_first_window] WARNING: %ld window(s) found but none usable "
            "(all AXDialog/AXApplication)\n", (long)count);
    fflush(stderr);
    CFRelease(windowsVal);
    return NULL;
}

// ─────────────────────────────────────────────
// 公开 C 接口
// ─────────────────────────────────────────────

// 激活 App（将其置于前台）
// 返回 0 成功，-1 失败
// 使用 get_running_app() 查找进程 → Cocoa 激活 → 失败则 open -b 回退
int ax_activate_app(const char *bundle_id) {
    NSRunningApplication *app = get_running_app(bundle_id);
    if (app) {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
        BOOL ok = [app activateWithOptions:NSApplicationActivateIgnoringOtherApps];
#pragma clang diagnostic pop
        if (ok) return 0;
    }

    // 终极回退：通过 shell 命令激活（SSH session 下 Cocoa 激活 API 可能失败）
    char cmd[512];
    snprintf(cmd, sizeof(cmd), "/usr/bin/open -b '%s'", bundle_id);
    int ret = system(cmd);
    return (ret == 0) ? 0 : -1;
}

// 在整个 App 中查找指定 description 的按钮
// 从 Window 开始遍历（绕过 headless AXApplication 自引用）
// 返回 AXUIElementRef（调用方负责 CFRelease），失败返回 NULL
void* ax_find_button(const char *bundle_id, const char *description) {
    AXUIElementRef win = get_first_window(bundle_id);
    if (!win) return NULL;
    NSString *desc = [NSString stringWithUTF8String:description];
    AXUIElementRef btn = find_button_recursive(win, desc, 0);
    CFRelease(win);
    return (void*)btn;
}

// 查找 description 前缀匹配的按钮（从 Window 开始遍历）
void* ax_find_button_prefix(const char *bundle_id, const char *prefix) {
    AXUIElementRef win = get_first_window(bundle_id);
    if (!win) return NULL;
    NSString *pfx = [NSString stringWithUTF8String:prefix];
    AXUIElementRef btn = find_button_prefix_recursive(win, pfx, 0);
    CFRelease(win);
    return (void*)btn;
}

// 查找 description 前缀匹配的 AXCheckBox（从 Window 开始遍历）
void* ax_find_checkbox_prefix(const char *bundle_id, const char *prefix) {
    AXUIElementRef win = get_first_window(bundle_id);
    if (!win) return NULL;
    NSString *pfx = [NSString stringWithUTF8String:prefix];
    AXUIElementRef cb = find_checkbox_prefix_recursive(win, pfx, 0);
    CFRelease(win);
    return (void*)cb;
}

// 读取 AXCheckBox 的当前 value（"0" 或 "1"）
// 返回 0 或 1，-1 表示失败
int ax_get_checkbox_value(void *element_ref) {
    if (!element_ref) return -1;
    AXUIElementRef elem = (AXUIElementRef)element_ref;
    CFTypeRef val = NULL;
    AXUIElementCopyAttributeValue(elem, kAXValueAttribute, &val);
    if (!val) return -1;
    NSString *valStr = (__bridge_transfer NSString *)val;
    return [valStr intValue];
}

// 点击元素（模拟按下）
// 返回 0 成功，-1 失败
int ax_click(void *element_ref) {
    if (!element_ref) return -1;
    AXUIElementRef elem = (AXUIElementRef)element_ref;
    AXError err = AXUIElementPerformAction(elem, kAXPressAction);
    return (err == kAXErrorSuccess) ? 0 : -1;
}

// 释放 AXUIElement 引用
void ax_release_element(void *element_ref) {
    if (element_ref) {
        CFRelease((AXUIElementRef)element_ref);
    }
}

// 读取剪贴板字符串内容
// 返回 malloc 的 C 字符串，调用方必须 free()；失败返回 NULL
char* ax_read_clipboard(void) {
    NSPasteboard *pb = [NSPasteboard generalPasteboard];
    NSString *str = [pb stringForType:NSPasteboardTypeString];
    if (!str) return NULL;
    const char *cstr = [str UTF8String];
    if (!cstr) return NULL;
    return strdup(cstr);
}

// 将字符串写入剪贴板（用于恢复原始内容）
void ax_write_clipboard(const char *text) {
    NSPasteboard *pb = [NSPasteboard generalPasteboard];
    [pb clearContents];
    if (text) {
        NSString *str = [NSString stringWithUTF8String:text];
        [pb setString:str forType:NSPasteboardTypeString];
    }
}

// 返回剪贴板的 changeCount（每次内容变化时单调递增）
// 用于检测 copy 操作是否真正更新了剪贴板
long ax_clipboard_change_count(void) {
    return (long)[NSPasteboard generalPasteboard].changeCount;
}

// ─────────────────────────────────────────────
// AXTextArea 内容收集（clipboard fallback 用）
// ─────────────────────────────────────────────

static void collect_textarea_values_recursive(AXUIElementRef element, int depth, NSMutableString *result) {
    if (depth > 50) return;

    CFTypeRef roleVal = NULL;
    AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &roleVal);
    if (roleVal && CFGetTypeID(roleVal) == CFStringGetTypeID()) {
        char roleBuf[32] = "";
        CFStringGetCString((CFStringRef)roleVal, roleBuf, sizeof(roleBuf), kCFStringEncodingUTF8);
        if (strcmp(roleBuf, "AXTextArea") == 0) {
            CFTypeRef val = NULL;
            if (AXUIElementCopyAttributeValue(element, kAXValueAttribute, &val) == kAXErrorSuccess && val) {
                if (CFGetTypeID(val) == CFStringGetTypeID()) {
                    NSString *text = (__bridge NSString *)val;
                    if (text.length > 0) {
                        if (result.length > 0) [result appendString:@"\n\n"];
                        [result appendString:text];
                    }
                }
                CFRelease(val);
            }
        }
    }
    if (roleVal) CFRelease(roleVal);

    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (children) {
        CFIndex count = CFArrayGetCount(children);
        for (CFIndex i = 0; i < count; i++) {
            collect_textarea_values_recursive(
                (AXUIElementRef)CFArrayGetValueAtIndex(children, i), depth + 1, result);
        }
        CFRelease(children);
    }
}

// 收集 App 窗口中所有 AXTextArea 的文本值，以换行分隔
// 用于 copy 按钮未更新剪贴板时的 fallback
// 返回 malloc 的 C 字符串，调用方必须 free()；失败返回 NULL
char* ax_read_all_textarea_values(const char *bundle_id) {
    AXUIElementRef win = get_first_window(bundle_id);
    if (!win) return NULL;

    NSMutableString *result = [NSMutableString new];
    collect_textarea_values_recursive(win, 0, result);
    CFRelease(win);

    if (result.length == 0) return NULL;
    return strdup([result UTF8String]);
}

// 等待指定 description 的按钮出现（从 Window 开始遍历）
// timeout_ms: 最长等待毫秒数
// poll_ms: 轮询间隔毫秒数
// 返回找到的元素（调用方负责 CFRelease），超时返回 NULL
void* ax_wait_for_button(const char *bundle_id, const char *description, int timeout_ms, int poll_ms) {
    int elapsed = 0;
    NSString *desc = [NSString stringWithUTF8String:description];
    while (elapsed < timeout_ms) {
        AXUIElementRef win = get_first_window(bundle_id);
        if (win) {
            AXUIElementRef btn = find_button_recursive(win, desc, 0);
            CFRelease(win);
            if (btn) return (void*)btn;
        }
        usleep(poll_ms * 1000);
        elapsed += poll_ms;
    }
    return NULL;
}

// 在文件打开对话框中输入路径（用于附件上传）
// 流程：等待 NSSavePanel/NSOpenPanel 出现 → 输入路径 → 确认
// 返回 0 成功，-1 失败
int ax_open_file_dialog_and_input(const char *path) {
    // 发送 Cmd+Shift+G 快捷键（打开 Go to Folder 对话框）
    CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);

    // Cmd+Shift+G
    CGEventRef keyDown = CGEventCreateKeyboardEvent(source, 5 /* G */, true);
    CGEventSetFlags(keyDown, kCGEventFlagMaskCommand | kCGEventFlagMaskShift);
    CGEventRef keyUp = CGEventCreateKeyboardEvent(source, 5 /* G */, false);
    CGEventSetFlags(keyUp, kCGEventFlagMaskCommand | kCGEventFlagMaskShift);

    CGEventPost(kCGHIDEventTap, keyDown);
    CGEventPost(kCGHIDEventTap, keyUp);
    CFRelease(keyDown);
    CFRelease(keyUp);
    CFRelease(source);

    usleep(500000); // 等待对话框出现

    // 输入路径字符串
    NSString *pathStr = [NSString stringWithUTF8String:path];
    for (NSUInteger i = 0; i < pathStr.length; i++) {
        unichar c = [pathStr characterAtIndex:i];
        CGEventRef charEvent = CGEventCreateKeyboardEvent(NULL, 0, true);
        CGEventKeyboardSetUnicodeString(charEvent, 1, &c);
        CGEventPost(kCGHIDEventTap, charEvent);
        CFRelease(charEvent);
        usleep(10000); // 10ms 间隔，避免字符丢失
    }

    usleep(200000); // 等待输入完成

    // 按回车确认
    CGEventRef enterDown = CGEventCreateKeyboardEvent(NULL, 36 /* Return */, true);
    CGEventRef enterUp = CGEventCreateKeyboardEvent(NULL, 36 /* Return */, false);
    CGEventPost(kCGHIDEventTap, enterDown);
    CGEventPost(kCGHIDEventTap, enterUp);
    CFRelease(enterDown);
    CFRelease(enterUp);

    return 0;
}

// 读取 macOS UserDefaults 中的字符串值
// 通过 NSTask 调用 `defaults read`，能正确读取沙盒 App 的 preferences
// 返回 malloc 的 C 字符串，调用方必须 free()；失败返回 NULL
char* ax_read_defaults(const char *app_id, const char *key) {
    NSTask *task = [[NSTask alloc] init];
    task.launchPath = @"/usr/bin/defaults";
    task.arguments = @[
        @"read",
        [NSString stringWithUTF8String:app_id],
        [NSString stringWithUTF8String:key]
    ];

    NSPipe *pipe = [NSPipe pipe];
    task.standardOutput = pipe;
    task.standardError = [NSPipe pipe]; // 丢弃 stderr

    @try {
        [task launch];
        [task waitUntilExit];
    } @catch (NSException *e) {
        return NULL;
    }

    if (task.terminationStatus != 0) return NULL;

    NSData *data = [[pipe fileHandleForReading] readDataToEndOfFile];
    if (!data || data.length == 0) return NULL;

    NSString *output = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
    output = [output stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
    if (!output || output.length == 0) return NULL;

    return strdup([output UTF8String]);
}

// 等待 App 的窗口出现（headless 场景下窗口可能延迟出现）
// 返回 0 表示窗口已就绪，-1 表示超时
int ax_wait_for_window(const char *bundle_id, int timeout_ms, int poll_ms) {
    int elapsed = 0;
    while (elapsed < timeout_ms) {
        AXUIElementRef win = get_first_window(bundle_id);
        if (win) {
            CFRelease(win);
            return 0;
        }
        usleep(poll_ms * 1000);
        elapsed += poll_ms;
    }
    return -1;
}

// 检查当前进程是否有 Accessibility 权限
// 有显示器时弹出系统授权引导弹窗（方便首次使用）
// headless（无显示器）时仅静默检查，避免触发 AppKit UI 导致崩溃
int ax_is_trusted(void) {
    if ([NSScreen screens].count > 0) {
        // 有显示器：弹出授权提示
        NSDictionary *options = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
        return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options) ? 1 : 0;
    }
    // headless：静默检查
    return AXIsProcessTrusted() ? 1 : 0;
}

// ─────────────────────────────────────────────
// 内容长度：递归统计所有 AXTextArea 的字符数
// ─────────────────────────────────────────────

static int count_text_length_recursive(AXUIElementRef element, int depth) {
    if (depth > 50) return 0;

    int total = 0;

    // 如果是 AXTextArea，累加其文本长度
    CFTypeRef roleVal = NULL;
    AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &roleVal);
    if (roleVal && CFGetTypeID(roleVal) == CFStringGetTypeID()) {
        char roleBuf[32] = "";
        CFStringGetCString((CFStringRef)roleVal, roleBuf, sizeof(roleBuf), kCFStringEncodingUTF8);
        if (strcmp(roleBuf, "AXTextArea") == 0) {
            CFTypeRef val = NULL;
            if (AXUIElementCopyAttributeValue(element, kAXValueAttribute, &val) == kAXErrorSuccess && val) {
                if (CFGetTypeID(val) == CFStringGetTypeID()) {
                    total += (int)CFStringGetLength((CFStringRef)val);
                }
                CFRelease(val);
            }
        }
    }
    if (roleVal) CFRelease(roleVal);

    // 递归子节点
    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (children) {
        CFIndex count = CFArrayGetCount(children);
        for (CFIndex i = 0; i < count; i++) {
            total += count_text_length_recursive(
                (AXUIElementRef)CFArrayGetValueAtIndex(children, i), depth + 1);
        }
        CFRelease(children);
    }
    return total;
}

// 返回 App 当前可见 AX 树中所有 AXTextArea 的字符总数（从 Window 开始遍历）
// 用于检测 Perplexity 是否还在生成（内容仍在增长 = 未完成）
int ax_get_content_length(const char *bundle_id) {
    AXUIElementRef win = get_first_window(bundle_id);
    if (!win) return 0;
    int total = count_text_length_recursive(win, 0);
    CFRelease(win);
    return total;
}

// ─────────────────────────────────────────────
// 诊断：递归打印所有 AX 元素
// ─────────────────────────────────────────────

static void dump_recursive(AXUIElementRef element, int depth) {
    // 避免无限深度
    if (depth > 50) return;

    CFTypeRef roleVal = NULL, descVal = NULL, valueVal = NULL, titleVal = NULL;
    AXUIElementCopyAttributeValue(element, kAXRoleAttribute,        &roleVal);
    AXUIElementCopyAttributeValue(element, kAXDescriptionAttribute, &descVal);
    AXUIElementCopyAttributeValue(element, kAXValueAttribute,       &valueVal);
    AXUIElementCopyAttributeValue(element, kAXTitleAttribute,       &titleVal);

    char roleBuf[128] = "?";
    char descBuf[256] = "-";
    char valBuf[512]  = "-";
    char titleBuf[256]= "-";

    if (roleVal && CFGetTypeID(roleVal) == CFStringGetTypeID())
        CFStringGetCString((CFStringRef)roleVal, roleBuf, sizeof(roleBuf), kCFStringEncodingUTF8);
    if (descVal && CFGetTypeID(descVal) == CFStringGetTypeID())
        CFStringGetCString((CFStringRef)descVal, descBuf, sizeof(descBuf), kCFStringEncodingUTF8);
    if (valueVal && CFGetTypeID(valueVal) == CFStringGetTypeID())
        CFStringGetCString((CFStringRef)valueVal, valBuf, sizeof(valBuf), kCFStringEncodingUTF8);
    if (titleVal && CFGetTypeID(titleVal) == CFStringGetTypeID())
        CFStringGetCString((CFStringRef)titleVal, titleBuf, sizeof(titleBuf), kCFStringEncodingUTF8);

    // 所有节点都打印
    fprintf(stderr, "%*s[%s] desc=%s title=%s val=%s\n",
           depth * 2, "", roleBuf, descBuf, titleBuf, valBuf);

    if (roleVal)  CFRelease(roleVal);
    if (descVal)  CFRelease(descVal);
    if (valueVal) CFRelease(valueVal);
    if (titleVal) CFRelease(titleVal);

    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (!children) return;

    CFIndex count = CFArrayGetCount(children);
    for (CFIndex i = 0; i < count; i++) {
        dump_recursive((AXUIElementRef)CFArrayGetValueAtIndex(children, i), depth + 1);
    }
    CFRelease(children);
}

void ax_dump_elements(const char *bundle_id) {
    AXUIElementRef app = get_app_element(bundle_id);
    if (!app) {
        fprintf(stderr, "ERROR: app not found for bundle_id=%s\n", bundle_id);
        fflush(stderr);
        return;
    }
    fprintf(stderr, "=== AX DUMP: %s ===\n", bundle_id);
    fflush(stderr);
    dump_recursive(app, 0);
    fprintf(stderr, "=== END DUMP ===\n");
    fflush(stderr);
    CFRelease(app);
}

// ─────────────────────────────────────────────
// 窗口详细诊断
// ─────────────────────────────────────────────

static int count_children(AXUIElementRef element) {
    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (!children) return 0;
    int count = (int)CFArrayGetCount(children);
    CFRelease(children);
    return count;
}

static void print_ax_string_attr(AXUIElementRef element, CFStringRef attr, const char *label) {
    CFTypeRef val = NULL;
    AXError err = AXUIElementCopyAttributeValue(element, attr, &val);
    if (err == kAXErrorSuccess && val) {
        if (CFGetTypeID(val) == CFStringGetTypeID()) {
            char buf[256] = "";
            CFStringGetCString((CFStringRef)val, buf, sizeof(buf), kCFStringEncodingUTF8);
            fprintf(stderr, "    %s: %s\n", label, buf);
        } else {
            fprintf(stderr, "    %s: <non-string type %lu>\n", label, CFGetTypeID(val));
        }
        CFRelease(val);
    } else {
        fprintf(stderr, "    %s: <unavailable, AXError=%d>\n", label, (int)err);
    }
}

static void print_ax_point_attr(AXUIElementRef element, CFStringRef attr, const char *label) {
    CFTypeRef val = NULL;
    AXError err = AXUIElementCopyAttributeValue(element, attr, &val);
    if (err == kAXErrorSuccess && val) {
        CGPoint pt;
        if (AXValueGetValue((AXValueRef)val, kAXValueCGPointType, &pt)) {
            fprintf(stderr, "    %s: (%.0f, %.0f)\n", label, pt.x, pt.y);
        } else {
            fprintf(stderr, "    %s: <bad AXValue>\n", label);
        }
        CFRelease(val);
    } else {
        fprintf(stderr, "    %s: <unavailable, AXError=%d>\n", label, (int)err);
    }
}

static void print_ax_size_attr(AXUIElementRef element, CFStringRef attr, const char *label) {
    CFTypeRef val = NULL;
    AXError err = AXUIElementCopyAttributeValue(element, attr, &val);
    if (err == kAXErrorSuccess && val) {
        CGSize sz;
        if (AXValueGetValue((AXValueRef)val, kAXValueCGSizeType, &sz)) {
            fprintf(stderr, "    %s: %.0f x %.0f\n", label, sz.width, sz.height);
        } else {
            fprintf(stderr, "    %s: <bad AXValue>\n", label);
        }
        CFRelease(val);
    } else {
        fprintf(stderr, "    %s: <unavailable, AXError=%d>\n", label, (int)err);
    }
}

void ax_dump_window_info(const char *bundle_id) {
    fprintf(stderr, "=== WINDOW DIAGNOSTICS: %s ===\n", bundle_id);

    // 检查 NSScreen
    NSArray *screens = [NSScreen screens];
    fprintf(stderr, "[screens] NSScreen count: %lu\n", (unsigned long)screens.count);
    for (NSUInteger i = 0; i < screens.count; i++) {
        NSScreen *s = screens[i];
        NSRect frame = s.frame;
        fprintf(stderr, "  screen[%lu]: %.0fx%.0f at (%.0f,%.0f) name=%s\n",
                (unsigned long)i, frame.size.width, frame.size.height,
                frame.origin.x, frame.origin.y,
                [s.localizedName UTF8String]);
    }

    pid_t pid = get_pid_for_bundle(bundle_id);
    if (pid < 0) {
        fprintf(stderr, "[error] app not found\n");
        fflush(stderr);
        return;
    }
    fprintf(stderr, "[app] pid=%d\n", pid);

    AXUIElementRef app = AXUIElementCreateApplication(pid);
    if (!app) {
        fprintf(stderr, "[error] cannot create AXUIElement for pid %d\n", pid);
        fflush(stderr);
        return;
    }

    // 获取 kAXWindowsAttribute
    CFTypeRef windowsVal = NULL;
    AXError err = AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &windowsVal);
    fprintf(stderr, "[kAXWindowsAttribute] AXError=%d\n", (int)err);

    if (!windowsVal || err != kAXErrorSuccess) {
        fprintf(stderr, "[windows] no windows attribute (NULL or error)\n");
        CFRelease(app);
        fflush(stderr);
        return;
    }

    if (CFGetTypeID(windowsVal) != CFArrayGetTypeID()) {
        fprintf(stderr, "[windows] unexpected type (not array), typeID=%lu\n", CFGetTypeID(windowsVal));
        CFRelease(windowsVal);
        CFRelease(app);
        fflush(stderr);
        return;
    }

    CFArrayRef windows = (CFArrayRef)windowsVal;
    CFIndex winCount = CFArrayGetCount(windows);
    fprintf(stderr, "[windows] count=%ld\n", (long)winCount);

    for (CFIndex i = 0; i < winCount; i++) {
        AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
        fprintf(stderr, "\n  --- window[%ld] ---\n", (long)i);

        print_ax_string_attr(win, kAXRoleAttribute, "role");
        print_ax_string_attr(win, kAXSubroleAttribute, "subrole");
        print_ax_string_attr(win, kAXTitleAttribute, "title");
        print_ax_point_attr(win, kAXPositionAttribute, "position");
        print_ax_size_attr(win, kAXSizeAttribute, "size");

        // minimized?
        CFTypeRef minimized = NULL;
        if (AXUIElementCopyAttributeValue(win, kAXMinimizedAttribute, &minimized) == kAXErrorSuccess && minimized) {
            fprintf(stderr, "    minimized: %s\n",
                    CFBooleanGetValue(minimized) ? "YES" : "NO");
            CFRelease(minimized);
        } else {
            fprintf(stderr, "    minimized: <unavailable>\n");
        }

        // focused?
        CFTypeRef focused = NULL;
        if (AXUIElementCopyAttributeValue(win, kAXFocusedAttribute, &focused) == kAXErrorSuccess && focused) {
            fprintf(stderr, "    focused: %s\n",
                    CFBooleanGetValue(focused) ? "YES" : "NO");
            CFRelease(focused);
        } else {
            fprintf(stderr, "    focused: <unavailable>\n");
        }

        // children count
        int childCount = count_children(win);
        fprintf(stderr, "    children: %d\n", childCount);

        // 打印前两层 children
        CFArrayRef children = NULL;
        AXUIElementCopyAttributeValue(win, kAXChildrenAttribute, (CFTypeRef *)&children);
        if (children) {
            CFIndex cc = CFArrayGetCount(children);
            for (CFIndex j = 0; j < cc && j < 20; j++) {
                AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, j);
                char roleBuf[64] = "?", descBuf[128] = "-";
                CFTypeRef r = NULL, d = NULL;
                AXUIElementCopyAttributeValue(child, kAXRoleAttribute, &r);
                AXUIElementCopyAttributeValue(child, kAXDescriptionAttribute, &d);
                if (r && CFGetTypeID(r) == CFStringGetTypeID())
                    CFStringGetCString((CFStringRef)r, roleBuf, sizeof(roleBuf), kCFStringEncodingUTF8);
                if (d && CFGetTypeID(d) == CFStringGetTypeID())
                    CFStringGetCString((CFStringRef)d, descBuf, sizeof(descBuf), kCFStringEncodingUTF8);
                int subCount = count_children(child);
                fprintf(stderr, "      child[%ld]: [%s] desc=%s children=%d\n",
                        (long)j, roleBuf, descBuf, subCount);
                if (r) CFRelease(r);
                if (d) CFRelease(d);
            }
            CFRelease(children);
        }

        // 打印 actions
        CFArrayRef actions = NULL;
        AXUIElementCopyActionNames(win, &actions);
        if (actions) {
            fprintf(stderr, "    actions:");
            for (CFIndex j = 0; j < CFArrayGetCount(actions); j++) {
                char actBuf[64] = "";
                CFStringGetCString(CFArrayGetValueAtIndex(actions, j), actBuf, sizeof(actBuf), kCFStringEncodingUTF8);
                fprintf(stderr, " %s", actBuf);
            }
            fprintf(stderr, "\n");
            CFRelease(actions);
        }
    }

    CFRelease(windowsVal);
    CFRelease(app);
    fprintf(stderr, "=== END WINDOW DIAGNOSTICS ===\n");
    fflush(stderr);
}

// 递归查找 AXScrollArea
static AXUIElementRef find_scroll_area_recursive(AXUIElementRef element, int depth) {
    if (depth > 15) return NULL;

    CFTypeRef roleVal = NULL;
    AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &roleVal);
    if (roleVal && CFGetTypeID(roleVal) == CFStringGetTypeID()) {
        char roleBuf[64];
        if (CFStringGetCString((CFStringRef)roleVal, roleBuf, sizeof(roleBuf), kCFStringEncodingUTF8)
            && strcmp(roleBuf, "AXScrollArea") == 0) {
            CFRelease(roleVal);
            CFRetain(element);
            return element;
        }
    }
    if (roleVal) CFRelease(roleVal);

    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (!children) return NULL;

    AXUIElementRef found = NULL;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex i = 0; i < count && !found; i++) {
        found = find_scroll_area_recursive((AXUIElementRef)CFArrayGetValueAtIndex(children, i), depth + 1);
    }
    CFRelease(children);
    return found;
}

// 递归查找拥有 AXScrollDownByPage action 的 AXGroup（SwiftUI 滚动容器）
static AXUIElementRef find_scrollable_group(AXUIElementRef element, int depth) {
    if (depth > 20) return NULL;

    // 检查当前节点是否有 AXScrollDownByPage action
    CFArrayRef actions = NULL;
    AXUIElementCopyActionNames(element, &actions);
    if (actions) {
        for (CFIndex i = 0; i < CFArrayGetCount(actions); i++) {
            CFStringRef action = CFArrayGetValueAtIndex(actions, i);
            if (CFStringCompare(action, CFSTR("AXScrollDownByPage"), 0) == kCFCompareEqualTo) {
                CFRelease(actions);
                CFRetain(element);
                return element;
            }
        }
        CFRelease(actions);
    }

    CFArrayRef children = NULL;
    AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, (CFTypeRef *)&children);
    if (!children) return NULL;

    AXUIElementRef found = NULL;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex i = 0; i < count && !found; i++) {
        found = find_scrollable_group((AXUIElementRef)CFArrayGetValueAtIndex(children, i), depth + 1);
    }
    CFRelease(children);
    return found;
}

// 滚动 Perplexity 内容区到底部
// 方法：通过 AX API 对 SwiftUI 滚动容器执行 AXScrollDownByPage
// 纯 AX 调用，不经过 window server，不需要屏幕坐标，headless 可用
// 返回 0=成功, -1=失败（scrollable group 未找到或滚动失败）
int ax_scroll_to_bottom(const char *bundle_id) {
    pid_t pid = get_pid_for_bundle(bundle_id);
    if (pid < 0) return -1;

    AXUIElementRef app = AXUIElementCreateApplication(pid);
    if (!app) return -1;

    // 从 kAXWindowsAttribute 拿第一个 window
    CFTypeRef windowsVal = NULL;
    AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &windowsVal);
    CFRelease(app);

    if (!windowsVal || CFGetTypeID(windowsVal) != CFArrayGetTypeID()) {
        if (windowsVal) CFRelease(windowsVal);
        return -1;
    }

    CFArrayRef windows = (CFArrayRef)windowsVal;
    if (CFArrayGetCount(windows) == 0) { CFRelease(windowsVal); return -1; }

    AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(windows, 0);

    // 找到带 AXScrollDownByPage 的滚动容器
    AXUIElementRef scrollable = find_scrollable_group(win, 0);
    CFRelease(windowsVal);

    if (!scrollable) {
        return -1;
    }

    AXError err = AXUIElementPerformAction(scrollable, CFSTR("AXScrollDownByPage"));
    CFRelease(scrollable);

    if (err == kAXErrorSuccess) {
        return 0;
    }
    fprintf(stderr, "[scroll] AXScrollDownByPage failed: %d\n", (int)err);
    fflush(stderr);
    return -1;
}

// ===== Display count =====

int ax_display_count(void) {
    @autoreleasepool {
        return (int)[NSScreen screens].count;
    }
}

// 检查 App 的 AX 树中是否存在指定 description 的按钮
// 返回 1=存在, 0=不存在
// 轻量级：找到后立即 CFRelease，不返回引用
int ax_has_button(const char *bundle_id, const char *description) {
    AXUIElementRef win = get_first_window(bundle_id);
    if (!win) return 0;
    NSString *desc = [NSString stringWithUTF8String:description];
    AXUIElementRef btn = find_button_recursive(win, desc, 0);
    CFRelease(win);
    if (btn) {
        CFRelease(btn);
        return 1;
    }
    return 0;
}

// 检查 App 主窗口直接子节点中是否存在 AXPopover（sources/model 选择器等）
// 只扫描一层深度，popover 始终作为窗口的直接子节点存在。
// 返回 1 表示有 popover，0 表示没有。
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

// 向前台 App 发送 Escape 键，用于关闭 popover / 弹出层。
// kVK_Escape = 53
void ax_press_escape(void) {
    CGEventRef keyDown = CGEventCreateKeyboardEvent(NULL, 53, true);
    CGEventRef keyUp   = CGEventCreateKeyboardEvent(NULL, 53, false);
    CGEventPost(kCGHIDEventTap, keyDown);
    CGEventPost(kCGHIDEventTap, keyUp);
    CFRelease(keyDown);
    CFRelease(keyUp);
}

