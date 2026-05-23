// Package prompts holds prompt-building helpers shared between the HTTP
// handlers, the scheduler, and the CLI. Anything that turns DB state
// into LLM-ready prompt strings lives here so the three callers can't
// drift.
package prompts

import (
	"fmt"
	"strings"

	"projectpat/internal/stack"
	"projectpat/internal/store"
)

// Stack returns a prompt-formatted summary of a project's stack picks
// (runtime / framework / storage / deploy / …) suitable for prepending
// to a user prompt. Empty string when no picks are recorded.
func Stack(s *store.Store, projectID int64) string {
	picks, err := s.ListStackPicks(projectID)
	if err != nil || len(picks) == 0 {
		return ""
	}
	return stack.FormatForPrompt(ToCatalogPicks(picks))
}

// Decisions returns a prompt block enumerating the project's resolved
// decisions, framed as load-bearing context the model shouldn't
// relitigate. Empty string when there are no decisions.
func Decisions(s *store.Store, projectID int64) string {
	decs, err := s.ListArtifacts(projectID, "decision")
	if err != nil || len(decs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Resolved decisions (do not relitigate; treat as load-bearing context):\n")
	for _, d := range decs {
		fmt.Fprintf(&b, "- %s → %s\n", oneLine(d.Title), oneLine(d.Body))
	}
	b.WriteString("\n")
	return b.String()
}

// AgentUserPrompt builds the user-message body for an agent invocation
// — global agents get a tight mission line; project-scoped agents get
// stack context + the live design doc folded in. Used by the manual
// run-now button, the cron scheduler, and the CLI's "pat agents run".
func AgentUserPrompt(s *store.Store, agent store.Agent) string {
	base := fmt.Sprintf("Run mission: %s. Produce a structured report.", agent.Purpose)
	if !agent.ProjectID.Valid {
		return base
	}
	proj, err := s.GetProject(agent.ProjectID.Int64)
	if err != nil {
		return base
	}
	stackCtx := Stack(s, proj.ID)
	return stackCtx + fmt.Sprintf(
		"Run mission for project %q. Purpose: %s.\n\nLive design doc:\n\n%s\n\nProduce a structured report.",
		proj.Title, agent.Purpose, proj.DesignDoc,
	)
}

// ToCatalogPicks converts store rows into the stack-catalog representation.
// Exported so callers building their own user prompts can reuse the
// formatter directly.
func ToCatalogPicks(picks []store.StackPick) []stack.Pick {
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

func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}
