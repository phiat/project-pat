// pat is the single binary for project-pat. With no subcommand it
// prints usage; `pat server` runs the HTTP server (the historical
// default); the other subcommands are a thin CLI over the same store
// + LLM client, so you can drive the workshop from a terminal or via
// the interactive `pat repl`.
package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[pat] ")

	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "help", "-h", "--help":
		usage(os.Stdout)
		return
	case "server":
		runServer()
		return
	case "repl":
		if err := runREPL(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	default:
		if err := runCLI(cmd, args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, `pat — project-pat workshop CLI

usage:
  pat server                                start the HTTP server (open http://localhost:8080)
  pat repl                                  interactive REPL — same commands, sticky 'use' context

ideas:
  pat ideas list
  pat ideas add "title" [body]
  pat ideas promote <id>

projects:
  pat projects list [--archived]
  pat projects show <id>
  pat projects archive <id>
  pat projects unarchive <id>
  pat projects materialize <id>             write workspace/<slug>/{DESIGN,DECISIONS,STACK,...}.md
  pat projects decide <id> "question" "answer"

agents:
  pat agents list
  pat agents run <id>                       run an agent now; streams output to stdout

runs:
  pat runs list [-n 20]

inbox:
  pat inbox list [--unread]
  pat inbox show <id>

flags:
  -h / --help                               show this and exit

config:
  reads .env and the same env vars as the server (LLM_API_KEY, DB_PATH, …).
  the CLI opens the same sqlite file the server uses — sqlite WAL handles
  concurrent processes safely, so you can drive the DB while the server
  is running.`)
}
