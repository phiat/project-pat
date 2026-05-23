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
	"projectpat/internal/store"
)

type Scheduler struct {
	store *store.Store
	llm   *llm.Client
	cron  *cron.Cron

	mu  sync.Mutex
	ids map[int64]cron.EntryID
}

func New(s *store.Store, l *llm.Client) *Scheduler {
	return &Scheduler{
		store: s,
		llm:   l,
		cron:  cron.New(),
		ids:   make(map[int64]cron.EntryID),
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
	ctx := sc.cron.Stop()
	<-ctx.Done()
}

func (sc *Scheduler) syncFromDB() error {
	agents, err := sc.store.ListAgents()
	if err != nil {
		return err
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// remove ids no longer present or disabled
	wanted := make(map[int64]bool)
	for _, a := range agents {
		if a.Enabled && strings.TrimSpace(a.Cron) != "" {
			wanted[a.ID] = true
		}
	}
	for id, entry := range sc.ids {
		if !wanted[id] {
			sc.cron.Remove(entry)
			delete(sc.ids, id)
		}
	}
	for _, a := range agents {
		if !wanted[a.ID] {
			continue
		}
		if _, ok := sc.ids[a.ID]; ok {
			continue
		}
		agentCopy := a
		entryID, err := sc.cron.AddFunc(a.Cron, func() { sc.runAgent(agentCopy) })
		if err != nil {
			log.Printf("scheduler: invalid cron %q for agent %s: %v", a.Cron, a.Name, err)
			continue
		}
		sc.ids[a.ID] = entryID
		log.Printf("scheduler: scheduled agent %s (#%d) on %q", a.Name, a.ID, a.Cron)
	}
	return nil
}

func (sc *Scheduler) reloadLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		if err := sc.syncFromDB(); err != nil {
			log.Printf("scheduler reload: %v", err)
		}
	}
}

func (sc *Scheduler) runAgent(a store.Agent) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	runID, _ := sc.store.StartRun(sql.NullInt64{Int64: a.ID, Valid: true}, sql.NullInt64{}, "cron", a.SystemPrompt)
	res, err := sc.llm.Complete(ctx, a.Model, a.SystemPrompt, fmt.Sprintf("Scheduled mission. Purpose: %s. Produce a concise structured report.", a.Purpose))
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
