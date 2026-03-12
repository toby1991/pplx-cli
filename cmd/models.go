package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// 已验证可用的模型列表（来自 Perplexity Desktop AX 树探索）
var availableModels = []struct {
	Name         string
	Description  string
	ButtonPrefix string
}{
	{"最佳 (sonar_pro)", "自动选择最佳模型（推荐）", "最佳"},
	{"Sonar", "Perplexity 自研模型，速度快", "Sonar"},
	{"GPT-5.4", "OpenAI GPT-5.4", "GPT-5"},
	{"Claude Sonnet 4.6", "Anthropic Claude Sonnet", "Claude Sonnet"},
	{"Claude Opus 4.6", "Anthropic Claude Opus（慢但强）", "Claude Opus"},
	{"Gemini 3.1 Pro", "Google Gemini Pro", "Gemini"},
	{"GPT-5.4 Thinking", "OpenAI GPT-5.4 带推理", "GPT-5.4 Thinking"},
	{"Claude Sonnet 4.6 Thinking", "Claude Sonnet 带推理", "Claude Sonnet 4.6 Thinking"},
	{"Kimi K2.5", "Moonshot AI Kimi K2.5", "Kimi"},
}

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "列出所有可用的 Perplexity 模型",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("可用模型（使用 --model <名称前缀> 指定）:")
		fmt.Println()
		for _, m := range availableModels {
			fmt.Printf("  %-36s %s\n", m.Name, m.Description)
			fmt.Printf("    使用: pplx --model %q \"query\"\n", m.ButtonPrefix)
			fmt.Println()
		}
	},
}
