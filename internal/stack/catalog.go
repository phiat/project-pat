// Package stack holds the curated tech-stack catalog: the fixed list of
// slots, the options that fill each slot, and the preset bundles that seed
// all slots at once. Edit this file to add or remove tech choices.
package stack

import (
	"fmt"
	"strings"
)

type Slot struct {
	Key   string
	Label string
	Help  string
}

type Option struct {
	ID       string
	Slot     string
	Label    string
	Blurb    string
	Runtimes []string // empty = compatible with all
}

type Preset struct {
	ID       string
	Label    string
	Blurb    string
	Picks    map[string]string // slot -> option ID ("" means leave blank)
	Versions map[string]string // slot -> version pin (optional)
}

var Slots = []Slot{
	{"runtime", "runtime", "language / runtime"},
	{"framework", "framework", "web or app framework"},
	{"storage", "storage", "database or file store"},
	{"frontend", "frontend", "rendering approach"},
	{"css", "css", "styling layer"},
	{"auth", "auth", "authentication"},
	{"jobs", "jobs", "background jobs / scheduling"},
	{"llm", "llm", "LLM / agent provider"},
	{"deploy", "deploy", "deploy target"},
	{"observability", "observability", "logs / metrics / errors"},
}

var Options = []Option{
	// runtime
	{"go", "runtime", "Go", "", nil},
	{"rust", "runtime", "Rust", "", nil},
	{"python", "runtime", "Python", "", nil},
	{"typescript", "runtime", "TypeScript", "", nil},
	{"node", "runtime", "Node.js", "plain JS, no TS", nil},
	{"deno", "runtime", "Deno", "", nil},

	// framework
	{"htmx-go", "framework", "htmx-over-Go", "stdlib mux + templates", []string{"go"}},
	{"chi", "framework", "chi", "minimal Go router", []string{"go"}},
	{"echo", "framework", "Echo", "", []string{"go"}},
	{"gin", "framework", "Gin", "", []string{"go"}},
	{"nextjs", "framework", "Next.js", "", []string{"typescript", "node"}},
	{"remix", "framework", "Remix", "", []string{"typescript", "node"}},
	{"sveltekit", "framework", "SvelteKit", "", []string{"typescript", "node"}},
	{"hono", "framework", "Hono", "edge-first", []string{"typescript", "node", "deno"}},
	{"fastapi", "framework", "FastAPI", "", []string{"python"}},
	{"django", "framework", "Django", "", []string{"python"}},
	{"flask", "framework", "Flask", "", []string{"python"}},
	{"axum", "framework", "Axum", "", []string{"rust"}},
	{"actix", "framework", "Actix Web", "", []string{"rust"}},
	{"clap-tokio", "framework", "clap + tokio", "CLI", []string{"rust"}},
	{"uv-jupyter", "framework", "uv + Jupyter", "data notebook", []string{"python"}},

	// storage
	{"sqlite", "storage", "SQLite", "", nil},
	{"postgres", "storage", "Postgres", "", nil},
	{"mysql", "storage", "MySQL", "", nil},
	{"duckdb", "storage", "DuckDB", "", nil},
	{"redis", "storage", "Redis", "", nil},
	{"mongo", "storage", "MongoDB", "", nil},
	{"files", "storage", "files only", "JSON / markdown on disk", nil},
	{"d1", "storage", "Cloudflare D1", "", nil},
	{"turso", "storage", "Turso (libSQL)", "", nil},
	{"supabase", "storage", "Supabase", "", nil},

	// frontend
	{"htmx", "frontend", "htmx", "", nil},
	{"react", "frontend", "React", "", []string{"typescript", "node"}},
	{"solid", "frontend", "Solid", "", []string{"typescript", "node"}},
	{"svelte", "frontend", "Svelte", "", []string{"typescript", "node"}},
	{"vue", "frontend", "Vue", "", []string{"typescript", "node"}},
	{"none-fe", "frontend", "none (server only)", "", nil},

	// css
	{"handwritten-css", "css", "handwritten CSS", "", nil},
	{"tailwind", "css", "Tailwind", "", nil},
	{"pico", "css", "Pico", "", nil},
	{"daisyui", "css", "DaisyUI", "", []string{"typescript", "node"}},
	{"none-css", "css", "none", "", nil},

	// auth
	{"none-auth", "auth", "none (single-user)", "", nil},
	{"session-cookie", "auth", "session cookie + password", "", nil},
	{"oauth", "auth", "OAuth (custom)", "", nil},
	{"clerk", "auth", "Clerk", "", nil},
	{"auth0", "auth", "Auth0", "", nil},
	{"supabase-auth", "auth", "Supabase Auth", "", nil},

	// jobs
	{"cron", "jobs", "cron (in-process)", "", nil},
	{"robfig-cron", "jobs", "robfig/cron", "Go cron lib", []string{"go"}},
	{"temporal", "jobs", "Temporal", "", nil},
	{"river", "jobs", "river", "pg-based Go queue", []string{"go"}},
	{"inngest", "jobs", "Inngest", "", []string{"typescript", "node"}},
	{"celery", "jobs", "Celery", "", []string{"python"}},
	{"none-jobs", "jobs", "none", "", nil},

	// llm
	{"deepseek", "llm", "DeepSeek", "", nil},
	{"anthropic", "llm", "Anthropic Claude", "", nil},
	{"openai", "llm", "OpenAI", "", nil},
	{"ollama", "llm", "local Ollama", "", nil},
	{"none-llm", "llm", "none", "", nil},

	// deploy
	{"localhost", "deploy", "localhost", "", nil},
	{"vps", "deploy", "VPS (DIY)", "", nil},
	{"fly", "deploy", "Fly.io", "", nil},
	{"vercel", "deploy", "Vercel", "", []string{"typescript", "node"}},
	{"cloudflare", "deploy", "Cloudflare Workers", "", nil},
	{"railway", "deploy", "Railway", "", nil},
	{"render", "deploy", "Render", "", nil},
	{"docker", "deploy", "Docker only", "self-host elsewhere", nil},

	// observability
	{"stdlog", "observability", "stdlib log", "", nil},
	{"zerolog", "observability", "zerolog", "", []string{"go"}},
	{"slog", "observability", "log/slog", "Go 1.21+", []string{"go"}},
	{"tracing", "observability", "tracing", "Rust ecosystem", []string{"rust"}},
	{"otel", "observability", "OpenTelemetry", "", nil},
	{"sentry", "observability", "Sentry", "", nil},
	{"none-obs", "observability", "none", "", nil},
}

var Presets = []Preset{
	{
		ID: "this-app", Label: "this app", Blurb: "mirrors project-pat itself",
		Picks: map[string]string{
			"runtime": "go", "framework": "htmx-go", "storage": "sqlite",
			"frontend": "htmx", "css": "handwritten-css", "auth": "none-auth",
			"jobs": "robfig-cron", "llm": "deepseek", "deploy": "localhost",
			"observability": "stdlog",
		},
		Versions: map[string]string{"runtime": "1.26"},
	},
	{
		ID: "single-binary-svc", Label: "single-binary service", Blurb: "Go + Postgres + Fly",
		Picks: map[string]string{
			"runtime": "go", "framework": "chi", "storage": "postgres",
			"frontend": "none-fe", "css": "none-css", "auth": "session-cookie",
			"jobs": "river", "llm": "none-llm", "deploy": "fly",
			"observability": "zerolog",
		},
	},
	{
		ID: "data-notebook", Label: "data notebook", Blurb: "Python + uv + DuckDB",
		Picks: map[string]string{
			"runtime": "python", "framework": "uv-jupyter", "storage": "duckdb",
			"frontend": "none-fe", "css": "none-css", "auth": "none-auth",
			"jobs": "none-jobs", "llm": "none-llm", "deploy": "localhost",
			"observability": "none-obs",
		},
	},
	{
		ID: "ts-fullstack", Label: "typescript fullstack", Blurb: "Next.js + Postgres + Vercel",
		Picks: map[string]string{
			"runtime": "typescript", "framework": "nextjs", "storage": "postgres",
			"frontend": "react", "css": "tailwind", "auth": "clerk",
			"jobs": "inngest", "llm": "anthropic", "deploy": "vercel",
			"observability": "sentry",
		},
	},
	{
		ID: "rust-cli", Label: "rust cli tool", Blurb: "clap + tokio + tracing",
		Picks: map[string]string{
			"runtime": "rust", "framework": "clap-tokio", "storage": "sqlite",
			"frontend": "none-fe", "css": "none-css", "auth": "none-auth",
			"jobs": "none-jobs", "llm": "none-llm", "deploy": "localhost",
			"observability": "tracing",
		},
	},
}

// SlotByKey returns the slot definition for a given key.
func SlotByKey(key string) (Slot, bool) {
	for _, s := range Slots {
		if s.Key == key {
			return s, true
		}
	}
	return Slot{}, false
}

// OptionByID returns the option for a given id.
func OptionByID(id string) (Option, bool) {
	for _, o := range Options {
		if o.ID == id {
			return o, true
		}
	}
	return Option{}, false
}

// OptionsForSlot returns options for the slot. If runtime is non-empty,
// options listing runtimes filter to those that include the runtime; an
// option with empty Runtimes is treated as compatible with any runtime.
func OptionsForSlot(slot, runtime string) []Option {
	var out []Option
	for _, o := range Options {
		if o.Slot != slot {
			continue
		}
		if runtime == "" || IsOptionCompatible(o, runtime) {
			out = append(out, o)
		}
	}
	return out
}

// IsOptionCompatible reports whether an option is compatible with the
// given runtime. Options with empty Runtimes are compatible with all.
func IsOptionCompatible(o Option, runtime string) bool {
	if len(o.Runtimes) == 0 {
		return true
	}
	for _, r := range o.Runtimes {
		if r == runtime {
			return true
		}
	}
	return false
}

// PresetByID returns the preset for a given id.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Pick is a small denormalized projection of a stored stack pick used by
// the formatter and the UI without dragging the store dependency in.
type Pick struct {
	Slot     string
	OptionID string
	FreeText string
	Version  string
	Note     string
}

// FormatForPrompt produces a short bulleted stack-context block suitable
// for prepending to an LLM user message. Returns "" if no picks resolve.
func FormatForPrompt(picks []Pick) string {
	var lines []string
	for _, s := range Slots {
		p := findPick(picks, s.Key)
		val := resolveValue(p)
		if val == "" {
			continue
		}
		if p.Version != "" {
			val = val + " " + p.Version
		}
		if p.Note != "" {
			val = val + " (" + p.Note + ")"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", s.Label, val))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Stack context:\n" + strings.Join(lines, "\n") + "\n\n"
}

func findPick(picks []Pick, slot string) Pick {
	for _, p := range picks {
		if p.Slot == slot {
			return p
		}
	}
	return Pick{}
}

func resolveValue(p Pick) string {
	if p.OptionID != "" {
		if o, ok := OptionByID(p.OptionID); ok {
			return o.Label
		}
	}
	return strings.TrimSpace(p.FreeText)
}

// IncompatibleSlots returns the slot keys whose stored OptionID is not
// compatible with the given runtime. Used to surface a warning banner
// after a runtime swap.
func IncompatibleSlots(picks []Pick, runtime string) []string {
	if runtime == "" {
		return nil
	}
	var out []string
	for _, p := range picks {
		if p.Slot == "runtime" || p.OptionID == "" {
			continue
		}
		if o, ok := OptionByID(p.OptionID); ok && !IsOptionCompatible(o, runtime) {
			out = append(out, p.Slot)
		}
	}
	return out
}
