package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	ModelFlashKey = "flash"
	ModelProKey   = "pro"
)

type Client struct {
	apiKey     string
	baseURL    string
	modelFlash string
	modelPro   string
	http       *http.Client
}

func New(apiKey, baseURL, modelFlash, modelPro string) *Client {
	return &Client{
		apiKey:     apiKey,
		baseURL:    baseURL,
		modelFlash: modelFlash,
		modelPro:   modelPro,
		http:       &http.Client{Timeout: 180 * time.Second},
	}
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
		Finish  string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c *Client) ModelFor(key string) string {
	if key == ModelProKey {
		return c.modelPro
	}
	return c.modelFlash
}

type CompleteResult struct {
	Text        string
	TokensIn    int
	TokensOut   int
	Model       string
}

func (c *Client) Complete(ctx context.Context, modelKey, system, user string) (*CompleteResult, error) {
	model := c.ModelFor(modelKey)
	msgs := []Message{}
	if system != "" {
		msgs = append(msgs, Message{Role: "system", Content: system})
	}
	msgs = append(msgs, Message{Role: "user", Content: user})

	body, err := json.Marshal(ChatRequest{
		Model:       model,
		Messages:    msgs,
		Temperature: 0.7,
		Stream:      false,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("deepseek %d: %s", resp.StatusCode, string(raw))
	}

	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode: %w (body=%s)", err, string(raw))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("deepseek api: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("deepseek: empty choices (body=%s)", string(raw))
	}
	return &CompleteResult{
		Text:      out.Choices[0].Message.Content,
		TokensIn:  out.Usage.PromptTokens,
		TokensOut: out.Usage.CompletionTokens,
		Model:     model,
	}, nil
}
