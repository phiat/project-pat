# project-pat — common tasks. `just` with no args lists recipes.

_default:
    @just --list --unsorted

# run the http server (open http://localhost:8080)
run:
    go run ./cmd/pat server

# open the interactive REPL against the same DB
repl:
    go run ./cmd/pat repl

# pass-through to any pat subcommand: `just pat ideas list`
pat *args:
    go run ./cmd/pat {{args}}

# build the single binary to ./bin/pat
build:
    @mkdir -p bin
    go build -o bin/pat ./cmd/pat

# run all tests
test:
    go test ./...

# run tests with the race detector
test-race:
    go test -race ./...

# format + vet + build (quick sanity check before committing)
check:
    gofmt -l . | (! grep .) || (echo "gofmt issues — run \`just fmt\`" && exit 1)
    go vet ./...
    go build ./...

# format all go files in-place
fmt:
    gofmt -w .
    go mod tidy

# tidy modules
tidy:
    go mod tidy

# remove build artifacts (keeps the DB)
clean:
    rm -rf bin
