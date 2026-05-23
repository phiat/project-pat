package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"projectpat/internal/stack"
	"projectpat/internal/store"
)

// materializeProject writes the project's current state to disk under
// workspace/<slug>/ as a set of Markdown files. Existing files are
// overwritten so the on-disk view is always a snapshot of the DB.
func (h *Handler) materializeProject(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	if h.WorkspaceDir == "" {
		http.Error(w, "WORKSPACE_DIR not configured", 500)
		return
	}
	if err := h.writeProjectArtifacts(proj); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(204)
}

func (h *Handler) writeProjectArtifacts(proj *store.Project) error {
	dir := filepath.Join(h.WorkspaceDir, proj.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	decisions, _ := h.S.ListArtifacts(proj.ID, "decision")
	critique, _ := h.S.LatestArtifact(proj.ID, "critique")
	brief, _ := h.S.LatestArtifact(proj.ID, "brief")
	briefRecon, _ := h.S.LatestArtifact(proj.ID, "brief_recon")
	var briefItems []store.BriefItem
	if brief != nil {
		briefItems, _ = h.S.ListBriefItems(brief.ID)
	}
	picks, _ := h.S.ListStackPicks(proj.ID)

	files := map[string]string{
		"README.md":    buildWorkspaceReadme(proj, decisions, critique, brief),
		"DESIGN.md":    buildWorkspaceDesign(proj),
		"DECISIONS.md": buildWorkspaceDecisions(decisions),
		"STACK.md":     buildWorkspaceStack(picks),
	}
	if critique != nil {
		files["CRITIQUE.md"] = buildWorkspaceCritique(*critique)
	}
	if brief != nil {
		files["BRIEF.md"] = buildWorkspaceBrief(*brief, briefItems)
	}
	if briefRecon != nil && brief != nil && briefRecon.CreatedAt.After(brief.CreatedAt) {
		files["BRIEF_RECON.md"] = fmt.Sprintf("# Reconciliation — %s\n\n_recorded: %s_\n\n%s\n",
			briefRecon.Title, briefRecon.CreatedAt.UTC().Format(time.RFC3339), briefRecon.Body)
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// workspacePath returns the absolute-ish path to the project's workspace
// directory and a bool indicating whether at least one materialised file
// (README.md) exists.
func (h *Handler) workspacePath(proj *store.Project) (string, bool) {
	if h.WorkspaceDir == "" || proj == nil {
		return "", false
	}
	dir := filepath.Join(h.WorkspaceDir, proj.Slug)
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		return dir, true
	}
	return dir, false
}

func buildWorkspaceReadme(p *store.Project, decs []store.Artifact, crit, brief *store.Artifact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", p.Title)
	if p.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", p.Summary)
	}
	fmt.Fprintf(&b, "_materialized: %s · status: %s_\n\n", time.Now().UTC().Format(time.RFC3339), p.Status)
	b.WriteString("## index\n\n")
	b.WriteString("- [DESIGN.md](DESIGN.md) — design doc\n")
	if crit != nil {
		b.WriteString("- [CRITIQUE.md](CRITIQUE.md) — latest critique\n")
	}
	if brief != nil {
		b.WriteString("- [BRIEF.md](BRIEF.md) — latest research brief\n")
	}
	if len(decs) > 0 {
		fmt.Fprintf(&b, "- [DECISIONS.md](DECISIONS.md) — %d resolved decision(s)\n", len(decs))
	} else {
		b.WriteString("- [DECISIONS.md](DECISIONS.md) — (none yet)\n")
	}
	b.WriteString("- [STACK.md](STACK.md) — current stack picks\n")
	return b.String()
}

func buildWorkspaceDesign(p *store.Project) string {
	if strings.TrimSpace(p.DesignDoc) == "" {
		return "# Design — " + p.Title + "\n\n_(no doc yet — draft via the workshop UI)_\n"
	}
	return p.DesignDoc
}

func buildWorkspaceDecisions(decs []store.Artifact) string {
	if len(decs) == 0 {
		return "# Decisions\n\n_(none yet)_\n"
	}
	var b strings.Builder
	b.WriteString("# Decisions\n\n")
	for _, d := range decs {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n_recorded: %s_\n\n", d.Title, d.Body, d.CreatedAt.UTC().Format(time.RFC3339))
	}
	return b.String()
}

func buildWorkspaceCritique(a store.Artifact) string {
	return fmt.Sprintf("# Critique\n\n_recorded: %s_\n\n%s\n", a.CreatedAt.UTC().Format(time.RFC3339), a.Body)
}

func buildWorkspaceBrief(a store.Artifact, items []store.BriefItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Research brief — %s\n\n_recorded: %s_\n\n", a.Title, a.CreatedAt.UTC().Format(time.RFC3339))
	b.WriteString(a.Body)
	if len(items) > 0 {
		b.WriteString("\n\n## reading list (with notes)\n\n")
		for _, it := range items {
			marker := "[ ]"
			switch it.Status {
			case "read":
				marker = "[x]"
			case "reading":
				marker = "[~]"
			}
			fmt.Fprintf(&b, "- %s %s", marker, it.Text)
			if it.Note != "" {
				fmt.Fprintf(&b, " — _%s_", it.Note)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func buildWorkspaceStack(picks []store.StackPick) string {
	if len(picks) == 0 {
		return "# Stack\n\n_(no picks yet)_\n"
	}
	var b strings.Builder
	b.WriteString("# Stack\n\n")
	for _, p := range picks {
		slot := p.Slot
		if s, ok := stack.SlotByKey(p.Slot); ok {
			slot = s.Label
		}
		value := p.FreeText
		if p.OptionID != "" {
			if o, ok := stack.OptionByID(p.OptionID); ok {
				value = o.Label
			} else {
				value = p.OptionID
			}
		}
		fmt.Fprintf(&b, "- **%s**: %s", slot, value)
		if p.Version != "" {
			fmt.Fprintf(&b, " · `%s`", p.Version)
		}
		if p.Note != "" {
			fmt.Fprintf(&b, " — _%s_", p.Note)
		}
		b.WriteString("\n")
	}
	return b.String()
}
