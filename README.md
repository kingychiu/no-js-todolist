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

Lint and test stack: one binary (`golangci-lint` with CLI flags only — `errcheck`, `staticcheck`, `govet`, `ineffassign`, `goimports`) plus `govulncheck`. Tests use stdlib `testing` and `goquery`.

## Running

```bash
make run                   # starts the server on :8080
make test                  # runs the test suite
make cover                 # writes coverage.html + per-function table
make check                 # fmt + lint + govulncheck + test
make build                 # produces a single static binary
make clean                 # removes build / coverage / SQLite artifacts
```

The SQLite database is created automatically on first run; migrations execute on startup.

## Testing strategy

Three categories of tests live in `main_test.go`:

1. **FSM unit tests** — table-driven over all 9 ordered pairs of `(current, next)` states.
2. **Handler + template contract tests** — `httptest` request → `goquery` parse → assert on selectors and `hx-*` attributes (e.g., "after PUT on a Pending todo, the response contains a button with text 'Complete' and `hx-put` pointing at `/todos/{id}/progress`").
3. **Cross-reference test** — fetches the rendered page shell, collects every element ID, then asserts that every `hx-target` referenced by any handler response resolves to an ID that actually exists. This substitutes for browser-based DOM verification and catches the most common HTMX failure (stale/typoed target).

No browser is used. No JSDOM. No Chrome binary. Tests run in well under a second.

## Project layout

See `CLAUDE.md` for the full folder structure and per-area rules. Briefly:

```
main.go          handlers.go       fsm.go          render.go
views/           static/           migrations/     db/ (sqlc-generated)
query.sql        sqlc.yaml         Makefile        main_test.go
.claude/rules/   path-scoped rule files for each subsystem
```

## Acknowledged trade-offs

- **DOM-level HTMX bugs are not caught in CI.** Things like an `hx-target` that resolves to an element hidden by an earlier swap, or `hx-trigger` timing edge cases, can only be observed in a real browser. The cross-reference test catches the common class (typoed/stale target) at the contract level; the rest is accepted risk for the speed gains.
- **No real-time UI.** No WebSockets, no SSE, no client-side state. Every change is a request. For a todo list this is the right shape; for chat apps or live dashboards it isn't.
- **One-database scaling.** SQLite + auto-migrate on startup is single-process by design. Horizontal scaling would require migrating that pattern.

## License

MIT.
