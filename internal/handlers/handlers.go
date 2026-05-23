package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"projectpat/internal/llm"
	"projectpat/internal/stack"
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
	mux.HandleFunc("/inbox", h.inbox)
	mux.HandleFunc("/inbox/", h.inboxActions)
	mux.HandleFunc("/briefs/", h.briefActions)
	mux.HandleFunc("/board", h.board)
	mux.HandleFunc("/board/cluster", h.boardCluster)
	mux.HandleFunc("/board/synthesize", h.boardSynthesize)
	mux.HandleFunc("/board/data", h.boardData)
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
		critique, _ := h.S.LatestArtifact(proj.ID, "critique")
		orders, _ := h.S.ListAgentsForProject(proj.ID)
		timeline, _ := h.S.RunsForProject(proj.ID, 12)
		brief, _ := h.S.LatestArtifact(proj.ID, "brief")
		var briefItems []store.BriefItem
		if brief != nil {
			briefItems, _ = h.S.ListBriefItems(brief.ID)
		}
		stackData := h.buildStackPanelData(proj)
		h.render(w, "project_detail", map[string]any{
			"Title":      proj.Title,
			"Project":    proj,
			"Critique":   critique,
			"Orders":     orders,
			"Timeline":   timeline,
			"Brief":      brief,
			"BriefItems": briefItems,
			"Stack":      stackData,
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
		case "stack":
			h.stackUpsert(w, r, proj)
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
	picks, _ := h.S.ListStackPicks(proj.ID)
	runtime := runtimeOptionID(picks)
	incompats := stack.IncompatibleSlots(picksToCatalog(picks), runtime)
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
			} else {
				c.Empty = true
			}
			break
		}
		if incompatSet[s.Key] {
			c.Incompat = true
		}
		chips = append(chips, c)
	}

	data := stackPanelData{
		ProjectID: proj.ID,
		PresetID:  proj.StackPreset,
		Chips:     chips,
		Incompats: incompats,
		Presets:   stack.Presets,
	}
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

func picksToCatalog(picks []store.StackPick) []stack.Pick {
	out := make([]stack.Pick, 0, len(picks))
	for _, p := range picks {
		out = append(out, stack.Pick{
			Slot:     p.Slot,
			OptionID: p.OptionID,
			FreeText: p.FreeText,
			Version:  p.Version,
			Note:     p.Note,
		})
	}
	return out
}

func (h *Handler) buildStackPanelData(proj *store.Project) stackPanelData {
	picks, _ := h.S.ListStackPicks(proj.ID)
	runtime := runtimeOptionID(picks)
	incompats := stack.IncompatibleSlots(picksToCatalog(picks), runtime)
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
			} else {
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
	picks, err := h.S.ListStackPicks(projectID)
	if err != nil || len(picks) == 0 {
		return ""
	}
	return stack.FormatForPrompt(picksToCatalog(picks))
}

func (h *Handler) streamBrief(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	ctx, cancel := context.WithTimeout(r.Context(), 240*time.Second)
	defer cancel()

	topic := strings.TrimSpace(r.FormValue("topic"))
	if topic == "" {
		topic = proj.Title
	}
	userPrompt := fmt.Sprintf(
		"Project: %s\nSummary: %s\n\nTopic to research: %s\n\nProduce a research brief.",
		proj.Title, proj.Summary, topic,
	)
	runID, _ := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: proj.ID, Valid: true}, "brief", userPrompt)
	res, err := h.LLM.CompleteStream(ctx, llm.ModelProKey, systemBriefPrompt, userPrompt,
		func(chunk string) { writeSSE(w, flusher, "delta", chunk) })
	if err != nil {
		_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		writeSSE(w, flusher, "error", "brief failed: "+err.Error())
		return
	}
	_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)

	briefID, err := h.S.CreateArtifact(store.Artifact{
		ProjectID: proj.ID, Kind: "brief", Title: topic, Body: res.Text,
	})
	if err != nil {
		log.Printf("brief artifact: %v", err)
	}
	if items := web.ExtractReadingList(res.Text); len(items) > 0 && briefID > 0 {
		if err := h.S.CreateBriefItems(briefID, items); err != nil {
			log.Printf("brief items: %v", err)
		}
	}

	writeSSE(w, flusher, "end", string(web.RenderMarkdown(res.Text)))
}

func (h *Handler) briefActions(w http.ResponseWriter, r *http.Request) {
	// /briefs/{briefID}/items/{itemID}/{action}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 || parts[0] != "briefs" || parts[2] != "items" {
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
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(204)
}

func (h *Handler) streamCritique(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	if strings.TrimSpace(proj.DesignDoc) == "" {
		http.Error(w, "no design doc to critique", 400)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	ctx, cancel := context.WithTimeout(r.Context(), 240*time.Second)
	defer cancel()

	userPrompt := h.stackContext(proj.ID) + fmt.Sprintf("Project: %s\n\nDesign doc:\n\n%s", proj.Title, proj.DesignDoc)
	runID, _ := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: proj.ID, Valid: true}, "critique", userPrompt)
	res, err := h.LLM.CompleteStream(ctx, llm.ModelProKey, systemCritiquePrompt, userPrompt,
		func(chunk string) { writeSSE(w, flusher, "delta", chunk) })
	if err != nil {
		_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		writeSSE(w, flusher, "error", "critique failed: "+err.Error())
		return
	}
	_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
	if _, err := h.S.CreateArtifact(store.Artifact{ProjectID: proj.ID, Kind: "critique", Body: res.Text}); err != nil {
		log.Printf("critique store: %v", err)
	}
	writeSSE(w, flusher, "end", string(web.RenderMarkdown(res.Text)))
}

func (h *Handler) streamApplyCritique(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	crit, err := h.S.LatestArtifact(proj.ID, "critique")
	if err != nil {
		http.Error(w, "no critique to apply", 400)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	ctx, cancel := context.WithTimeout(r.Context(), 240*time.Second)
	defer cancel()

	userPrompt := h.stackContext(proj.ID) + fmt.Sprintf("Project: %s\n\nExisting design doc:\n\n%s\n\nCritique to address:\n\n%s\n\nProduce a revised design doc that addresses each numbered point in the critique. Preserve sections that the critique didn't flag.",
		proj.Title, proj.DesignDoc, crit.Body)
	runID, _ := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: proj.ID, Valid: true}, "apply-critique", userPrompt)
	res, err := h.LLM.CompleteStream(ctx, llm.ModelProKey, systemDesignDocPrompt, userPrompt,
		func(chunk string) { writeSSE(w, flusher, "delta", chunk) })
	if err != nil {
		_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		writeSSE(w, flusher, "error", "apply failed: "+err.Error())
		return
	}
	_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
	_ = h.S.UpdateProjectDoc(proj.ID, res.Text)
	writeSSE(w, flusher, "end", string(web.RenderMarkdown(res.Text)))
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

	userPrompt := h.stackContext(proj.ID) + fmt.Sprintf("Project title: %s\nSummary: %s\n\nExisting doc (may be empty):\n%s\n\nProduce or refine the design doc.",
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

		var projID sql.NullInt64
		userPrompt := fmt.Sprintf("Run mission: %s. Produce a structured report.", agent.Purpose)
		if agent.ProjectID.Valid {
			projID = agent.ProjectID
			if proj, err := h.S.GetProject(agent.ProjectID.Int64); err == nil {
				userPrompt = h.stackContext(proj.ID) + fmt.Sprintf(
					"Run mission for project %q. Purpose: %s.\n\nLive design doc:\n\n%s\n\nProduce a structured report.",
					proj.Title, agent.Purpose, proj.DesignDoc,
				)
			}
		}

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
	}()
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	ctx, cancel := context.WithTimeout(r.Context(), 240*time.Second)
	defer cancel()

	var b strings.Builder
	b.WriteString("Ideas:\n")
	for _, i := range ideas {
		fmt.Fprintf(&b, "- id=%d title=%q body=%q\n", i.ID, i.Title, truncate(i.Body, 280))
	}
	userPrompt := b.String()

	runID, _ := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{}, "cluster", userPrompt)
	res, err := h.LLM.CompleteStream(ctx, llm.ModelProKey, systemClustererPrompt, userPrompt,
		func(chunk string) { writeSSE(w, flusher, "delta", chunk) })
	if err != nil {
		_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		writeSSE(w, flusher, "error", "cluster failed: "+err.Error())
		return
	}
	_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)

	parsed, parseErr := parseClustererJSON(res.Text)
	if parseErr != nil {
		writeSSE(w, flusher, "error", "parse failed: "+parseErr.Error())
		return
	}
	if err := h.S.ReplaceClusterData(parsed.clusters, parsed.links); err != nil {
		writeSSE(w, flusher, "error", "store failed: "+err.Error())
		return
	}
	writeSSE(w, flusher, "end", "ok")
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	pid, err := h.S.CreateProject(store.Project{
		Title:   a.Title + " × " + b.Title,
		Summary: "synthesis of ideas #" + strconv.FormatInt(a.ID, 10) + " and #" + strconv.FormatInt(b.ID, 10),
	})
	if err != nil {
		writeSSE(w, flusher, "error", "create project: "+err.Error())
		return
	}
	writeSSE(w, flusher, "project", strconv.FormatInt(pid, 10))

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	userPrompt := fmt.Sprintf(
		"Two ideas have been selected for synthesis. Produce a single design doc that combines them — the goal is to find the unified project that subsumes both, not to merge them mechanically.\n\nIdea A (#%d): %s\n%s\n\nIdea B (#%d): %s\n%s\n\nThis is a fresh project with no chosen tech stack yet. In the Open questions section, list the load-bearing stack choices the author will need to make (runtime/framework/storage/deploy) — frame them as concrete trade-offs given what the ideas imply, not as a generic checklist.",
		a.ID, a.Title, a.Body, b.ID, b.Title, b.Body,
	)
	runID, _ := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: pid, Valid: true}, "synthesize", userPrompt)
	res, err := h.LLM.CompleteStream(ctx, llm.ModelProKey, systemDesignDocPrompt, userPrompt,
		func(chunk string) { writeSSE(w, flusher, "delta", chunk) })
	if err != nil {
		_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		writeSSE(w, flusher, "error", "synth failed: "+err.Error())
		return
	}
	_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
	_ = h.S.UpdateProjectDoc(pid, res.Text)
	writeSSE(w, flusher, "end", string(web.RenderMarkdown(res.Text)))
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

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", `'`, "&#39;")
	return r.Replace(s)
}

func firstLineSummary(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
