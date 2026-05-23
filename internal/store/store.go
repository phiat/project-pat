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
		`INSERT INTO agents(name, purpose, system_prompt, model, cron, enabled) VALUES(?,?,?,?,?,?)`,
		a.Name, a.Purpose, a.SystemPrompt, ifEmpty(a.Model, "flash"), a.Cron, boolToInt(a.Enabled),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListAgents() ([]Agent, error) {
	rows, err := s.DB.Query(`SELECT id, name, purpose, system_prompt, model, cron, enabled, created_at FROM agents ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		var enabled int
		if err := rows.Scan(&a.ID, &a.Name, &a.Purpose, &a.SystemPrompt, &a.Model, &a.Cron, &enabled, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Enabled = enabled != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAgent(id int64) (*Agent, error) {
	row := s.DB.QueryRow(`SELECT id, name, purpose, system_prompt, model, cron, enabled, created_at FROM agents WHERE id=?`, id)
	var a Agent
	var enabled int
	if err := row.Scan(&a.ID, &a.Name, &a.Purpose, &a.SystemPrompt, &a.Model, &a.Cron, &enabled, &a.CreatedAt); err != nil {
		return nil, err
	}
	a.Enabled = enabled != 0
	return &a, nil
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
