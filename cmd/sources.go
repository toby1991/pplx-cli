package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// 可用的内容来源列表
var availableSources = []struct {
	Key         string
	Name        string
	Description string
}{
	{"web", "网页", "网页搜索（默认）"},
	{"academic", "学术", "学术论文"},
	{"finance", "财务", "财经数据"},
	{"social", "社交", "社交媒体"},
}

var sourcesCmd = &cobra.Command{
	Use:   "sources",
	Short: "列出所有可用的内容来源",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("可用内容来源（使用 --sources <key1,key2> 指定）:")
		fmt.Println()
		for _, s := range availableSources {
			fmt.Printf("  %-12s %s\n", s.Key, s.Description)
		}
		fmt.Println()
		fmt.Println("示例:")
		fmt.Println("  pplx --sources web,academic \"量子计算论文\"")
	},
}
