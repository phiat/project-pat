# project-pat

a single-user workshop for ideation, periodic research, and design-doc drafting — powered by deepseek agents, served as htmx-over-go.

> **status**: scaffolding. core flows are wired (ideas → projects → design docs, cron-scheduled agents, run log). prototype-from-doc / sandboxes / workspace materialization are stubs to fill in next.

## stack

- **go 1.26** (HTTP server, html/template, stdlib mux)
- **sqlite** (`modernc.org/sqlite` — pure-go, no cgo)
- **htmx 2** + a small dark css (palette: black forest / dry sage / beige / baltic blue / bright snow / platinum)
- **three.js** background (tessellated cubic landscape, plane-window vantage, slow forward drift)
- **deepseek** chat completions (`flash` for quick drafts, `pro` for deeper passes)
- **robfig/cron** for the agent scheduler

## layout

```
cmd/server/main.go                   wires everything
internal/
  config/      .env loader + paths
  db/          sqlite open + migrations
  store/       data access (ideas, projects, agents, runs, artifacts)
  llm/         deepseek client
  handlers/    HTTP handlers (htmx-friendly partials)
  scheduler/   cron loop that ticks scheduled agents
  web/
    templates.go        + templates/  (layout + page templates)
    static/css/app.css
    static/js/background.js
workspace/        per-project workdirs (gitignored, materialized later)
```

## run it

```bash
mise install                # pins go 1.26 via mise.toml
mise exec -- go mod tidy
mise exec -- go run ./cmd/server
```

Then open <http://localhost:8080>.

Required `.env`:
```
# Generic LLM config (any OpenAI-compatible provider, or Anthropic):
LLM_PROVIDER=deepseek       # openai | deepseek | anthropic | openrouter | ollama | groq | together
LLM_API_KEY=sk-...          # falls back to DEEPSEEK_API_KEY / OPENAI_API_KEY / ANTHROPIC_API_KEY
# LLM_BASE_URL=...          # override base URL; each provider has a sensible default
# LLM_MODEL_FLASH=...       # tier 1: fast/cheap (per-provider default)
# LLM_MODEL_PRO=...         # tier 2: deeper/slower (per-provider default)
# LLM_REASONING_FLASH=off   # off | minimal | low | medium | high  (default: off)
# LLM_REASONING_PRO=medium  # off | minimal | low | medium | high  (default: medium)

# HTTP:
# HOST=0.0.0.0              # bind address; default 0.0.0.0 (LAN/Tailscale-reachable). Set to 127.0.0.1 to lock to localhost.
# PORT=8080
# DB_PATH=projectpat.db
```

The server binds on `0.0.0.0` by default — reachable on `http://localhost:8080`, your LAN IP, and your Tailscale IP. There's no auth (single-user tool), so don't expose it to the public internet without putting it behind a reverse proxy / VPN / SSH tunnel.

### Provider defaults

| provider     | base URL (default)                       | flash default              | pro default                  |
|--------------|------------------------------------------|----------------------------|------------------------------|
| `deepseek`   | `https://api.deepseek.com/v1`            | `deepseek-v4-flash`        | `deepseek-v4-pro`            |
| `openai`     | `https://api.openai.com/v1`              | `gpt-4o-mini`              | `gpt-4o`                     |
| `anthropic`  | `https://api.anthropic.com/v1`           | `claude-haiku-4-5-20251001`| `claude-opus-4-7`            |
| `openrouter` | `https://openrouter.ai/api/v1`           | _(set explicitly)_         | _(set explicitly)_           |
| `ollama`     | `http://localhost:11434/v1`              | _(set explicitly)_         | _(set explicitly)_           |
| `groq`       | `https://api.groq.com/openai/v1`         | `llama-3.1-8b-instant`     | `llama-3.3-70b-versatile`    |
| `together`   | `https://api.together.xyz/v1`            | _(set explicitly)_         | _(set explicitly)_           |

All non-Anthropic providers go through one OpenAI-compatible driver. Anthropic gets its own `/v1/messages` driver (with SSE streaming). Legacy `DEEPSEEK_*` env names still work for back-compat.

### Reasoning / thinking effort

Each tier can request extra "thinking" from the model:

| effort    | OpenAI (`reasoning_effort`) | Anthropic (`thinking.budget_tokens`) |
|-----------|-----------------------------|---------------------------------------|
| `off`     | _omitted_                   | _disabled_                            |
| `minimal` | `minimal`                   | 1 024                                 |
| `low`     | `low`                       | 4 000                                 |
| `medium`  | `medium`                    | 12 000                                |
| `high`    | `high`                      | 32 000                                |

Drivers silently omit the parameter when a model doesn't accept it, so leaving `off` is safe across all providers. Provider-specific caveats: Anthropic forces `temperature=1.0` when thinking is enabled and auto-bumps `max_tokens` by 4 096 above the budget for the answer. OpenAI's `reasoning_effort` is only meaningful on o1/o3/gpt-5-class models.

## what's wired

- **`/`** — overview: counts + recent runs + streaming quick-draft form (creates an idea and seeds it via flash).
- **`/ideas`** — capture pad; promote an idea to a project.
- **`/projects`** — project list; create from scratch or via promote.
- **`/projects/:id`** — the heart of the workshop. Streaming panels for:
  - **design doc** — draft / refine via flash or pro, output renders as markdown
  - **critique** — pro critic produces a scorecard + numbered weaknesses + concrete edits; "apply critique" runs a second pro pass that refines the doc against those notes
  - **research brief** — pro brief with claims, counter-claims, open questions, and a parsed reading list. Items are an interactive checklist; per-item notes persist
  - **standing orders** — agents scoped to this project; their prompt is auto-templated with the live design doc on every run
  - **timeline** — recent runs touching the project (any trigger)
- **`/agents`** — define cron-scheduled missions globally (or scope them via standing orders on a project). "run now" fires off-thread.
- **`/board`** — your idea constellation. Click *cluster ideas* and a pro agent groups them into themes and emits weighted edges; the view renders as a 2D force-directed graph over the landscape. Click two nodes to select them and hit *synthesize* — pro writes a unified design doc for the combined project and you land on it.
- **`/inbox`** — agent reports (cron + manual) land here with unread badges, star, and a unified-style line diff vs the previous successful run by the same agent. Opening an item marks it read.
- **`/runs`** — full LLM run log with token usage, errors, and expandable rendered output.

All LLM endpoints stream over SSE; the UI shows tokens arriving in real-time, then swaps in rendered markdown when the stream ends.

## next slices (not yet built)

- materialize project artifacts to `workspace/<slug>/` on disk.
- multi-agent project teams (planner → critic → drafter chain across multiple system prompts).
- prototype generation tier: scaffold a runnable repo per project.
- reconciliation pass for research briefs (reread the brief against the user's per-item notes).
- scheduler reload via in-process notify rather than the 30-second poll.
