package web

import (
	"html/template"
	"strings"
)

// LineDiff is a simple LCS-based line diff producing HTML with classed
// rows for additions, deletions, and context. Context lines are clipped
// to a small window around changes.
func LineDiff(oldText, newText string) template.HTML {
	if oldText == "" && newText == "" {
		return template.HTML(`<p class="muted">nothing to diff.</p>`)
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
	return wrapDiff(clipContext(ops, 2))
}

type diffOp struct {
	kind string // same | add | del
	line string
}

func allLines(text, kind string) []diffOp {
	out := make([]diffOp, 0)
	for _, l := range strings.Split(text, "\n") {
		out = append(out, diffOp{kind, l})
	}
	return out
}

func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// dp[i][j] = LCS length of a[i:] and b[j:]
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
			out = append(out, diffOp{"same", a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, diffOp{"del", a[i]})
			i++
		default:
			out = append(out, diffOp{"add", b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffOp{"del", a[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffOp{"add", b[j]})
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
			out = append(out, diffOp{"gap", "…"})
			gap = true
		}
	}
	return out
}

func wrapDiff(ops []diffOp) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="diff">`)
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
		b.WriteString(template.HTMLEscapeString(op.line))
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

