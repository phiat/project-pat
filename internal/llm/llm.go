// Package llm wraps multiple LLM providers behind a single Client. The
// project uses two tier-keys ("flash" / "pro") which each provider maps
// to a concrete model id via its own config. Callers depend on *Client
// regardless of which provider is active.
package llm

import (
	"context"
	"fmt"
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

// driver is the internal provider interface. Adding a new provider means
// implementing this and wiring it into New.
type driver interface {
	Complete(ctx context.Context, modelKey, system, user string) (*CompleteResult, error)
	CompleteStream(ctx context.Context, modelKey, system, user string, onChunk func(string)) (*CompleteResult, error)
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
//   openai      — any OpenAI-compatible /v1/chat/completions endpoint
//   deepseek    — alias for openai with the DeepSeek default base URL
//   openrouter  — alias for openai with the OpenRouter default base URL
//   ollama      — alias for openai with the local Ollama default base URL
//   groq        — alias for openai with the Groq default base URL
//   together    — alias for openai with the Together default base URL
//   anthropic   — Anthropic /v1/messages
//
// An empty provider defaults to "openai".
func New(opts Opts) (*Client, error) {
	p := strings.ToLower(strings.TrimSpace(opts.Provider))
	if p == "" {
		p = "openai"
	}
	switch p {
	case "anthropic", "claude":
		return &Client{drv: newAnthropic(opts, baseURLOrDefault(opts.BaseURL, "https://api.anthropic.com/v1"))}, nil
	case "openai":
		return &Client{drv: newOpenAI("openai", opts, baseURLOrDefault(opts.BaseURL, "https://api.openai.com/v1"))}, nil
	case "deepseek":
		return &Client{drv: newOpenAI("deepseek", opts, baseURLOrDefault(opts.BaseURL, "https://api.deepseek.com/v1"))}, nil
	case "openrouter":
		return &Client{drv: newOpenAI("openrouter", opts, baseURLOrDefault(opts.BaseURL, "https://openrouter.ai/api/v1"))}, nil
	case "ollama":
		return &Client{drv: newOpenAI("ollama", opts, baseURLOrDefault(opts.BaseURL, "http://localhost:11434/v1"))}, nil
	case "groq":
		return &Client{drv: newOpenAI("groq", opts, baseURLOrDefault(opts.BaseURL, "https://api.groq.com/openai/v1"))}, nil
	case "together":
		return &Client{drv: newOpenAI("together", opts, baseURLOrDefault(opts.BaseURL, "https://api.together.xyz/v1"))}, nil
	}
	return nil, fmt.Errorf("unknown LLM provider %q (try: openai, deepseek, anthropic, openrouter, ollama, groq, together)", opts.Provider)
}

// ReasoningFor returns the configured effort for a tier key (informational).
func (c *Client) ReasoningFor(key string) string { return c.drv.reasoningFor(key) }

func (c *Client) Provider() string { return c.drv.name() }

func (c *Client) ModelFor(key string) string { return c.drv.modelFor(key) }

func (c *Client) Complete(ctx context.Context, modelKey, system, user string) (*CompleteResult, error) {
	return c.drv.Complete(ctx, modelKey, system, user)
}

func (c *Client) CompleteStream(ctx context.Context, modelKey, system, user string, onChunk func(string)) (*CompleteResult, error) {
	return c.drv.CompleteStream(ctx, modelKey, system, user, onChunk)
}

func baseURLOrDefault(base, def string) string {
	if strings.TrimSpace(base) == "" {
		return def
	}
	return strings.TrimRight(base, "/")
}
