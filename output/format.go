package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/toby1991/pplx-cli/driver"
)

// IsTerminal 标记当前 stdout 是否为终端（非 pipe）
var IsTerminal = isatty.IsTerminal(os.Stdout.Fd())

// ─────────────────────────────────────────────
// 样式定义（lipgloss）
// ─────────────────────────────────────────────

var (
	styleDivider = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238")).
			Bold(false)

	styleAnswer = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255"))

	styleCitationIndex = lipgloss.NewStyle().
				Foreground(lipgloss.Color("33")).
				Bold(true)

	styleCitationTitle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("250"))

	styleCitationURL = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240"))

	styleMeta = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)

	styleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	stylePrompt = lipgloss.NewStyle().
			Foreground(lipgloss.Color("33")).
			Bold(true)
)

// ─────────────────────────────────────────────
// Spinner
// ─────────────────────────────────────────────

// Spinner 在 TTY 模式下显示旋转等待动画
type Spinner struct {
	msg  string
	done chan struct{}
	once sync.Once
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewSpinner 创建一个新的 Spinner（只在 TTY 模式下有效果）
func NewSpinner(msg string) *Spinner {
	return &Spinner{
		msg:  msg,
		done: make(chan struct{}),
	}
}

// Start 开始显示 spinner（非阻塞）
func (s *Spinner) Start() {
	if !IsTerminal {
		return
	}
	go func() {
		i := 0
		for {
			select {
			case <-s.done:
				// 清除 spinner 行
				fmt.Fprintf(os.Stderr, "\r%-60s\r", "")
				return
			default:
				frame := spinnerFrames[i%len(spinnerFrames)]
				fmt.Fprintf(os.Stderr, "\r%s %s", frame, s.msg)
				time.Sleep(80 * time.Millisecond)
				i++
			}
		}
	}()
}

// Stop 停止 spinner
func (s *Spinner) Stop() {
	s.once.Do(func() {
		close(s.done)
		time.Sleep(100 * time.Millisecond) // 等待 goroutine 清除行
	})
}

// ─────────────────────────────────────────────
// 输出结果
// ─────────────────────────────────────────────

// PrintResult 根据当前环境决定输出格式
//   - jsonMode: 输出 JSON
//   - quiet: 只输出答案正文，无来源和元数据
func PrintResult(result *driver.SearchResult, jsonMode bool, quiet bool) {
	if jsonMode {
		printJSON(result)
		return
	}

	if IsTerminal {
		printRich(result, quiet)
	} else {
		printPlain(result, quiet)
	}
}

// printJSON 输出 JSON 格式
func printJSON(result *driver.SearchResult) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
}

// printRich 终端富文本输出（带颜色、分隔线）
func printRich(result *driver.SearchResult, quiet bool) {
	divider := strings.Repeat("─", 60)

	fmt.Println()
	fmt.Println(styleDivider.Render(divider))
	fmt.Println()
	fmt.Println(styleAnswer.Render(result.Answer))
	fmt.Println()

	if !quiet && len(result.Citations) > 0 {
		fmt.Println(styleDivider.Render("来源："))
		for _, c := range result.Citations {
			fmt.Printf("  %s %s\n",
				styleCitationIndex.Render(fmt.Sprintf("[%d]", c.Index)),
				styleCitationTitle.Render(c.Title),
			)
			if c.URL != "" {
				fmt.Printf("      %s\n", styleCitationURL.Render(c.URL))
			}
		}
		fmt.Println()
	}

	if !quiet && (result.Mode != "" || result.Model != "") {
		fmt.Println(styleMeta.Render(fmt.Sprintf("模式: %s  模型: %s", result.Mode, result.Model)))
	}

	fmt.Println(styleDivider.Render(divider))
}

// printPlain pipe 模式纯文本输出（无颜色）
func printPlain(result *driver.SearchResult, quiet bool) {
	fmt.Println(result.Answer)

	if !quiet && len(result.Citations) > 0 {
		fmt.Println()
		fmt.Println("来源：")
		for _, c := range result.Citations {
			if c.URL != "" {
				fmt.Printf("[%d] %s %s\n", c.Index, c.Title, c.URL)
			} else {
				fmt.Printf("[%d] %s\n", c.Index, c.Title)
			}
		}
	}
}

// PrintError 输出错误信息到 stderr
func PrintError(err error) {
	if IsTerminal {
		fmt.Fprintln(os.Stderr, styleError.Render("错误："+err.Error()))
	} else {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
	}
}

// PrintStatus 输出当前状态（mode + model）
func PrintStatus(mode, model string) {
	if IsTerminal {
		fmt.Printf("%s %s\n", styleMeta.Render("模式:"), styleAnswer.Render(mode))
		fmt.Printf("%s %s\n", styleMeta.Render("模型:"), styleAnswer.Render(model))
	} else {
		fmt.Printf("mode: %s\nmodel: %s\n", mode, model)
	}
}

// Prompt 输出交互式 REPL 提示符
func Prompt() string {
	if IsTerminal {
		return stylePrompt.Render("pplx> ")
	}
	return "pplx> "
}
