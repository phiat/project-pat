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

// openaiDriver speaks the OpenAI /v1/chat/completions protocol. It also
// covers any compatible vendor (DeepSeek, OpenRouter, Together, Groq,
// Ollama, Mistral, etc) that exposes the same shape.
type openaiDriver struct {
	provider   string
	apiKey     string
	baseURL    string
	modelFlash string
	modelPro   string
	reasoning  map[string]string // tier key → effort
	http       *http.Client
}

func newOpenAI(provider string, opts Opts, baseURL string) *openaiDriver {
	return &openaiDriver{
		provider:   provider,
		apiKey:     opts.APIKey,
		baseURL:    baseURL,
		modelFlash: opts.ModelFlash,
		modelPro:   opts.ModelPro,
		reasoning: map[string]string{
			ModelFlashKey: opts.ReasoningFlash,
			ModelProKey:   opts.ReasoningPro,
		},
		http: &http.Client{Timeout: 240 * time.Second},
	}
}

func (d *openaiDriver) name() string { return d.provider }

func (d *openaiDriver) modelFor(key string) string {
	if key == ModelProKey {
		return d.modelPro
	}
	return d.modelFlash
}

func (d *openaiDriver) reasoningFor(key string) string { return d.reasoning[key] }

type openaiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiChatReq struct {
	Model           string      `json:"model"`
	Messages        []openaiMsg `json:"messages"`
	Temperature     float64     `json:"temperature,omitempty"`
	MaxTokens       int         `json:"max_tokens,omitempty"`
	Stream          bool        `json:"stream"`
	StreamOptions   *streamOpts `json:"stream_options,omitempty"`
	ReasoningEffort string      `json:"reasoning_effort,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiChatResp struct {
	Choices []struct {
		Message openaiMsg `json:"message"`
		Finish  string    `json:"finish_reason"`
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

// openaiEffort maps our shared effort vocabulary to the OpenAI parameter
// space. "off" / empty / unrecognised => "" so the field is omitted.
func openaiEffort(effort string) string {
	switch effort {
	case "minimal", "low", "medium", "high":
		return effort
	}
	return ""
}

func (d *openaiDriver) buildMessages(system, user string) []openaiMsg {
	msgs := make([]openaiMsg, 0, 2)
	if system != "" {
		msgs = append(msgs, openaiMsg{Role: "system", Content: system})
	}
	msgs = append(msgs, openaiMsg{Role: "user", Content: user})
	return msgs
}

func (d *openaiDriver) Complete(ctx context.Context, modelKey, system, user string) (*CompleteResult, error) {
	model := d.modelFor(modelKey)
	body, err := json.Marshal(openaiChatReq{
		Model:           model,
		Messages:        d.buildMessages(system, user),
		Temperature:     0.7,
		Stream:          false,
		ReasoningEffort: openaiEffort(d.reasoningFor(modelKey)),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+d.apiKey)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("%s read body: %w", d.provider, readErr)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %d: %s", d.provider, resp.StatusCode, string(raw))
	}
	var out openaiChatResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode: %w (body=%s)", err, string(raw))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%s api: %s", d.provider, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%s: empty choices (body=%s)", d.provider, string(raw))
	}
	return &CompleteResult{
		Text:      out.Choices[0].Message.Content,
		TokensIn:  out.Usage.PromptTokens,
		TokensOut: out.Usage.CompletionTokens,
		Model:     model,
	}, nil
}

func (d *openaiDriver) CompleteStream(ctx context.Context, modelKey, system, user string, onChunk func(string)) (*CompleteResult, error) {
	model := d.modelFor(modelKey)
	body, err := json.Marshal(openaiChatReq{
		Model:           model,
		Messages:        d.buildMessages(system, user),
		Temperature:     0.7,
		Stream:          true,
		StreamOptions:   &streamOpts{IncludeUsage: true},
		ReasoningEffort: openaiEffort(d.reasoningFor(modelKey)),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if d.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+d.apiKey)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s %d: %s", d.provider, resp.StatusCode, string(raw))
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
