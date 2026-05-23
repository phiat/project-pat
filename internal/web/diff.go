package web

import (
	"fmt"
	"html/template"
	"strings"
)

// LineDiff returns a styled HTML diff between two texts. Lines are diffed
// with LCS; adjacent del/add pairs that share enough tokens get refined
// to inline word-level highlights (`<span class="diff-tok-{del,add}">`)
// so the reader can see *what* changed within a line instead of just
// scanning two strikethrough rows. Context lines outside changed runs
// are clipped with "…" separators.
func LineDiff(oldText, newText string) template.HTML {
	if oldText == "" && newText == "" {
		return template.HTML(`<p class="muted">nothing to diff.</p>`)
	}
	if oldText == newText {
		return template.HTML(`<p class="muted">no changes vs previous run.</p>`)
	}
	if oldText == "" {
		return wrapDiff(allLines(newText, "add"))
	}
	if newText == "" {
		return wrapDiff(allLines(oldText, "del"))
	}
	a := strings.Split(oldText, "\n")
	b := strings.Split(newText, "\n")
	ops := lcsDiff(a, b)
	ops = pairAndRefine(ops)
	ops = clipContext(ops, 2)
	return wrapDiff(ops)
}

type diffOp struct {
	kind string // same | add | del | gap
	line string
	// When non-empty, wrapDiff renders this as the inline content
	// (already HTML-safe) instead of escaping `line`.
	html template.HTML
}

func allLines(text, kind string) []diffOp {
	out := make([]diffOp, 0)
	for _, l := range strings.Split(text, "\n") {
		out = append(out, diffOp{kind: kind, line: l})
	}
	return out
}

func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	out := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, diffOp{kind: "same", line: a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, diffOp{kind: "del", line: a[i]})
			i++
		default:
			out = append(out, diffOp{kind: "add", line: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffOp{kind: "del", line: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffOp{kind: "add", line: b[j]})
	}
	return out
}

// clipContext keeps `ctx` same-lines around each run of changes, replacing
// long stretches of identical text with a "…" separator.
func clipContext(ops []diffOp, ctx int) []diffOp {
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind != "same" {
			lo, hi := i-ctx, i+ctx
			if lo < 0 {
				lo = 0
			}
			if hi > len(ops)-1 {
				hi = len(ops) - 1
			}
			for k := lo; k <= hi; k++ {
				keep[k] = true
			}
		}
	}
	out := make([]diffOp, 0, len(ops))
	gap := false
	for i, op := range ops {
		if keep[i] {
			out = append(out, op)
			gap = false
		} else if !gap {
			out = append(out, diffOp{kind: "gap", line: "…"})
			gap = true
		}
	}
	return out
}

// pairAndRefine walks the ops list and, whenever it sees a run of dels
// immediately followed by a run of adds, pairs them by position. Each
// paired (del, add) with sufficient token overlap is rewritten with
// inline word-level highlights; non-similar pairs and surplus dels/adds
// pass through unchanged.
func pairAndRefine(ops []diffOp) []diffOp {
	out := make([]diffOp, 0, len(ops))
	i := 0
	for i < len(ops) {
		if ops[i].kind != "del" {
			out = append(out, ops[i])
			i++
			continue
		}
		// Collect the del-run, then the immediately-following add-run.
		j := i
		for j < len(ops) && ops[j].kind == "del" {
			j++
		}
		k := j
		for k < len(ops) && ops[k].kind == "add" {
			k++
		}
		dels := ops[i:j]
		adds := ops[j:k]

		paired := len(dels)
		if len(adds) < paired {
			paired = len(adds)
		}
		for p := 0; p < paired; p++ {
			d := dels[p]
			a := adds[p]
			if similarLines(d.line, a.line) {
				dh, ah := refinePair(d.line, a.line)
				out = append(out,
					diffOp{kind: "del", line: d.line, html: dh},
					diffOp{kind: "add", line: a.line, html: ah},
				)
			} else {
				out = append(out, d, a)
			}
		}
		for p := paired; p < len(dels); p++ {
			out = append(out, dels[p])
		}
		for p := paired; p < len(adds); p++ {
			out = append(out, adds[p])
		}
		i = k
	}
	return out
}

// similarLines returns true if two lines share enough whitespace-split
// tokens to be worth a token-level refinement (Jaccard ≥ 0.35).
func similarLines(a, b string) bool {
	if a == b {
		return true
	}
	at := strings.Fields(a)
	bt := strings.Fields(b)
	if len(at) == 0 && len(bt) == 0 {
		return true
	}
	if len(at) == 0 || len(bt) == 0 {
		return false
	}
	setA := make(map[string]struct{}, len(at))
	for _, t := range at {
		setA[t] = struct{}{}
	}
	common := 0
	setB := make(map[string]struct{}, len(bt))
	for _, t := range bt {
		if _, dup := setB[t]; dup {
			continue
		}
		setB[t] = struct{}{}
		if _, ok := setA[t]; ok {
			common++
		}
	}
	denom := len(setA)
	if len(setB) > denom {
		denom = len(setB)
	}
	if denom == 0 {
		return false
	}
	return float64(common)/float64(denom) >= 0.35
}

// refinePair returns the two lines as HTML with inline diff spans on the
// changed tokens. The whitespace split simplifies token alignment; the
// downside is collapsing repeated whitespace into single spaces in the
// rendered output, which is fine for prose / Markdown LLM output.
func refinePair(oldLine, newLine string) (template.HTML, template.HTML) {
	a := strings.Fields(oldLine)
	b := strings.Fields(newLine)
	ops := tokenLCS(a, b)
	var del, add strings.Builder
	delFirst, addFirst := true, true
	for _, op := range ops {
		switch op.kind {
		case "same":
			sepWrite(&del, " ", &delFirst)
			sepWrite(&add, " ", &addFirst)
			del.WriteString(template.HTMLEscapeString(op.line))
			add.WriteString(template.HTMLEscapeString(op.line))
		case "del":
			sepWrite(&del, " ", &delFirst)
			del.WriteString(`<span class="diff-tok-del">`)
			del.WriteString(template.HTMLEscapeString(op.line))
			del.WriteString(`</span>`)
		case "add":
			sepWrite(&add, " ", &addFirst)
			add.WriteString(`<span class="diff-tok-add">`)
			add.WriteString(template.HTMLEscapeString(op.line))
			add.WriteString(`</span>`)
		}
	}
	return template.HTML(del.String()), template.HTML(add.String())
}

func sepWrite(b *strings.Builder, sep string, first *bool) {
	if *first {
		*first = false
		return
	}
	b.WriteString(sep)
}

// tokenLCS is the same LCS construction as lcsDiff but at token granularity.
func tokenLCS(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	out := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, diffOp{kind: "same", line: a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, diffOp{kind: "del", line: a[i]})
			i++
		default:
			out = append(out, diffOp{kind: "add", line: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffOp{kind: "del", line: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffOp{kind: "add", line: b[j]})
	}
	return out
}

func statsOf(ops []diffOp) (adds, dels int) {
	for _, op := range ops {
		switch op.kind {
		case "add":
			adds++
		case "del":
			dels++
		}
	}
	return
}

func wrapDiff(ops []diffOp) template.HTML {
	adds, dels := statsOf(ops)
	var b strings.Builder
	b.WriteString(`<div class="diff">`)
	if adds+dels > 0 {
		fmt.Fprintf(&b, `<div class="diff-stats"><span class="diff-stats-add">+%d</span> <span class="diff-stats-del">−%d</span></div>`, adds, dels)
	}
	for _, op := range ops {
		switch op.kind {
		case "same":
			b.WriteString(`<div class="diff-line diff-same"><span class="diff-mark"> </span>`)
		case "add":
			b.WriteString(`<div class="diff-line diff-add"><span class="diff-mark">+</span>`)
		case "del":
			b.WriteString(`<div class="diff-line diff-del"><span class="diff-mark">−</span>`)
		case "gap":
			b.WriteString(`<div class="diff-line diff-gap"><span class="diff-mark"> </span>`)
		}
		if op.html != "" {
			b.WriteString(string(op.html))
		} else {
			b.WriteString(template.HTMLEscapeString(op.line))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}
