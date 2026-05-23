package web

import (
	"strings"
	"testing"
)

func TestLineDiff_BothEmpty(t *testing.T) {
	got := string(LineDiff("", ""))
	if !strings.Contains(got, "nothing to diff") {
		t.Errorf("got=%q want 'nothing to diff' panel", got)
	}
}

func TestLineDiff_Identical(t *testing.T) {
	got := string(LineDiff("alpha\nbeta", "alpha\nbeta"))
	if !strings.Contains(got, "no changes") {
		t.Errorf("identical inputs should report no changes; got=%q", got)
	}
}

func TestLineDiff_OnlyAdditions(t *testing.T) {
	got := string(LineDiff("", "line one\nline two"))
	if !strings.Contains(got, "diff-add") {
		t.Errorf("expected diff-add rows; got=%q", got)
	}
	if !strings.Contains(got, "+2") {
		t.Errorf("expected +2 in stats; got=%q", got)
	}
}

func TestLineDiff_PureLineReplacement(t *testing.T) {
	// Lines with no token overlap → no refinement; classic del+add rows.
	old := "the quick brown fox"
	now := "a tortoise meanders sideways"
	got := string(LineDiff(old, now))
	if strings.Contains(got, "diff-tok-add") || strings.Contains(got, "diff-tok-del") {
		t.Errorf("dissimilar lines should NOT get tok-level refinement; got=%q", got)
	}
	if !strings.Contains(got, "diff-add") || !strings.Contains(got, "diff-del") {
		t.Errorf("expected add+del rows; got=%q", got)
	}
}

func TestLineDiff_SimilarLineRefined(t *testing.T) {
	// Most tokens shared → expect inline tok highlights.
	old := "alpha beta gamma delta epsilon"
	now := "alpha beta GAMMA delta epsilon"
	got := string(LineDiff(old, now))
	if !strings.Contains(got, `<span class="diff-tok-del">gamma</span>`) {
		t.Errorf("expected inline tok-del on 'gamma'; got=%q", got)
	}
	if !strings.Contains(got, `<span class="diff-tok-add">GAMMA</span>`) {
		t.Errorf("expected inline tok-add on 'GAMMA'; got=%q", got)
	}
	if !strings.Contains(got, "+1") || !strings.Contains(got, "−1") {
		t.Errorf("expected +1/−1 stats; got=%q", got)
	}
}

func TestLineDiff_StatsHeaderOmittedWhenNoChange(t *testing.T) {
	got := string(LineDiff("a", "a"))
	if strings.Contains(got, "diff-stats") {
		t.Errorf("identical inputs shouldn't render stats header; got=%q", got)
	}
}

func TestSimilarLines(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"alpha beta gamma", "alpha beta gamma", true},
		{"alpha beta gamma delta", "alpha beta gamma omega", true},
		{"x y z", "totally other words", false},
		{"", "", true},
		{"", "anything", false},
		// Single-token replacement in a 4-token line: shared 3/4 = 0.75
		{"the quick brown fox", "the quick brown dog", true},
		// One token in common out of many: below 0.35
		{"foo a b c d e", "foo q r s t u", false},
	}
	for _, c := range cases {
		if got := similarLines(c.a, c.b); got != c.want {
			t.Errorf("similarLines(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestRefinePair_AlignmentSanity(t *testing.T) {
	dh, ah := refinePair("the quick brown fox jumps", "the slow brown fox jumps")
	if !strings.Contains(string(dh), `<span class="diff-tok-del">quick</span>`) {
		t.Errorf("del side wrong: %q", dh)
	}
	if !strings.Contains(string(ah), `<span class="diff-tok-add">slow</span>`) {
		t.Errorf("add side wrong: %q", ah)
	}
	// Kept tokens appear in both, un-spanned
	for _, side := range []string{string(dh), string(ah)} {
		for _, w := range []string{"the", "brown", "fox", "jumps"} {
			if !strings.Contains(side, w) {
				t.Errorf("kept token %q missing from %q", w, side)
			}
		}
	}
}

func TestPairAndRefine_HandlesUnpairedRuns(t *testing.T) {
	// 3 dels, 1 add: should pair 1, leave 2 dels alone
	ops := []diffOp{
		{kind: "del", line: "alpha beta"},
		{kind: "del", line: "two"},
		{kind: "del", line: "three"},
		{kind: "add", line: "alpha BETA"},
	}
	out := pairAndRefine(ops)
	adds, dels := statsOf(out)
	if adds != 1 || dels != 3 {
		t.Errorf("stats wrong: adds=%d dels=%d want 1/3", adds, dels)
	}
	// the first del-add pair should be refined (similar)
	foundRefined := false
	for _, op := range out {
		if op.html != "" {
			foundRefined = true
			break
		}
	}
	if !foundRefined {
		t.Errorf("expected first pair to be refined")
	}
}
