package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("deepseek read body: %w", readErr)
	}
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

// CompleteStream streams deltas via onChunk and returns the full accumulated
// result. The DeepSeek API mirrors OpenAI's SSE format.
func (c *Client) CompleteStream(ctx context.Context, modelKey, system, user string, onChunk func(string)) (*CompleteResult, error) {
	model := c.ModelFor(modelKey)
	msgs := []Message{}
	if system != "" {
		msgs = append(msgs, Message{Role: "system", Content: system})
	}
	msgs = append(msgs, Message{Role: "user", Content: user})

	type streamReq struct {
		Model         string    `json:"model"`
		Messages      []Message `json:"messages"`
		Temperature   float64   `json:"temperature,omitempty"`
		Stream        bool      `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	sr := streamReq{Model: model, Messages: msgs, Temperature: 0.7, Stream: true}
	sr.StreamOptions.IncludeUsage = true

	body, err := json.Marshal(sr)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("deepseek %d: %s", resp.StatusCode, string(raw))
	}

	type chunkResp struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	var full strings.Builder
	var tokIn, tokOut int
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ch chunkResp
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			continue
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != "" {
				full.WriteString(c.Delta.Content)
				if onChunk != nil {
					onChunk(c.Delta.Content)
				}
			}
		}
		if ch.Usage != nil {
			tokIn = ch.Usage.PromptTokens
			tokOut = ch.Usage.CompletionTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read: %w", err)
	}
	return &CompleteResult{Text: full.String(), TokensIn: tokIn, TokensOut: tokOut, Model: model}, nil
}
