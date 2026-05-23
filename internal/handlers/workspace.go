package handlers

import (
	"net/http"

	"projectpat/internal/store"
	"projectpat/internal/workspace"
)

// materializeProject delegates to internal/workspace so the same code
// path serves the CLI's "pat projects materialize" command.
func (h *Handler) materializeProject(w http.ResponseWriter, r *http.Request, proj *store.Project) {
	if _, err := workspace.Materialize(h.S, h.WorkspaceDir, proj); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(204)
}

// workspacePath returns the on-disk dir for the project and whether it
// has been materialized (README.md exists).
func (h *Handler) workspacePath(proj *store.Project) (string, bool) {
	return workspace.Path(h.WorkspaceDir, proj)
}
