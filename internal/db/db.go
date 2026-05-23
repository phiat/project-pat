package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=cache_size(-20000)&_txlock=immediate",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single writer connection. With _txlock=immediate, two connections
	// racing to BEGIN can hit SQLITE_BUSY despite busy_timeout — capping
	// at one removes the contention entirely. database/sql serialises
	// callers behind this single conn, which is fine for a single-user
	// workshop and avoids any chance of write-write deadlock.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS ideas (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    title     TEXT NOT NULL,
    body      TEXT NOT NULL DEFAULT '',
    status    TEXT NOT NULL DEFAULT 'open',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS projects (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    slug          TEXT NOT NULL UNIQUE,
    title         TEXT NOT NULL,
    summary       TEXT NOT NULL DEFAULT '',
    design_doc    TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'drafting',
    from_idea     INTEGER REFERENCES ideas(id),
    stack_preset  TEXT NOT NULL DEFAULT '',
    archived_at   DATETIME,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_projects_active ON projects(archived_at) WHERE archived_at IS NULL;

CREATE TABLE IF NOT EXISTS project_stack (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    slot       TEXT NOT NULL,
    option_id  TEXT NOT NULL DEFAULT '',
    free_text  TEXT NOT NULL DEFAULT '',
    version    TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, slot)
);

CREATE TABLE IF NOT EXISTS agents (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    purpose       TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL,
    model         TEXT NOT NULL DEFAULT 'flash',
    cron          TEXT NOT NULL DEFAULT '',
    enabled       INTEGER NOT NULL DEFAULT 1,
    project_id    INTEGER REFERENCES projects(id) ON DELETE SET NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id     INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    project_id   INTEGER REFERENCES projects(id) ON DELETE SET NULL,
    trigger_kind TEXT NOT NULL DEFAULT 'manual',
    status       TEXT NOT NULL DEFAULT 'pending',
    prompt       TEXT NOT NULL DEFAULT '',
    output       TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    tokens_in    INTEGER NOT NULL DEFAULT 0,
    tokens_out   INTEGER NOT NULL DEFAULT 0,
    started_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at  DATETIME
);

CREATE TABLE IF NOT EXISTS artifacts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS inbox_items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     INTEGER NOT NULL UNIQUE REFERENCES runs(id) ON DELETE CASCADE,
    summary    TEXT NOT NULL DEFAULT '',
    starred    INTEGER NOT NULL DEFAULT 0,
    read_at    DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS idea_clusters (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    label     TEXT NOT NULL,
    position  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS idea_cluster_members (
    cluster_id INTEGER NOT NULL REFERENCES idea_clusters(id) ON DELETE CASCADE,
    idea_id    INTEGER NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
    PRIMARY KEY (cluster_id, idea_id)
);
CREATE TABLE IF NOT EXISTS idea_links (
    idea_a INTEGER NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
    idea_b INTEGER NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
    weight REAL NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (idea_a, idea_b)
);

CREATE TABLE IF NOT EXISTS brief_items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    brief_id   INTEGER NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    text       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'unread',
    note       TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_brief_items_brief ON brief_items(brief_id);

CREATE INDEX IF NOT EXISTS idx_runs_agent ON runs(agent_id);
CREATE INDEX IF NOT EXISTS idx_runs_project ON runs(project_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_project ON artifacts(project_id);
CREATE INDEX IF NOT EXISTS idx_inbox_unread ON inbox_items(read_at);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if err := renameLegacyTriggerColumn(db); err != nil {
		return err
	}
	if err := addAgentProjectColumn(db); err != nil {
		return err
	}
	if err := addProjectStackPresetColumn(db); err != nil {
		return err
	}
	return addProjectArchivedAtColumn(db)
}

func addProjectArchivedAtColumn(db *sql.DB) error {
	has, err := columnExists(db, "projects", "archived_at")
	if err != nil || has {
		return err
	}
	if _, err := db.Exec(`ALTER TABLE projects ADD COLUMN archived_at DATETIME`); err != nil {
		return err
	}
	// Partial index — only narrows the working set, doesn't break unique slugs.
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_projects_active ON projects(archived_at) WHERE archived_at IS NULL`)
	return err
}

func addProjectStackPresetColumn(db *sql.DB) error {
	has, err := columnExists(db, "projects", "stack_preset")
	if err != nil || has {
		return err
	}
	_, err = db.Exec(`ALTER TABLE projects ADD COLUMN stack_preset TEXT NOT NULL DEFAULT ''`)
	return err
}

func addAgentProjectColumn(db *sql.DB) error {
	has, err := columnExists(db, "agents", "project_id")
	if err != nil || has {
		return err
	}
	_, err = db.Exec(`ALTER TABLE agents ADD COLUMN project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL`)
	return err
}

func columnExists(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

func renameLegacyTriggerColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "trigger" {
			rows.Close()
			_, err := db.Exec(`ALTER TABLE runs RENAME COLUMN "trigger" TO trigger_kind`)
			return err
		}
	}
	return rows.Err()
}
