package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/toby1991/pplx-cli/driver"
	"github.com/toby1991/pplx-cli/output"
)

var flagAPIModel string

var apiCmd = &cobra.Command{
	Use:   "api [query]",
	Short: "通过 Perplexity API 搜索（需要 PERPLEXITY_API_KEY）",
	Long: `通过 Perplexity Sonar REST API 进行搜索，而非 Desktop App UI 自动化。

需要设置环境变量 PERPLEXITY_API_KEY。
免费额度每月 $5，超出后需付费。

用法示例:
  pplx api "量子计算是什么"
  pplx api --model sonar-reasoning "解释相对论"
  pplx api models                          列出 API 可用模型`,
	RunE:          runAPISearch,
	Args:          cobra.ArbitraryArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// apiModelsCmd 列出 API 可用模型
var apiModelsCmd = &cobra.Command{
	Use:   "models",
	Short: "列出 Perplexity API 可用模型",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Perplexity API 可用模型（使用 --model <id> 指定）:")
		fmt.Println()
		for _, m := range driver.APIModels {
			fmt.Printf("  %-24s %s\n", m.ID, m.Description)
		}
		fmt.Println()
		fmt.Println("示例:")
		fmt.Println("  pplx api --model sonar-reasoning \"量子计算\"")
	},
}

func init() {
	apiCmd.Flags().StringVar(&flagAPIModel, "model", "",
		"API 模型 ID, 如: sonar, sonar-pro, sonar-reasoning（默认 sonar-pro）")
	apiCmd.AddCommand(apiModelsCmd)
}

func runAPISearch(cmd *cobra.Command, args []string) error {
	apiKey := os.Getenv("PERPLEXITY_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("环境变量 PERPLEXITY_API_KEY 未设置\n请设置后重试: export PERPLEXITY_API_KEY=pplx-...")
	}

	if len(args) == 0 {
		return fmt.Errorf("请提供搜索查询\n用法: pplx api \"your query\"")
	}

	query := args[0]
	if len(args) > 1 {
		query = fmt.Sprintf("%s", args[0])
		for _, a := range args[1:] {
			query += " " + a
		}
	}

	spin := output.NewSpinner("正在通过 Perplexity API 搜索...")
	spin.Start()

	result, err := driver.APISearch(apiKey, flagAPIModel, query)
	spin.Stop()

	if err != nil {
		return fmt.Errorf("API search failed: %w", err)
	}

	output.PrintResult(result, flagJSON, flagQuiet)
	return nil
}
