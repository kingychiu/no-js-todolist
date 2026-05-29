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

## Architecture: FSMs + pure functions + a thin imperative shell

The agentic-coding harness is fast not because the language is fast, but because the *test surface is narrow*. Every state transition in this repo is a `switch` on a string-typed constant. Every piece of game logic is a pure function over a strongly-typed struct. The only impure code is `handlers.go` (HTTP + DB), `game_snake_runtime.go` (the goroutine that owns Snake's loop), and `cmd/server/main.go` (`sql.Open` + `Start`).

That separation is what makes ~50 tests run in under two seconds with zero mocks and zero infrastructure setup.

### Why FSMs

Each FSM is a small `switch` method on a string-typed constant — `WizardState`, `T48State`, `SnakeState`, `MSState`. The valid transitions are enumerable, so the entire transition space gets covered by one table-driven test:

```go
func (s T48State) CanTransitionTo(next T48State) bool {
    switch s {
    case T48Playing: return next == T48Won || next == T48Lost
    case T48Won:     return next == T48Continued || next == T48Lost
    case T48Continued: return next == T48Lost
    }
    return false
}
```

A single test iterates every `(from, to)` pair against a hand-written `allowed[from][to]` map. 16 pairs covered in ~15 lines. 100% coverage of `CanTransitionTo` on first commit, no integration setup, no mocks.

The same FSM is enforced **at the database** via optimistic `UPDATE … WHERE state = ?` clauses. A stale or invalid transition gets `rowsAffected == 0` and the handler returns an OOB error banner. That closes the TOCTOU race between "handler reads state" and "handler writes new state" — no transaction needed, no in-memory lock, just SQL.

Four FSMs compose: the Wizard FSM orchestrates the lobby (6 states), each per-game FSM handles its own lifecycle (3-4 states). They don't know about each other; `handlers.go` is the bridge.

### Why pure functions

The classic blockers to unit testing — globals, time, RNG, DB, file I/O — are all factored out as parameters:

```go
// Pure: same input → same output. No package-level state, no Now(), no rand global.
func Tick(board SnakeBoard, rng *rand.Rand) (SnakeBoard, SnakeState)
func ApplyMove(board T48Board, dir T48Direction, rng *rand.Rand) (T48Board, T48State, bool)
func RevealCell(board MSBoard, x, y int, rng *rand.Rand) (MSBoard, MSState)
```

Tests pass a seeded RNG; behavior becomes deterministic:

```go
b := T48Board{Cells: [][]int{{2,2,0,0}, ...}}
after, _, _ := ApplyMove(b, T48Left, rand.New(rand.NewSource(1)))
if after.Cells[0][0] != 4 { t.Errorf("expected merge to 4") }
```

No mocks, no fakes, no test doubles. The same `ApplyMove` runs in production and in tests; the only difference is the seed. Hundreds of constructed-board cases can run per millisecond.

### The imperative shell is small and named

| File | The impure thing it does |
|---|---|
| `handlers.go` | HTTP request parsing, DB reads/writes, session cookies, render |
| `game_snake_runtime.go` | Per-session goroutine, ticker, mutex-protected board, long-poll waiters |
| `cmd/server/main.go` | `sql.Open`, `arcade.NewApp(...).Start(":8080")` |

Everything else — every FSM, every board update, every win/loss check, every direction validation, every flood-fill — is pure. Game-over branches in the shell *call into* pure functions; they don't reimplement the logic.

### The empirical payoff

The pattern delivers reliably across all three game modules. Coverage on each pure layer **on its first commit**:

| Module | Pure layer coverage at ship |
|---|---|
| `game_2048.go` (Tick / merge / Hit2048 / HasValidMoves / NewT48Board) | 85-100% |
| `game_minesweeper.go` (RevealCell + flood fill, FlagCell, placeMines, countAdjacentMines) | 85-100% |
| `game_snake.go` (Tick / SetDirection / NewSnakeBoardView) | 100% |
| `game_snake_runtime.go` (goroutine + waiters + game-over callback) | 88-100% |

Pure layers ship at 90%+ basically for free. The persistent coverage gaps live in `handlers.go` (synthetic-failure branches) — exactly the layer where the FSM-and-pure-function discipline doesn't apply. **Bugs in game logic surface in pure unit tests instead of flaky integration tests** — which is the agentic feedback loop optimization the project is designed to validate.

The full discipline is codified in `.claude/rules/fsm.md` and `.claude/rules/pure_functions.md` (both auto-load when Claude touches `wizard.go` or `game_*.go`).

## What's in the arcade

| Game | FSM states | Difficulty knobs | Score = |
|---|---|---|---|
| **Snake** | `idle → playing → game_over` | tick 250ms / 150ms / 80ms | snake length |
| **2048** | `playing → won → continued → lost` | 5×5 / 4×4 / 3×3 grid | tile-merge score |
| **Minesweeper** | `playing → won → lost` | 9×9/10 / 16×16/40 / 24×24/99 | -seconds (lower is better) |

Snake earns the **long-polling tier** documented under "Real-time interactivity has a ceiling" — its server-side game loop runs in a per-session goroutine (`game_snake_runtime.go`, the project's only piece of stateful in-memory machinery), the client long-polls `/game/snake/board` for the next frame, direction-key presses are separate fire-and-forget POSTs that push into the goroutine's input channel. The display fragment (`snake_board.html`) and the interactive controls (the parent `step_playing` template in `wizard.html`) are deliberately separated so HTMX's listeners stay bound while the board cycles. Everything else (2048, Minesweeper, the wizard) is pure turn-based HTMX: one HTTP request = one move = one re-render.

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

Two test layers in two packages, plus one structural guard.

**`main_test.go`** — in-process white-box tests (`package arcade`):

1. **FSM unit tests** — table-driven over every (current, next) pair for the Wizard FSM and each of the three per-game FSMs (4 matrices total).
2. **Pure game-logic tests** — direct calls to `Tick`/`ApplyMove`/`RevealCell`/`SetDirection`/`compactAndMergeRow`/etc. with constructed boards. Microsecond-fast, zero infrastructure, no mocks.
3. **Snake runtime tests** — `Start`/`Stop`/`Snapshot`/`WaitNextFrame`/`PushDirection` against a real goroutine running at 20-30ms ticks. Includes the game-over callback firing on collision.
4. **Handler + template contract tests** — `httptest` request → `goquery` parse → assert on selectors and `hx-*` attributes. Covers each route's happy path and at least one rejection.
5. **Cross-reference test** — every `hx-target="#X"` and `hx-swap-oob` id in any handler response resolves to an element ID in the rendered shell.
6. **Long-poll structural test** — reads every long-polling template's source and asserts no `hx-post`/`hx-put`/`hx-delete`/`hx-patch` substrings. Codifies the rule that interactive triggers don't live inside fragments that get swapped on a tight loop. (Added after a real bug — see `.claude/rules/views.md` "Interactive triggers must NOT live inside self-replacing templates".)

**`e2e/e2e_test.go`** — black-box user-story tests (`package e2e`):

7. **Full arcade flow per game** — name → pick game → pick difficulty → start → play → quit. One test for each of 2048, Minesweeper, and Snake. Snake's covers the long-poll: opens `/game/snake/board`, posts a direction, fetches the next frame.
8. **Wizard skip-ahead rejected** — POSTing a step-3 request while in step-1 returns the OOB error banner.
9. **Back-nav** — game_chosen → back returns to the game picker.
10. **Replay-from-finished** — clicking Replay from the finished step lands back in playing with the same game/difficulty.
11. **Different-game-from-finished** — clicking Different game returns to the game picker with the name preserved.

Per-test SQLite lives in `t.TempDir()` (not `:memory:` — that breaks under `database/sql` connection pooling). No browser, no JSDOM, no Chrome binary. ~50 tests, full suite runs in under two seconds. Total coverage hovers around 73% (pure layers at 90-100%, handler/wiring layers at 55-75% — the expected shape).

## Project layout

See `CLAUDE.md` for the full folder structure and per-area rules. Briefly:

```
app.go                       (package arcade — NewApp, RunMigrations, embeds)
wizard.go                    (Wizard FSM — orchestrates the step-form lobby)
game_2048.go                 (2048 FSM + pure board logic)
game_minesweeper.go          (Minesweeper FSM + pure RevealCell/FlagCell)
game_snake.go                (Snake FSM + pure Tick/SetDirection)
game_snake_runtime.go        (Impure shell: per-session goroutine + long-poll waiters)
handlers.go                  (Echo handlers — bridge wizard + game FSMs)
render.go                    (template parsing + Render helper)
main_test.go                 (white-box tests)
cmd/server/main.go           (package main entrypoint)
e2e/e2e_test.go              (package e2e black-box tests)
views/                       (html/template files — layout + wizard steps + per-game boards)
static/                      (vendored Pico.css)
migrations/                  (Goose SQL — sessions, game_states, leaderboard)
query.sql, sqlc.yaml         (sqlc setup)
db/ (sqlc-generated)         Makefile
.claude/rules/               path-scoped rule files for each subsystem
```

## Acknowledged trade-offs

### DOM-level HTMX bugs are not caught in CI

`hx-target` that resolves to an element hidden by an earlier swap, `hx-trigger` event timing, history/restore edge cases — only a real browser sees these. The cross-reference test catches the most common silent failure (typoed/stale target) at the contract level.

One concrete instance of this trade-off has already played out: Snake's direction controls were originally placed inside the long-polling fragment that gets replaced every ~150ms. The backend handler was correct, all tests passed, but in a real browser the click/keydown listeners were unreliable because HTMX has to rebind elements that are constantly being destroyed and recreated. Caught by manual play-through, fixed by moving controls into a stable parent template, and codified afterwards by a structural test that reads each long-poll template's source and asserts no state-mutating `hx-*` attributes. The full rule lives in `.claude/rules/views.md` ("Interactive triggers must NOT live inside self-replacing templates"). That's an example of how the harness handles bugs of this class: not by adding a browser to CI, but by turning each discovered failure mode into a static structural assertion.

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
