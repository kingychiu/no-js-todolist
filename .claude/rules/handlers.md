---
paths:
  - "handlers.go"
  - "app.go"
  - "cmd/server/**"
---

# HTTP Layer Rules

Applies to `handlers.go`, `app.go`, and `cmd/server/main.go`. The HTTP boundary bridges the wizard FSM, per-game FSMs, pure game-logic functions, and the database.

## `app.go` — application wiring (`package arcade`)

`app.go` owns the embeds and the `NewApp` constructor:
1. `//go:embed migrations/*.sql` → `migrationsFS`.
2. `//go:embed static/*` → `staticFS`.
3. `NewApp(sqldb *sql.DB) (*echo.Echo, error)` — runs migrations, loads templates, builds the `Handlers` struct, registers routes on a fresh `*echo.Echo`, and returns it ready to `Start()`.
4. `RunMigrations(sqldb *sql.DB) error` — exported so e2e tests can drive setup; called internally by `NewApp`.

**Forbidden in `app.go`:** handler bodies, FSM checks, template execution, game logic.

## `cmd/server/main.go` — the entrypoint (`package main`)

Small. Open SQLite (with the WAL/sync DSN), defer close, call `arcade.NewApp`, start the server on `:8080`. Nothing else. No flags, no config files, no logging beyond `log.Println`. If you need more, lift it into `app.go` first.

## Handler shape — every state-mutating handler follows this pattern

```
1. Resolve the session from the cookie (create on first request).
2. Parse input from echo.Context (form value, path param).
3. If the action is a wizard transition (next step, back, replay, restart):
   a. Load the current wizard state.
   b. Compute the intended target from the user's action.
   c. Check WizardState.CanTransitionTo(target). If false → OOB error.
   d. UPDATE sessions SET wizard_state=? WHERE id=? AND wizard_state=?
      Check rowsAffected; if 0 → reload + OOB error.
4. If the action is a game move:
   a. Load the active game state + board from DB.
   b. Call the pure game-logic function: (newBoard, newState) = ApplyMove(board, input)
   c. If newState differs, check GameState.CanTransitionTo(newState). If false → OOB error.
   d. UPDATE game_states with optimistic lock on fsm_state. If 0 rows → OOB error.
   e. If the game ended, insert a leaderboard row and transition the wizard to finished.
5. Render the appropriate HTML fragment (or empty body for DELETE-style actions).
6. Return HTML. Never JSON. Never a redirect for HTMX requests.
```

## Routes (illustrative)

Exact paths are finalized at implementation time. Illustrative shape:

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/` | Render the current wizard step based on the session's `wizard_state`. |
| `POST` | `/wizard/name` | Submit name → `unnamed → named`. |
| `POST` | `/wizard/game` | Submit game choice → `named → game_chosen`. |
| `POST` | `/wizard/difficulty` | Submit difficulty → `game_chosen → difficulty_chosen`. |
| `POST` | `/wizard/start` | Begin game → `difficulty_chosen → playing` (initializes game_states row). |
| `POST` | `/wizard/back` | Go back one step. Wizard FSM validates. |
| `POST` | `/wizard/replay` | From `finished`, replay with same game+difficulty. |
| `POST` | `/wizard/restart` | From any step (in `finished`), reset to `named` keeping the name. |
| `GET` | `/game/snake/board` | Long-poll the next Snake frame. Blocks server-side on the goroutine's channel. |
| `POST` | `/game/snake/direction` | Push a direction change into the Snake goroutine's input channel. |
| `POST` | `/game/2048/move` | Apply an arrow-key move. Returns the new board fragment. |
| `POST` | `/game/minesweeper/reveal/:x/:y` | Reveal a cell. Returns the updated board (or game-over view). |
| `POST` | `/game/minesweeper/flag/:x/:y` | Toggle a flag. Returns the updated cell. |
| `GET` | `/leaderboard` | Query string `?game=X&difficulty=Y` filters the top-N. |

The base layout MUST contain `<div id="error-banner">` for OOB error swaps and `<main id="wizard-frame">` as the primary swap target for wizard step transitions.

## Error responses are 200 + OOB banner, not 4xx

HTMX swallows 4xx by default. Respond with HTTP 200 containing both the unchanged view AND `<div id="error-banner" hx-swap-oob="true">…</div>`.

## Concurrency: trust the optimistic UPDATE

Don't do `SELECT → check → UPDATE` (TOCTOU race). Use `UPDATE … WHERE state = expected_state` and check `rowsAffected`. 0 means rejection — reload current state, render OOB error.

This applies to both wizard transitions (against `sessions.wizard_state`) and game-move transitions (against `game_states.fsm_state`).

## Input handling

- Trim whitespace on free-text inputs (name).
- Validate `game` against an allowlist (`snake`, `2048`, `minesweeper`).
- Validate `difficulty` against an allowlist (`easy`, `medium`, `hard`).
- `html/template` auto-escapes by default. Don't disable. Don't use `template.HTML` on user input.

## Handler struct

```go
type Handlers struct {
    Q     *db.Queries
    Views *Views
    Snake *SnakeRuntime   // owns the per-session Snake goroutines + long-poll waiters
}
```

The `SnakeRuntime` is separate because Snake's per-session goroutine + long-poll waiters need a registry. 2048 and Minesweeper don't need a runtime — their state is fully persisted and computed per request.

## Logging

stdlib `log` only. One line on server start. Error logs on unrecoverable DB/template errors. No request-level logging, no structured logger, no zap/zerolog.
