package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	perplexityAPIURL  = "https://api.perplexity.ai/chat/completions"
	defaultAPIModel   = "sonar-pro"
	apiRequestTimeout = 120 * time.Second
)

// APIModels 列出 Perplexity Sonar API 可用的模型
var APIModels = []struct {
	ID          string
	Name        string
	Description string
}{
	{"sonar", "Sonar", "轻量搜索模型，速度快、成本低"},
	{"sonar-pro", "Sonar Pro", "高级搜索模型，更深入（默认）"},
	{"sonar-reasoning", "Sonar Reasoning", "带推理的搜索模型，适合复杂问题"},
	{"sonar-reasoning-pro", "Sonar Reasoning Pro", "专业推理搜索，最强但最慢"},
}

// apiRequest 是发送给 Perplexity API 的请求体
type apiRequest struct {
	Model    string       `json:"model"`
	Messages []apiMessage `json:"messages"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// apiResponse 是 Perplexity API 的响应体
type apiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Role    string `json:"role"`
		} `json:"message"`
	} `json:"choices"`
	Citations []string `json:"citations"`
	Model     string   `json:"model"`
}

// APISearch 通过 Perplexity REST API 搜索
// apiKey: PERPLEXITY_API_KEY
// model: 模型 ID（空字符串则使用 defaultAPIModel）
// query: 搜索查询
func APISearch(apiKey, model, query string) (*SearchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("PERPLEXITY_API_KEY is not set")
	}

	if model == "" {
		model = defaultAPIModel
	}

	reqBody := apiRequest{
		Model: model,
		Messages: []apiMessage{
			{Role: "user", Content: query},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", perplexityAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: apiRequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBytes))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("API returned empty response")
	}

	// 构造统一的 SearchResult
	result := &SearchResult{
		Answer: apiResp.Choices[0].Message.Content,
		Model:  apiResp.Model,
		Mode:   "api",
	}

	// 解析 citations（API 返回的是 URL 字符串数组）
	for i, url := range apiResp.Citations {
		result.Citations = append(result.Citations, Citation{
			Index: i + 1,
			Title: url, // API 只返回 URL，没有标题
			URL:   url,
		})
	}

	return result, nil
}
