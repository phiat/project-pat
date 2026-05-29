// Package llm wraps multiple LLM providers behind a single Client. The
// project uses two tier-keys ("flash" / "pro") which each provider maps
// to a concrete model id via its own config. Callers depend on *Client
// regardless of which provider is active.
package llm

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

const (
	ModelFlashKey = "flash"
	ModelProKey   = "pro"
)

// CompleteResult is the shared return shape across providers.
type CompleteResult struct {
	Text      string
	TokensIn  int
	TokensOut int
	Model     string
}

// StreamHandler bundles the per-event callbacks for CompleteStream. Both
// fields may be nil. Reasoning tokens (DeepSeek's reasoning_content,
// Anthropic's thinking deltas) are surfaced separately from final
// content so callers can render them differently — typically muted, and
// not persisted into the final run output.
type StreamHandler struct {
	OnContent   func(string)
	OnReasoning func(string)
}

// driver is the internal provider interface. Adding a new provider means
// implementing this and wiring it into New.
type driver interface {
	Complete(ctx context.Context, modelKey, system, user string) (*CompleteResult, error)
	CompleteStream(ctx context.Context, modelKey, system, user string, h StreamHandler) (*CompleteResult, error)
	modelFor(key string) string
	reasoningFor(key string) string
	name() string
}

// Client is the value handlers/scheduler depend on. It hides which
// provider is active so call sites stay unchanged when providers swap.
type Client struct {
	drv driver
}

// Opts is the constructor input. Empty Provider defaults to "openai".
// ReasoningFlash / ReasoningPro accept: off | minimal | low | medium | high.
type Opts struct {
	Provider       string
	APIKey         string
	BaseURL        string
	ModelFlash     string
	ModelPro       string
	ReasoningFlash string // per-tier reasoning/thinking effort
	ReasoningPro   string
}

// New returns a Client for the given provider. Recognised provider keys:
//
//	openai      — any OpenAI-compatible /v1/chat/completions endpoint
//	deepseek    — alias for openai with the DeepSeek default base URL
//	openrouter  — alias for openai with the OpenRouter default base URL
//	ollama      — alias for openai with the local Ollama default base URL
//	groq        — alias for openai with the Groq default base URL
//	together    — alias for openai with the Together default base URL
//	anthropic   — Anthropic /v1/messages
//
// An empty provider defaults to "openai".
func New(opts Opts) (*Client, error) {
	p := strings.ToLower(strings.TrimSpace(opts.Provider))
	if p == "" {
		p = "openai"
	}
	base, defURL := opts.BaseURL, ""
	switch p {
	case "anthropic", "claude":
		defURL = "https://api.anthropic.com/v1"
	case "openai":
		defURL = "https://api.openai.com/v1"
	case "deepseek":
		defURL = "https://api.deepseek.com/v1"
	case "openrouter":
		defURL = "https://openrouter.ai/api/v1"
	case "ollama":
		defURL = "http://localhost:11434/v1"
	case "groq":
		defURL = "https://api.groq.com/openai/v1"
	case "together":
		defURL = "https://api.together.xyz/v1"
	default:
		return nil, fmt.Errorf("unknown LLM provider %q (try: openai, deepseek, anthropic, openrouter, ollama, groq, together)", opts.Provider)
	}
	resolved, err := resolveBaseURL(base, defURL)
	if err != nil {
		return nil, fmt.Errorf("LLM_BASE_URL: %w", err)
	}
	if p == "anthropic" || p == "claude" {
		return &Client{drv: newAnthropic(opts, resolved)}, nil
	}
	return &Client{drv: newOpenAI(p, opts, resolved)}, nil
}

// ReasoningFor returns the configured effort for a tier key (informational).
func (c *Client) ReasoningFor(key string) string { return c.drv.reasoningFor(key) }

func (c *Client) Provider() string { return c.drv.name() }

func (c *Client) ModelFor(key string) string { return c.drv.modelFor(key) }

func (c *Client) Complete(ctx context.Context, modelKey, system, user string) (*CompleteResult, error) {
	return c.drv.Complete(ctx, modelKey, system, user)
}

func (c *Client) CompleteStream(ctx context.Context, modelKey, system, user string, h StreamHandler) (*CompleteResult, error) {
	return c.drv.CompleteStream(ctx, modelKey, system, user, h)
}

// resolveBaseURL validates a user-supplied LLM_BASE_URL or falls back to
// the provider default. Only http/https are allowed — this blocks file://,
// gopher://, and other schemes that the stdlib http client happens to
// understand (and SSRF-flavoured surprises like file:///etc/passwd).
func resolveBaseURL(base, def string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return def, nil
	}
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("scheme %q not allowed; use http or https", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
