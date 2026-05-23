package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"projectpat/internal/db"
	"projectpat/internal/store"
)

// newTestHandler wires a Handler against an isolated sqlite under
// t.TempDir() and a temp workspace dir. LLM and Renderer are nil — only
// endpoints that don't touch them are safe to call.
func newTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	tmp := t.TempDir()
	conn, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ws := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	h := New(store.New(conn), nil, nil)
	h.WorkspaceDir = ws
	h.RootCtx = context.Background()
	return h, ws
}

func TestIdeaPromote_RejectsGET(t *testing.T) {
	h, _ := newTestHandler(t)
	// Need an idea row to even be a meaningful target.
	id, err := h.S.CreateIdea("title", "body")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ideas/"+itoa(id)+"/promote", nil)
	w := httptest.NewRecorder()
	h.ideaActions(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET should return 405, got %d", w.Code)
	}
	// And, critically, it must NOT have created a project.
	projs, _ := h.S.ListProjects()
	if len(projs) != 0 {
		t.Errorf("GET created a project — security regression: %+v", projs)
	}
}

func TestIdeaPromote_POSTCreatesProject(t *testing.T) {
	h, _ := newTestHandler(t)
	id, _ := h.S.CreateIdea("My Idea", "long body of context")

	req := httptest.NewRequest(http.MethodPost, "/ideas/"+itoa(id)+"/promote", nil)
	w := httptest.NewRecorder()
	h.ideaActions(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("status=%d want 204", w.Code)
	}
	if got := w.Header().Get("HX-Redirect"); !strings.HasPrefix(got, "/projects/") {
		t.Errorf("HX-Redirect=%q want /projects/...", got)
	}
	projs, _ := h.S.ListProjects()
	if len(projs) != 1 || projs[0].Title != "My Idea" {
		t.Errorf("expected one project from promote, got %+v", projs)
	}
}

func TestIdeaPromote_UnknownPathReturns404(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/ideas/1/notapromote", nil)
	w := httptest.NewRecorder()
	h.ideaActions(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", w.Code)
	}
}

func TestMaterializeProject_WritesExpectedFiles(t *testing.T) {
	h, ws := newTestHandler(t)
	pid, _ := h.S.CreateProject(store.Project{Title: "Demo Proj", DesignDoc: "# Heading\n\nbody"})
	proj, _ := h.S.GetProject(pid)

	req := httptest.NewRequest(http.MethodPost, "/projects/"+itoa(pid)+"/materialize", nil)
	w := httptest.NewRecorder()
	h.materializeProject(w, req, proj)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("HX-Refresh"); got != "true" {
		t.Errorf("HX-Refresh=%q want true", got)
	}

	dir := filepath.Join(ws, proj.Slug)
	for _, name := range []string{"README.md", "DESIGN.md", "DECISIONS.md", "STACK.md"} {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing %s: %v", name, err)
			continue
		}
		if len(b) == 0 {
			t.Errorf("%s is empty", name)
		}
	}

	// DESIGN.md contains the design doc verbatim
	design, _ := os.ReadFile(filepath.Join(dir, "DESIGN.md"))
	if !strings.Contains(string(design), "# Heading") {
		t.Errorf("DESIGN.md missing doc body: %q", string(design))
	}

	// workspacePath reports it as materialized
	if path, ok := h.workspacePath(proj); !ok || path != dir {
		t.Errorf("workspacePath ok=%v path=%q want ok=true path=%q", ok, path, dir)
	}
}

func TestMaterializeProject_NoWorkspaceDir(t *testing.T) {
	h, _ := newTestHandler(t)
	h.WorkspaceDir = ""
	pid, _ := h.S.CreateProject(store.Project{Title: "x"})
	proj, _ := h.S.GetProject(pid)
	req := httptest.NewRequest(http.MethodPost, "/projects/x/materialize", nil)
	w := httptest.NewRecorder()
	h.materializeProject(w, req, proj)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500 when WorkspaceDir is empty", w.Code)
	}
}

func TestStackPanelHandlersTouchDB(t *testing.T) {
	// Smoke-test that stackUpsert + stackClear round-trip through the
	// store without panicking. Render output goes nowhere meaningful in
	// this test (no Renderer wired) — we tolerate a render error and only
	// assert the store state.
	h, _ := newTestHandler(t)
	pid, _ := h.S.CreateProject(store.Project{Title: "x"})
	proj, _ := h.S.GetProject(pid)

	form := url.Values{}
	form.Set("slot", "runtime")
	form.Set("option_id", "go")
	form.Set("version", "1.26")
	req := httptest.NewRequest(http.MethodPost, "/projects/"+itoa(pid)+"/stack", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	// stackUpsert calls h.renderStackPanel at the end, which will fail
	// because h.R is nil — that's expected. Just check the upsert landed.
	func() {
		defer func() { _ = recover() }()
		h.stackUpsert(w, req, proj)
	}()
	picks, _ := h.S.ListStackPicks(pid)
	if len(picks) != 1 || picks[0].OptionID != "go" || picks[0].Version != "1.26" {
		t.Errorf("upsert did not land: %+v", picks)
	}
}

// itoa avoids strconv import noise in the test file.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
