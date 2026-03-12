package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/toby1991/pplx-cli/driver"
	"github.com/toby1991/pplx-cli/output"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "显示当前 Perplexity 搜索模式和模型",
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, model, err := driver.GetStatus()
		if err != nil {
			return fmt.Errorf("failed to read status: %w", err)
		}
		output.PrintStatus(mode, model)
		return nil
	},
}
