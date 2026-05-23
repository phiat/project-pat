package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"projectpat/internal/db"
)

// newTestStore opens an isolated sqlite under t.TempDir() and runs the
// production migrations. Closing happens via t.Cleanup.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn)
}

func TestIdeaCRUD(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateIdea("title", "body")
	if err != nil {
		t.Fatalf("CreateIdea: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}
	got, err := s.GetIdea(id)
	if err != nil {
		t.Fatalf("GetIdea: %v", err)
	}
	if got.Title != "title" || got.Body != "body" {
		t.Errorf("got=%+v", got)
	}
	if got.Status != "open" {
		t.Errorf("default status=%q want open", got.Status)
	}
	list, err := s.ListIdeas()
	if err != nil {
		t.Fatalf("ListIdeas: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Errorf("list=%+v", list)
	}
}

func TestIdeaListOrdersDescByID(t *testing.T) {
	s := newTestStore(t)
	for _, n := range []string{"a", "b", "c"} {
		if _, err := s.CreateIdea(n, ""); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.ListIdeas()
	if len(list) != 3 {
		t.Fatalf("want 3 got %d", len(list))
	}
	if list[0].Title != "c" || list[2].Title != "a" {
		t.Errorf("ordering wrong: %+v", list)
	}
}

func TestProjectCRUDAndSlug(t *testing.T) {
	s := newTestStore(t)
	pid, err := s.CreateProject(Project{Title: "Some Project", Summary: "s"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got, err := s.GetProject(pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Slug != "some-project" {
		t.Errorf("auto slug=%q want some-project", got.Slug)
	}
	if got.Status != "drafting" {
		t.Errorf("default status=%q want drafting", got.Status)
	}
	if err := s.UpdateProjectDoc(pid, "# new doc"); err != nil {
		t.Fatalf("UpdateProjectDoc: %v", err)
	}
	got, _ = s.GetProject(pid)
	if got.DesignDoc != "# new doc" {
		t.Errorf("doc not updated: %q", got.DesignDoc)
	}
}

func TestArchiveAndUnarchive(t *testing.T) {
	s := newTestStore(t)
	pid, _ := s.CreateProject(Project{Title: "active"})

	got, _ := s.GetProject(pid)
	if got.ArchivedAt.Valid {
		t.Errorf("fresh project should not be archived: %+v", got.ArchivedAt)
	}

	if err := s.ArchiveProject(pid); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	got, _ = s.GetProject(pid)
	if !got.ArchivedAt.Valid {
		t.Errorf("ArchiveProject did not stamp archived_at")
	}

	// active list excludes it
	for _, p := range mustList(t, s.ListProjects) {
		if p.ID == pid {
			t.Errorf("archived project still in ListProjects()")
		}
	}
	// archived list includes it
	if !containsProject(mustList(t, s.ListArchivedProjects), pid) {
		t.Errorf("archived project missing from ListArchivedProjects()")
	}
	// all list includes it
	if !containsProject(mustList(t, s.ListProjectsAll), pid) {
		t.Errorf("archived project missing from ListProjectsAll()")
	}

	if err := s.UnarchiveProject(pid); err != nil {
		t.Fatalf("UnarchiveProject: %v", err)
	}
	got, _ = s.GetProject(pid)
	if got.ArchivedAt.Valid {
		t.Errorf("UnarchiveProject did not clear archived_at: %+v", got.ArchivedAt)
	}
}

func mustList(t *testing.T, fn func() ([]Project, error)) []Project {
	t.Helper()
	ps, err := fn()
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func containsProject(ps []Project, id int64) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}

func TestProjectSlugUniqueRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateProject(Project{Title: "foo"}); err != nil {
		t.Fatal(err)
	}
	// second insert with same generated slug should fail (UNIQUE constraint)
	if _, err := s.CreateProject(Project{Title: "foo"}); err == nil {
		t.Errorf("expected UNIQUE violation on duplicate slug")
	}
}

func TestAgentCRUDAndScoping(t *testing.T) {
	s := newTestStore(t)
	pid, _ := s.CreateProject(Project{Title: "scope"})

	// Note: CreateAgent persists Enabled as-passed. The HTTP layer always
	// sets Enabled:true on form submit, so we mirror that here.
	globalID, err := s.CreateAgent(Agent{Name: "global", SystemPrompt: "p", Enabled: true})
	if err != nil {
		t.Fatalf("global agent: %v", err)
	}
	scopedID, err := s.CreateAgent(Agent{
		Name: "scoped", SystemPrompt: "p", Enabled: true,
		ProjectID: sql.NullInt64{Int64: pid, Valid: true},
	})
	if err != nil {
		t.Fatalf("scoped agent: %v", err)
	}

	all, _ := s.ListAgents()
	if len(all) != 2 {
		t.Errorf("ListAgents got %d want 2", len(all))
	}
	scoped, _ := s.ListAgentsForProject(pid)
	if len(scoped) != 1 || scoped[0].ID != scopedID {
		t.Errorf("ListAgentsForProject=%+v want only #%d", scoped, scopedID)
	}

	g, err := s.GetAgent(globalID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Enabled != true || g.Model != "flash" {
		t.Errorf("agent defaults wrong: %+v", g)
	}
}

func TestRunsAndArtifacts(t *testing.T) {
	s := newTestStore(t)
	pid, _ := s.CreateProject(Project{Title: "p"})

	runID, err := s.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: pid, Valid: true}, "draft", "user prompt")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := s.FinishRun(runID, "ok", "output text", "", 10, 20); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	runs, _ := s.ListRuns(10)
	if len(runs) != 1 || runs[0].Status != "ok" || runs[0].TokensIn != 10 {
		t.Errorf("ListRuns=%+v", runs)
	}

	if _, err := s.CreateArtifact(Artifact{ProjectID: pid, Kind: "critique", Body: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateArtifact(Artifact{ProjectID: pid, Kind: "critique", Body: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateArtifact(Artifact{ProjectID: pid, Kind: "brief", Body: "B"}); err != nil {
		t.Fatal(err)
	}

	crits, _ := s.ListArtifacts(pid, "critique")
	if len(crits) != 2 || crits[0].Body != "second" {
		t.Errorf("critique list wrong (newest first): %+v", crits)
	}
	latest, err := s.LatestArtifact(pid, "critique")
	if err != nil || latest == nil || latest.Body != "second" {
		t.Errorf("LatestArtifact=%+v err=%v", latest, err)
	}

	// LatestArtifact for a kind with nothing returns sql.ErrNoRows-style nil
	if got, err := s.LatestArtifact(pid, "nonexistent"); err == nil && got != nil {
		t.Errorf("expected miss for missing kind, got %+v", got)
	}
}

func TestStackPickUpsert(t *testing.T) {
	s := newTestStore(t)
	pid, _ := s.CreateProject(Project{Title: "p"})

	if err := s.UpsertStackPick(StackPick{ProjectID: pid, Slot: "runtime", OptionID: "go", Version: "1.26"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertStackPick(StackPick{ProjectID: pid, Slot: "runtime", OptionID: "go", Version: "1.27", Note: "bumped"}); err != nil {
		t.Fatal(err)
	}
	picks, _ := s.ListStackPicks(pid)
	if len(picks) != 1 {
		t.Fatalf("upsert should leave one row, got %d", len(picks))
	}
	if picks[0].Version != "1.27" || picks[0].Note != "bumped" {
		t.Errorf("upsert did not update: %+v", picks[0])
	}

	if err := s.ClearStackSlot(pid, "runtime"); err != nil {
		t.Fatal(err)
	}
	picks, _ = s.ListStackPicks(pid)
	if len(picks) != 0 {
		t.Errorf("clear did not delete: %+v", picks)
	}
}

func TestBriefItemsBatch(t *testing.T) {
	s := newTestStore(t)
	pid, _ := s.CreateProject(Project{Title: "p"})
	briefID, _ := s.CreateArtifact(Artifact{ProjectID: pid, Kind: "brief", Body: "b"})

	if err := s.CreateBriefItems(briefID, []string{"alpha", "beta", "gamma"}); err != nil {
		t.Fatalf("CreateBriefItems: %v", err)
	}
	items, _ := s.ListBriefItems(briefID)
	if len(items) != 3 || items[0].Text != "alpha" || items[2].Text != "gamma" {
		t.Errorf("items wrong order/contents: %+v", items)
	}

	if err := s.UpdateBriefItemStatus(items[1].ID, "read"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateBriefItemNote(items[1].ID, "weak"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetBriefItem(items[1].ID)
	if got.Status != "read" || got.Note != "weak" {
		t.Errorf("update lost: %+v", got)
	}

	// CreateBriefItems on empty slice is a no-op, not an error
	if err := s.CreateBriefItems(briefID, nil); err != nil {
		t.Errorf("empty CreateBriefItems: %v", err)
	}
}

func TestInboxFlow(t *testing.T) {
	s := newTestStore(t)
	runID, _ := s.StartRun(sql.NullInt64{}, sql.NullInt64{}, "manual", "p")
	_ = s.FinishRun(runID, "ok", "out", "", 0, 0)

	id, err := s.CreateInboxItem(runID, "summary line")
	if err != nil {
		t.Fatalf("CreateInboxItem: %v", err)
	}
	n, _ := s.UnreadInboxCount()
	if n != 1 {
		t.Errorf("unread count=%d want 1", n)
	}
	if err := s.MarkInboxRead(id); err != nil {
		t.Fatal(err)
	}
	n, _ = s.UnreadInboxCount()
	if n != 0 {
		t.Errorf("after read unread=%d want 0", n)
	}

	if err := s.ToggleInboxStar(id); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetInboxItem(id)
	if !got.Starred {
		t.Errorf("expected starred")
	}
	_ = s.ToggleInboxStar(id)
	got, _ = s.GetInboxItem(id)
	if got.Starred {
		t.Errorf("toggle off failed")
	}
}

func TestUnreadInboxCountForProject(t *testing.T) {
	s := newTestStore(t)
	pid, _ := s.CreateProject(Project{Title: "p"})
	agentID, _ := s.CreateAgent(Agent{Name: "a", SystemPrompt: "x", ProjectID: sql.NullInt64{Int64: pid, Valid: true}})

	// run tied via agent's project_id
	r1, _ := s.StartRun(sql.NullInt64{Int64: agentID, Valid: true}, sql.NullInt64{}, "cron", "p")
	_ = s.FinishRun(r1, "ok", "o", "", 0, 0)
	_, _ = s.CreateInboxItem(r1, "via agent")

	// run tied directly via run's project_id
	r2, _ := s.StartRun(sql.NullInt64{}, sql.NullInt64{Int64: pid, Valid: true}, "manual", "p")
	_ = s.FinishRun(r2, "ok", "o", "", 0, 0)
	_, _ = s.CreateInboxItem(r2, "direct")

	// unrelated run
	r3, _ := s.StartRun(sql.NullInt64{}, sql.NullInt64{}, "manual", "p")
	_ = s.FinishRun(r3, "ok", "o", "", 0, 0)
	_, _ = s.CreateInboxItem(r3, "unrelated")

	n, err := s.UnreadInboxCountForProject(pid)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("project unread=%d want 2", n)
	}
}

func TestCritiquePending(t *testing.T) {
	s := newTestStore(t)
	pid, _ := s.CreateProject(Project{Title: "p"})

	if pending, _ := s.CritiquePending(pid); pending {
		t.Errorf("fresh project should not have pending critique")
	}
	_, _ = s.CreateArtifact(Artifact{ProjectID: pid, Kind: "critique", Body: "c"})
	// SQLite CURRENT_TIMESTAMP has 1-second resolution; the project and
	// artifact are created in the same second under the test loop so we
	// rewind the project's updated_at to make the comparison meaningful.
	if _, err := s.DB.Exec(`UPDATE projects SET updated_at = datetime(updated_at, '-2 seconds') WHERE id = ?`, pid); err != nil {
		t.Fatal(err)
	}
	pending, err := s.CritiquePending(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Errorf("expected pending after artifact create (with rewound project ts)")
	}
	// Touching the doc must reset updated_at to now (> artifact.created_at)
	// and consume the pending flag.
	if err := s.UpdateProjectDoc(pid, "new"); err != nil {
		t.Fatal(err)
	}
	pending, err = s.CritiquePending(pid)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Errorf("expected NOT pending after doc update")
	}
}

func TestPreviousRunOutput(t *testing.T) {
	s := newTestStore(t)
	agentID, _ := s.CreateAgent(Agent{Name: "a", SystemPrompt: "x"})

	r1, _ := s.StartRun(sql.NullInt64{Int64: agentID, Valid: true}, sql.NullInt64{}, "cron", "p")
	_ = s.FinishRun(r1, "ok", "first", "", 0, 0)
	r2, _ := s.StartRun(sql.NullInt64{Int64: agentID, Valid: true}, sql.NullInt64{}, "cron", "p")
	_ = s.FinishRun(r2, "ok", "second", "", 0, 0)
	r3, _ := s.StartRun(sql.NullInt64{Int64: agentID, Valid: true}, sql.NullInt64{}, "cron", "p")
	_ = s.FinishRun(r3, "failed", "broken", "err", 0, 0)

	prev, err := s.PreviousRunOutput(agentID, r3)
	if err != nil {
		t.Fatal(err)
	}
	if prev != "second" {
		t.Errorf("prev=%q want second (latest ok before r3)", prev)
	}

	// no prior runs at all → empty string, no error
	other, _ := s.CreateAgent(Agent{Name: "b", SystemPrompt: "x"})
	rx, _ := s.StartRun(sql.NullInt64{Int64: other, Valid: true}, sql.NullInt64{}, "cron", "p")
	_ = s.FinishRun(rx, "ok", "first-and-only", "", 0, 0)
	prev2, err := s.PreviousRunOutput(other, rx)
	if err != nil || prev2 != "" {
		t.Errorf("first-ever run prev=%q err=%v want empty/nil", prev2, err)
	}
}

func TestReplaceClusterDataNormalisesAB(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		_, _ = s.CreateIdea("i", "")
	}
	clusters := []struct {
		Label   string
		IdeaIDs []int64
	}{
		{Label: "alpha", IdeaIDs: []int64{1, 2}},
		{Label: "beta", IdeaIDs: []int64{3}},
	}
	// Pass an edge with a>b — store should normalise to (min, max) so the
	// idea_links primary key never collides.
	links := []IdeaLink{
		{A: 3, B: 1, Weight: 0.5, Reason: "x"},
		{A: 2, B: 3, Weight: 0.4, Reason: "y"},
		{A: 5, B: 5, Weight: 0.9, Reason: "self-loop"}, // should be dropped
	}
	if err := s.ReplaceClusterData(clusters, links); err != nil {
		t.Fatalf("ReplaceClusterData: %v", err)
	}
	got, _ := s.ListIdeaLinks()
	if len(got) != 2 {
		t.Errorf("expected 2 links (self-loop dropped), got %d: %+v", len(got), got)
	}
	for _, l := range got {
		if l.A >= l.B {
			t.Errorf("link not normalised: %+v", l)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Simple Title", "simple-title"},
		{"  spaces  around  ", "spaces-around"},
		{"weird $$$ chars!!", "weird-chars"},
		{"emoji 🤖 stays-out", "emoji-stays-out"},
		{"---only-dashes---", "only-dashes"},
		{"", "untitled"},
		{"!!!", "untitled"},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q)=%q want %q", c.in, got, c.want)
		}
	}
	long := "abcdefghij" // 10 chars
	for i := 0; i < 10; i++ {
		long += "abcdefghij"
	}
	if got := Slugify(long); len(got) > 60 {
		t.Errorf("long slug not truncated: len=%d", len(got))
	}
}
