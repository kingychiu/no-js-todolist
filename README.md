# no-js-arcade

A single-player **HTMX arcade** with classic games (Snake, 2048, Minesweeper), a step-form lobby that onboards the player into a chosen game+difficulty, and per-difficulty leaderboards. **Zero custom JavaScript.** Built as an experiment in optimizing the agentic-coding harness.

## Why this exists

Frontend testing is a nightmare for AI agents. Most modern web stacks demand a browser to verify anything that touches the UI: a headless Chrome process, a JS runtime, async event loops, retries for flaky DOM resolution, mocks for everything network-shaped. When an agent edits a React component, the loop to confirm "did this work?" runs through Playwright or Puppeteer, takes seconds per assertion, and often fails for reasons that have nothing to do with the change. The agent can't iterate fast, and humans end up babysitting.

This project pushes that complexity out of the system entirely. By delegating all UI state mutations to the server and shipping rendered HTML over the wire, every user-visible change becomes an HTTP request whose response is a string of HTML. That string can be parsed, asserted against, and verified in milliseconds — by an agent, in a pure Go test, with no browser in sight.

Games and step-form wizards are the *harder* version of this challenge: they involve composed state machines, board state, scoring, leaderboards, and a non-trivial user flow. If the harness can model them without a browser, it can model anything in this style.

## The design

| Constraint | Why it matters for agentic coding |
|---|---|
| **Zero custom JavaScript.** HTMX CDN only. No Alpine.js, no hyperscript, no inline `onclick`. No HTMX extensions either (HTMX 2.0 moved them out of core). | If there's no JS to debug, agents don't need a JS runtime to verify behavior. |
| **No JSON responses.** Handlers return rendered HTML fragments. | The response body IS the assertion target. No serialization layer to test separately. |
| **Composed Go FSMs.** One Wizard FSM orchestrates the lobby; three per-game FSMs handle gameplay phases. All are small `switch` methods on string types. | State invariants are testable as pure functions; handler-level enforcement closes TOCTOU races. |
| **Classless CSS** (Pico.css v2, vendored). | Templates stay semantic. Tests assert on `button[hx-put]`, not `.btn-primary`. |
| **`httptest` + `goquery`** for all tests. | Pure Go. ~1ms per request. No browser, no Node, no JSDOM. |

The wizard FSM ensures users can't skip from "unnamed" straight to "playing." Each per-game FSM ensures moves after a win/loss are rejected at the database level (`UPDATE … WHERE state = ?` returning 0 rows → OOB error banner). The same row partial is reused for board rendering whether it's the initial load or a per-move response, so there's one source of truth for what the board looks like.

## What's in the arcade

| Game | FSM states | Difficulty knobs | Score = |
|---|---|---|---|
| **Snake** | `idle → playing → game_over` | tick 250ms / 150ms / 80ms | snake length |
| **2048** | `playing → won → continued → lost` | 5×5 / 4×4 / 3×3 grid | tile-merge score |
| **Minesweeper** | `playing → won → lost` | 9×9/10 / 16×16/40 / 24×24/99 | -seconds (lower is better) |

Snake earns the **long-polling tier** documented under "Real-time interactivity has a ceiling" — its server-side game loop runs in a per-session goroutine, the client long-polls for the next board, direction-key presses are separate fire-and-forget POSTs that push into the goroutine's input channel. Everything else (2048, Minesweeper, the wizard) is pure turn-based HTMX: one HTTP request = one move = one re-render.

## The step-form lobby

1. **Name** — required, free text
2. **Pick game** — Snake / 2048 / Minesweeper
3. **Pick difficulty** — Easy / Medium / Hard (knobs differ per game)
4. **Play** — the game itself, in the same wizard frame
5. **Finished + Leaderboard** — score posted to the per-`(game, difficulty)` leaderboard; Replay / Change game / Change name links return to the appropriate earlier step

Backward navigation is server-driven: clicking "Change game" sets the wizard state back to `named` and re-renders the step-2 fragment. The wizard FSM enforces which transitions are legal from each step.

## Stack

- **Go** — single static binary, embeds migrations, templates, and assets.
- **Echo** (`labstack/echo/v4`) — HTTP router.
- **SQLite** (`mattn/go-sqlite3`) — single-file DB, WAL mode, `_sync=NORMAL`.
- **Goose** (`pressly/goose/v3`) — SQL migrations, embedded with `go:embed`, run on startup.
- **sqlc** — typed Go from raw SQL. No ORM.
- **`html/template`** (stdlib) — auto-escaping, no second codegen step on top of sqlc.
- **HTMX** (CDN, core only) — the only client-side library. No extensions, no Alpine, no hyperscript.
- **Pico.css v2** (vendored, `go:embed`) — classless styling.

Lint/test stack: three installed binaries — `goimports`, `golangci-lint` with CLI flags only (`errcheck`, `staticcheck`, `govet`, `ineffassign`), and `govulncheck`. No `.golangci.yml`. Tests use stdlib `testing` and `goquery`.

## Running

```bash
make run                   # starts the server on :8080
make test                  # runs the full test suite (unit + e2e)
make test-unit             # in-process integration tests only
make test-e2e              # black-box HTTP user-story tests only
make cover                 # writes coverage.html + per-function table
make check                 # fmt + lint + govulncheck + test
make build                 # produces a single static binary at ./no-js-arcade
make clean                 # removes build / coverage / SQLite artifacts
```

The SQLite database is created automatically on first run; migrations execute on startup. Identity is a session-cookie UUID — no real auth, name is just a display string.

## Testing strategy

Two test layers in two packages:

**`main_test.go`** — in-process integration tests (`package arcade`):

1. **FSM unit tests** — table-driven over every (current, next) pair for the Wizard FSM and each per-game FSM.
2. **Handler + template contract tests** — `httptest` request → `goquery` parse → assert on selectors and `hx-*` attributes.
3. **Optimistic-locking guards** — each FSM's optimistic UPDATE returns 0 rows when the expected state is stale.
4. **Cross-reference test** — every `hx-target="#X"` and `hx-swap-oob` id in any handler response resolves to an element ID in the rendered shell.

**`e2e/e2e_test.go`** — black-box user-story tests (`package e2e`):

5. **Full arcade flow per game** — name → pick game → pick difficulty → play to completion → leaderboard reflects the score.
6. **Wizard skip-ahead rejected** — POSTing a step-3 request while in step-1 returns the OOB error banner.
7. **Move-after-game-over rejected** — finish a game, simulate a late move (two-tab race), get the OOB error.
8. **Replay preserves game+difficulty** — Replay link from `finished` lands back in `playing` with the same game/difficulty.
9. **Leaderboard separated by difficulty** — scores on Easy and Hard show in separate top-10 views.
10. **Snake long-poll delivers next frame** — open the long-poll endpoint, push a direction into the channel, assert the streamed HTML reflects the new snake position.

Per-test SQLite lives in `t.TempDir()` (not `:memory:` — that breaks under `database/sql` connection pooling). No browser, no JSDOM, no Chrome binary. Full suite runs in well under three seconds.

## Project layout

See `CLAUDE.md` for the full folder structure and per-area rules. Briefly:

```
app.go                       (package arcade — NewApp, RunMigrations, embeds)
wizard.go                    (Wizard FSM — orchestrates the step-form lobby)
game_snake.go                (Snake FSM + runtime + goroutine loop)
game_2048.go                 (2048 FSM + board logic)
game_minesweeper.go          (Minesweeper FSM + board logic)
handlers.go                  (Echo handlers — bridge wizard + game FSMs)
render.go                    (template parsing + Render helper)
main_test.go                 (white-box tests)
cmd/server/main.go           (package main entrypoint)
e2e/e2e_test.go              (package e2e black-box tests)
views/                       (html/template files — layout + step + per-game templates)
static/                      (vendored Pico.css)
migrations/                  (Goose SQL — sessions, game_states, leaderboard)
query.sql, sqlc.yaml         (sqlc setup)
db/ (sqlc-generated)         Makefile
.claude/rules/               path-scoped rule files for each subsystem
```

## Acknowledged trade-offs

### DOM-level HTMX bugs are not caught in CI

`hx-target` that resolves to an element hidden by an earlier swap, `hx-trigger` event timing, history/restore edge cases — only a real browser sees these. The cross-reference test catches the most common silent failure (typoed/stale target) at the contract level; the rest is accepted risk for the speed gains.

### Real-time interactivity has a ceiling

The architecture handles per-user state changes (click → POST → HTML response) in tens of milliseconds. For freshness across users or background processes, the stack scales like this:

- **Refresh-style updates (seconds).** Native HTMX polling: `<div hx-get="/dashboard" hx-trigger="every 5s" hx-swap="outerHTML">`. No extensions, no second script tag.
- **Near-real-time push (~100ms), still within the rules.** HTMX long-polling. The Snake game uses this: server-side game loop in a goroutine; client long-polls for the next frame; direction-key POSTs push into a channel. Each cycle is a complete HTTP request returning HTML — testable with `httptest.NewServer`.
- **Sub-50ms push to many idle clients.** Needs SSE or WebSockets — both are second `<script>` tags. Pick a different stack.
- **Collaborative editing (Google Docs, Figma).** Needs CRDTs, bidirectional sync, thick client. "HTML is the API" fundamentally can't represent in-flight merge state. Wrong stack, full stop.

### One-database scaling

SQLite + auto-migrate on startup is single-process by design. Per-session Snake goroutines live in-memory on the same process. Horizontal scaling would require lifting that goroutine state into a separate worker pool with shared state — out of scope.

## License

MIT.
