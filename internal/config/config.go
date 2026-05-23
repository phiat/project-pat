package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	// LLM
	LLMProvider       string // openai | deepseek | anthropic | openrouter | ollama | groq | together
	LLMAPIKey         string
	LLMBaseURL        string
	LLMModelFlash     string
	LLMModelPro       string
	LLMReasoningFlash string // off | minimal | low | medium | high
	LLMReasoningPro   string // off | minimal | low | medium | high

	// HTTP
	Host string
	Port string

	// Storage / paths
	DBPath       string
	WorkspaceDir string
	TemplatesDir string
	StaticDir    string
}

func Load() (*Config, error) {
	loadDotenv(".env")

	cfg := &Config{
		LLMProvider:       getenvDefault("LLM_PROVIDER", inferProvider()),
		LLMAPIKey:         firstNonEmpty("LLM_API_KEY", "DEEPSEEK_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"),
		LLMBaseURL:        firstNonEmpty("LLM_BASE_URL", "DEEPSEEK_BASE_URL"),
		LLMModelFlash:     firstNonEmpty("LLM_MODEL_FLASH", "DEEPSEEK_MODEL_FLASH"),
		LLMModelPro:       firstNonEmpty("LLM_MODEL_PRO", "DEEPSEEK_MODEL_PRO"),
		LLMReasoningFlash: normalizeEffort(getenvDefault("LLM_REASONING_FLASH", "off")),
		LLMReasoningPro:   normalizeEffort(getenvDefault("LLM_REASONING_PRO", "medium")),
		Host:              getenvDefault("HOST", "0.0.0.0"),
		Port:              getenvDefault("PORT", "8080"),
		DBPath:            getenvDefault("DB_PATH", "projectpat.db"),
		WorkspaceDir:      getenvDefault("WORKSPACE_DIR", "workspace"),
		TemplatesDir:      getenvDefault("TEMPLATES_DIR", "internal/web/templates"),
		StaticDir:         getenvDefault("STATIC_DIR", "internal/web/static"),
	}

	// Fill model defaults based on provider when caller didn't override.
	if cfg.LLMModelFlash == "" {
		cfg.LLMModelFlash = defaultModelFlash(cfg.LLMProvider)
	}
	if cfg.LLMModelPro == "" {
		cfg.LLMModelPro = defaultModelPro(cfg.LLMProvider)
	}

	if cfg.LLMAPIKey == "" {
		return nil, fmt.Errorf("LLM_API_KEY is required (or one of: DEEPSEEK_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY) — set in .env")
	}
	return cfg, nil
}

// inferProvider picks a provider when LLM_PROVIDER is unset, based on
// which legacy env keys are present. Falls back to "openai".
func inferProvider() string {
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		return "deepseek"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "anthropic"
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return "openai"
	}
	return "openai"
}

func defaultModelFlash(provider string) string {
	switch strings.ToLower(provider) {
	case "deepseek":
		return "deepseek-v4-flash"
	case "anthropic", "claude":
		return "claude-haiku-4-5-20251001"
	case "openai":
		return "gpt-4o-mini"
	case "groq":
		return "llama-3.1-8b-instant"
	}
	return ""
}

func defaultModelPro(provider string) string {
	switch strings.ToLower(provider) {
	case "deepseek":
		return "deepseek-v4-pro"
	case "anthropic", "claude":
		return "claude-opus-4-7"
	case "openai":
		return "gpt-4o"
	case "groq":
		return "llama-3.3-70b-versatile"
	}
	return ""
}

// normalizeEffort canonicalises a reasoning-effort string. Empty or any
// unrecognised value becomes "off" so downstream drivers can safely skip
// the parameter.
func normalizeEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "none", "false", "0", "":
		return "off"
	case "minimal", "min":
		return "minimal"
	case "low":
		return "low"
	case "medium", "med", "mid":
		return "medium"
	case "high", "max":
		return "high"
	}
	return "off"
}

func firstNonEmpty(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func getenvDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func loadDotenv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}
