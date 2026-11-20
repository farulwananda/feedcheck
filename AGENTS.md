# AGENTS.md — feedcheck

`feedcheck`: RSS, Atom, and JSON Feed health inspector (freshness, validity, response time). Open-source Go CLI.

## How to work

1. **Think before coding.** State your assumptions. If there are several interpretations, name them instead of picking one silently. Ask only when the answer cannot be found in the code or the repo docs; if it can be inferred, proceed.
2. **Simplicity first.** Write the minimum code that solves the request. No features, abstractions, or configuration that were not asked for. If 200 lines could be 50, rewrite it.
3. **Surgical changes.** Touch only what the task needs. Don't "improve" adjacent code, comments, or formatting. Match the existing style. If you notice unrelated dead code, mention it; don't delete it. Remove only the imports/variables your own change made unused.
4. **Goal-driven execution.** Define a check for "done": for a bug, write a test that reproduces it, then make it pass. For multi-step work, state a short plan as `step -> verify: check`. If you cannot run the tests, say so; never claim they passed.

## Code structure

- Entry point in `cmd/feedcheck/`, logic in `internal/`, fixtures in `testdata/`. Keep packages small and follow the existing layout.
- Prefer the standard library; add a dependency only when it removes real complexity, and say why.
- Tests use local fixtures in `testdata/`, not live feeds.

## This project

- Go 1.27. Before finishing run what CI runs: `go vet ./...`, `go test -race ./...`, and `go build ./cmd/feedcheck`. Format with `gofmt`.
- Releases are built by `.github/workflows/release.yml` from tags. Don't create tags or releases unless asked.
