# no-js-todolist

A Todo List with **zero custom JavaScript** — built as an experiment in optimizing the agentic-coding harness, not because the world needs another todo app.

## Why this exists

Frontend testing is a nightmare for AI agents. Most modern web stacks demand a browser to verify anything that touches the UI: a headless Chrome process, a JS runtime, async event loops, retries for flaky DOM resolution, mocks for everything network-shaped. When an agent edits a React component, the loop to confirm "did this work?" runs through Playwright or Puppeteer, takes seconds per assertion, and often fails for reasons that have nothing to do with the change. The agent can't iterate fast, and humans end up babysitting.

This project pushes that complexity out of the system entirely. By delegating all UI state mutations to the server and shipping rendered HTML over the wire, every user-visible change becomes an HTTP request whose response is a string of HTML. That string can be parsed, asserted against, and verified in milliseconds — by an agent, in a pure Go test, with no browser in sight.

The goal is a tight, deterministic harness: edit → run `make check` → see green or red in under a second. Agents iterate quickly, mistakes are caught at the contract level, and the whole loop runs in CI without spinning up a headless browser.

## The design

| Constraint | Why it matters for agentic coding |
|---|---|
| **Zero custom JavaScript.** HTMX CDN only. No Alpine.js, no hyperscript, no inline `onclick`. | If there's no JS to debug, agents don't need a JS runtime to verify behavior. |
| **No JSON responses.** Handlers return rendered HTML fragments. | The response body IS the assertion target. No serialization layer to test separately. |
| **Native Go FSM** (a `switch` on `type TodoState string`). | The state machine is a pure function — trivially unit-testable, no DB needed. |
| **Classless CSS** (Pico.css v2, vendored). | Templates stay semantic. Tests assert on `button[hx-put]`, not `.btn-primary`. |
| **`httptest` + `goquery`** for all tests. | Pure Go. ~1ms per request. No browser, no Node, no JSDOM. |

The state machine is enforced at the database boundary via `UPDATE … WHERE id = ? AND status = ?` — invalid transitions return zero rows affected and surface as an HTMX out-of-band error banner. The same row partial is reused for list rendering and for per-row updates, so there's one source of truth for what a row looks like.

## Stack

- **Go** — single static binary, embeds migrations and assets.
- **Echo** (`labstack/echo/v4`) — HTTP router.
- **SQLite** (`mattn/go-sqlite3`) — single-file DB, WAL mode.
- **Goose** (`pressly/goose/v3`) — SQL migrations, embedded with `go:embed`, run on startup.
- **sqlc** — typed Go from raw SQL. No ORM.
- **`html/template`** (stdlib) — auto-escaping, no second codegen step on top of sqlc.
- **HTMX** (CDN) — the only client-side library.
- **Pico.css v2** (vendored, `go:embed`) — classless styling.

Lint and test stack: three installed binaries — `goimports` (formatting), `golangci-lint` with CLI flags only (`errcheck`, `staticcheck`, `govet`, `ineffassign`), and `govulncheck` (stdlib + dep CVEs). No `.golangci.yml`. Tests use stdlib `testing` and `goquery`.

## Running

```bash
make run                   # starts the server on :8080
make test                  # runs the full test suite (unit + e2e)
make test-unit             # in-process integration tests only
make test-e2e              # black-box HTTP user-story tests only
make cover                 # writes coverage.html + per-function table
make check                 # fmt + lint + govulncheck + test
make build                 # produces a single static binary at ./no-js-todolist
make clean                 # removes build / coverage / SQLite artifacts
```

The SQLite database is created automatically on first run; migrations execute on startup.

## Testing strategy

Two test layers, in two packages:

**`main_test.go`** — in-process integration tests (white-box, `package todolist`):

1. **FSM unit tests** — table-driven over all 9 ordered pairs of `(current, next)` states.
2. **Handler + template contract tests** — `httptest` request → `goquery` parse → assert on selectors and `hx-*` attributes (e.g., "after PUT on a Pending todo, the response contains a button with text 'Complete' and `hx-put` pointing at `/todos/{id}/progress`").
3. **Optimistic-locking guard** — directly exercises `UpdateTodoStatus` with a stale `expected_status` and asserts `rowsAffected == 0`. Verifies DB-level FSM enforcement.
4. **Cross-reference test** — fetches the rendered page shell, collects every element ID, asserts every `hx-target` referenced by any handler response resolves to an existing ID. Substitutes for browser-based DOM verification.

**`e2e/e2e_test.go`** — black-box user-story tests (`package e2e`, separate Go package — cannot import internal helpers):

5. **Full lifecycle** — add → start work → complete → reload, verify state via HTTP at every step.
6. **Progress-then-reject** — drive a todo to completed, then attempt to progress again; assert the OOB error banner returns alongside the unchanged row.
7. **Add-delete cycle** — add two todos, delete one, reload, assert only the survivor remains with its title intact.
8. **Concurrent progress race** — two simultaneous PUTs on the same Pending todo; assert exactly two valid responses and at least one success, tolerating either ordering (the optimistic-lock rejection or both succeeding in sequence are both valid outcomes).

Per-test SQLite lives in `t.TempDir()` (not `:memory:` — that breaks under `database/sql` connection pooling). No browser, no JSDOM, no Chrome binary. Full suite runs in well under two seconds.

## Project layout

See `CLAUDE.md` for the full folder structure and per-area rules. Briefly:

```
app.go           handlers.go       fsm.go          render.go         (package todolist)
main_test.go                                                         (white-box tests)
cmd/server/main.go                                                   (package main entrypoint)
e2e/e2e_test.go                                                      (package e2e black-box tests)
views/           static/           migrations/     db/ (sqlc-generated)
query.sql        sqlc.yaml         Makefile
.claude/rules/   path-scoped rule files for each subsystem
```

The root is `package todolist` so both `cmd/server` and `e2e/` can import the wiring via `todolist.NewApp(sqldb)`. The e2e package boundary physically prevents black-box tests from reaching into internal helpers.

## Acknowledged trade-offs

- **DOM-level HTMX bugs are not caught in CI.** Things like an `hx-target` that resolves to an element hidden by an earlier swap, or `hx-trigger` timing edge cases, can only be observed in a real browser. The cross-reference test catches the common class (typoed/stale target) at the contract level; the rest is accepted risk for the speed gains.
- **No real-time UI.** No WebSockets, no SSE, no client-side state. Every change is a request. For a todo list this is the right shape; for chat apps or live dashboards it isn't.
- **One-database scaling.** SQLite + auto-migrate on startup is single-process by design. Horizontal scaling would require migrating that pattern.

## License

MIT.
