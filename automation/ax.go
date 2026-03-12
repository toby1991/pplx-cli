package automation

// #cgo CFLAGS: -x objective-c -fmodules -fobjc-arc
// #cgo LDFLAGS: -framework Cocoa -framework ApplicationServices -framework CoreGraphics
// #include "ax.h"
// #include <stdlib.h>
import "C"
import (
	"fmt"
	"time"
	"unsafe"
)

// Element 封装 AXUIElementRef，调用 Release() 后不能再使用
type Element struct {
	ref unsafe.Pointer
}

// Release 释放底层 AXUIElement 引用
func (e *Element) Release() {
	if e != nil && e.ref != nil {
		C.ax_release_element(e.ref)
		e.ref = nil
	}
}

// IsTrusted 检查当前进程是否拥有 Accessibility 权限
func IsTrusted() bool {
	return C.ax_is_trusted() == 1
}

// ActivateApp 将指定 Bundle ID 的 App 置于前台
func ActivateApp(bundleID string) error {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	if C.ax_activate_app(cBundleID) != 0 {
		return fmt.Errorf("failed to activate app: %s (is it running?)", bundleID)
	}
	return nil
}

// FindButton 在 App 中查找 description 精确匹配的按钮
func FindButton(bundleID, description string) (*Element, error) {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	cDesc := C.CString(description)
	defer C.free(unsafe.Pointer(cDesc))

	ref := C.ax_find_button(cBundleID, cDesc)
	if ref == nil {
		return nil, fmt.Errorf("button not found: %q", description)
	}
	return &Element{ref: ref}, nil
}

// FindButtonPrefix 在 App 中查找 description 以 prefix 开头的按钮
func FindButtonPrefix(bundleID, prefix string) (*Element, error) {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	cPrefix := C.CString(prefix)
	defer C.free(unsafe.Pointer(cPrefix))

	ref := C.ax_find_button_prefix(cBundleID, cPrefix)
	if ref == nil {
		return nil, fmt.Errorf("button with prefix not found: %q", prefix)
	}
	return &Element{ref: ref}, nil
}

// FindCheckboxPrefix 在 App 中查找 description 以 prefix 开头的 AXCheckBox
func FindCheckboxPrefix(bundleID, prefix string) (*Element, error) {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	cPrefix := C.CString(prefix)
	defer C.free(unsafe.Pointer(cPrefix))

	ref := C.ax_find_checkbox_prefix(cBundleID, cPrefix)
	if ref == nil {
		return nil, fmt.Errorf("checkbox with prefix not found: %q", prefix)
	}
	return &Element{ref: ref}, nil
}

// GetCheckboxValue 获取 checkbox 的当前值（true = 勾选）
func GetCheckboxValue(elem *Element) (bool, error) {
	if elem == nil || elem.ref == nil {
		return false, fmt.Errorf("nil element")
	}
	v := C.ax_get_checkbox_value(elem.ref)
	if v < 0 {
		return false, fmt.Errorf("failed to read checkbox value")
	}
	return v == 1, nil
}

// Click 模拟点击元素
func Click(elem *Element) error {
	if elem == nil || elem.ref == nil {
		return fmt.Errorf("nil element")
	}
	if C.ax_click(elem.ref) != 0 {
		return fmt.Errorf("click failed")
	}
	return nil
}

// ReadClipboard 读取剪贴板字符串内容
func ReadClipboard() (string, error) {
	cStr := C.ax_read_clipboard()
	if cStr == nil {
		return "", fmt.Errorf("clipboard is empty or not a string")
	}
	defer C.free(unsafe.Pointer(cStr))
	return C.GoString(cStr), nil
}

// WriteClipboard 将字符串写入剪贴板
func WriteClipboard(text string) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	C.ax_write_clipboard(cText)
}

// ClipboardChangeCount 返回剪贴板的 changeCount（每次内容变化时单调递增）
// 用于检测 copy 操作是否真正更新了剪贴板
func ClipboardChangeCount() int64 {
	return int64(C.ax_clipboard_change_count())
}

// ReadAllTextAreaValues 收集 App 窗口中所有 AXTextArea 的文本值
// 用于 copy 按钮未更新剪贴板时的 fallback
func ReadAllTextAreaValues(bundleID string) (string, error) {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	cStr := C.ax_read_all_textarea_values(cBundleID)
	if cStr == nil {
		return "", fmt.Errorf("no AXTextArea values found")
	}
	defer C.free(unsafe.Pointer(cStr))
	return C.GoString(cStr), nil
}

// WaitForButton 等待指定 description 的按钮出现，轮询直到超时
func WaitForButton(bundleID, description string, timeout, poll time.Duration) (*Element, error) {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	cDesc := C.CString(description)
	defer C.free(unsafe.Pointer(cDesc))

	timeoutMs := C.int(timeout.Milliseconds())
	pollMs := C.int(poll.Milliseconds())

	ref := C.ax_wait_for_button(cBundleID, cDesc, timeoutMs, pollMs)
	if ref == nil {
		return nil, fmt.Errorf("timed out waiting for button %q (waited %v)", description, timeout)
	}
	return &Element{ref: ref}, nil
}

// OpenFileDialogAndInput 在文件对话框中输入路径（用于附件上传）
func OpenFileDialogAndInput(path string) error {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	if C.ax_open_file_dialog_and_input(cPath) != 0 {
		return fmt.Errorf("failed to input path in file dialog")
	}
	return nil
}

// DumpElements 打印 App 当前所有 AX 元素（诊断用）
func DumpElements(bundleID string) {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	C.ax_dump_elements(cBundleID)
}

// DumpWindowInfo 打印窗口详细诊断信息（窗口数量、属性、children 结构）
func DumpWindowInfo(bundleID string) {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	C.ax_dump_window_info(cBundleID)
}

// ScrollToBottom 向 App 发送 AXScrollDownByPage，将滚动区域向下滚动一页
// 返回 true 表示滚动成功，false 表示 scrollable group 未找到或滚动失败
func ScrollToBottom(bundleID string) bool {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	return C.ax_scroll_to_bottom(cBundleID) == 0
}

// GetContentLength 返回 App 中所有 AXTextArea 字符总数。
// 生成过程中数值递增；停止增长表示生成完成。
func GetContentLength(bundleID string) int {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	return int(C.ax_get_content_length(cBundleID))
}

// ReadDefaults 读取 macOS UserDefaults 中的字符串值
func ReadDefaults(appID, key string) (string, error) {
	cAppID := C.CString(appID)
	defer C.free(unsafe.Pointer(cAppID))
	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))

	cVal := C.ax_read_defaults(cAppID, cKey)
	if cVal == nil {
		return "", fmt.Errorf("key %q not found in defaults for %q", key, appID)
	}
	defer C.free(unsafe.Pointer(cVal))
	return C.GoString(cVal), nil
}

// WaitForWindow 等待 App 的 AXWindow 出现（headless 场景下窗口可能延迟出现）
func WaitForWindow(bundleID string, timeout, poll time.Duration) error {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	timeoutMs := C.int(timeout.Milliseconds())
	pollMs := C.int(poll.Milliseconds())

	if C.ax_wait_for_window(cBundleID, timeoutMs, pollMs) != 0 {
		return fmt.Errorf("timed out waiting for window of %s (waited %v)", bundleID, timeout)
	}
	return nil
}

// DisplayCount 返回当前连接的显示器数量（NSScreen count）
// 在 headless 场景下，如果没有物理显示器或虚拟显示器，返回 0
func DisplayCount() int {
	return int(C.ax_display_count())
}

// HasButton 检查 App 的 AX 树中是否存在指定 description 的按钮
// 轻量级检测，不返回元素引用，不需要调用 Release
func HasButton(bundleID, description string) bool {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	cDesc := C.CString(description)
	defer C.free(unsafe.Pointer(cDesc))
	return C.ax_has_button(cBundleID, cDesc) == 1
}

// HasPopover 检查 App 主窗口直接子节点中是否存在 AXPopover（sources/model 选择器等）
// 轻量级检测，不返回元素引用
func HasPopover(bundleID string) bool {
	cBundleID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cBundleID))
	return C.ax_has_popover(cBundleID) == 1
}

// PressEscape 向前台 App 发送 Escape 键，用于关闭 popover / 弹出层
func PressEscape() {
	C.ax_press_escape()
}
