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
- **`/inbox`** — agent reports (cron + manual) land here with unread badges, star, and a unified-style line diff vs the previous successful run by the same agent. Opening an item marks it read.
- **`/runs`** — full LLM run log with token usage, errors, and expandable rendered output.

All LLM endpoints stream over SSE; the UI shows tokens arriving in real-time, then swaps in rendered markdown when the stream ends.

## next slices (not yet built)

- materialize project artifacts to `workspace/<slug>/` on disk.
- multi-agent project teams (planner → critic → drafter chain across multiple system prompts).
- prototype generation tier: scaffold a runnable repo per project.
- prompt-library versioning for agents (history + diff + test-against-prior-runs).
- idea cluster board (force-directed three.js graph).
- reconciliation pass for research briefs (reread the brief against the user's per-item notes).
- scheduler reload via in-process notify rather than the 30-second poll.
