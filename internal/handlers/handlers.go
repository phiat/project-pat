package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"projectpat/internal/llm"
	"projectpat/internal/prompts"
	"projectpat/internal/stack"
	"projectpat/internal/store"
	"projectpat/internal/web"
)

type Handler struct {
	S   *store.Store
	LLM *llm.Client
	R   *web.Renderer
	// RootCtx is cancelled when the server is shutting down. Background
	// work (e.g. manual agent runs that outlive the originating request)
	// should derive from this so it stops cleanly on Ctrl-C.
	RootCtx context.Context
	// OnAgentChanged, if set, is called after an agent is created/updated
	// so the scheduler can re-read its set immediately rather than waiting
	// for the next 30s tick.
	OnAgentChanged func()
	// WorkspaceDir is the on-disk root for materialised project artifacts.
	// Empty disables materialisation.
	WorkspaceDir string

	// bg tracks detached goroutines (e.g. manual agent runs) so the
	// server can drain them before closing the DB on shutdown.
	bg sync.WaitGroup
}

func New(s *store.Store, l *llm.Client, r *web.Renderer) *Handler {
	return &Handler{S: s, LLM: l, R: r, RootCtx: context.Background()}
}

// Background launches a tracked goroutine. Callers should derive any
// context from h.RootCtx so the work is cancelled on shutdown — Wait
// only blocks the close of resources (DB, etc.), it does not cancel.
func (h *Handler) Background(fn func()) {
	h.bg.Add(1)
	go func() {
		defer h.bg.Done()
		fn()
	}()
}

// Wait blocks until all background goroutines finish, or until ctx
// expires. Returns ctx.Err() on timeout, nil on clean drain.
func (h *Handler) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		h.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	mux.HandleFunc("/inbox", h.inbox)
	mux.HandleFunc("/inbox/", h.inboxActions)
	mux.HandleFunc("/briefs/", h.briefActions)
	mux.HandleFunc("/board", h.board)
	mux.HandleFunc("/board/cluster", h.boardCluster)
	mux.HandleFunc("/board/synthesize", h.boardSynthesize)
	mux.HandleFunc("/board/data", h.boardData)
	mux.HandleFunc("/floor", h.floor)
	mux.HandleFunc("/floor/tiles", h.floorTiles)
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
			fmt.Fprintf(w, `<form hx-post="/ideas/%d/promote" hx-swap="none" hx-disabled-elt="find button"><button class="btn-ghost" type="submit"><span class="label-idle">promote →</span><span class="label-busy">promoting…</span></button></form></div></li>`, i.ID)
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
	h.streamSSE(w, r, streamSpec{
		trigger:   "quick-draft",
		modelKey:  llm.ModelFlashKey,
		system:    systemSeedPrompt,
		user:      fmt.Sprintf("Title: %s\n\nContext:\n%s", title, body),
		timeout:   120 * time.Second,
		preEvents: []sseEvent{{Event: "meta", Data: fmt.Sprintf("idea #%d", ideaID)}},
		onSuccess: func(text string, runID int64) (string, string) {
			// Fold the seeded sketch back into the idea so a reload of
			// /ideas shows the crystallized version, not the raw input it
			// was generated from. (The idea was just created above with the
			// user's rough body.)
			if strings.TrimSpace(text) != "" {
				if err := h.S.UpdateIdeaBody(ideaID, text); err != nil {
					log.Printf("quick-draft updateIdeaBody(%d): %v", ideaID, err)
				}
			}
			return string(web.RenderMarkdown(text)), ""
		},
	})
}

func (h *Handler) ideaActions(w http.ResponseWriter, r *http.Request) {
	// /ideas/{id}/promote — POST only; a GET here would let any prefetch
	// or embedded <img> trigger a project create.
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
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
	// Idempotent: if this idea was already promoted, send the user to
	// that project instead of erroring on a duplicate slug.
	if existing, err := h.S.ProjectByIdea(idea.ID); err == nil && existing != nil {
		w.Header().Set("HX-Redirect", fmt.Sprintf("/projects/%d", existing.ID))
		w.WriteHeader(204)
		return
	}
	slug := uniqueProjectSlug(h.S, store.Slugify(idea.Title))
	pid, err := h.S.CreateProject(store.Project{
		Slug:     slug,
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

// uniqueProjectSlug appends -2, -3, … until the slug is free. Slugs are
// short, so a linear probe is fine.
func uniqueProjectSlug(s *store.Store, base string) string {
	if base == "" {
		base = "project"
	}
	candidate := base
	for i := 2; ; i++ {
		if _, err := s.ProjectBySlug(candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
		if i > 1000 {
			return candidate
		}
	}
}

// ---- projects ----

func (h *Handler) projects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		showArchived := r.URL.Query().Get("archived") == "1"
		var projs []store.Project
		if showArchived {
			projs, _ = h.S.ListProjectsAll()
		} else {
			projs, _ = h.S.ListProjects()
		}
		archivedN, _ := h.S.ListArchivedProjects()
		h.render(w, "projects", map[string]any{
			"Title":         "projects",
			"Projects":      projs,
			"ShowArchived":  showArchived,
			"ArchivedCount": len(archivedN),
		})
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
		critique, _ := h.S.LatestArtifact(proj.ID, "critique")
		orders, _ := h.S.ListAgentsForProject(proj.ID)
		timeline, _ := h.S.RunsForProject(proj.ID, 12)
		brief, _ := h.S.LatestArtifact(proj.ID, "brief")
		var briefItems []store.BriefItem
		if brief != nil {
			briefItems, _ = h.S.ListBriefItems(brief.ID)
		}
		decisions, _ := h.S.ListArtifacts(proj.ID, "decision")
		stackData := h.buildStackPanelData(proj)
		wsPath, wsMaterialized := h.workspacePath(proj)
		briefRecon, _ := h.S.LatestArtifact(proj.ID, "brief_recon")
		// Only show reconcile if it's newer than the brief itself (else
		// it's stale and confusing).
		if brief != nil && briefRecon != nil && briefRecon.CreatedAt.Before(brief.CreatedAt) {
			briefRecon = nil
		}
		h.render(w, "project_detail", map[string]any{
			"Title":           proj.Title,
			"Project":         proj,
			"Critique":        critique,
			"Orders":          orders,
			"Timeline":        timeline,
			"Brief":           brief,
			"BriefItems":      briefItems,
			"BriefRecon":      briefRecon,
			"Decisions":       decisions,
			"Stack":           stackData,
			"WorkspacePath":   wsPath,
			"WorkspaceOnDisk": wsMaterialized,
		})
		return
	}
	if len(parts) == 3 && r.Method == http.MethodPost {
		switch parts[2] {
		case "draft":
			h.streamDraft(w, r, proj)
			return
		case "critique":
			h.streamCritique(w, r, proj)
			return
		case "apply-critique":
			h.streamApplyCritique(w, r, proj)
			return
		case "standing-orders":
			h.createStandingOrder(w, r, proj)
			return
		case "brief":
			h.streamBrief(w, r, proj)
			return
		case "brief-reconcile":
			h.streamBriefReconcile(w, r, proj)
			return
		case "stack":
			h.stackUpsert(w, r, proj)
			return
		case "devil":
			h.streamDevil(w, r, proj)
			return
		case "decisions":
			h.commitDecision(w, r, proj)
			return
		case "materialize":
			h.materializeProject(w, r, proj)
			return
		case "team-draft":
			h.streamTeamDraft(w, r, proj)
			return
		case "prototype":
			h.streamPrototype(w, r, proj)
			return
		case "archive":
			if err := h.S.ArchiveProject(proj.ID); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("HX-Refresh", "true")
			w.WriteHeader(204)
			return
		case "unarchive":
			if err := h.S.UnarchiveProject(proj.ID); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("HX-Refresh", "true")
			w.WriteHeader(204)
			return
		}
	}
	if len(parts) == 4 && parts[2] == "stack" && r.Method == http.MethodPost {
		switch parts[3] {
		case "preset":
			h.stackApplyPreset(w, r, proj)
			return
		case "clear":
			h.stackClear(w, r, proj)
			return
		}
	}
	if len(parts) >= 4 && parts[2] == "stack" && parts[3] == "popover" && r.Method == http.MethodGet {
		if len(parts) == 4 {
			h.stackPopover(w, r, proj)
			return
		}
		if len(parts) == 5 && parts[4] == "close" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(""))
			return
		}
	}
	http.NotFound(w, r)
}

// ---- stack handlers ----

func (h *Handler) renderStackPanel(w http.ResponseWriter, proj *store.Project) {
	data := h.buildStackPanelData(proj)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.R.RenderPartial(w, "stack_panel", data); err != nil {
		log.Printf("render stack_panel: %v", err)
		http.Error(w, "render", 500)
	}
}

func (h *Handler) stackPopover(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	slotKey := r.URL.Query().Get("slot")
	slot, ok := stack.SlotByKey(slotKey)
	if !ok {
		http.Error(w, "bad slot", 400)
		return
	}
	picks, _ := h.S.ListStackPicks(proj.ID)
	runtime := runtimeOptionID(picks)
	var current store.StackPick
	for _, p := range picks {
		if p.Slot == slotKey {
			current = p
			break
		}
	}
	// For the runtime slot itself, don't filter by runtime
	filterRuntime := runtime
	if slotKey == "runtime" {
		filterRuntime = ""
	}
	options := stack.OptionsForSlot(slotKey, filterRuntime)
	// also include options NOT compatible with current runtime but only for non-runtime slots
	if slotKey != "runtime" && runtime != "" {
		all := stack.OptionsForSlot(slotKey, "")
		seen := make(map[string]bool, len(options))
		for _, o := range options {
			seen[o.ID] = true
		}
		for _, o := range all {
			if !seen[o.ID] {
				options = append(options, o)
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.R.RenderPartial(w, "stack_popover", stackPopoverData{
		ProjectID: proj.ID,
		Slot:      slot,
		Current:   current,
		Options:   options,
		Runtime:   runtime,
	})
}

func (h *Handler) stackUpsert(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	slotKey := r.FormValue("slot")
	if _, ok := stack.SlotByKey(slotKey); !ok {
		http.Error(w, "bad slot", 400)
		return
	}
	optionID := strings.TrimSpace(r.FormValue("option_id"))
	freeText := strings.TrimSpace(r.FormValue("free_text"))
	version := strings.TrimSpace(r.FormValue("version"))
	note := strings.TrimSpace(r.FormValue("note"))

	// blank both = clear
	if optionID == "" && freeText == "" && version == "" && note == "" {
		_ = h.S.ClearStackSlot(proj.ID, slotKey)
	} else {
		if optionID != "" {
			if _, ok := stack.OptionByID(optionID); !ok {
				http.Error(w, "bad option", 400)
				return
			}
		}
		_ = h.S.UpsertStackPick(store.StackPick{
			ProjectID: proj.ID, Slot: slotKey,
			OptionID: optionID, FreeText: freeText, Version: version, Note: note,
		})
	}
	h.renderStackPanel(w, proj)
}

func (h *Handler) stackClear(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	slotKey := r.URL.Query().Get("slot")
	if slotKey == "" {
		slotKey = r.FormValue("slot")
	}
	if _, ok := stack.SlotByKey(slotKey); !ok {
		http.Error(w, "bad slot", 400)
		return
	}
	_ = h.S.ClearStackSlot(proj.ID, slotKey)
	h.renderStackPanel(w, proj)
}

func (h *Handler) stackApplyPreset(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	presetID := r.FormValue("preset_id")
	if presetID == "" {
		_ = h.S.SetProjectStackPreset(proj.ID, "")
		proj.StackPreset = ""
		h.renderStackPanel(w, proj)
		return
	}
	preset, ok := stack.PresetByID(presetID)
	if !ok {
		http.Error(w, "bad preset", 400)
		return
	}
	for slotKey, optID := range preset.Picks {
		ver := preset.Versions[slotKey]
		_ = h.S.UpsertStackPick(store.StackPick{
			ProjectID: proj.ID, Slot: slotKey, OptionID: optID, Version: ver,
		})
	}
	_ = h.S.SetProjectStackPreset(proj.ID, presetID)
	proj.StackPreset = presetID
	h.renderStackPanel(w, proj)
}

type chipData struct {
	SlotKey   string
	SlotLabel string
	Value     string
	Version   string
	Empty     bool
	Incompat  bool
}

type stackPanelData struct {
	ProjectID int64
	PresetID  string
	Chips     []chipData
	Incompats []string
	Presets   []stack.Preset
}

type stackPopoverData struct {
	ProjectID int64
	Slot      stack.Slot
	Current   store.StackPick
	Options   []stack.Option
	Runtime   string
}

func runtimeOptionID(picks []store.StackPick) string {
	for _, p := range picks {
		if p.Slot == "runtime" {
			return p.OptionID
		}
	}
	return ""
}

func (h *Handler) buildStackPanelData(proj *store.Project) stackPanelData {
	picks, _ := h.S.ListStackPicks(proj.ID)
	runtime := runtimeOptionID(picks)
	incompats := stack.IncompatibleSlots(prompts.ToCatalogPicks(picks), runtime)
	incompatSet := make(map[string]bool, len(incompats))
	for _, s := range incompats {
		incompatSet[s] = true
	}
	chips := make([]chipData, 0, len(stack.Slots))
	for _, s := range stack.Slots {
		c := chipData{SlotKey: s.Key, SlotLabel: s.Label, Empty: true}
		for _, p := range picks {
			if p.Slot != s.Key {
				continue
			}
			c.Empty = false
			c.Version = p.Version
			if p.OptionID != "" {
				if o, ok := stack.OptionByID(p.OptionID); ok {
					c.Value = o.Label
				} else {
					c.Value = p.OptionID
				}
			} else if p.FreeText != "" {
				c.Value = p.FreeText
			} else if p.Version == "" {
				// no option, no free text, no version → genuinely empty.
				// A version-only pick stays non-empty so the chip (and its
				// version pin) still renders.
				c.Empty = true
			}
			break
		}
		if incompatSet[s.Key] {
			c.Incompat = true
		}
		chips = append(chips, c)
	}
	return stackPanelData{
		ProjectID: proj.ID,
		PresetID:  proj.StackPreset,
		Chips:     chips,
		Incompats: incompats,
		Presets:   stack.Presets,
	}
}

func (h *Handler) stackContext(projectID int64) string {
	return prompts.Stack(h.S, projectID)
}

// ---- devil's office hours ----

func (h *Handler) streamDevil(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	decisions, _ := h.S.ListArtifacts(proj.ID, "decision")
	critique, _ := h.S.LatestArtifact(proj.ID, "critique")
	var critBody string
	if critique != nil {
		critBody = critique.Body
	}
	user := fmt.Sprintf(
		"Project: %s\nSummary: %s\n\nDesign doc:\n%s\n\nLatest critique:\n%s\n\nResolved decisions so far:\n%s",
		proj.Title, proj.Summary, proj.DesignDoc, critBody, formatDecisionsList(decisions),
	)
	h.streamSSE(w, r, streamSpec{
		trigger:   "devil",
		projectID: sql.NullInt64{Int64: proj.ID, Valid: true},
		modelKey:  llm.ModelProKey,
		system:    systemDevilPrompt,
		user:      user,
		timeout:   90 * time.Second,
	})
}

func (h *Handler) commitDecision(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	question := strings.TrimSpace(r.FormValue("question"))
	answer := strings.TrimSpace(r.FormValue("answer"))
	if question == "" || answer == "" {
		http.Error(w, "question and answer required", 400)
		return
	}
	if _, err := h.S.CreateArtifact(store.Artifact{
		ProjectID: proj.ID, Kind: "decision", Title: question, Body: answer,
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(204)
}

func formatDecisionsList(decisions []store.Artifact) string {
	if len(decisions) == 0 {
		return "(none yet)"
	}
	var b strings.Builder
	for _, d := range decisions {
		fmt.Fprintf(&b, "- Q: %s\n  A: %s\n", oneLine(d.Title), oneLine(d.Body))
	}
	return b.String()
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}

func (h *Handler) decisionsContext(projectID int64) string {
	return prompts.Decisions(h.S, projectID)
}

func (h *Handler) streamBrief(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	topic := strings.TrimSpace(r.FormValue("topic"))
	if topic == "" {
		topic = proj.Title
	}
	userPrompt := fmt.Sprintf(
		"Project: %s\nSummary: %s\n\nTopic to research: %s\n\nProduce a research brief.",
		proj.Title, proj.Summary, topic,
	)
	h.streamSSE(w, r, streamSpec{
		trigger:   "brief",
		projectID: sql.NullInt64{Int64: proj.ID, Valid: true},
		modelKey:  llm.ModelProKey,
		system:    systemBriefPrompt,
		user:      userPrompt,
		timeout:   240 * time.Second,
		onSuccess: func(text string, runID int64) (string, string) {
			briefID, err := h.S.CreateArtifact(store.Artifact{
				ProjectID: proj.ID, Kind: "brief", Title: topic, Body: text,
			})
			if err != nil {
				log.Printf("brief artifact: %v", err)
			}
			if items := web.ExtractReadingList(text); len(items) > 0 && briefID > 0 {
				if err := h.S.CreateBriefItems(briefID, items); err != nil {
					log.Printf("brief items: %v", err)
				}
			}
			return string(web.RenderMarkdown(text)), ""
		},
	})
}

// streamBriefReconcile re-reads the latest brief against the user's
// per-item reading notes and produces a fresh artifact ("brief_recon")
// that names what the notes confirmed, complicated, or opened up.
func (h *Handler) streamBriefReconcile(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	brief, err := h.S.LatestArtifact(proj.ID, "brief")
	if err != nil || brief == nil {
		http.Error(w, "no brief to reconcile", 400)
		return
	}
	items, _ := h.S.ListBriefItems(brief.ID)
	if !anyBriefNotes(items) {
		http.Error(w, "no per-item notes yet — read a few items first", 400)
		return
	}
	var notes strings.Builder
	for _, it := range items {
		if strings.TrimSpace(it.Note) == "" && it.Status != "read" {
			continue
		}
		marker := "[unread]"
		switch it.Status {
		case "read":
			marker = "[read]"
		case "reading":
			marker = "[reading]"
		}
		fmt.Fprintf(&notes, "- %s %s\n", marker, it.Text)
		if strings.TrimSpace(it.Note) != "" {
			fmt.Fprintf(&notes, "  note: %s\n", oneLine(it.Note))
		}
	}
	userPrompt := fmt.Sprintf(
		"Project: %s\nSummary: %s\n\nOriginal brief:\n\n%s\n\nReading log (status + the author's per-item notes):\n\n%s\n\nProduce the reconciliation pass.",
		proj.Title, proj.Summary, brief.Body, notes.String(),
	)
	h.streamSSE(w, r, streamSpec{
		trigger:   "brief-reconcile",
		projectID: sql.NullInt64{Int64: proj.ID, Valid: true},
		modelKey:  llm.ModelProKey,
		system:    systemBriefReconcilePrompt,
		user:      userPrompt,
		timeout:   240 * time.Second,
		onSuccess: func(text string, runID int64) (string, string) {
			if _, err := h.S.CreateArtifact(store.Artifact{
				ProjectID: proj.ID, Kind: "brief_recon", Title: brief.Title, Body: text,
			}); err != nil {
				log.Printf("brief_recon artifact: %v", err)
			}
			return string(web.RenderMarkdown(text)), ""
		},
	})
}

func anyBriefNotes(items []store.BriefItem) bool {
	for _, it := range items {
		if strings.TrimSpace(it.Note) != "" || it.Status == "read" {
			return true
		}
	}
	return false
}

func (h *Handler) briefActions(w http.ResponseWriter, r *http.Request) {
	// /briefs/{briefID}/items/{itemID}/{action}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 || parts[0] != "briefs" || parts[2] != "items" {
		http.NotFound(w, r)
		return
	}
	briefID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	itemID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	item, err := h.S.GetBriefItem(itemID)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if item.BriefID != briefID {
		http.NotFound(w, r)
		return
	}
	switch parts[4] {
	case "status":
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		next := r.FormValue("status")
		if next != "unread" && next != "reading" && next != "read" {
			http.Error(w, "bad status", 400)
			return
		}
		_ = h.S.UpdateBriefItemStatus(itemID, next)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, renderBriefItemRow(*item, next, item.Note))
	case "note":
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		note := strings.TrimSpace(r.FormValue("note"))
		_ = h.S.UpdateBriefItemNote(itemID, note)
		w.WriteHeader(204)
	default:
		http.NotFound(w, r)
	}
}

func renderBriefItemRow(b store.BriefItem, status, note string) string {
	checked := ""
	cls := "brief-item"
	if status == "read" {
		checked = "checked"
		cls += " brief-item-read"
	} else if status == "reading" {
		cls += " brief-item-reading"
	}
	var nextStatus string
	if status == "read" {
		nextStatus = "unread"
	} else {
		nextStatus = "read"
	}
	return fmt.Sprintf(`<li id="bi-%d" class="%s">
  <form hx-post="/briefs/%d/items/%d/status" hx-target="#bi-%d" hx-swap="outerHTML" style="display:inline">
    <input type="hidden" name="status" value="%s">
    <button class="brief-check" type="submit" aria-label="toggle">%s</button>
  </form>
  <span class="brief-text">%s</span>
  <input class="brief-note" type="text" name="note" placeholder="note…" value="%s"
         hx-post="/briefs/%d/items/%d/note" hx-trigger="change" hx-swap="none">
</li>`,
		b.ID, cls,
		b.BriefID, b.ID, b.ID,
		nextStatus,
		map[bool]string{true: "✓", false: "○"}[checked == "checked"],
		htmlEscape(b.Text), htmlEscape(note),
		b.BriefID, b.ID,
	)
}

func (h *Handler) createStandingOrder(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	a := store.Agent{
		Name:         strings.TrimSpace(r.FormValue("name")),
		Purpose:      strings.TrimSpace(r.FormValue("purpose")),
		SystemPrompt: strings.TrimSpace(r.FormValue("system_prompt")),
		Model:        strings.TrimSpace(r.FormValue("model")),
		Cron:         strings.TrimSpace(r.FormValue("cron")),
		Enabled:      true,
		ProjectID:    sql.NullInt64{Int64: proj.ID, Valid: true},
	}
	if a.Name == "" || a.SystemPrompt == "" {
		http.Error(w, "name and system_prompt required", 400)
		return
	}
	if _, err := h.S.CreateAgent(a); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if h.OnAgentChanged != nil {
		h.OnAgentChanged()
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(204)
}

func (h *Handler) streamCritique(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	if strings.TrimSpace(proj.DesignDoc) == "" {
		http.Error(w, "no design doc to critique", 400)
		return
	}
	focus := strings.TrimSpace(r.FormValue("focus"))
	userPrompt := h.stackContext(proj.ID) + h.decisionsContext(proj.ID) + fmt.Sprintf("Project: %s\n\nDesign doc:\n\n%s", proj.Title, proj.DesignDoc)
	if focus != "" {
		userPrompt += "\n\nFocus this critique on: " + focus
	}
	h.streamSSE(w, r, streamSpec{
		trigger:   "critique",
		projectID: sql.NullInt64{Int64: proj.ID, Valid: true},
		modelKey:  llm.ModelProKey,
		system:    systemCritiquePrompt,
		user:      userPrompt,
		timeout:   240 * time.Second,
		onSuccess: func(text string, runID int64) (string, string) {
			if _, err := h.S.CreateArtifact(store.Artifact{ProjectID: proj.ID, Kind: "critique", Body: text}); err != nil {
				log.Printf("critique artifact: %v", err)
			}
			return string(web.RenderMarkdown(text)), ""
		},
	})
}

func (h *Handler) streamApplyCritique(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	crit, err := h.S.LatestArtifact(proj.ID, "critique")
	if err != nil {
		http.Error(w, "no critique to apply", 400)
		return
	}
	focus := strings.TrimSpace(r.FormValue("focus"))
	userPrompt := h.stackContext(proj.ID) + h.decisionsContext(proj.ID) + fmt.Sprintf("Project: %s\n\nExisting design doc:\n\n%s\n\nCritique to address:\n\n%s\n\nProduce a revised design doc that addresses each numbered point in the critique. Preserve sections that the critique didn't flag.",
		proj.Title, proj.DesignDoc, crit.Body)
	if focus != "" {
		userPrompt += "\n\nWhile applying the critique, prioritise: " + focus
	}
	h.streamSSE(w, r, streamSpec{
		trigger:   "apply-critique",
		projectID: sql.NullInt64{Int64: proj.ID, Valid: true},
		modelKey:  llm.ModelProKey,
		system:    systemDesignDocPrompt,
		user:      userPrompt,
		timeout:   240 * time.Second,
		onSuccess: func(text string, runID int64) (string, string) {
			if err := h.S.UpdateProjectDoc(proj.ID, text); err != nil {
				log.Printf("updateProjectDoc(%d): %v", proj.ID, err)
			}
			return string(web.RenderMarkdown(text)), ""
		},
	})
}

func (h *Handler) streamDraft(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	modelKey := r.FormValue("model")
	if modelKey != llm.ModelProKey {
		modelKey = llm.ModelFlashKey
	}
	focus := strings.TrimSpace(r.FormValue("focus"))

	userPrompt := h.stackContext(proj.ID) + h.decisionsContext(proj.ID) + fmt.Sprintf("Project title: %s\nSummary: %s\n\nExisting doc (may be empty):\n%s\n\nProduce or refine the design doc.",
		proj.Title, proj.Summary, proj.DesignDoc)
	if focus != "" {
		userPrompt += "\n\nFocus this pass on: " + focus
	}

	h.streamSSE(w, r, streamSpec{
		trigger:   "project-draft",
		projectID: sql.NullInt64{Int64: proj.ID, Valid: true},
		modelKey:  modelKey,
		system:    systemDesignDocPrompt,
		user:      userPrompt,
		timeout:   180 * time.Second,
		onSuccess: func(text string, runID int64) (string, string) {
			if err := h.S.UpdateProjectDoc(proj.ID, text); err != nil {
				log.Printf("updateProjectDoc(%d): %v", proj.ID, err)
			}
			return string(web.RenderMarkdown(text)), ""
		},
	})
}

// writeSSE writes one SSE event. Each line of data becomes its own
// data: line per spec. \r characters are stripped because the SSE wire
// format also recognises CR as a line terminator and some clients
// (notably curl with --max-time) get confused by CR-LF inputs.
func writeSSE(w http.ResponseWriter, f http.Flusher, event, data string) {
	fmt.Fprintf(w, "event: %s\n", event)
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
	f.Flush()
}

// openSSE sets the SSE response headers and returns the Flusher. Headers
// are only committed when the first event is flushed, so callers can
// still emit a plain http.Error before openSSE is called.
func openSSE(w http.ResponseWriter) http.Flusher {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return nil
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	return flusher
}

// sseEvent is an SSE event waiting to be emitted before the LLM stream.
type sseEvent struct{ Event, Data string }

// streamSpec drives the streamSSE helper.
type streamSpec struct {
	trigger   string
	agentID   sql.NullInt64
	projectID sql.NullInt64
	modelKey  string
	system    string
	user      string
	timeout   time.Duration
	preEvents []sseEvent
	// onSuccess runs after FinishRun and may persist artifacts. It returns
	// the payload for the "end" event, or a non-empty errMsg to emit an
	// "error" event instead (e.g. parse failure on the LLM output).
	onSuccess func(text string, runID int64) (endPayload string, errMsg string)
}

// streamSSE handles the common shape of every LLM-streaming endpoint:
// run row, headers, optional pre-events, deltas, finish, end. StartRun
// runs *before* SSE headers are committed so a DB failure surfaces as a
// plain 500. Partial output is preserved if the client disconnects mid-stream.
func (h *Handler) streamSSE(w http.ResponseWriter, r *http.Request, spec streamSpec) {
	runID, err := h.S.StartRun(spec.agentID, spec.projectID, spec.trigger, spec.user)
	if err != nil {
		log.Printf("startRun(%s): %v", spec.trigger, err)
		http.Error(w, spec.trigger+": failed to record run", 500)
		return
	}

	flusher := openSSE(w)
	if flusher == nil {
		_ = h.S.FinishRun(runID, "failed", "", "streaming unsupported", 0, 0)
		return
	}
	// SSE writes from the main path and the heartbeat goroutine need to
	// be serialised — http.ResponseWriter is not safe for concurrent use.
	var sseMu sync.Mutex
	emit := func(event, data string) {
		sseMu.Lock()
		defer sseMu.Unlock()
		writeSSE(w, flusher, event, data)
	}
	// Meta event first so the client can render "started run #N · pro
	// (deepseek-v4-pro, thinking=medium) — waiting for first token…"
	// even before the model produces anything. Reasoning models can take
	// 30s+ before a visible content token, and the old protocol gave the
	// UI nothing to show until then.
	emit("meta", fmt.Sprintf("run=%d trigger=%s tier=%s model=%s reasoning=%s",
		runID, spec.trigger, spec.modelKey, h.LLM.ModelFor(spec.modelKey), h.LLM.ReasoningFor(spec.modelKey)))
	for _, e := range spec.preEvents {
		emit(e.Event, e.Data)
	}

	timeout := spec.timeout
	if timeout == 0 {
		timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Heartbeat: SSE comment line every 10s until the stream returns.
	// Keeps intermediate proxies + the client from giving up on a silent
	// connection during the thinking phase. Stops when the request
	// finishes (defer close).
	hbStop := make(chan struct{})
	defer close(hbStop)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				sseMu.Lock()
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
				sseMu.Unlock()
			}
		}
	}()

	// Accumulate chunks so we can persist partial output if the client
	// disconnects (ctx cancelled before LLM completes). Reasoning tokens
	// are streamed separately so the UI can show progress, but they
	// don't roll into the final persisted text.
	var partial strings.Builder
	res, err := h.LLM.CompleteStream(ctx, spec.modelKey, spec.system, spec.user, llm.StreamHandler{
		OnContent: func(chunk string) {
			partial.WriteString(chunk)
			emit("delta", chunk)
		},
		OnReasoning: func(chunk string) {
			emit("reasoning", chunk)
		},
	})
	if err != nil {
		status := "failed"
		// Only a *client* disconnect (tab close, network drop) counts as a
		// cancellation worth preserving the partial under. That surfaces as
		// the request context being cancelled. Our own spec.timeout firing
		// is context.DeadlineExceeded on the child ctx — that's a genuine
		// failure (slow/hung model), so it stays "failed" even though we
		// still persist whatever streamed.
		canceled := r.Context().Err() != nil || errors.Is(err, context.Canceled)
		if canceled && partial.Len() > 0 {
			status = "cancelled"
		}
		if fErr := h.S.FinishRun(runID, status, partial.String(), err.Error(), 0, 0); fErr != nil {
			log.Printf("finishRun(%d): %v", runID, fErr)
		}
		// emit (not raw writeSSE) so this can't race the heartbeat goroutine,
		// which is still ticking until the deferred close(hbStop) on return.
		emit("error", spec.trigger+" failed: "+err.Error())
		return
	}
	if fErr := h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut); fErr != nil {
		log.Printf("finishRun(%d): %v", runID, fErr)
	}

	if spec.onSuccess != nil {
		endPayload, errMsg := spec.onSuccess(res.Text, runID)
		if errMsg != "" {
			emit("error", errMsg)
			return
		}
		emit("end", endPayload)
	} else {
		emit("end", string(web.RenderMarkdown(res.Text)))
	}
}

// ---- multi-agent team draft (planner → critic → drafter) ----

// streamTeamDraft runs three sequential pro passes:
//  1. planner  — produces a structured plan/outline given the current doc.
//  2. critic   — critiques the plan, names risks, suggests adjustments.
//  3. drafter  — produces the final design doc using plan + critique.
//
// Intermediate phases are persisted as artifacts so the chain is auditable;
// the drafter's output overwrites the project's design doc. The client
// stream emits "phase" SSE events between phases so the UI can re-anchor.
func (h *Handler) streamTeamDraft(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	runID, err := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: proj.ID, Valid: true}, "team-draft", proj.Title)
	if err != nil {
		log.Printf("team-draft startRun: %v", err)
		http.Error(w, "failed to record run", 500)
		return
	}
	flusher := openSSE(w)
	if flusher == nil {
		_ = h.S.FinishRun(runID, "failed", "", "streaming unsupported", 0, 0)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Minute)
	defer cancel()

	// Serialise every write — the heartbeat goroutine below and the phase /
	// delta writes both touch the ResponseWriter, which is not safe for
	// concurrent use.
	var sseMu sync.Mutex
	emit := func(event, data string) {
		sseMu.Lock()
		defer sseMu.Unlock()
		writeSSE(w, flusher, event, data)
	}
	// Keepalive every 10s. The three sequential pro passes here are the
	// longest-thinking path in the app; without keepalives an intermediate
	// proxy or the client can give up during a silent thinking phase.
	hbStop := make(chan struct{})
	defer close(hbStop)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				sseMu.Lock()
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
				sseMu.Unlock()
			}
		}
	}()

	stackCtx := h.stackContext(proj.ID)
	decCtx := h.decisionsContext(proj.ID)
	baseCtx := stackCtx + decCtx + fmt.Sprintf("Project: %s\nSummary: %s\n\nExisting doc (may be empty):\n%s\n",
		proj.Title, proj.Summary, proj.DesignDoc)

	totalIn, totalOut := 0, 0
	abort := func(phase string, err error, accum string) {
		// Persist whatever the in-flight phase managed to stream and bail.
		emit("error", phase+": "+err.Error())
		status := "failed"
		if r.Context().Err() != nil && accum != "" {
			status = "cancelled"
		}
		_ = h.S.FinishRun(runID, status, accum, phase+": "+err.Error(), totalIn, totalOut)
	}

	// Phase 1: planner
	emit("phase", "planner")
	var plan strings.Builder
	plannerRes, err := h.LLM.CompleteStream(ctx, llm.ModelProKey, systemTeamPlannerPrompt,
		baseCtx+"\nProduce a structured plan as instructed.",
		llm.StreamHandler{
			OnContent:   func(c string) { plan.WriteString(c); emit("delta", c) },
			OnReasoning: func(c string) { emit("reasoning", c) },
		})
	if err != nil {
		abort("planner", err, plan.String())
		return
	}
	totalIn += plannerRes.TokensIn
	totalOut += plannerRes.TokensOut
	if _, e := h.S.CreateArtifact(store.Artifact{ProjectID: proj.ID, Kind: "team_plan", Body: plannerRes.Text}); e != nil {
		log.Printf("team_plan artifact: %v", e)
	}

	// Phase 2: critic
	emit("phase", "critic")
	var crit strings.Builder
	criticUser := baseCtx + fmt.Sprintf("\nPlan to critique:\n\n%s\n", plannerRes.Text)
	criticRes, err := h.LLM.CompleteStream(ctx, llm.ModelProKey, systemTeamCriticPrompt, criticUser,
		llm.StreamHandler{
			OnContent:   func(c string) { crit.WriteString(c); emit("delta", c) },
			OnReasoning: func(c string) { emit("reasoning", c) },
		})
	if err != nil {
		abort("critic", err, crit.String())
		return
	}
	totalIn += criticRes.TokensIn
	totalOut += criticRes.TokensOut
	if _, e := h.S.CreateArtifact(store.Artifact{ProjectID: proj.ID, Kind: "team_critique", Body: criticRes.Text}); e != nil {
		log.Printf("team_critique artifact: %v", e)
	}

	// Phase 3: drafter
	emit("phase", "drafter")
	var draft strings.Builder
	drafterUser := baseCtx + fmt.Sprintf("\nPlanner output:\n\n%s\n\nCritic notes on the plan:\n\n%s\n\nProduce the final design doc that resolves the critic's notes and follows the agreed plan.",
		plannerRes.Text, criticRes.Text)
	drafterRes, err := h.LLM.CompleteStream(ctx, llm.ModelProKey, systemDesignDocPrompt, drafterUser,
		llm.StreamHandler{
			OnContent:   func(c string) { draft.WriteString(c); emit("delta", c) },
			OnReasoning: func(c string) { emit("reasoning", c) },
		})
	if err != nil {
		abort("drafter", err, draft.String())
		return
	}
	totalIn += drafterRes.TokensIn
	totalOut += drafterRes.TokensOut

	if err := h.S.UpdateProjectDoc(proj.ID, drafterRes.Text); err != nil {
		log.Printf("team-draft updateProjectDoc: %v", err)
	}
	if e := h.S.FinishRun(runID, "ok", drafterRes.Text, "", totalIn, totalOut); e != nil {
		log.Printf("team-draft finishRun: %v", e)
	}
	emit("end", string(web.RenderMarkdown(drafterRes.Text)))
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
		if h.OnAgentChanged != nil {
			h.OnAgentChanged()
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
	h.Background(func() {
		ctx, cancel := context.WithTimeout(h.RootCtx, 180*time.Second)
		defer cancel()

		var projID sql.NullInt64
		if agent.ProjectID.Valid {
			projID = agent.ProjectID
		}
		userPrompt := prompts.AgentUserPrompt(h.S, *agent)

		runID, _ := h.S.StartRun(sql.NullInt64{Int64: agent.ID, Valid: true}, projID, "manual", agent.SystemPrompt)
		res, err := h.LLM.Complete(ctx, agent.Model, agent.SystemPrompt, userPrompt)
		if err != nil {
			log.Printf("agent #%d run failed: %v", agent.ID, err)
			_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
			return
		}
		_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
		if _, err := h.S.CreateInboxItem(runID, firstLineSummary(res.Text, 120)); err != nil {
			log.Printf("inbox enqueue: %v", err)
		}
	})
	w.Header().Set("HX-Trigger", "agent-run-started")
	w.WriteHeader(202)
}

// ---- board ----

type boardPayload struct {
	Ideas    []boardIdeaJSON    `json:"ideas"`
	Clusters []boardClusterJSON `json:"clusters"`
	Links    []boardLinkJSON    `json:"links"`
}
type boardIdeaJSON struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	ClusterID int64  `json:"cluster_id"`
}
type boardClusterJSON struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}
type boardLinkJSON struct {
	A      int64   `json:"a"`
	B      int64   `json:"b"`
	Weight float64 `json:"weight"`
	Reason string  `json:"reason"`
}

func (h *Handler) board(w http.ResponseWriter, r *http.Request) {
	ideas, _ := h.S.ListBoardIdeas()
	h.render(w, "board", map[string]any{
		"Title":     "board",
		"IdeaCount": len(ideas),
	})
}

func (h *Handler) boardData(w http.ResponseWriter, r *http.Request) {
	ideas, _ := h.S.ListBoardIdeas()
	clusters, _ := h.S.ListClusters()
	links, _ := h.S.ListIdeaLinks()

	out := boardPayload{}
	for _, i := range ideas {
		var cid int64
		if i.ClusterID.Valid {
			cid = i.ClusterID.Int64
		}
		out.Ideas = append(out.Ideas, boardIdeaJSON{ID: i.ID, Title: i.Title, Body: i.Body, ClusterID: cid})
	}
	for _, c := range clusters {
		out.Clusters = append(out.Clusters, boardClusterJSON{ID: c.ID, Label: c.Label})
	}
	for _, l := range links {
		out.Links = append(out.Links, boardLinkJSON{A: l.A, B: l.B, Weight: l.Weight, Reason: l.Reason})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) boardCluster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	ideas, err := h.S.ListIdeas()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if len(ideas) < 2 {
		http.Error(w, "need at least 2 ideas to cluster", 400)
		return
	}

	var b strings.Builder
	b.WriteString("Ideas:\n")
	for _, i := range ideas {
		fmt.Fprintf(&b, "- id=%d title=%q body=%q\n", i.ID, i.Title, truncate(i.Body, 280))
	}
	userPrompt := b.String()

	h.streamSSE(w, r, streamSpec{
		trigger:  "cluster",
		modelKey: llm.ModelProKey,
		system:   systemClustererPrompt,
		user:     userPrompt,
		timeout:  240 * time.Second,
		onSuccess: func(text string, runID int64) (string, string) {
			parsed, err := parseClustererJSON(text)
			if err != nil {
				return "", "parse failed: " + err.Error()
			}
			if err := h.S.ReplaceClusterData(parsed.clusters, parsed.links); err != nil {
				return "", "store failed: " + err.Error()
			}
			return "ok", ""
		},
	})
}

func (h *Handler) boardSynthesize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	aID, err1 := strconv.ParseInt(r.FormValue("a"), 10, 64)
	bID, err2 := strconv.ParseInt(r.FormValue("b"), 10, 64)
	if err1 != nil || err2 != nil || aID == bID {
		http.Error(w, "need two distinct idea ids", 400)
		return
	}
	a, err := h.S.GetIdea(aID)
	if err != nil {
		http.Error(w, "idea a not found", 404)
		return
	}
	b, err := h.S.GetIdea(bID)
	if err != nil {
		http.Error(w, "idea b not found", 404)
		return
	}

	pid, err := h.S.CreateProject(store.Project{
		Title:   a.Title + " × " + b.Title,
		Summary: "synthesis of ideas #" + strconv.FormatInt(a.ID, 10) + " and #" + strconv.FormatInt(b.ID, 10),
	})
	if err != nil {
		http.Error(w, "create project: "+err.Error(), 500)
		return
	}
	userPrompt := fmt.Sprintf(
		"Two ideas have been selected for synthesis. Produce a single design doc that combines them — the goal is to find the unified project that subsumes both, not to merge them mechanically.\n\nIdea A (#%d): %s\n%s\n\nIdea B (#%d): %s\n%s\n\nThis is a fresh project with no chosen tech stack yet. In the Open questions section, list the load-bearing stack choices the author will need to make (runtime/framework/storage/deploy) — frame them as concrete trade-offs given what the ideas imply, not as a generic checklist.",
		a.ID, a.Title, a.Body, b.ID, b.Title, b.Body,
	)
	h.streamSSE(w, r, streamSpec{
		trigger:   "synthesize",
		projectID: sql.NullInt64{Int64: pid, Valid: true},
		modelKey:  llm.ModelProKey,
		system:    systemDesignDocPrompt,
		user:      userPrompt,
		timeout:   180 * time.Second,
		preEvents: []sseEvent{{Event: "project", Data: strconv.FormatInt(pid, 10)}},
		onSuccess: func(text string, runID int64) (string, string) {
			if err := h.S.UpdateProjectDoc(pid, text); err != nil {
				log.Printf("updateProjectDoc(%d): %v", pid, err)
			}
			return string(web.RenderMarkdown(text)), ""
		},
	})
}

type clustererResult struct {
	clusters []struct {
		Label   string
		IdeaIDs []int64
	}
	links []store.IdeaLink
}

func parseClustererJSON(text string) (*clustererResult, error) {
	// extract the first fenced ```json block, else first balanced {...}
	raw := extractFencedJSON(text)
	if raw == "" {
		raw = extractFirstObject(text)
	}
	if raw == "" {
		return nil, fmt.Errorf("no JSON block found")
	}
	var parsed struct {
		Clusters []struct {
			Label   string  `json:"label"`
			IdeaIDs []int64 `json:"idea_ids"`
		} `json:"clusters"`
		Edges []struct {
			A      int64   `json:"a"`
			B      int64   `json:"b"`
			Weight float64 `json:"weight"`
			Reason string  `json:"reason"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, err
	}
	out := &clustererResult{}
	for _, c := range parsed.Clusters {
		out.clusters = append(out.clusters, struct {
			Label   string
			IdeaIDs []int64
		}{Label: c.Label, IdeaIDs: c.IdeaIDs})
	}
	for _, e := range parsed.Edges {
		out.links = append(out.links, store.IdeaLink{A: e.A, B: e.B, Weight: e.Weight, Reason: e.Reason})
	}
	return out, nil
}

func extractFencedJSON(text string) string {
	const open = "```json"
	i := strings.Index(text, open)
	if i < 0 {
		return ""
	}
	rest := text[i+len(open):]
	if j := strings.Index(rest, "```"); j >= 0 {
		return strings.TrimSpace(rest[:j])
	}
	return ""
}

func extractFirstObject(text string) string {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inStr {
			if esc {
				esc = false
			} else if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

// ---- workshop floor ----

type floorTile struct {
	Project         store.Project
	LastPara        string
	LastDecision    *store.Artifact
	UnreadInbox     int
	CritiquePending bool
	StaleHours      int
	AttentionScore  float64
	Action          tileAction
}

type tileAction struct {
	Label string
	Href  string
	Class string
}

func (h *Handler) floor(w http.ResponseWriter, r *http.Request) {
	tiles := h.buildFloorTiles()
	h.render(w, "floor", map[string]any{
		"Title": "workshop floor",
		"Tiles": tiles,
	})
}

func (h *Handler) floorTiles(w http.ResponseWriter, r *http.Request) {
	tiles := h.buildFloorTiles()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.R.RenderPartial(w, "floor_tiles", map[string]any{"Tiles": tiles}); err != nil {
		log.Printf("render floor_tiles: %v", err)
		http.Error(w, "render", 500)
	}
}

func (h *Handler) buildFloorTiles() []floorTile {
	projs, err := h.S.ListProjects()
	if err != nil {
		log.Printf("floor: list projects: %v", err)
		return nil
	}
	now := time.Now()
	tiles := make([]floorTile, 0, len(projs))
	for _, p := range projs {
		t := floorTile{Project: p}
		t.LastPara = lastParagraph(p.DesignDoc, 240)
		if d, err := h.S.LatestArtifact(p.ID, "decision"); err == nil && d != nil {
			t.LastDecision = d
		}
		if n, err := h.S.UnreadInboxCountForProject(p.ID); err == nil {
			t.UnreadInbox = n
		}
		if cp, err := h.S.CritiquePending(p.ID); err == nil {
			t.CritiquePending = cp
		}
		t.StaleHours = int(now.Sub(p.UpdatedAt).Hours())
		t.AttentionScore = computeAttention(p, t, now)
		t.Action = pickAction(p, t)
		tiles = append(tiles, t)
	}
	sort.SliceStable(tiles, func(i, j int) bool {
		if tiles[i].AttentionScore != tiles[j].AttentionScore {
			return tiles[i].AttentionScore > tiles[j].AttentionScore
		}
		return tiles[i].Project.UpdatedAt.After(tiles[j].Project.UpdatedAt)
	})
	return tiles
}

// computeAttention scores how much a project is asking for the user's
// time right now. Higher = more urgent.
func computeAttention(p store.Project, t floorTile, now time.Time) float64 {
	score := 0.0
	if strings.TrimSpace(p.DesignDoc) == "" {
		score += 30
	}
	if t.CritiquePending {
		score += 25
	}
	if t.UnreadInbox > 0 {
		score += math.Min(float64(t.UnreadInbox)*8, 24)
	}
	if p.DesignDoc != "" && t.LastDecision == nil {
		score += 6
	}
	ageHours := now.Sub(p.UpdatedAt).Hours()
	if ageHours > 24 {
		score += math.Min((ageHours-24)/12, 10)
	}
	switch p.Status {
	case "drafting":
		score += 2
	case "shipped", "done":
		// archival is tracked by archived_at (and the floor already excludes
		// archived projects), so it isn't a status value here.
		score -= 15
	}
	return score
}

func pickAction(p store.Project, t floorTile) tileAction {
	href := fmt.Sprintf("/projects/%d", p.ID)
	switch {
	case t.UnreadInbox > 0:
		return tileAction{
			Label: fmt.Sprintf("inbox · %d unread", t.UnreadInbox),
			Href:  "/inbox?filter=unread",
			Class: "btn-primary",
		}
	case t.CritiquePending:
		return tileAction{Label: "apply critique →", Href: href + "#critique-panel", Class: "btn-primary"}
	case strings.TrimSpace(p.DesignDoc) == "":
		return tileAction{Label: "draft doc", Href: href, Class: "btn-primary"}
	case t.LastDecision == nil:
		return tileAction{Label: "summon devil", Href: href + "#devil-panel", Class: "btn-ghost"}
	default:
		return tileAction{Label: "open", Href: href, Class: "btn-ghost"}
	}
}

// lastParagraph plucks the last non-empty paragraph from a markdown doc,
// stripping leading header / bullet markers so the floor tile shows
// readable prose rather than a section title.
func lastParagraph(doc string, limit int) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	parts := strings.Split(doc, "\n\n")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := stripLeadMarkdown(strings.TrimSpace(parts[i]))
		if candidate == "" {
			continue
		}
		candidate = strings.ReplaceAll(candidate, "\n", " ")
		return truncate(candidate, limit)
	}
	return ""
}

func stripLeadMarkdown(s string) string {
	for {
		s = strings.TrimSpace(s)
		switch {
		case strings.HasPrefix(s, "#"):
			s = strings.TrimLeft(s, "#")
		case strings.HasPrefix(s, "- "), strings.HasPrefix(s, "* "), strings.HasPrefix(s, "+ "):
			s = s[2:]
		case strings.HasPrefix(s, ">"):
			s = strings.TrimLeft(s, ">")
		default:
			return s
		}
	}
}

// ---- inbox ----

func (h *Handler) inbox(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	items, _ := h.S.ListInboxItems(filter)
	unread, _ := h.S.UnreadInboxCount()
	h.render(w, "inbox", map[string]any{
		"Title":  "inbox",
		"Items":  items,
		"Filter": filter,
		"Unread": unread,
	})
}

func (h *Handler) inboxActions(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	item, err := h.S.GetInboxItem(id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		var prev string
		var diff template.HTML
		if item.AgentID.Valid {
			prev, _ = h.S.PreviousRunOutput(item.AgentID.Int64, item.RunID)
			if prev != "" {
				diff = web.LineDiff(prev, item.RunOutput)
			}
		}
		_ = h.S.MarkInboxRead(id)
		h.render(w, "inbox_item", map[string]any{
			"Title":   "inbox item",
			"Item":    item,
			"HasPrev": prev != "",
			"Diff":    diff,
		})
		return
	}
	if len(parts) == 3 && r.Method == http.MethodPost {
		switch parts[2] {
		case "read":
			_ = h.S.MarkInboxRead(id)
			w.WriteHeader(204)
			return
		case "star":
			_ = h.S.ToggleInboxStar(id)
			w.Header().Set("HX-Refresh", "true")
			w.WriteHeader(204)
			return
		}
	}
	http.NotFound(w, r)
}

// ---- runs ----

func (h *Handler) runs(w http.ResponseWriter, r *http.Request) {
	runs, _ := h.S.ListRuns(100)
	h.render(w, "runs", map[string]any{"Title": "runs", "Runs": runs})
}

// ---- helpers ----

func (h *Handler) render(w http.ResponseWriter, page string, data any) {
	if m, ok := data.(map[string]any); ok {
		if _, present := m["UnreadInbox"]; !present {
			if n, err := h.S.UnreadInboxCount(); err == nil {
				m["UnreadInbox"] = n
			}
		}
	}
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

// htmlEscape aliases the stdlib escaper so call sites don't churn; this
// avoids allocating a fresh strings.Replacer on every invocation.
var htmlEscape = template.HTMLEscapeString

func firstLineSummary(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(s, n)
}

// truncate clips s to n runes (not bytes) so multibyte content — CJK,
// emoji — never gets cut mid-rune into an invalid-UTF-8 glyph before "…".
func truncate(s string, n int) string {
	if len(s) <= n { // byte len ≤ n ⇒ rune count ≤ n; cheap fast path
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

const systemSeedPrompt = `You are an idea-seeding assistant. Given a rough title and optional context, output a tight Markdown sketch with: (1) one-line crystallized framing, (2) why now / who cares, (3) 3-5 angles to explore, (4) the smallest possible first slice. Keep it under 250 words.`

const systemDesignDocPrompt = `You are a senior engineer drafting a design doc. Output Markdown with: ## Problem, ## Goals, ## Non-goals, ## Approach, ## Data model, ## Open questions, ## First slice. Be concrete and brief; favor short bullets over prose. If an existing doc is provided, refine it rather than rewrite from scratch.`

const systemCritiquePrompt = `You are a senior engineer reviewing a design doc. Be skeptical but constructive. Output Markdown:

## Scorecard
A short table with rows: clarity / feasibility / risk surface / novelty / first-slice realism. Each scored 1-10 with a 5-15 word justification.

## Weaknesses
Numbered list. Each item: one-line headline (bold), then a 1-2 sentence elaboration that names the specific section or claim being critiqued.

## Concrete edits
Numbered list, cross-referenced to weaknesses by number. Each item is a specific change the author should make ("replace X with Y", "drop the bullet about Z", "add a section on...").

Keep total length under ~500 words. Never invent constraints not present in the doc; if the doc is too vague to critique a dimension, say so.`

const systemBriefPrompt = `You are a research assistant producing a structured brief for an engineering project. Output Markdown with the following sections, in order, and NOTHING else:

## Claims
Numbered list. Each item: a single load-bearing claim about the topic the project is touching, expressed as a sentence the author should believe is true. 4-8 items.

## Counter-claims
Numbered list. For each claim above, surface the strongest opposing view someone would raise, or "—" if none. Reference the claim number.

## Open questions
Numbered list. 3-6 honest "I don't know" questions the author should resolve before committing to the approach.

## Reading list
Bulleted list (use '-'). Each line: a concrete thing to read or watch — paper title, blog post, talk, RFC, source-code module — followed by a 5-15 word reason. Prefer foundational references over recency. 4-10 items. One per line, no nested sub-bullets.

Do not invent URLs you don't know exist. If you don't know a canonical reference, describe what to search for instead (e.g. "Original Raft paper by Ongaro & Ousterhout").`

const systemDevilPrompt = `You are "the devil" — a forceful interlocutor reading a project's design doc, latest critique, and the user's prior resolved decisions. Your only job is to ask ONE sharp question that forces the user to commit to a specific decision they've been deferring or quietly avoiding.

Rules:
- One question. No preamble, no critique, no "I noticed…", no options unless naming them is the question.
- Phrased to demand a concrete answer — ideally binary, trinary, or "under N chars". Make it hard to weasel out of.
- Under 220 characters total.
- Reference specific text from the doc or critique when possible ("you keep saying X — pick Y or Z").
- Be curt. The user opted into pressure.
- NEVER repeat a question that's already a resolved decision in the list.

Output the question alone, no quotes, no formatting.`

const systemPrototypePrompt = "You scaffold a minimal runnable prototype repo for a project, given its design doc and chosen stack. Output ONLY a fenced JSON block with this shape:\n\n```json\n{\n  \"files\": [\n    {\"path\": \"main.go\", \"content\": \"...\"},\n    {\"path\": \"README.md\", \"content\": \"...\"}\n  ]\n}\n```\n\nRules:\n- 3-8 files total (hard max 12)\n- paths are RELATIVE; never use a leading / or .. — no traversal\n- each file content under 4 KB\n- include: an entry point, the dependency manifest for the chosen runtime (go.mod / package.json / pyproject.toml / Cargo.toml / etc.), and a README.md explaining how to run it\n- the prototype should compile/run end-to-end on a fresh dev machine — no missing imports, no TODO placeholders that block running\n- mirror the chosen stack exactly; if the stack is empty or self-contradictory, pick the smallest sensible default and call that out in the README\n- output the JSON block and absolutely NOTHING else (no preamble, no trailing commentary)"

const systemTeamPlannerPrompt = `You are the planner on a small engineering team. Given a project's existing design notes (possibly empty), produce a structured plan that the rest of the team will use as scaffolding for the final design doc.

Output Markdown with these sections, in order:

## Frame
2-4 sentences that name the actual problem to be solved, in the author's voice. Avoid generic framings.

## Plan outline
Numbered list of 4-8 work-units. Each item: a short headline (bold) + one sentence on what it produces or proves. Sequence matters — earlier items must be doable without later ones.

## Risks
Bulleted list. 3-5 things that could derail this if not addressed early.

## Open levers
Bulleted list. 2-4 decisions the author still needs to make where the choice meaningfully changes the plan.

Keep total length under ~450 words. Do not draft the final design doc — that's the drafter's job.`

const systemTeamCriticPrompt = `You are the critic on a small engineering team. The planner has produced a plan; before the drafter writes the final design doc, you give the plan a hard read.

Output Markdown with these sections, in order:

## Verdict
One sentence: is this plan sound, partly sound, or off? Be direct.

## What's right
Bulleted list. 2-4 things the plan got right that the drafter should preserve verbatim.

## Adjustments
Numbered list. Specific changes the drafter should make: reorder a step, drop a step, expand a vague bullet, swap an assumption. Reference plan items by their headline. 3-6 items.

## Gut-check questions for the drafter
Bulleted list. 2-3 questions the drafter should be able to answer in the doc — if they can't, the plan isn't ready.

Keep total length under ~400 words. Don't redraft the plan; emit edits.`

const systemBriefReconcilePrompt = `You re-read a research brief alongside the author's notes from doing the reading. The brief was generated upfront; the notes capture what actually held up versus what didn't. Your job is to produce a tight reconciliation — what changed and what to do next.

Output Markdown with these sections, in order, and NOTHING else:

## Confirmed
Bulleted list. Claims (or supporting reasons) from the original brief that the author's notes strengthened. Quote or paraphrase the specific note that confirmed it.

## Complicated
Bulleted list. Claims that the notes weakened, contradicted, or muddied. Be specific about which note did the complicating.

## New open questions
Numbered list (start fresh — don't reuse the brief's numbering). Questions that the notes surfaced and the original brief did not anticipate. 2-5 items.

## Suggested next moves
Numbered list. Concrete things the author should do next: re-read a source, look up X, draft a section, run an experiment. 2-5 items.

Rules:
- ground every claim in a specific item from the reading log when possible — naming the item by its text or a short paraphrase
- if an item has [read] status but no note, treat its content as "consumed but unannotated" and don't fabricate what the author thought
- never invent notes the author didn't write
- keep total length under ~500 words`

const systemClustererPrompt = `You are clustering a user's open ideas to surface hidden adjacencies.

You will receive a list of ideas with numeric ids, titles, and short bodies. Produce:

1. A brief paragraph (1-3 sentences) about what jumped out — themes, surprising adjacencies, the riskiest cluster. This is for the user to read; keep it human and specific to the actual ideas given.

2. Then a single fenced JSON block (triple-backticks with json tag) with this shape:

` + "```" + `json
{
  "clusters": [
    {"label": "short 2-4 word theme", "idea_ids": [1, 3, 7]}
  ],
  "edges": [
    {"a": 1, "b": 3, "weight": 0.72, "reason": "short clause, no period"}
  ]
}
` + "```" + `

Rules:
- every idea id from the input MUST appear in exactly one cluster
- create as few clusters as the ideas honestly support (often 2-5); a singleton cluster is fine if an idea is truly orphan
- edges only between idea pairs with meaningful adjacency (weight ≥ 0.3); skip weak ones; symmetric (don't include both a→b and b→a)
- weight is a float in [0, 1]
- ids must be integers exactly as provided in the input

Output nothing else after the JSON block.`
