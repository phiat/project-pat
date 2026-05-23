package store

import (
	"database/sql"
	"regexp"
	"strings"
	"time"
)

type Store struct{ DB *sql.DB }

func New(db *sql.DB) *Store { return &Store{DB: db} }

type Idea struct {
	ID        int64
	Title     string
	Body      string
	Status    string
	CreatedAt time.Time
}

type Project struct {
	ID         int64
	Slug       string
	Title      string
	Summary    string
	DesignDoc  string
	Status     string
	FromIdea   sql.NullInt64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Agent struct {
	ID           int64
	Name         string
	Purpose      string
	SystemPrompt string
	Model        string
	Cron         string
	Enabled      bool
	ProjectID    sql.NullInt64
	CreatedAt    time.Time
}

type Run struct {
	ID         int64
	AgentID    sql.NullInt64
	AgentName  string
	ProjectID  sql.NullInt64
	TriggerKind string
	Status     string
	Prompt     string
	Output     string
	Error      string
	TokensIn   int
	TokensOut  int
	StartedAt  time.Time
	FinishedAt sql.NullTime
}

// Ideas

func (s *Store) CreateIdea(title, body string) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO ideas(title, body) VALUES(?,?)`, title, body)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListIdeas() ([]Idea, error) {
	rows, err := s.DB.Query(`SELECT id, title, body, status, created_at FROM ideas ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Idea
	for rows.Next() {
		var i Idea
		if err := rows.Scan(&i.ID, &i.Title, &i.Body, &i.Status, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) GetIdea(id int64) (*Idea, error) {
	row := s.DB.QueryRow(`SELECT id, title, body, status, created_at FROM ideas WHERE id=?`, id)
	var i Idea
	if err := row.Scan(&i.ID, &i.Title, &i.Body, &i.Status, &i.CreatedAt); err != nil {
		return nil, err
	}
	return &i, nil
}

// Projects

func (s *Store) CreateProject(p Project) (int64, error) {
	if p.Slug == "" {
		p.Slug = Slugify(p.Title)
	}
	res, err := s.DB.Exec(
		`INSERT INTO projects(slug, title, summary, design_doc, status, from_idea) VALUES(?,?,?,?,?,?)`,
		p.Slug, p.Title, p.Summary, p.DesignDoc, ifEmpty(p.Status, "drafting"), p.FromIdea,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.DB.Query(`SELECT id, slug, title, summary, design_doc, status, from_idea, created_at, updated_at FROM projects ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Summary, &p.DesignDoc, &p.Status, &p.FromIdea, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProject(id int64) (*Project, error) {
	row := s.DB.QueryRow(`SELECT id, slug, title, summary, design_doc, status, from_idea, created_at, updated_at FROM projects WHERE id=?`, id)
	var p Project
	if err := row.Scan(&p.ID, &p.Slug, &p.Title, &p.Summary, &p.DesignDoc, &p.Status, &p.FromIdea, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) UpdateProjectDoc(id int64, designDoc string) error {
	_, err := s.DB.Exec(`UPDATE projects SET design_doc=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, designDoc, id)
	return err
}

// Agents

func (s *Store) CreateAgent(a Agent) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO agents(name, purpose, system_prompt, model, cron, enabled, project_id) VALUES(?,?,?,?,?,?,?)`,
		a.Name, a.Purpose, a.SystemPrompt, ifEmpty(a.Model, "flash"), a.Cron, boolToInt(a.Enabled), a.ProjectID,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListAgents() ([]Agent, error) {
	rows, err := s.DB.Query(`SELECT id, name, purpose, system_prompt, model, cron, enabled, project_id, created_at FROM agents ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		var enabled int
		if err := rows.Scan(&a.ID, &a.Name, &a.Purpose, &a.SystemPrompt, &a.Model, &a.Cron, &enabled, &a.ProjectID, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Enabled = enabled != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListAgentsForProject(projectID int64) ([]Agent, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, purpose, system_prompt, model, cron, enabled, project_id, created_at
         FROM agents WHERE project_id = ? ORDER BY id DESC`, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		var enabled int
		if err := rows.Scan(&a.ID, &a.Name, &a.Purpose, &a.SystemPrompt, &a.Model, &a.Cron, &enabled, &a.ProjectID, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Enabled = enabled != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAgent(id int64) (*Agent, error) {
	row := s.DB.QueryRow(`SELECT id, name, purpose, system_prompt, model, cron, enabled, project_id, created_at FROM agents WHERE id=?`, id)
	var a Agent
	var enabled int
	if err := row.Scan(&a.ID, &a.Name, &a.Purpose, &a.SystemPrompt, &a.Model, &a.Cron, &enabled, &a.ProjectID, &a.CreatedAt); err != nil {
		return nil, err
	}
	a.Enabled = enabled != 0
	return &a, nil
}

// RunsForProject returns recent runs filtered by project_id (via agent or
// direct project association). Used for the project-detail timeline.
func (s *Store) RunsForProject(projectID int64, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.Query(`
        SELECT r.id, r.agent_id, COALESCE(a.name,''), r.project_id, r.trigger_kind, r.status,
               r.prompt, r.output, r.error, r.tokens_in, r.tokens_out, r.started_at, r.finished_at
        FROM runs r LEFT JOIN agents a ON a.id = r.agent_id
        WHERE r.project_id = ? OR (a.project_id = ?)
        ORDER BY r.id DESC LIMIT ?`, projectID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.AgentID, &r.AgentName, &r.ProjectID, &r.TriggerKind, &r.Status,
			&r.Prompt, &r.Output, &r.Error, &r.TokensIn, &r.TokensOut, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Runs

func (s *Store) StartRun(agentID sql.NullInt64, projectID sql.NullInt64, trigger, prompt string) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO runs(agent_id, project_id, trigger_kind, status, prompt) VALUES(?,?,?,?,?)`,
		agentID, projectID, trigger, "running", prompt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishRun(id int64, status, output, errMsg string, tokIn, tokOut int) error {
	_, err := s.DB.Exec(
		`UPDATE runs SET status=?, output=?, error=?, tokens_in=?, tokens_out=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`,
		status, output, errMsg, tokIn, tokOut, id,
	)
	return err
}

func (s *Store) ListRuns(limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(`
        SELECT r.id, r.agent_id, COALESCE(a.name,''), r.project_id, r.trigger_kind, r.status,
               r.prompt, r.output, r.error, r.tokens_in, r.tokens_out, r.started_at, r.finished_at
        FROM runs r LEFT JOIN agents a ON a.id = r.agent_id
        ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.AgentID, &r.AgentName, &r.ProjectID, &r.TriggerKind, &r.Status,
			&r.Prompt, &r.Output, &r.Error, &r.TokensIn, &r.TokensOut, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Artifacts

type Artifact struct {
	ID        int64
	ProjectID int64
	Kind      string
	Title     string
	Body      string
	CreatedAt time.Time
}

func (s *Store) CreateArtifact(a Artifact) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO artifacts(project_id, kind, title, body) VALUES(?,?,?,?)`,
		a.ProjectID, a.Kind, a.Title, a.Body,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListArtifacts(projectID int64, kind string) ([]Artifact, error) {
	rows, err := s.DB.Query(
		`SELECT id, project_id, kind, title, body, created_at FROM artifacts
         WHERE project_id=? AND kind=? ORDER BY id DESC`,
		projectID, kind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Kind, &a.Title, &a.Body, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) LatestArtifact(projectID int64, kind string) (*Artifact, error) {
	row := s.DB.QueryRow(
		`SELECT id, project_id, kind, title, body, created_at FROM artifacts
         WHERE project_id=? AND kind=? ORDER BY id DESC LIMIT 1`,
		projectID, kind,
	)
	var a Artifact
	if err := row.Scan(&a.ID, &a.ProjectID, &a.Kind, &a.Title, &a.Body, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// Inbox

type InboxItem struct {
	ID        int64
	RunID     int64
	Summary   string
	Starred   bool
	ReadAt    sql.NullTime
	CreatedAt time.Time
	// joined from runs + agents for list rendering
	AgentID    sql.NullInt64
	AgentName  string
	RunStatus  string
	RunOutput  string
	StartedAt  time.Time
}

func (s *Store) CreateInboxItem(runID int64, summary string) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT INTO inbox_items(run_id, summary) VALUES(?,?)`,
		runID, summary,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UnreadInboxCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE read_at IS NULL`).Scan(&n)
	return n, err
}

func (s *Store) ListInboxItems(filter string) ([]InboxItem, error) {
	var where string
	switch filter {
	case "unread":
		where = "WHERE i.read_at IS NULL"
	case "starred":
		where = "WHERE i.starred = 1"
	}
	rows, err := s.DB.Query(`
        SELECT i.id, i.run_id, i.summary, i.starred, i.read_at, i.created_at,
               r.agent_id, COALESCE(a.name,''), r.status, r.output, r.started_at
        FROM inbox_items i
        JOIN runs r ON r.id = i.run_id
        LEFT JOIN agents a ON a.id = r.agent_id
        ` + where + `
        ORDER BY i.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxItem
	for rows.Next() {
		var i InboxItem
		var starred int
		if err := rows.Scan(&i.ID, &i.RunID, &i.Summary, &starred, &i.ReadAt, &i.CreatedAt,
			&i.AgentID, &i.AgentName, &i.RunStatus, &i.RunOutput, &i.StartedAt); err != nil {
			return nil, err
		}
		i.Starred = starred != 0
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) GetInboxItem(id int64) (*InboxItem, error) {
	row := s.DB.QueryRow(`
        SELECT i.id, i.run_id, i.summary, i.starred, i.read_at, i.created_at,
               r.agent_id, COALESCE(a.name,''), r.status, r.output, r.started_at
        FROM inbox_items i
        JOIN runs r ON r.id = i.run_id
        LEFT JOIN agents a ON a.id = r.agent_id
        WHERE i.id = ?`, id)
	var i InboxItem
	var starred int
	if err := row.Scan(&i.ID, &i.RunID, &i.Summary, &starred, &i.ReadAt, &i.CreatedAt,
		&i.AgentID, &i.AgentName, &i.RunStatus, &i.RunOutput, &i.StartedAt); err != nil {
		return nil, err
	}
	i.Starred = starred != 0
	return &i, nil
}

func (s *Store) MarkInboxRead(id int64) error {
	_, err := s.DB.Exec(`UPDATE inbox_items SET read_at = CURRENT_TIMESTAMP WHERE id=? AND read_at IS NULL`, id)
	return err
}

func (s *Store) ToggleInboxStar(id int64) error {
	_, err := s.DB.Exec(`UPDATE inbox_items SET starred = 1 - starred WHERE id=?`, id)
	return err
}

// Brief items

type BriefItem struct {
	ID        int64
	BriefID   int64
	Text      string
	Status    string
	Note      string
	Position  int
	CreatedAt time.Time
}

func (s *Store) CreateBriefItems(briefID int64, texts []string) error {
	if len(texts) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO brief_items(brief_id, text, position) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, t := range texts {
		if _, err := stmt.Exec(briefID, t, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListBriefItems(briefID int64) ([]BriefItem, error) {
	rows, err := s.DB.Query(
		`SELECT id, brief_id, text, status, note, position, created_at
         FROM brief_items WHERE brief_id=? ORDER BY position ASC`, briefID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BriefItem
	for rows.Next() {
		var b BriefItem
		if err := rows.Scan(&b.ID, &b.BriefID, &b.Text, &b.Status, &b.Note, &b.Position, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) GetBriefItem(id int64) (*BriefItem, error) {
	row := s.DB.QueryRow(
		`SELECT id, brief_id, text, status, note, position, created_at FROM brief_items WHERE id=?`, id,
	)
	var b BriefItem
	if err := row.Scan(&b.ID, &b.BriefID, &b.Text, &b.Status, &b.Note, &b.Position, &b.CreatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) UpdateBriefItemStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE brief_items SET status=? WHERE id=?`, status, id)
	return err
}

func (s *Store) UpdateBriefItemNote(id int64, note string) error {
	_, err := s.DB.Exec(`UPDATE brief_items SET note=? WHERE id=?`, note, id)
	return err
}

// Idea board (clusters + links)

type Cluster struct {
	ID    int64
	Label string
}

type IdeaLink struct {
	A      int64
	B      int64
	Weight float64
	Reason string
}

type BoardIdea struct {
	ID        int64
	Title     string
	Body      string
	ClusterID sql.NullInt64
}

// ReplaceClusterData wipes prior cluster + link snapshots and writes the
// supplied clustering atomically.
func (s *Store) ReplaceClusterData(clusters []struct {
	Label   string
	IdeaIDs []int64
}, links []IdeaLink) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM idea_cluster_members`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM idea_clusters`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM idea_links`); err != nil {
		return err
	}
	cstmt, err := tx.Prepare(`INSERT INTO idea_clusters(label, position) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer cstmt.Close()
	mstmt, err := tx.Prepare(`INSERT INTO idea_cluster_members(cluster_id, idea_id) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer mstmt.Close()
	for i, c := range clusters {
		res, err := cstmt.Exec(c.Label, i)
		if err != nil {
			return err
		}
		cid, _ := res.LastInsertId()
		for _, ideaID := range c.IdeaIDs {
			if _, err := mstmt.Exec(cid, ideaID); err != nil {
				return err
			}
		}
	}
	lstmt, err := tx.Prepare(`INSERT OR REPLACE INTO idea_links(idea_a, idea_b, weight, reason) VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer lstmt.Close()
	for _, l := range links {
		a, b := l.A, l.B
		if a > b {
			a, b = b, a
		}
		if a == b {
			continue
		}
		if _, err := lstmt.Exec(a, b, l.Weight, l.Reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListClusters() ([]Cluster, error) {
	rows, err := s.DB.Query(`SELECT id, label FROM idea_clusters ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cluster
	for rows.Next() {
		var c Cluster
		if err := rows.Scan(&c.ID, &c.Label); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListBoardIdeas() ([]BoardIdea, error) {
	rows, err := s.DB.Query(`
        SELECT i.id, i.title, i.body, m.cluster_id
        FROM ideas i
        LEFT JOIN idea_cluster_members m ON m.idea_id = i.id
        ORDER BY i.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BoardIdea
	for rows.Next() {
		var b BoardIdea
		if err := rows.Scan(&b.ID, &b.Title, &b.Body, &b.ClusterID); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) ListIdeaLinks() ([]IdeaLink, error) {
	rows, err := s.DB.Query(`SELECT idea_a, idea_b, weight, reason FROM idea_links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdeaLink
	for rows.Next() {
		var l IdeaLink
		if err := rows.Scan(&l.A, &l.B, &l.Weight, &l.Reason); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PreviousRunOutput returns the output of the most recent prior successful
// run by the same agent, or "" if none. Used for inbox diff.
func (s *Store) PreviousRunOutput(agentID, beforeRunID int64) (string, error) {
	row := s.DB.QueryRow(`
        SELECT output FROM runs
        WHERE agent_id = ? AND id < ? AND status = 'ok'
        ORDER BY id DESC LIMIT 1`, agentID, beforeRunID)
	var s2 string
	err := row.Scan(&s2)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s2, err
}

// helpers

func ifEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "untitled"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}
