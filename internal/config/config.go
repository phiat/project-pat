package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	DeepSeekAPIKey   string
	DeepSeekBaseURL  string
	ModelFlash       string
	ModelPro         string
	Port             string
	DBPath           string
	WorkspaceDir     string
	TemplatesDir     string
	StaticDir        string
}

func Load() (*Config, error) {
	loadDotenv(".env")

	cfg := &Config{
		DeepSeekAPIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		DeepSeekBaseURL: getenvDefault("DEEPSEEK_BASE_URL", "https://api.deepseek.com/v1"),
		ModelFlash:      getenvDefault("DEEPSEEK_MODEL_FLASH", "deepseek-v4-flash"),
		ModelPro:        getenvDefault("DEEPSEEK_MODEL_PRO", "deepseek-v4-pro"),
		Port:            getenvDefault("PORT", "8080"),
		DBPath:          getenvDefault("DB_PATH", "projectpat.db"),
		WorkspaceDir:    getenvDefault("WORKSPACE_DIR", "workspace"),
		TemplatesDir:    getenvDefault("TEMPLATES_DIR", "internal/web/templates"),
		StaticDir:       getenvDefault("STATIC_DIR", "internal/web/static"),
	}
	if cfg.DeepSeekAPIKey == "" {
		return nil, fmt.Errorf("DEEPSEEK_API_KEY is required (set in .env)")
	}
	return cfg, nil
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
