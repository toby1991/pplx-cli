package cmd

import (
	"github.com/spf13/cobra"
	"github.com/toby1991/pplx-cli/automation"
	"github.com/toby1991/pplx-cli/driver"
)

var dumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "列出当前 Perplexity 窗口所有 AX 元素（诊断用）",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 先输出窗口详细诊断
		automation.DumpWindowInfo(driver.BundleID)
		// 再输出完整 AX 树
		automation.DumpElements(driver.BundleID)
		return nil
	},
}
