package driver

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/toby1991/pplx-cli/automation"
)

const (
	BundleID = "ai.perplexity.mac"

	// UserDefaults 键
	defaultsKeyMode  = "currentSearchModeID"
	defaultsKeyModel = "currentSearchModeModelID_v2"

	// 搜索等待超时（deep research 可能需要 3-4 分钟）
	searchTimeout = 300 * time.Second
	searchPoll    = 500 * time.Millisecond

	// 操作后等待 UI 响应
	uiDelay      = 300 * time.Millisecond
	popoverDelay = 400 * time.Millisecond

	// 窗口等待超时（headless 场景窗口可能延迟出现）
	windowTimeout = 10 * time.Second
	windowPoll    = 500 * time.Millisecond
)

// EnsureAppRunning 确保 Perplexity Desktop App 正在运行。
// 如果未运行，用 `open -b` 启动并等待 AX 可访问。
func EnsureAppRunning() error {
	// 先试激活，如果成功说明已在运行
	if err := automation.ActivateApp(BundleID); err == nil {
		return nil
	}

	// 未运行，用 open -b 启动
	if err := exec.Command("open", "-b", BundleID).Run(); err != nil {
		return fmt.Errorf("failed to launch Perplexity: %w", err)
	}

	// 轮询等待 app 可通过 AX 访问
	deadline := time.Now().Add(windowTimeout)
	for time.Now().Before(deadline) {
		if err := automation.ActivateApp(BundleID); err == nil {
			// app 可访问了，再等窗口出现
			break
		}
		time.Sleep(windowPoll)
	}

	return nil
}

// ensureWindow 激活 App 并等待其窗口出现
// headless 场景下 AXWindow 可能不存在或延迟出现，此函数保证后续 AX 操作有窗口可用
func ensureWindow() error {
	if err := EnsureAppRunning(); err != nil {
		return err
	}
	if err := automation.WaitForWindow(BundleID, windowTimeout, windowPoll); err != nil {
		return fmt.Errorf("Perplexity window not available: %w (is a display connected or virtual display configured?)", err)
	}
	return nil
}

// NavigateToHome 确保 Perplexity 处于首页状态。
// 如果当前在搜索结果页（存在 chevron-left 返回按钮），点击返回到首页。
// 首页的判定依据：world 按钮（来源选择器）只在首页存在。
// 多次点击以处理可能的多层导航或动画延迟。
func NavigateToHome() error {
	if err := ensureWindow(); err != nil {
		return err
	}

	// 如果 sources/model popover 处于打开状态，world 按钮在 AX 树中不可见，
	// HasButton("world") 会返回 false，导致 NavigateToHome 误判为"不在首页"并死循环。
	// 检测到 popover 时发 Escape 键关闭它，再继续首页检测。
	if automation.HasPopover(BundleID) {
		fmt.Fprintf(os.Stderr, "[nav] popover open, pressing Escape to dismiss\n")
		automation.PressEscape()
		time.Sleep(uiDelay)
	}

	const maxAttempts = 5

	for i := 0; i < maxAttempts; i++ {
		// 首页标志：world 按钮只在首页存在
		if automation.HasButton(BundleID, "world") {
			fmt.Fprintf(os.Stderr, "[nav] on home page (world button found)\n")
			return nil
		}

		// 不在首页：尝试点击返回按钮
		backBtn, err := automation.FindButton(BundleID, "chevron-left")
		if err != nil {
			// 既没有 world 也没有 chevron-left，可能页面在加载中，等一下
			fmt.Fprintf(os.Stderr, "[nav] attempt %d: neither world nor chevron-left found, waiting...\n", i+1)
			time.Sleep(uiDelay)
			continue
		}

		fmt.Fprintf(os.Stderr, "[nav] attempt %d: on results page, clicking back...\n", i+1)
		if err := automation.Click(backBtn); err != nil {
			backBtn.Release()
			return fmt.Errorf("failed to click back button: %w", err)
		}
		backBtn.Release()

		// 等待页面切换动画
		time.Sleep(500 * time.Millisecond)
	}

	// 最终检查
	if automation.HasButton(BundleID, "world") {
		return nil
	}

	return fmt.Errorf("failed to navigate to home page after %d attempts (world button not found)", maxAttempts)
}

// SearchResult 搜索结果
type SearchResult struct {
	Answer    string     `json:"answer"`
	Citations []Citation `json:"citations"`
	Mode      string     `json:"mode"`
	Model     string     `json:"model"`
}

// Citation 来源引用
type Citation struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// sourceCheckboxMap 来源键 → AXCheckBox description 前缀
var sourceCheckboxMap = map[string]string{
	"web":      "网页",
	"academic": "学术",
	"finance":  "财务",
	"social":   "社交",
}

// Search 执行搜索，等待结果并返回 SearchResult
//
// 流程：
//  1. 保存当前剪贴板内容
//  2. open perplexity-app://search?q=<encoded>
//  3. 激活 App，等待 copy 按钮出现（表示回答完成）
//  4. 点击 copy 按钮
//  5. 读取剪贴板内容
//  6. 恢复原始剪贴板
//  7. 解析答案和来源
func Search(query string) (*SearchResult, error) {
	// 读取当前状态
	mode, _ := getCurrentMode()
	model, _ := getCurrentModel()

	// 保存剪贴板原始内容
	origClipboard, _ := automation.ReadClipboard()

	// 构造 URL scheme，打开 Perplexity 并填入查询
	encoded := url.QueryEscape(query)
	openURL := fmt.Sprintf("perplexity-app://search?q=%s", encoded)
	if err := exec.Command("open", openURL).Run(); err != nil {
		return nil, fmt.Errorf("failed to open Perplexity: %w", err)
	}

	// 激活 App 并确保窗口可用（headless 场景需要等待窗口出现）
	// 等待 2s：让 Perplexity 完成 URL 处理、清除旧回答，再开始跟踪内容长度
	time.Sleep(2 * time.Second)
	if err := ensureWindow(); err != nil {
		return nil, err
	}

	// 等待 copy 按钮出现（表示回答已完成）
	// 每次轮询前先滚动到底部，确保 copy 按钮进入视口（AX 树仅包含可见元素）
	copyBtn, err := waitForButtonWithScroll(BundleID, "copy", searchTimeout, searchPoll)
	if err != nil {
		return nil, fmt.Errorf("search timed out (Perplexity didn't finish): %w", err)
	}
	defer copyBtn.Release()

	// 点击 copy 按钮，带剪贴板验证和重试
	// 已知问题：copy 按钮点击偶尔不更新剪贴板，需要重试
	const maxCopyRetries = 3
	prevChangeCount := automation.ClipboardChangeCount()
	var content string

	for attempt := 1; attempt <= maxCopyRetries; attempt++ {
		if err := automation.Click(copyBtn); err != nil {
			return nil, fmt.Errorf("failed to click copy button: %w", err)
		}

		// 等待剪贴板写入
		time.Sleep(300 * time.Millisecond)

		newChangeCount := automation.ClipboardChangeCount()
		if newChangeCount != prevChangeCount {
			// 剪贴板已更新
			content, err = automation.ReadClipboard()
			if err == nil && content != "" {
				fmt.Fprintf(os.Stderr, "[copy] clipboard updated on attempt %d\n", attempt)
				break
			}
		}

		if attempt < maxCopyRetries {
			fmt.Fprintf(os.Stderr, "[copy] clipboard not updated (attempt %d/%d), retrying...\n",
				attempt, maxCopyRetries)
			time.Sleep(200 * time.Millisecond)
		}
	}

	// 如果 copy 按钮始终未更新剪贴板，回退到直接读取 AXTextArea
	if content == "" {
		fmt.Fprintf(os.Stderr, "[copy] clipboard fallback: reading AXTextArea values directly\n")
		textContent, textErr := automation.ReadAllTextAreaValues(BundleID)
		if textErr != nil || textContent == "" {
			return nil, fmt.Errorf("failed to get answer: copy button didn't update clipboard and AXTextArea fallback failed")
		}
		content = textContent
	}

	// 恢复原始剪贴板（异步，不影响返回结果）
	if origClipboard != "" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			automation.WriteClipboard(origClipboard)
		}()
	}

	// 解析结果
	answer, citations := parseClipboardContent(content)

	return &SearchResult{
		Answer:    answer,
		Citations: citations,
		Mode:      mode,
		Model:     model,
	}, nil
}

// SetModel 通过模型名称前缀切换模型
// 流程：确保窗口 → 点击 model 按钮 → 等待 popover → 按前缀查找并点击
func SetModel(modelName string) error {
	// 确保窗口可用
	if err := ensureWindow(); err != nil {
		return err
	}

	// 点击 model 按钮打开 popover
	modelBtn, err := automation.FindButton(BundleID, "model")
	if err != nil {
		return fmt.Errorf("model button not found (is Perplexity open?): %w", err)
	}
	defer modelBtn.Release()

	if err := automation.Click(modelBtn); err != nil {
		return fmt.Errorf("failed to open model popover: %w", err)
	}

	// 等待 popover 动画
	time.Sleep(popoverDelay)

	// 在 popover 中按前缀查找模型按钮
	targetBtn, err := automation.FindButtonPrefix(BundleID, modelName)
	if err != nil {
		return fmt.Errorf("model %q not found in popover: %w", modelName, err)
	}
	defer targetBtn.Release()

	if err := automation.Click(targetBtn); err != nil {
		return fmt.Errorf("failed to select model: %w", err)
	}

	time.Sleep(uiDelay)
	return nil
}

// SetSources 设置内容来源
// sources: map[string]bool，key: "web"|"academic"|"finance"|"social"，value: 是否启用
// 流程：确保窗口 → 点击 world 按钮 → 等待 popover → 读取各 checkbox 状态 → 按需点击切换
func SetSources(sources map[string]bool) error {
	// 确保窗口可用
	if err := ensureWindow(); err != nil {
		return err
	}

	// 点击 world 按钮打开来源选择 popover
	worldBtn, err := automation.FindButton(BundleID, "world")
	if err != nil {
		return fmt.Errorf("world button not found (is Perplexity open?): %w", err)
	}
	defer worldBtn.Release()

	if err := automation.Click(worldBtn); err != nil {
		return fmt.Errorf("failed to open sources popover: %w", err)
	}

	time.Sleep(popoverDelay)

	// 遍历要设置的来源
	for key, wantEnabled := range sources {
		prefix, ok := sourceCheckboxMap[key]
		if !ok {
			return fmt.Errorf("unknown source %q, valid: web, academic, finance, social", key)
		}

		cb, err := automation.FindCheckboxPrefix(BundleID, prefix)
		if err != nil {
			return fmt.Errorf("source checkbox %q not found: %w", prefix, err)
		}

		currentVal, err := automation.GetCheckboxValue(cb)
		if err != nil {
			cb.Release()
			return fmt.Errorf("failed to read checkbox state: %w", err)
		}

		// 只在需要改变时点击
		if currentVal != wantEnabled {
			if err := automation.Click(cb); err != nil {
				cb.Release()
				return fmt.Errorf("failed to toggle source %q: %w", key, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		cb.Release()
	}

	// 点击其他地方关闭 popover
	time.Sleep(uiDelay)
	return nil
}

// UploadFile 上传附件到当前对话
// 流程：点击 attach 按钮 → Cmd+Shift+G → 输入路径 → Enter
func UploadFile(path string) error {
	attachBtn, err := automation.FindButton(BundleID, "attach")
	if err != nil {
		return fmt.Errorf("attach button not found: %w", err)
	}
	defer attachBtn.Release()

	if err := automation.Click(attachBtn); err != nil {
		return fmt.Errorf("failed to click attach button: %w", err)
	}

	time.Sleep(500 * time.Millisecond)

	if err := automation.OpenFileDialogAndInput(path); err != nil {
		return fmt.Errorf("failed to input file path: %w", err)
	}

	return nil
}

// waitForButtonWithScroll 等待内容生成完成后再寻找指定按钮。
//
// 生成完成判定策略（Fix 12: 基于 UI 状态检测，替代固定超时）：
//
//	Perplexity 在生成过程中（包括 research/deep search 阶段），AX 树中存在一个
//	desc="checkbox" 的 AXButton（疑似 "stop generating" 控件）。生成完成后该按钮消失，
//	取而代之的是 bookmark、分享此主题、microphone 等按钮。
//
//	新逻辑：
//	1. 每轮循环：检测 checkbox 按钮 → 读取 contentLen → ScrollToBottom
//	2. 如果 checkbox 按钮存在 → Perplexity 正在工作（research/generating），
//	   即使 contentLen=0 也不会误判为卡死
//	3. 如果 checkbox 按钮消失且 contentLen>0 且稳定 → 生成完成 → 查找 copy 按钮
//	4. 如果 checkbox 按钮消失且 contentLen=0 持续 30s → 真正卡死 → 报错
//
//	相比旧逻辑（固定 90s researchTimeout）：
//	- 不会在 deep research 阶段误超时（即使 research 持续 5 分钟）
//	- 不会在内容为 0 时误判稳定（checkbox 按钮存在 = 还没完成）
//	- 生成完成的检测更准确（UI 状态 vs 内容长度猜测）
const (
	stableRequired = 3 // 连续 N 次相同长度 ≈ 内容稳定（3×800ms ≈ 2.4s）
	minContentLen  = 1 // 只要有内容就开始跟踪；research 阶段为 0 不会触发

	// generatingIndicator: Perplexity 生成过程中存在的 AXButton description。
	// 生成完成后消失。用于准确判断 Perplexity 是否仍在工作。
	generatingIndicator = "checkbox"

	// noActivityTimeout: checkbox 按钮已消失且 content=0 的最长容忍时间。
	// 超过此时间认为 Perplexity 无响应。比原来的 90s researchTimeout 短，
	// 因为这个超时只在"没有任何活动迹象"时才触发。
	noActivityTimeout = 30 * time.Second
)

func waitForButtonWithScroll(bundleID, desc string, timeout, poll time.Duration) (*automation.Element, error) {
	deadline := time.Now().Add(timeout)
	prevLen := 0
	stableCount := 0
	contentStable := false
	var noActivitySince time.Time // 追踪"无活动"（checkbox 消失 + content=0）的起始时间
	iteration := 0

	for time.Now().Before(deadline) {
		time.Sleep(800 * time.Millisecond)
		iteration++

		// 读取两个关键信号
		currentLen := automation.GetContentLength(bundleID)
		generating := automation.HasButton(bundleID, generatingIndicator)

		fmt.Fprintf(os.Stderr, "[wait] #%d len=%d prev=%d stable=%d/%d generating=%v\n",
			iteration, currentLen, prevLen, stableCount, stableRequired, generating)

		// ─── 无活动检测 ───
		// 只有在 checkbox 按钮消失（不在生成）且 content=0 时才开始计时
		if !generating && currentLen == 0 {
			if noActivitySince.IsZero() {
				noActivitySince = time.Now()
				fmt.Fprintf(os.Stderr, "[wait] no activity detected (generating=false, content=0), starting timer\n")
			} else if time.Since(noActivitySince) > noActivityTimeout {
				return nil, fmt.Errorf("no activity for %v: Perplexity stopped without producing content (try again)", noActivityTimeout)
			}
		} else {
			// 有活动迹象（正在生成 或 有内容），重置计时器
			if !noActivitySince.IsZero() {
				fmt.Fprintf(os.Stderr, "[wait] activity resumed (generating=%v, len=%d)\n", generating, currentLen)
				noActivitySince = time.Time{}
			}
		}

		// ─── 正在生成中：只跟踪，不做稳定性判断 ───
		if generating {
			// Perplexity 正在工作，重置稳定性（即使内容暂时不变也不算"稳定"）
			stableCount = 0
			contentStable = false
			prevLen = currentLen
			continue
		}

		// ─── checkbox 消失了 = 生成可能已完成，开始稳定性检测 ───
		if currentLen >= minContentLen && currentLen == prevLen {
			stableCount++
		} else {
			stableCount = 0
		}
		prevLen = currentLen

		// 如果之前认为稳定但内容又变了，重置
		if contentStable && stableCount == 0 {
			contentStable = false
			fmt.Fprintf(os.Stderr, "[wait] content changed after stability, resetting\n")
		}

		if !contentStable && stableCount >= stableRequired {
			contentStable = true
			fmt.Fprintf(os.Stderr, "[wait] content stable at %d chars (generating=false), scrolling to find button...\n", currentLen)
		}

		// ─── 内容稳定且不在生成：连续滚动找 copy 按钮 ───
		if contentStable {
			const (
				maxTotalAttempts  = 80  // 总尝试次数上限
				maxSuccessScrolls = 50  // 成功滚动次数上限（50 页足以覆盖任何长回答）
				maxConsecFails    = 15  // 连续 N 次 scrollable group 不可用 → 早期 abort
				scrollOkDelay     = 300 // 成功滚动后等待 ms（等 AX tree 更新视口）
				scrollFailDelay   = 500 // 失败后等待 ms（等 AX tree 重建）
			)
			successScrolls := 0
			consecFails := 0

			for attempt := 0; attempt < maxTotalAttempts && successScrolls < maxSuccessScrolls; attempt++ {
				scrolled := automation.ScrollToBottom(bundleID)
				if scrolled {
					successScrolls++
					consecFails = 0
					time.Sleep(time.Duration(scrollOkDelay) * time.Millisecond)

					// 滚动成功后检查按钮（视口已变化）
					elem, err := automation.FindButton(bundleID, desc)
					if err == nil {
						fmt.Fprintf(os.Stderr, "[wait] found button %q after %d successful scroll(s) (%d total attempts)\n",
							desc, successScrolls, attempt+1)
						return elem, nil
					}
				} else {
					consecFails++
					if consecFails >= maxConsecFails {
						fmt.Fprintf(os.Stderr, "[wait] scrollable group unavailable for %d consecutive attempts, aborting scroll phase\n", consecFails)
						break
					}
					time.Sleep(time.Duration(scrollFailDelay) * time.Millisecond)
				}
			}

			fmt.Fprintf(os.Stderr, "[wait] button not found after %d successful scrolls (%d fails), resetting stability\n",
				successScrolls, consecFails)
			contentStable = false
			stableCount = 0
		}
	}
	return nil, fmt.Errorf("timed out waiting for button %q (waited %v)", desc, timeout)
}

// GetStatus 获取当前模式和模型（从 UserDefaults 读取）
func GetStatus() (mode, model string, err error) {
	mode, err = getCurrentMode()
	if err != nil {
		mode = "unknown"
		err = nil
	}
	model, err = getCurrentModel()
	if err != nil {
		model = "unknown"
		err = nil
	}
	return mode, model, nil
}

// getCurrentMode 从 UserDefaults 读取当前搜索模式
func getCurrentMode() (string, error) {
	return automation.ReadDefaults(BundleID, defaultsKeyMode)
}

// getCurrentModel 从 UserDefaults 读取当前模型
func getCurrentModel() (string, error) {
	return automation.ReadDefaults(BundleID, defaultsKeyModel)
}

// parseClipboardContent 解析剪贴板内容
//
// Perplexity copy 的格式（中文界面）：
//
//	<答案正文>
//
//	来源
//	[1] Title
//	URL
//	[2] Title
//	URL
//	...
//
// 英文界面格式：
//
//	<答案正文>
//
//	Sources
//	[1] Title
//	URL
//	...
func parseClipboardContent(content string) (answer string, citations []Citation) {
	// 分割答案和来源部分
	// 先尝试中文分隔符
	separators := []string{"\n\n来源\n", "\n\nSources\n", "\n\n来源：\n"}
	sourcesPart := ""
	answerPart := content

	for _, sep := range separators {
		if idx := strings.Index(content, sep); idx != -1 {
			answerPart = content[:idx]
			sourcesPart = content[idx+len(sep):]
			break
		}
	}

	answer = strings.TrimSpace(answerPart)

	if sourcesPart == "" {
		return answer, nil
	}

	// 解析来源列表
	// 格式：[N] Title\nURL\n 或 [N] Title URL（单行）
	lines := strings.Split(sourcesPart, "\n")
	var current *Citation

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 检测 [N] 开头
		if strings.HasPrefix(line, "[") {
			// 保存上一个
			if current != nil {
				citations = append(citations, *current)
			}

			// 解析 [N] Title 或 [N] Title URL
			var idx int
			var rest string
			if n, err := fmt.Sscanf(line, "[%d]", &idx); n == 1 && err == nil {
				// 找到 "] " 后的内容
				bracketEnd := strings.Index(line, "] ")
				if bracketEnd != -1 {
					rest = strings.TrimSpace(line[bracketEnd+2:])
				}
			}

			current = &Citation{Index: idx}

			// 判断 rest 是否包含 URL（以 http 开头）
			if strings.HasPrefix(rest, "http") {
				current.URL = rest
			} else {
				// 检查 rest 末尾是否有 URL
				lastSpace := strings.LastIndex(rest, " ")
				if lastSpace != -1 && strings.HasPrefix(rest[lastSpace+1:], "http") {
					current.Title = strings.TrimSpace(rest[:lastSpace])
					current.URL = rest[lastSpace+1:]
				} else {
					current.Title = rest
				}
			}
		} else if current != nil {
			// 这一行可能是 URL 或 Title 的续行
			if strings.HasPrefix(line, "http") {
				if current.URL == "" {
					current.URL = line
				}
			} else if current.Title == "" {
				current.Title = line
			}
		}
	}

	// 保存最后一个
	if current != nil {
		citations = append(citations, *current)
	}

	return answer, citations
}
