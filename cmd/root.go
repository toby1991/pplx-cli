package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/toby1991/pplx-cli/automation"
	"github.com/toby1991/pplx-cli/driver"
	"github.com/toby1991/pplx-cli/output"
)

// 全局 flags
var (
	flagModel   string
	flagSources string
	flagJSON    bool
	flagQuiet   bool
)

// rootCmd 是所有命令的父节点
var rootCmd = &cobra.Command{
	Use:   "pplx [query]",
	Short: "Perplexity AI 命令行搜索工具",
	Long: `pplx — 通过 Perplexity Desktop App 进行 AI 搜索

用法示例:
  pplx "量子计算是什么"                  一次性查询
  pplx --model "Claude Sonnet" "解释X"   指定模型
  echo "query" | pplx                    从 stdin 读取
  pplx "query" --json | jq '.answer'     JSON 输出
  pplx                                   进入交互式对话

注意：首次运行需要在「系统设置 → 隐私与安全性 → 辅助功能」中授权终端应用。`,
	RunE:          runSearch,
	Args:          cobra.ArbitraryArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute 是程序入口，由 main.go 调用
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagModel, "model", "",
		"模型名称前缀, 如: Sonar, Claude Sonnet, 最佳, GPT-5")
	rootCmd.PersistentFlags().StringVar(&flagSources, "sources", "",
		"内容来源（逗号分隔）: web,academic,finance,social")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false,
		"以 JSON 格式输出结果")
	rootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false,
		"只输出答案正文，不显示来源和元数据")

	// 注册子命令
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(modelsCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(dumpCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(sourcesCmd)
	rootCmd.AddCommand(apiCmd)
	rootCmd.AddCommand(setupCaffeinateCmd)
	rootCmd.AddCommand(removeCaffeinateCmd)
}

// runSearch 处理搜索逻辑：stdin / positional args / 交互式 REPL
func runSearch(cmd *cobra.Command, args []string) error {
	// 检查 Accessibility 权限
	if !automation.IsTrusted() {
		// 尝试自动打开系统设置的辅助功能页面
		_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Run()
		return fmt.Errorf(
			"缺少 Accessibility 权限\n已尝试打开「系统设置 → 隐私与安全性 → 辅助功能」\n请在列表中勾选运行本工具的终端应用，然后重新运行")
	}

	// 确定查询来源
	query, isREPL, err := resolveQuery(args)
	if err != nil {
		return err
	}

	if isREPL {
		return runREPL()
	}

	return doSearch(query)
}

// resolveQuery 按优先级确定查询字符串：
//  1. 命令行 positional args
//  2. stdin pipe
//  3. 无输入 → 进入 REPL
func resolveQuery(args []string) (query string, isREPL bool, err error) {
	// 1. positional args
	if len(args) > 0 {
		return strings.Join(args, " "), false, nil
	}

	// 2. 检测 stdin 是否为 pipe
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// stdin 是 pipe 或文件重定向
		var sb strings.Builder
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			sb.WriteString(scanner.Text())
			sb.WriteString("\n")
		}
		q := strings.TrimSpace(sb.String())
		if q == "" {
			return "", false, fmt.Errorf("stdin 为空")
		}
		return q, false, nil
	}

	// 3. 进入交互式 REPL
	return "", true, nil
}

// doSearch 执行一次搜索（含 model/sources 预设置）
func doSearch(query string) error {
	// 预设置 model
	if flagModel != "" {
		if err := driver.SetModel(flagModel); err != nil {
			return fmt.Errorf("set model failed: %w", err)
		}
	}

	// 预设置 sources
	if flagSources != "" {
		sources, err := parseSources(flagSources)
		if err != nil {
			return err
		}
		if err := driver.SetSources(sources); err != nil {
			return fmt.Errorf("set sources failed: %w", err)
		}
	}

	// 显示 spinner
	spin := output.NewSpinner("正在搜索 Perplexity...")
	spin.Start()

	result, err := driver.Search(query)
	spin.Stop()

	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	output.PrintResult(result, flagJSON, flagQuiet)
	return nil
}

// runREPL 交互式多轮对话模式
func runREPL() error {
	if output.IsTerminal {
		fmt.Println("Perplexity AI 交互模式  (输入 'exit' 或 Ctrl+C 退出)")
		fmt.Println()
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		if output.IsTerminal {
			fmt.Print(output.Prompt())
		}

		if !scanner.Scan() {
			break
		}

		query := strings.TrimSpace(scanner.Text())
		if query == "" {
			continue
		}
		if query == "exit" || query == "quit" || query == "q" {
			break
		}

		if err := doSearch(query); err != nil {
			output.PrintError(err)
		}

		if output.IsTerminal {
			fmt.Println()
		}
	}

	return nil
}

// parseSources 解析 "--sources web,academic" 格式的字符串
func parseSources(s string) (map[string]bool, error) {
	valid := map[string]bool{"web": false, "academic": false, "finance": false, "social": false}
	result := map[string]bool{}

	parts := strings.Split(s, ",")
	for _, p := range parts {
		key := strings.TrimSpace(strings.ToLower(p))
		if _, ok := valid[key]; !ok {
			return nil, fmt.Errorf("unknown source %q, valid: web,academic,finance,social", key)
		}
		result[key] = true
	}
	return result, nil
}
