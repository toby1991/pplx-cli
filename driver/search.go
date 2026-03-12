package driver

import "fmt"

// Backend 搜索后端类型
type Backend string

const (
	BackendUI  Backend = "ui"  // Desktop App AX 自动化
	BackendAPI Backend = "api" // Perplexity REST API
)

// ParseBackend 解析后端字符串，返回错误如果无效
func ParseBackend(s string) (Backend, error) {
	switch s {
	case "ui":
		return BackendUI, nil
	case "api":
		return BackendAPI, nil
	default:
		return "", fmt.Errorf("unknown backend %q, valid: ui, api", s)
	}
}

// Dispatcher 搜索调度器，管理 primary/fallback 后端
type Dispatcher struct {
	Primary       Backend
	Fallback      Backend         // 空字符串表示无 fallback
	APIKey        string          // API 后端需要的 key
	PrimaryModel  string          // primary 后端的默认模型
	FallbackModel string          // fallback 后端的默认模型
	UISources     map[string]bool // UI 后端的默认内容来源（nil 表示不设置）
}

// Search 使用 primary 后端搜索，失败时尝试 fallback。
// requestModel: 请求级模型覆盖（空字符串则用服务级默认值）
// requestSources: 请求级内容来源覆盖（nil 则用服务级默认值，仅 UI 后端有效）
func (d *Dispatcher) Search(query, requestModel string, requestSources map[string]bool) (*SearchResult, error) {
	// primary 搜索：请求级参数覆盖服务级默认
	primaryModel := d.PrimaryModel
	if requestModel != "" {
		primaryModel = requestModel
	}
	primarySources := d.UISources
	if len(requestSources) > 0 {
		primarySources = requestSources
	}

	result, primaryErr := d.searchWith(d.Primary, primaryModel, primarySources, query)
	if primaryErr == nil {
		return result, nil
	}

	// 无 fallback，直接返回 primary 的错误
	if d.Fallback == "" {
		return nil, primaryErr
	}

	// fallback 搜索：始终使用服务级默认值
	result, fallbackErr := d.searchWith(d.Fallback, d.FallbackModel, d.UISources, query)
	if fallbackErr == nil {
		return result, nil
	}

	return nil, fmt.Errorf("primary (%s): %v; fallback (%s): %v", d.Primary, primaryErr, d.Fallback, fallbackErr)
}

// searchWith 使用指定后端搜索，处理模型切换和内容来源设置
func (d *Dispatcher) searchWith(backend Backend, model string, sources map[string]bool, query string) (*SearchResult, error) {
	switch backend {
	case BackendUI:
		// 确保在首页：SetModel/SetSources 需要首页的 UI 元素，
		// 且从首页发起搜索保证新建线程而非追加到旧对话
		if err := NavigateToHome(); err != nil {
			return nil, fmt.Errorf("navigate to home: %w", err)
		}
		if model != "" {
			if err := SetModel(model); err != nil {
				return nil, fmt.Errorf("set model: %w", err)
			}
		}
		if len(sources) > 0 {
			if err := SetSources(sources); err != nil {
				return nil, fmt.Errorf("set sources: %w", err)
			}
		}
		return Search(query)
	case BackendAPI:
		return APISearch(d.APIKey, model, query)
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
}
