// Package workspace materializes a project's live DB state to disk as
// a folder of Markdown files under <dir>/<slug>/. It's used by both the
// HTTP handler (a button on project_detail.html) and the CLI ("pat
// projects materialize"), so the logic lives here rather than in either
// caller.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"projectpat/internal/stack"
	"projectpat/internal/store"
)

// Materialize writes the project's current state to <dir>/<proj.Slug>/
// as a set of Markdown files. Existing files are overwritten so the
// on-disk view is always a snapshot of the DB. Returns the directory
// that was written to.
func Materialize(s *store.Store, dir string, proj *store.Project) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("workspace dir not configured")
	}
	if proj == nil {
		return "", fmt.Errorf("nil project")
	}
	target := filepath.Join(dir, proj.Slug)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	decisions, _ := s.ListArtifacts(proj.ID, "decision")
	critique, _ := s.LatestArtifact(proj.ID, "critique")
	brief, _ := s.LatestArtifact(proj.ID, "brief")
	briefRecon, _ := s.LatestArtifact(proj.ID, "brief_recon")
	var briefItems []store.BriefItem
	if brief != nil {
		briefItems, _ = s.ListBriefItems(brief.ID)
	}
	picks, _ := s.ListStackPicks(proj.ID)

	files := map[string]string{
		"README.md":    buildReadme(proj, decisions, critique, brief),
		"DESIGN.md":    buildDesign(proj),
		"DECISIONS.md": buildDecisions(decisions),
		"STACK.md":     buildStack(picks),
	}
	if critique != nil {
		files["CRITIQUE.md"] = buildCritique(*critique)
	}
	if brief != nil {
		files["BRIEF.md"] = buildBrief(*brief, briefItems)
	}
	if briefRecon != nil && brief != nil && briefRecon.CreatedAt.After(brief.CreatedAt) {
		files["BRIEF_RECON.md"] = fmt.Sprintf("# Reconciliation — %s\n\n_recorded: %s_\n\n%s\n",
			briefRecon.Title, briefRecon.CreatedAt.UTC().Format(time.RFC3339), briefRecon.Body)
	}
	for name, content := range files {
		path := filepath.Join(target, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", name, err)
		}
	}
	return target, nil
}

// Path returns the on-disk path for the project's workspace and a bool
// indicating whether the directory has been materialized (README.md
// presence is the canonical marker).
func Path(dir string, proj *store.Project) (string, bool) {
	if dir == "" || proj == nil {
		return "", false
	}
	target := filepath.Join(dir, proj.Slug)
	if _, err := os.Stat(filepath.Join(target, "README.md")); err == nil {
		return target, true
	}
	return target, false
}

func buildReadme(p *store.Project, decs []store.Artifact, crit, brief *store.Artifact) string {
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

func buildDesign(p *store.Project) string {
	if strings.TrimSpace(p.DesignDoc) == "" {
		return "# Design — " + p.Title + "\n\n_(no doc yet — draft via the workshop UI)_\n"
	}
	return p.DesignDoc
}

func buildDecisions(decs []store.Artifact) string {
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

func buildCritique(a store.Artifact) string {
	return fmt.Sprintf("# Critique\n\n_recorded: %s_\n\n%s\n", a.CreatedAt.UTC().Format(time.RFC3339), a.Body)
}

func buildBrief(a store.Artifact, items []store.BriefItem) string {
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

func buildStack(picks []store.StackPick) string {
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
