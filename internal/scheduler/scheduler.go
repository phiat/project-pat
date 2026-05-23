package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"projectpat/internal/llm"
	"projectpat/internal/stack"
	"projectpat/internal/store"
)

type scheduled struct {
	entryID cron.EntryID
	cron    string
}

type Scheduler struct {
	store *store.Store
	llm   *llm.Client
	cron  *cron.Cron

	mu     sync.Mutex
	ids    map[int64]scheduled
	notify chan struct{}
	done   chan struct{}

	// RootCtx is the parent for cron-fired agent goroutines so they stop
	// when the server shuts down.
	RootCtx context.Context
}

func New(s *store.Store, l *llm.Client) *Scheduler {
	return &Scheduler{
		store:   s,
		llm:     l,
		cron:    cron.New(),
		ids:     make(map[int64]scheduled),
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		RootCtx: context.Background(),
	}
}

func (sc *Scheduler) Start() error {
	if err := sc.syncFromDB(); err != nil {
		return err
	}
	sc.cron.Start()
	go sc.reloadLoop()
	return nil
}

func (sc *Scheduler) Stop() {
	close(sc.done)
	ctx := sc.cron.Stop()
	<-ctx.Done()
}

// Reload pings the scheduler to re-read agents from the DB now. Non-blocking;
// if a reload is already queued, this is a no-op.
func (sc *Scheduler) Reload() {
	select {
	case sc.notify <- struct{}{}:
	default:
	}
}

func (sc *Scheduler) syncFromDB() error {
	agents, err := sc.store.ListAgents()
	if err != nil {
		return err
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// build the want-set
	wanted := make(map[int64]store.Agent, len(agents))
	for _, a := range agents {
		if a.Enabled && strings.TrimSpace(a.Cron) != "" {
			wanted[a.ID] = a
		}
	}
	// drop entries no longer present, disabled, or whose cron changed
	for id, sched := range sc.ids {
		a, keep := wanted[id]
		if !keep || a.Cron != sched.cron {
			sc.cron.Remove(sched.entryID)
			delete(sc.ids, id)
			if !keep {
				log.Printf("scheduler: unscheduled agent #%d", id)
			} else {
				log.Printf("scheduler: rescheduling agent #%d (cron changed)", id)
			}
		}
	}
	// add any newly-wanted entries
	for id, a := range wanted {
		if _, ok := sc.ids[id]; ok {
			continue
		}
		agentCopy := a
		entryID, err := sc.cron.AddFunc(a.Cron, func() { sc.runAgent(agentCopy) })
		if err != nil {
			log.Printf("scheduler: invalid cron %q for agent %s: %v", a.Cron, a.Name, err)
			continue
		}
		sc.ids[id] = scheduled{entryID: entryID, cron: a.Cron}
		log.Printf("scheduler: scheduled agent %s (#%d) on %q", a.Name, a.ID, a.Cron)
	}
	return nil
}

// reloadLoop reacts to explicit Reload() pings (e.g. after agent CRUD)
// and to a slower backstop ticker.
func (sc *Scheduler) reloadLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-sc.done:
			return
		case <-sc.notify:
		case <-t.C:
		}
		if err := sc.syncFromDB(); err != nil {
			log.Printf("scheduler reload: %v", err)
		}
	}
}

func (sc *Scheduler) runAgent(a store.Agent) {
	ctx, cancel := context.WithTimeout(sc.RootCtx, 5*time.Minute)
	defer cancel()

	var projID sql.NullInt64
	userPrompt := fmt.Sprintf("Scheduled mission. Purpose: %s. Produce a concise structured report.", a.Purpose)
	if a.ProjectID.Valid {
		projID = a.ProjectID
		if proj, err := sc.store.GetProject(a.ProjectID.Int64); err == nil {
			picks, _ := sc.store.ListStackPicks(proj.ID)
			stackCtx := stack.FormatForPrompt(picksToCatalog(picks))
			userPrompt = stackCtx + fmt.Sprintf(
				"Scheduled mission for project %q. Purpose: %s.\n\nLive design doc:\n\n%s\n\nProduce a concise structured report relevant to the project's current state.",
				proj.Title, a.Purpose, proj.DesignDoc,
			)
		}
	}

	runID, _ := sc.store.StartRun(sql.NullInt64{Int64: a.ID, Valid: true}, projID, "cron", a.SystemPrompt)
	res, err := sc.llm.Complete(ctx, a.Model, a.SystemPrompt, userPrompt)
	if err != nil {
		log.Printf("scheduler: agent %s failed: %v", a.Name, err)
		_ = sc.store.FinishRun(runID, "failed", "", err.Error(), 0, 0)
		return
	}
	_ = sc.store.FinishRun(runID, "ok", res.Text, "", res.TokensIn, res.TokensOut)
	if _, err := sc.store.CreateInboxItem(runID, firstLine(res.Text, 120)); err != nil {
		log.Printf("scheduler: inbox enqueue failed: %v", err)
	}
}

func picksToCatalog(picks []store.StackPick) []stack.Pick {
	out := make([]stack.Pick, 0, len(picks))
	for _, p := range picks {
		out = append(out, stack.Pick{
			Slot:     p.Slot,
			OptionID: p.OptionID,
			FreeText: p.FreeText,
			Version:  p.Version,
			Note:     p.Note,
		})
	}
	return out
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
