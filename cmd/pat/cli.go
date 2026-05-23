package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"projectpat/internal/config"
	"projectpat/internal/db"
	"projectpat/internal/llm"
	"projectpat/internal/prompts"
	"projectpat/internal/store"
	"projectpat/internal/workspace"
)

// cliCtx bundles the long-lived dependencies a CLI command needs. The
// LLM client is built lazily — only commands that talk to the model
// pay the construction cost (which is just config validation).
type cliCtx struct {
	cfg   *config.Config
	store *store.Store
	llm   *llm.Client
	out   io.Writer
	close func()
}

func openCtx() (*cliCtx, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}
	client, err := llm.New(llm.Opts{
		Provider:       cfg.LLMProvider,
		APIKey:         cfg.LLMAPIKey,
		BaseURL:        cfg.LLMBaseURL,
		ModelFlash:     cfg.LLMModelFlash,
		ModelPro:       cfg.LLMModelPro,
		ReasoningFlash: cfg.LLMReasoningFlash,
		ReasoningPro:   cfg.LLMReasoningPro,
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("llm: %w", err)
	}
	return &cliCtx{
		cfg:   cfg,
		store: store.New(conn),
		llm:   client,
		out:   os.Stdout,
		close: func() { _ = conn.Close() },
	}, nil
}

// runCLI dispatches a single subcommand invocation. The "subcommand"
// here is the first positional after `pat`; the inner verbs (list/add/
// show/…) are parsed inside each handler so they can each pick their
// own flag style.
func runCLI(cmd string, args []string) error {
	c, err := openCtx()
	if err != nil {
		return err
	}
	defer c.close()
	return dispatch(c, cmd, args)
}

func dispatch(c *cliCtx, cmd string, args []string) error {
	switch cmd {
	case "ideas":
		return ideasCmd(c, args)
	case "projects":
		return projectsCmd(c, args)
	case "agents":
		return agentsCmd(c, args)
	case "runs":
		return runsCmd(c, args)
	case "inbox":
		return inboxCmd(c, args)
	}
	return fmt.Errorf("unknown command %q — try `pat help`", cmd)
}

// ---- ideas ----

func ideasCmd(c *cliCtx, args []string) error {
	verb, rest := shift(args)
	switch verb {
	case "", "list":
		ideas, err := c.store.ListIdeas()
		if err != nil {
			return err
		}
		if len(ideas) == 0 {
			fmt.Fprintln(c.out, "no ideas yet.")
			return nil
		}
		for _, i := range ideas {
			fmt.Fprintf(c.out, "#%d  %s\n", i.ID, i.Title)
			if i.Body != "" {
				fmt.Fprintf(c.out, "      %s\n", truncate(oneLine(i.Body), 100))
			}
		}
		return nil
	case "add":
		if len(rest) == 0 {
			return fmt.Errorf("usage: pat ideas add \"title\" [body]")
		}
		title := rest[0]
		body := strings.Join(rest[1:], " ")
		id, err := c.store.CreateIdea(title, body)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "created idea #%d\n", id)
		return nil
	case "promote":
		id, err := parseID(rest, "ideas promote <id>")
		if err != nil {
			return err
		}
		idea, err := c.store.GetIdea(id)
		if err != nil {
			return err
		}
		pid, err := c.store.CreateProject(store.Project{
			Title:    idea.Title,
			Summary:  truncate(idea.Body, 200),
			FromIdea: sql.NullInt64{Int64: idea.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "promoted idea #%d → project #%d\n", id, pid)
		return nil
	}
	return fmt.Errorf("unknown ideas verb %q (try: list / add / promote)", verb)
}

// ---- projects ----

func projectsCmd(c *cliCtx, args []string) error {
	verb, rest := shift(args)
	switch verb {
	case "", "list":
		archived := hasFlag(rest, "--archived")
		var projs []store.Project
		var err error
		if archived {
			projs, err = c.store.ListProjectsAll()
		} else {
			projs, err = c.store.ListProjects()
		}
		if err != nil {
			return err
		}
		if len(projs) == 0 {
			fmt.Fprintln(c.out, "no projects.")
			return nil
		}
		for _, p := range projs {
			tag := ""
			if p.ArchivedAt.Valid {
				tag = " [archived]"
			}
			fmt.Fprintf(c.out, "#%d  %s  · %s · %s%s\n", p.ID, p.Title, p.Slug, p.Status, tag)
		}
		return nil
	case "show":
		id, err := parseID(rest, "projects show <id>")
		if err != nil {
			return err
		}
		p, err := c.store.GetProject(id)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "# %s (#%d · %s · %s)\n", p.Title, p.ID, p.Slug, p.Status)
		if p.Summary != "" {
			fmt.Fprintf(c.out, "%s\n\n", p.Summary)
		}
		if p.ArchivedAt.Valid {
			fmt.Fprintf(c.out, "archived: %s\n\n", p.ArchivedAt.Time.UTC().Format(time.RFC3339))
		}
		if strings.TrimSpace(p.DesignDoc) == "" {
			fmt.Fprintln(c.out, "(no design doc yet)")
		} else {
			fmt.Fprintln(c.out, p.DesignDoc)
		}
		return nil
	case "archive":
		id, err := parseID(rest, "projects archive <id>")
		if err != nil {
			return err
		}
		if err := c.store.ArchiveProject(id); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "archived project #%d\n", id)
		return nil
	case "unarchive":
		id, err := parseID(rest, "projects unarchive <id>")
		if err != nil {
			return err
		}
		if err := c.store.UnarchiveProject(id); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "unarchived project #%d\n", id)
		return nil
	case "materialize":
		id, err := parseID(rest, "projects materialize <id>")
		if err != nil {
			return err
		}
		p, err := c.store.GetProject(id)
		if err != nil {
			return err
		}
		dir, err := workspace.Materialize(c.store, c.cfg.WorkspaceDir, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "wrote workspace artifacts to %s\n", dir)
		return nil
	case "decide":
		if len(rest) < 3 {
			return fmt.Errorf("usage: pat projects decide <id> \"question\" \"answer\"")
		}
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			return fmt.Errorf("bad id %q", rest[0])
		}
		question := rest[1]
		answer := strings.Join(rest[2:], " ")
		if _, err := c.store.CreateArtifact(store.Artifact{
			ProjectID: id, Kind: "decision", Title: question, Body: answer,
		}); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "recorded decision on project #%d\n", id)
		return nil
	}
	return fmt.Errorf("unknown projects verb %q (try: list / show / archive / unarchive / materialize / decide)", verb)
}

// ---- agents ----

func agentsCmd(c *cliCtx, args []string) error {
	verb, rest := shift(args)
	switch verb {
	case "", "list":
		agents, err := c.store.ListAgents()
		if err != nil {
			return err
		}
		if len(agents) == 0 {
			fmt.Fprintln(c.out, "no agents.")
			return nil
		}
		for _, a := range agents {
			scope := "global"
			if a.ProjectID.Valid {
				scope = fmt.Sprintf("project #%d", a.ProjectID.Int64)
			}
			cronTxt := a.Cron
			if cronTxt == "" {
				cronTxt = "(manual)"
			}
			state := "on"
			if !a.Enabled {
				state = "off"
			}
			fmt.Fprintf(c.out, "#%d  %s  · %s · %s · %s · %s\n", a.ID, a.Name, scope, a.Model, cronTxt, state)
			if a.Purpose != "" {
				fmt.Fprintf(c.out, "      %s\n", truncate(a.Purpose, 100))
			}
		}
		return nil
	case "run":
		id, err := parseID(rest, "agents run <id>")
		if err != nil {
			return err
		}
		agent, err := c.store.GetAgent(id)
		if err != nil {
			return err
		}
		userPrompt := prompts.AgentUserPrompt(c.store, *agent)
		var projID sql.NullInt64
		if agent.ProjectID.Valid {
			projID = agent.ProjectID
		}
		runID, _ := c.store.StartRun(sql.NullInt64{Int64: agent.ID, Valid: true}, projID, "cli", agent.SystemPrompt)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := c.llm.CompleteStream(ctx, agent.Model, agent.SystemPrompt, userPrompt, llm.StreamHandler{
			OnContent: func(chunk string) { fmt.Fprint(c.out, chunk) },
		})
		fmt.Fprintln(c.out)
		if err != nil {
			_ = c.store.FinishRun(runID, "failed", "", err.Error(), 0, 0)
			return fmt.Errorf("agent run failed: %w", err)
		}
		_ = c.store.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
		if _, err := c.store.CreateInboxItem(runID, firstLine(res.Text, 120)); err != nil {
			// non-fatal; just note
			fmt.Fprintf(os.Stderr, "warn: inbox enqueue failed: %v\n", err)
		}
		fmt.Fprintf(c.out, "\n→ run #%d  ok  %d in / %d out\n", runID, res.TokensIn, res.TokensOut)
		return nil
	}
	return fmt.Errorf("unknown agents verb %q (try: list / run)", verb)
}

// ---- runs ----

func runsCmd(c *cliCtx, args []string) error {
	verb, rest := shift(args)
	switch verb {
	case "", "list":
		n := 20
		for i := 0; i < len(rest)-1; i++ {
			if rest[i] == "-n" {
				if v, err := strconv.Atoi(rest[i+1]); err == nil {
					n = v
				}
			}
		}
		runs, err := c.store.ListRuns(n)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			fmt.Fprintln(c.out, "no runs.")
			return nil
		}
		for _, r := range runs {
			agent := r.AgentName
			if agent == "" {
				agent = "ad-hoc"
			}
			finished := "·"
			if r.FinishedAt.Valid {
				finished = r.FinishedAt.Time.UTC().Format("15:04:05")
			}
			fmt.Fprintf(c.out, "#%-5d %s  %-10s %-12s %s  in=%d out=%d\n",
				r.ID, r.StartedAt.UTC().Format("2006-01-02 15:04"),
				r.Status, r.TriggerKind, agent, r.TokensIn, r.TokensOut)
			if r.Error != "" {
				fmt.Fprintf(c.out, "      err: %s\n", truncate(oneLine(r.Error), 100))
			}
			_ = finished
		}
		return nil
	}
	return fmt.Errorf("unknown runs verb %q (try: list)", verb)
}

// ---- inbox ----

func inboxCmd(c *cliCtx, args []string) error {
	verb, rest := shift(args)
	switch verb {
	case "", "list":
		filter := ""
		if hasFlag(rest, "--unread") {
			filter = "unread"
		} else if hasFlag(rest, "--starred") {
			filter = "starred"
		}
		items, err := c.store.ListInboxItems(filter)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(c.out, "inbox empty.")
			return nil
		}
		for _, it := range items {
			star := " "
			if it.Starred {
				star = "★"
			}
			read := "•"
			if !it.ReadAt.Valid {
				read = "○"
			}
			agent := it.AgentName
			if agent == "" {
				agent = "ad-hoc"
			}
			fmt.Fprintf(c.out, "%s %s #%-4d %s  %s\n", read, star, it.ID, agent, truncate(it.Summary, 80))
		}
		return nil
	case "show":
		id, err := parseID(rest, "inbox show <id>")
		if err != nil {
			return err
		}
		it, err := c.store.GetInboxItem(id)
		if err != nil {
			return err
		}
		agent := it.AgentName
		if agent == "" {
			agent = "ad-hoc"
		}
		fmt.Fprintf(c.out, "inbox #%d · %s · %s · run #%d\n\n", it.ID, agent, it.RunStatus, it.RunID)
		fmt.Fprintln(c.out, it.RunOutput)
		_ = c.store.MarkInboxRead(id)
		return nil
	}
	return fmt.Errorf("unknown inbox verb %q (try: list / show)", verb)
}

// ---- small helpers ----

func shift(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func parseID(args []string, usage string) (int64, error) {
	if len(args) == 0 {
		return 0, fmt.Errorf("usage: pat %s", usage)
	}
	v, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad id %q", args[0])
	}
	return v, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(s, n)
}
