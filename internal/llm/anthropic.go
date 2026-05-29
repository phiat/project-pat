package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const anthropicVersion = "2023-06-01"

var (
	anthropicUnknownEvents   sync.Map // event-type -> struct{} (already-logged set)
)

func warnOnceAnthropicEvent(typ string) {
	if _, loaded := anthropicUnknownEvents.LoadOrStore(typ, struct{}{}); loaded {
		return
	}
	log.Printf("anthropic: unknown SSE event type %q (driver may need updating)", typ)
}

// anthropicDriver speaks the Anthropic /v1/messages protocol.
type anthropicDriver struct {
	apiKey     string
	baseURL    string
	modelFlash string
	modelPro   string
	reasoning  map[string]string // tier key → effort
	http       *http.Client
}

func newAnthropic(opts Opts, baseURL string) *anthropicDriver {
	return &anthropicDriver{
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

func (d *anthropicDriver) name() string { return "anthropic" }

func (d *anthropicDriver) modelFor(key string) string {
	if key == ModelProKey {
		return d.modelPro
	}
	return d.modelFlash
}

func (d *anthropicDriver) reasoningFor(key string) string { return d.reasoning[key] }

// anthropicThinkingBudget maps our shared effort vocabulary to a token
// budget for Anthropic extended thinking. 0 means thinking disabled.
func anthropicThinkingBudget(effort string) int {
	switch effort {
	case "minimal":
		return 1024
	case "low":
		return 4000
	case "medium":
		return 12000
	case "high":
		return 32000
	}
	return 0
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicReq struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMsg     `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream,omitempty"`
	Thinking  *anthropicThinking `json:"thinking,omitempty"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// buildReq fills the shared fields and applies the per-tier thinking budget.
// When thinking is enabled, Anthropic requires max_tokens > budget_tokens
// and temperature must be 1.0 (so we just omit it).
func (d *anthropicDriver) buildReq(modelKey, system, user string, stream bool) anthropicReq {
	// answerTokens is the headroom reserved for the visible answer. Anthropic
	// truncates output at max_tokens, so a too-low cap silently cuts long
	// outputs (e.g. the prototype scaffolder's multi-file JSON manifest).
	const answerTokens = 8192
	req := anthropicReq{
		Model:     d.modelFor(modelKey),
		System:    system,
		Messages:  []anthropicMsg{{Role: "user", Content: user}},
		MaxTokens: answerTokens,
		Stream:    stream,
	}
	if budget := anthropicThinkingBudget(d.reasoningFor(modelKey)); budget > 0 {
		req.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: budget}
		// headroom for the answer on top of the thinking budget
		req.MaxTokens = budget + answerTokens
	}
	return req
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (d *anthropicDriver) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	if d.apiKey != "" {
		req.Header.Set("x-api-key", d.apiKey)
	}
	return req, nil
}

func (d *anthropicDriver) Complete(ctx context.Context, modelKey, system, user string) (*CompleteResult, error) {
	model := d.modelFor(modelKey)
	body, err := json.Marshal(d.buildReq(modelKey, system, user, false))
	if err != nil {
		return nil, err
	}
	req, err := d.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("anthropic read body: %w", readErr)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("anthropic %d: %s", resp.StatusCode, string(raw))
	}
	var out anthropicResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode: %w (body=%s)", err, string(raw))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("anthropic api: %s", out.Error.Message)
	}
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return &CompleteResult{
		Text:      text.String(),
		TokensIn:  out.Usage.InputTokens,
		TokensOut: out.Usage.OutputTokens,
		Model:     model,
	}, nil
}

func (d *anthropicDriver) CompleteStream(ctx context.Context, modelKey, system, user string, h StreamHandler) (*CompleteResult, error) {
	model := d.modelFor(modelKey)
	body, err := json.Marshal(d.buildReq(modelKey, system, user, true))
	if err != nil {
		return nil, err
	}
	req, err := d.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "text/event-stream")
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic %d: %s", resp.StatusCode, string(raw))
	}

	// Anthropic SSE has typed events; we only care about content_block_delta
	// (text increments) and message_delta (final usage). Other events are
	// safe to ignore.
	type contentBlockDelta struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	}
	type messageStart struct {
		Type    string `json:"type"`
		Message struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	type messageDelta struct {
		Type  string `json:"type"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
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
		if payload == "" {
			continue
		}
		// peek type
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(payload), &head); err != nil {
			log.Printf("llm/anthropic: skipping unparseable stream chunk: %v · %q", err, truncateForLog(payload, 200))
			continue
		}
		switch head.Type {
		case "content_block_delta":
			var cb contentBlockDelta
			if err := json.Unmarshal([]byte(payload), &cb); err == nil {
				switch cb.Delta.Type {
				case "text_delta":
					if cb.Delta.Text != "" {
						full.WriteString(cb.Delta.Text)
						if h.OnContent != nil {
							h.OnContent(cb.Delta.Text)
						}
					}
				case "thinking_delta":
					// Anthropic extended-thinking tokens. Stream to the
					// caller so the UI can show progress during the
					// thinking phase, but don't fold into res.Text.
					if cb.Delta.Thinking != "" && h.OnReasoning != nil {
						h.OnReasoning(cb.Delta.Thinking)
					}
				}
			}
		case "message_start":
			var ms messageStart
			if err := json.Unmarshal([]byte(payload), &ms); err == nil {
				tokIn = ms.Message.Usage.InputTokens
				tokOut = ms.Message.Usage.OutputTokens
			}
		case "message_delta":
			var md messageDelta
			if err := json.Unmarshal([]byte(payload), &md); err == nil && md.Usage.OutputTokens > 0 {
				tokOut = md.Usage.OutputTokens
			}
		case "error":
			// Anthropic streams a typed error event on overload /
			// rate-limit. Surface it instead of silently truncating.
			var ee struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal([]byte(payload), &ee)
			msg := ee.Error.Message
			if msg == "" {
				msg = "anthropic stream error event"
			}
			return nil, fmt.Errorf("anthropic stream error (%s): %s", ee.Error.Type, msg)
		case "content_block_start", "content_block_stop", "message_stop", "ping":
			// expected but uninteresting events; ignore quietly
		default:
			// Log once per process per unknown type so an API change
			// renaming an event surfaces in logs rather than silently
			// zeroing output.
			warnOnceAnthropicEvent(head.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read: %w", err)
	}
	return &CompleteResult{Text: full.String(), TokensIn: tokIn, TokensOut: tokOut, Model: model}, nil
}
