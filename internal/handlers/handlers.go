package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"projectpat/internal/llm"
	"projectpat/internal/store"
	"projectpat/internal/web"
)

type Handler struct {
	S   *store.Store
	LLM *llm.Client
	R   *web.Renderer
}

func New(s *store.Store, l *llm.Client, r *web.Renderer) *Handler {
	return &Handler{S: s, LLM: l, R: r}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/", h.home)
	mux.HandleFunc("/ideas", h.ideas)
	mux.HandleFunc("/ideas/quick", h.quickDraft)
	mux.HandleFunc("/ideas/", h.ideaActions)
	mux.HandleFunc("/projects", h.projects)
	mux.HandleFunc("/projects/", h.projectActions)
	mux.HandleFunc("/agents", h.agents)
	mux.HandleFunc("/agents/", h.agentActions)
	mux.HandleFunc("/runs", h.runs)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	ideas, _ := h.S.ListIdeas()
	projs, _ := h.S.ListProjects()
	agents, _ := h.S.ListAgents()
	runs, _ := h.S.ListRuns(8)
	h.render(w, "home", map[string]any{
		"Title":        "overview",
		"IdeaCount":    len(ideas),
		"ProjectCount": len(projs),
		"AgentCount":   len(agents),
		"RecentRuns":   runs,
	})
}

// ---- ideas ----

func (h *Handler) ideas(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ideas, _ := h.S.ListIdeas()
		h.render(w, "ideas", map[string]any{"Title": "ideas", "Ideas": ideas})
	case http.MethodPost:
		title := strings.TrimSpace(r.FormValue("title"))
		body := strings.TrimSpace(r.FormValue("body"))
		if title == "" {
			http.Error(w, "title required", 400)
			return
		}
		if _, err := h.S.CreateIdea(title, body); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		ideas, _ := h.S.ListIdeas()
		// htmx swaps outerHTML on #idea-list
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintln(w, `<section id="idea-list" class="panel">`)
		if len(ideas) == 0 {
			fmt.Fprintln(w, `<p class="muted">no ideas yet.</p></section>`)
			return
		}
		fmt.Fprintln(w, `<ul class="stack">`)
		for _, i := range ideas {
			fmt.Fprintf(w, `<li class="row"><div class="row-main"><div class="row-title">%s</div>`, htmlEscape(i.Title))
			if i.Body != "" {
				fmt.Fprintf(w, `<div class="row-sub">%s</div>`, htmlEscape(truncate(i.Body, 220)))
			}
			fmt.Fprintf(w, `</div><div class="row-actions"><span class="muted">%s</span>`, i.CreatedAt.Format("2006-01-02 15:04"))
			fmt.Fprintf(w, `<form hx-post="/ideas/%d/promote" hx-swap="none"><button class="btn-ghost">promote →</button></form></div></li>`, i.ID)
		}
		fmt.Fprintln(w, `</ul></section>`)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) quickDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	if title == "" {
		http.Error(w, "title required", 400)
		return
	}
	ideaID, err := h.S.CreateIdea(title, body)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	writeSSE(w, flusher, "meta", fmt.Sprintf("idea #%d", ideaID))
	runID, _ := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{}, "quick-draft", title+"\n\n"+body)
	res, err := h.LLM.CompleteStream(ctx, llm.ModelFlashKey, systemSeedPrompt, fmt.Sprintf("Title: %s\n\nContext:\n%s", title, body),
		func(chunk string) { writeSSE(w, flusher, "delta", chunk) })
	if err != nil {
		_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		writeSSE(w, flusher, "error", "draft failed: "+err.Error())
		return
	}
	_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
	writeSSE(w, flusher, "end", string(web.RenderMarkdown(res.Text)))
}

func (h *Handler) ideaActions(w http.ResponseWriter, r *http.Request) {
	// /ideas/{id}/promote
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[2] != "promote" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	idea, err := h.S.GetIdea(id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	pid, err := h.S.CreateProject(store.Project{
		Title:    idea.Title,
		Summary:  truncate(idea.Body, 200),
		FromIdea: sql.NullInt64{Int64: idea.ID, Valid: true},
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/projects/%d", pid))
	w.WriteHeader(204)
}

// ---- projects ----

func (h *Handler) projects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projs, _ := h.S.ListProjects()
		h.render(w, "projects", map[string]any{"Title": "projects", "Projects": projs})
	case http.MethodPost:
		title := strings.TrimSpace(r.FormValue("title"))
		summary := strings.TrimSpace(r.FormValue("summary"))
		if title == "" {
			http.Error(w, "title required", 400)
			return
		}
		if _, err := h.S.CreateProject(store.Project{Title: title, Summary: summary}); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		projs, _ := h.S.ListProjects()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintln(w, `<section id="project-list" class="grid grid-2">`)
		if len(projs) == 0 {
			fmt.Fprintln(w, `<p class="muted">no projects yet.</p></section>`)
			return
		}
		for _, p := range projs {
			fmt.Fprintf(w, `<a class="card card-link" href="/projects/%d">`, p.ID)
			fmt.Fprintf(w, `<div class="card-eyebrow"><span class="pill pill-active">%s</span></div>`, htmlEscape(p.Status))
			fmt.Fprintf(w, `<h3>%s</h3>`, htmlEscape(p.Title))
			if p.Summary != "" {
				fmt.Fprintf(w, `<p>%s</p>`, htmlEscape(truncate(p.Summary, 200)))
			}
			fmt.Fprintf(w, `<div class="card-stat muted">%s · %s</div></a>`,
				p.UpdatedAt.Format("2006-01-02 15:04"), htmlEscape(p.Slug))
		}
		fmt.Fprintln(w, `</section>`)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) projectActions(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// /projects/{id}            GET  → detail
	// /projects/{id}/draft      POST → run LLM draft
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	proj, err := h.S.GetProject(id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if len(parts) == 2 {
		h.render(w, "project_detail", map[string]any{"Title": proj.Title, "Project": proj})
		return
	}
	if len(parts) == 3 && parts[2] == "draft" && r.Method == http.MethodPost {
		h.streamDraft(w, r, proj)
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) streamDraft(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	modelKey := r.FormValue("model")
	if modelKey != llm.ModelProKey {
		modelKey = llm.ModelFlashKey
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	userPrompt := fmt.Sprintf("Project title: %s\nSummary: %s\n\nExisting doc (may be empty):\n%s\n\nProduce or refine the design doc.",
		proj.Title, proj.Summary, proj.DesignDoc)
	runID, _ := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: proj.ID, Valid: true}, "project-draft", userPrompt)

	res, err := h.LLM.CompleteStream(ctx, modelKey, systemDesignDocPrompt, userPrompt, func(chunk string) {
		writeSSE(w, flusher, "delta", chunk)
	})
	if err != nil {
		_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		writeSSE(w, flusher, "error", "draft failed: "+err.Error())
		return
	}
	_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
	_ = h.S.UpdateProjectDoc(proj.ID, res.Text)
	writeSSE(w, flusher, "end", string(web.RenderMarkdown(res.Text)))
}

// writeSSE writes one SSE event. Lines in data are split per spec.
func writeSSE(w http.ResponseWriter, f http.Flusher, event, data string) {
	fmt.Fprintf(w, "event: %s\n", event)
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
	f.Flush()
}

// ---- agents ----

func (h *Handler) agents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agents, _ := h.S.ListAgents()
		h.render(w, "agents", map[string]any{"Title": "agents", "Agents": agents})
	case http.MethodPost:
		a := store.Agent{
			Name:         strings.TrimSpace(r.FormValue("name")),
			Purpose:      strings.TrimSpace(r.FormValue("purpose")),
			SystemPrompt: strings.TrimSpace(r.FormValue("system_prompt")),
			Model:        strings.TrimSpace(r.FormValue("model")),
			Cron:         strings.TrimSpace(r.FormValue("cron")),
			Enabled:      true,
		}
		if a.Name == "" || a.SystemPrompt == "" {
			http.Error(w, "name and system_prompt required", 400)
			return
		}
		if _, err := h.S.CreateAgent(a); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		agents, _ := h.S.ListAgents()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderAgentList(w, agents)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (h *Handler) agentActions(w http.ResponseWriter, r *http.Request) {
	// /agents/{id}/run
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[2] != "run" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	agent, err := h.S.GetAgent(id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()
		runID, _ := h.S.StartRun(sql.NullInt64{Int64: agent.ID, Valid: true}, sql.NullInt64{}, "manual", agent.SystemPrompt)
		res, err := h.LLM.Complete(ctx, agent.Model, agent.SystemPrompt, fmt.Sprintf("Run mission: %s. Produce a structured report.", agent.Purpose))
		if err != nil {
			log.Printf("agent #%d run failed: %v", agent.ID, err)
			_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
			return
		}
		_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
	}()
	w.Header().Set("HX-Trigger", "agent-run-started")
	w.WriteHeader(202)
}

// ---- runs ----

func (h *Handler) runs(w http.ResponseWriter, r *http.Request) {
	runs, _ := h.S.ListRuns(100)
	h.render(w, "runs", map[string]any{"Title": "runs", "Runs": runs})
}

// ---- helpers ----

func (h *Handler) render(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.R.Render(w, page, data); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "render error", 500)
	}
}

func renderAgentList(w http.ResponseWriter, agents []store.Agent) {
	fmt.Fprintln(w, `<section id="agent-list" class="panel">`)
	if len(agents) == 0 {
		fmt.Fprintln(w, `<p class="muted">no agents yet.</p></section>`)
		return
	}
	fmt.Fprintln(w, `<table class="table"><thead><tr><th>name</th><th>model</th><th>cron</th><th>enabled</th><th></th></tr></thead><tbody>`)
	for _, a := range agents {
		fmt.Fprintf(w, `<tr><td><div class="row-title">%s</div>`, htmlEscape(a.Name))
		if a.Purpose != "" {
			fmt.Fprintf(w, `<div class="row-sub muted">%s</div>`, htmlEscape(a.Purpose))
		}
		fmt.Fprintf(w, `</td><td><span class="mono">%s</span></td>`, htmlEscape(a.Model))
		cronTxt := a.Cron
		if cronTxt == "" {
			cronTxt = "—"
		}
		fmt.Fprintf(w, `<td><span class="mono">%s</span></td>`, htmlEscape(cronTxt))
		state := "on"
		if !a.Enabled {
			state = "off"
		}
		fmt.Fprintf(w, `<td><span class="pill pill-active">%s</span></td>`, state)
		fmt.Fprintf(w, `<td><form hx-post="/agents/%d/run" hx-swap="none"><button class="btn-ghost">run now</button></form></td></tr>`, a.ID)
	}
	fmt.Fprintln(w, `</tbody></table></section>`)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", `'`, "&#39;")
	return r.Replace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

const systemSeedPrompt = `You are an idea-seeding assistant. Given a rough title and optional context, output a tight Markdown sketch with: (1) one-line crystallized framing, (2) why now / who cares, (3) 3-5 angles to explore, (4) the smallest possible first slice. Keep it under 250 words.`

const systemDesignDocPrompt = `You are a senior engineer drafting a design doc. Output Markdown with: ## Problem, ## Goals, ## Non-goals, ## Approach, ## Data model, ## Open questions, ## First slice. Be concrete and brief; favor short bullets over prose. If an existing doc is provided, refine it rather than rewrite from scratch.`
