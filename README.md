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
DEEPSEEK_API_KEY=sk-...
# optional overrides:
# DEEPSEEK_BASE_URL=https://api.deepseek.com/v1
# DEEPSEEK_MODEL_FLASH=deepseek-v4-flash
# DEEPSEEK_MODEL_PRO=deepseek-v4-pro
# PORT=8080
# DB_PATH=projectpat.db
```

Model names default to `deepseek-v4-flash` / `deepseek-v4-pro`. If your account exposes different identifiers (e.g. `deepseek-chat`, `deepseek-reasoner`), set the env vars above.

## what's wired

- **`/`** — overview: counts + recent runs + quick-draft form (creates an idea and seeds it via flash).
- **`/ideas`** — capture pad; promote an idea to a project.
- **`/projects`** — project list; create from scratch or via promote.
- **`/projects/:id`** — design doc with a "draft / refine" button (flash or pro).
- **`/agents`** — define cron-scheduled missions (name, purpose, system prompt, cron, model). "run now" fires off-thread.
- **`/runs`** — full LLM run log with token usage and errors.

## next slices (not yet built)

- materialize project artifacts to `workspace/<slug>/` (design doc as markdown, README, task list).
- multi-agent project teams (planner → critic → drafter).
- streaming responses (htmx SSE or fetch streams) instead of round-trip per draft.
- prototype generation tier: scaffold a runnable repo per project.
- inbox view for cron-agent reports (with read/dismiss).
- markdown rendering for design docs (currently shown as `<pre>`).
