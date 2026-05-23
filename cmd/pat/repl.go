package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// runREPL drops the user into an interactive prompt that takes the same
// commands as the top-level `pat <subcommand>` invocations. `use <id>`
// pins a sticky project context so subsequent commands that take a
// project id can omit it; `unuse` clears the context; `help` and
// `quit` / Ctrl-D do the obvious thing.
func runREPL() error {
	c, err := openCtx()
	if err != nil {
		return err
	}
	defer c.close()

	fmt.Fprintln(c.out, "pat repl. `help` for commands, `quit` or Ctrl-D to leave.")

	in := bufio.NewReader(os.Stdin)
	var stickyProject int64

	for {
		if stickyProject > 0 {
			fmt.Fprintf(c.out, "pat[#%d]> ", stickyProject)
		} else {
			fmt.Fprint(c.out, "pat> ")
		}
		line, err := in.ReadString('\n')
		if err == io.EOF {
			fmt.Fprintln(c.out)
			return nil
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		tokens, err := tokenize(line)
		if err != nil {
			fmt.Fprintln(c.out, "parse error:", err)
			continue
		}
		if len(tokens) == 0 {
			continue
		}

		cmd, args := tokens[0], tokens[1:]
		switch cmd {
		case "quit", "exit", "q":
			return nil
		case "help", "?":
			usage(os.Stdout)
			fmt.Fprintln(c.out, "\nrepl-only:")
			fmt.Fprintln(c.out, "  use <project-id>       pin a project so later commands can omit the id")
			fmt.Fprintln(c.out, "  unuse                  clear the pinned project")
			fmt.Fprintln(c.out, "  quit / exit / Ctrl-D   leave")
			continue
		case "use":
			id, err := parseID(args, "use <project-id>")
			if err != nil {
				fmt.Fprintln(c.out, err)
				continue
			}
			if _, err := c.store.GetProject(id); err != nil {
				fmt.Fprintln(c.out, "no such project:", err)
				continue
			}
			stickyProject = id
			fmt.Fprintf(c.out, "using project #%d\n", id)
			continue
		case "unuse":
			stickyProject = 0
			fmt.Fprintln(c.out, "cleared.")
			continue
		case "server":
			fmt.Fprintln(c.out, "server already implied — start it with `pat server` outside the REPL.")
			continue
		case "repl":
			fmt.Fprintln(c.out, "already in the REPL.")
			continue
		}

		// Splice the sticky project into commands that take an id.
		args = expandSticky(cmd, args, stickyProject)
		if err := dispatch(c, cmd, args); err != nil {
			fmt.Fprintln(c.out, "error:", err)
		}
	}
}

// expandSticky inserts the pinned project id where commands expect one,
// when the user hasn't supplied it. Conservative: only patches the
// well-known shapes; everything else is passed through unchanged so a
// pinned-but-irrelevant context can never cause harm.
func expandSticky(cmd string, args []string, pid int64) []string {
	if pid == 0 || len(args) == 0 {
		return args
	}
	verb := args[0]
	switch cmd {
	case "projects":
		switch verb {
		case "show", "archive", "unarchive", "materialize":
			if len(args) == 1 {
				return append(args, strconv.FormatInt(pid, 10))
			}
		case "decide":
			// decide always wants <id> "q" "a"; if no id given, splice it in
			if len(args) >= 3 {
				// looks like the user passed q + a only
				if _, err := strconv.ParseInt(args[1], 10, 64); err != nil {
					return append([]string{verb, strconv.FormatInt(pid, 10)}, args[1:]...)
				}
			}
		}
	}
	return args
}

// tokenize splits a REPL line into tokens with shell-ish quoting: double
// quotes group whitespace into a single argument and support \" escapes.
// Single quotes work the same. No globbing, no command substitution.
func tokenize(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	var quote rune
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}
