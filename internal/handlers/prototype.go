package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"projectpat/internal/llm"
	"projectpat/internal/stack"
	"projectpat/internal/store"
)

type protoFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// streamPrototype asks the pro model for a small file manifest describing
// a runnable skeleton for the project and writes the result to
// workspace/<slug>/proto/. Despite the name, this is *not* an SSE stream —
// the caller polls fetch() and renders the resulting text status. Run
// rows are recorded for auditability the same as streaming endpoints.
func (h *Handler) streamPrototype(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	if h.WorkspaceDir == "" {
		http.Error(w, "WORKSPACE_DIR not configured", 500)
		return
	}
	if strings.TrimSpace(proj.DesignDoc) == "" {
		http.Error(w, "draft a design doc first — there's nothing to scaffold from", 400)
		return
	}
	ctx, cancel := context.WithTimeout(h.RootCtx, 4*time.Minute)
	defer cancel()
	// Allow client disconnect to NOT kill the LLM call here — scaffolding
	// is expensive and partial output is useless, so we'd rather complete
	// even if the user closes the tab.
	_ = r

	picks, _ := h.S.ListStackPicks(proj.ID)
	stackList := stack.FormatForPrompt(picksToCatalog(picks))
	if stackList == "" {
		stackList = "(no stack chosen — pick the smallest sensible default given the doc)\n"
	}

	userPrompt := fmt.Sprintf(
		"Project: %s\nSummary: %s\n\n%sDesign doc:\n%s\n\nEmit the JSON manifest as instructed.",
		proj.Title, proj.Summary, stackList, proj.DesignDoc,
	)

	runID, _ := h.S.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: proj.ID, Valid: true}, "prototype", proj.Title)
	res, err := h.LLM.Complete(ctx, llm.ModelProKey, systemPrototypePrompt, userPrompt)
	if err != nil {
		_ = h.S.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		http.Error(w, "llm: "+err.Error(), 500)
		return
	}
	_ = h.S.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)

	files, err := parsePrototypeJSON(res.Text)
	if err != nil {
		http.Error(w, "parse manifest: "+err.Error(), 500)
		return
	}

	protoDir := filepath.Join(h.WorkspaceDir, proj.Slug, "proto")
	if err := os.MkdirAll(protoDir, 0o755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), 500)
		return
	}

	var written, skipped []string
	for _, f := range files {
		safe, ok := sanitizeRelPath(f.Path)
		if !ok {
			skipped = append(skipped, f.Path)
			continue
		}
		target := filepath.Join(protoDir, safe)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			skipped = append(skipped, f.Path)
			continue
		}
		if err := os.WriteFile(target, []byte(f.Content), 0o644); err != nil {
			log.Printf("prototype write %s: %v", target, err)
			skipped = append(skipped, f.Path)
			continue
		}
		written = append(written, safe)
	}
	if len(written) == 0 {
		http.Error(w, "no files written (manifest empty or unsafe)", 500)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	msg := fmt.Sprintf("%d file(s) under workspace/%s/proto/: %s",
		len(written), proj.Slug, strings.Join(written, ", "))
	if len(skipped) > 0 {
		msg += fmt.Sprintf(" · skipped %d unsafe path(s)", len(skipped))
	}
	fmt.Fprint(w, msg)
}

func parsePrototypeJSON(text string) ([]protoFile, error) {
	raw := extractFencedJSON(text)
	if raw == "" {
		raw = extractFirstObject(text)
	}
	if raw == "" {
		return nil, fmt.Errorf("no JSON block found in LLM output")
	}
	var parsed struct {
		Files []protoFile `json:"files"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Files) == 0 {
		return nil, fmt.Errorf("manifest has no files")
	}
	if len(parsed.Files) > 16 {
		return nil, fmt.Errorf("manifest has %d files; refusing (>16)", len(parsed.Files))
	}
	return parsed.Files, nil
}

// sanitizeRelPath validates that p is a safe relative path with no traversal,
// no absolute prefix, no NUL, and a reasonable length. Returns the cleaned
// path and true if safe.
func sanitizeRelPath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) {
		return "", false
	}
	if strings.HasPrefix(p, "/") || strings.ContainsRune(p, '\\') {
		return "", false
	}
	if len(p) > 200 {
		return "", false
	}
	cleaned := filepath.Clean(p)
	if cleaned == "." || cleaned == ".." {
		return "", false
	}
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return "", false
		}
	}
	return cleaned, true
}
