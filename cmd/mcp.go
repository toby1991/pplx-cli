package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
	"github.com/toby1991/pplx-cli/driver"
)

// searchPromptSuffix 附加在每次 MCP 搜索查询后，留空则不附加。
const searchPromptSuffix = ``

var (
	flagMCPPrimary       string
	flagMCPFallback      string
	flagMCPPrimaryModel  string
	flagMCPFallbackModel string
	flagMCPSources       string
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "启动 MCP Server (stdio transport)",
	Long: `以 MCP (Model Context Protocol) 服务器模式运行，通过 stdin/stdout 通信。

UI 后端需要 caffeinate 防止 headless Mac 睡眠导致 WindowServer 降级。
启动时会检测 caffeinate 进程，未运行则提示执行 pplx setup-caffeinate。

搜索后端:
  --primary ui|api          主搜索后端（默认 ui）
  --fallback ui|api         降级搜索后端（留空不降级）
  --primary-model MODEL     主后端默认模型
  --fallback-model MODEL    降级后端默认模型
  --sources web,academic,finance,social  UI 后端默认内容来源

使用 API 后端时需要设置环境变量 PERPLEXITY_API_KEY。
UI 模型名示例: GPT-5, Claude Sonnet, Sonar
API 模型 ID 示例: sonar-pro, sonar-reasoning

配置示例 (OpenCode ~/.config/opencode/config.json):
  {
    "mcpServers": {
      "pplx": {
        "type": "stdio",
        "command": "pplx",
        "args": [
          "mcp",
          "--primary", "api", "--primary-model", "sonar-pro",
          "--fallback", "ui", "--fallback-model", "GPT-5",
          "--sources", "web,academic,social"
        ],
        "env": {
          "PERPLEXITY_API_KEY": "pplx-..."
        }
      }
    }
  }`,
	RunE:          runMCP,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// mcpDispatcher 是 MCP server 运行期间共享的搜索调度器
var mcpDispatcher *driver.Dispatcher

func init() {
	mcpCmd.Flags().StringVar(&flagMCPPrimary, "primary", "ui",
		"主搜索后端: ui (Desktop App) 或 api (REST API)")
	mcpCmd.Flags().StringVar(&flagMCPFallback, "fallback", "",
		"降级搜索后端: ui 或 api（留空表示不降级）")
	mcpCmd.Flags().StringVar(&flagMCPPrimaryModel, "primary-model", "",
		"主后端默认模型（UI: GPT-5, Claude Sonnet; API: sonar-pro, sonar-reasoning）")
	mcpCmd.Flags().StringVar(&flagMCPFallbackModel, "fallback-model", "",
		"降级后端默认模型")
	mcpCmd.Flags().StringVar(&flagMCPSources, "sources", "",
		"UI 后端默认内容来源，逗号分隔: web,academic,finance,social")
}

func runMCP(cmd *cobra.Command, args []string) error {
	// 解析后端配置
	primary, err := driver.ParseBackend(flagMCPPrimary)
	if err != nil {
		return fmt.Errorf("invalid --primary: %w", err)
	}

	var fallback driver.Backend
	if flagMCPFallback != "" {
		fallback, err = driver.ParseBackend(flagMCPFallback)
		if err != nil {
			return fmt.Errorf("invalid --fallback: %w", err)
		}
		if fallback == primary {
			return fmt.Errorf("--fallback cannot be the same as --primary (%s)", primary)
		}
	}

	// 如果使用 API 后端，检查 key
	apiKey := os.Getenv("PERPLEXITY_API_KEY")
	if (primary == driver.BackendAPI || fallback == driver.BackendAPI) && apiKey == "" {
		return fmt.Errorf("PERPLEXITY_API_KEY environment variable is required when using API backend")
	}

	// 解析 --sources
	var uiSources map[string]bool
	if flagMCPSources != "" {
		var err2 error
		uiSources, err2 = parseSources(flagMCPSources)
		if err2 != nil {
			return fmt.Errorf("invalid --sources: %w", err2)
		}
	}

	// 构造调度器
	mcpDispatcher = &driver.Dispatcher{
		Primary:       primary,
		Fallback:      fallback,
		APIKey:        apiKey,
		PrimaryModel:  flagMCPPrimaryModel,
		FallbackModel: flagMCPFallbackModel,
		UISources:     uiSources,
	}

	// UI 后端需要 Perplexity Desktop App 运行 + caffeinate 防睡眠
	if primary == driver.BackendUI || fallback == driver.BackendUI {
		if err := driver.EnsureAppRunning(); err != nil {
			return fmt.Errorf("Perplexity Desktop App: %w", err)
		}
		// 检查 caffeinate 是否已由 LaunchAgent 启动
		if err := exec.Command("pgrep", "-x", "caffeinate").Run(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "[mcp] warning: caffeinate is not running — headless Mac may sleep and degrade WindowServer\n")
			fmt.Fprintf(cmd.ErrOrStderr(), "[mcp] hint: run `pplx setup-caffeinate` to install a persistent LaunchAgent\n")
		}
	}

	s := server.NewMCPServer(
		"Perplexity Research",
		Version,
		server.WithToolCapabilities(true),
	)

	// 注册 search tool
	searchTool := mcp.NewTool("search",
		mcp.WithDescription("Search Perplexity AI. Returns answer with source citations. Supports UI backend (Desktop App) and API backend (Sonar REST API) with optional fallback."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The search query"),
		),
		mcp.WithString("model",
			mcp.Description("Model name. For UI backend: model name prefix (e.g. Sonar, Claude Sonnet). For API backend: model ID (e.g. sonar-pro, sonar-reasoning). Call list_models to see available models."),
		),
		mcp.WithString("sources",
			mcp.Description("(UI backend only) Comma-separated content sources: web,academic,finance,social. Call list_sources to see all options."),
		),
	)
	s.AddTool(searchTool, handleSearch)

	// 注册 list_models tool
	listModelsTool := mcp.NewTool("list_models",
		mcp.WithDescription("List all available models for the current search backend."),
	)
	s.AddTool(listModelsTool, handleListModels)

	// 注册 list_sources tool
	listSourcesTool := mcp.NewTool("list_sources",
		mcp.WithDescription("List available content sources (UI backend only)."),
	)
	s.AddTool(listSourcesTool, handleListSources)

	return server.ServeStdio(s)
}

func handleListSources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(`Available content sources for Perplexity search (UI backend only):

- **web** — 网页搜索（默认）
- **academic** — 学术论文
- **finance** — 财经数据
- **social** — 社交媒体

Usage: pass comma-separated values in the "sources" parameter, e.g.: "web,academic"`), nil
}

func handleListModels(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var sb strings.Builder

	// 根据 primary 后端返回对应的模型列表
	switch mcpDispatcher.Primary {
	case driver.BackendUI:
		sb.WriteString("Available models (UI backend — Desktop App):\n\n")
		for _, m := range availableModels {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n  Use `model: \"%s\"` in search\n", m.Name, m.Description, m.ButtonPrefix))
		}
	case driver.BackendAPI:
		sb.WriteString("Available models (API backend — Sonar REST API):\n\n")
		for _, m := range driver.APIModels {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n  Use `model: \"%s\"` in search\n", m.Name, m.Description, m.ID))
		}
	}

	// 如果有 fallback，也列出
	if mcpDispatcher.Fallback != "" {
		sb.WriteString(fmt.Sprintf("\nFallback backend: %s\n", mcpDispatcher.Fallback))
		switch mcpDispatcher.Fallback {
		case driver.BackendUI:
			sb.WriteString("\nFallback models (UI backend):\n\n")
			for _, m := range availableModels {
				sb.WriteString(fmt.Sprintf("- %s — %s (prefix: \"%s\")\n", m.Name, m.Description, m.ButtonPrefix))
			}
		case driver.BackendAPI:
			sb.WriteString("\nFallback models (API backend):\n\n")
			for _, m := range driver.APIModels {
				sb.WriteString(fmt.Sprintf("- %s — %s (id: \"%s\")\n", m.Name, m.Description, m.ID))
			}
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query parameter is required"), nil
	}

	// 请求级覆盖参数（空值则由 Dispatcher 使用服务级默认）
	model := request.GetString("model", "")

	var requestSources map[string]bool
	if sourcesStr := request.GetString("sources", ""); sourcesStr != "" {
		var err2 error
		requestSources, err2 = parseSources(sourcesStr)
		if err2 != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid sources: %v", err2)), nil
		}
	}

	// 附加 search prompt suffix。
	// 优先级：环境变量 PPLX_PROMPT_SUFFIX > 编译期常量 searchPromptSuffix。
	suffix := os.Getenv("PPLX_PROMPT_SUFFIX")
	if suffix == "" {
		suffix = searchPromptSuffix
	}
	fullQuery := query
	if suffix != "" {
		fullQuery = query + "\n\n" + suffix
	}

	result, err := mcpDispatcher.Search(fullQuery, model, requestSources)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	// 格式化为 markdown
	var sb strings.Builder
	sb.WriteString(result.Answer)

	if len(result.Citations) > 0 {
		sb.WriteString("\n\n**Sources:**\n\n")
		for _, c := range result.Citations {
			if c.URL != "" {
				sb.WriteString(fmt.Sprintf("- [%d] [%s](%s)\n", c.Index, c.Title, c.URL))
			} else {
				sb.WriteString(fmt.Sprintf("- [%d] %s\n", c.Index, c.Title))
			}
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}
