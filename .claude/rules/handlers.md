---
paths:
  - "handlers.go"
  - "app.go"
  - "cmd/server/**"
---

# HTTP Layer Rules

Applies to `handlers.go`, `app.go`, and `cmd/server/main.go`. Three responsibilities split across two packages.

## `app.go` — application wiring (`package todolist`)

`app.go` owns the embeds and the `NewApp` constructor:
1. `//go:embed migrations/*.sql` → `migrationsFS`.
2. `//go:embed static/*` → `staticFS`.
3. `NewApp(sqldb *sql.DB) (*echo.Echo, error)` — runs migrations, loads templates, builds the `Handlers` struct, registers routes on a fresh `*echo.Echo`, returns it ready to `Start()`.
4. `RunMigrations(sqldb *sql.DB) error` — exported so e2e tests can drive setup if needed; called internally by `NewApp`.

**Forbidden in `app.go`:** handler bodies, FSM checks, template rendering logic.

## `cmd/server/main.go` — the entrypoint (`package main`)

The whole entrypoint is small:
1. Open SQLite (`sql.Open` with the WAL/sync DSN).
2. `defer func() { _ = sqldb.Close() }()`.
3. `e, err := todolist.NewApp(sqldb)`.
4. `e.Start(":8080")`.

Nothing else. No flags, no config files, no logging beyond `log.Println`. If you need more, lift it into `app.go` first.

## Handler shape — every handler follows this pattern

```
1. Parse input from echo.Context (path param, form value).
2. If the action is a state change:
   a. Load the current row (or use the optimistic UPDATE — see below).
   b. Call currentState.CanTransitionTo(next).
   c. If false: render the unchanged row + an OOB error banner. Return 200.
3. Execute the sqlc query.
4. Render the appropriate HTML fragment (or empty body for DELETE).
5. Return HTML. Never JSON. Never a redirect for HTMX requests.
```

## Routes

| Method | Path | Behavior |
|---|---|---|
| `GET` | `/` | Render `index.html` with the full list. |
| `POST` | `/todos` | Insert; return the new `<li>` (append swap) or the refreshed list partial. |
| `PUT` | `/todos/:id/progress` | FSM transition; return the updated `<li>`, or unchanged row + OOB error. |
| `DELETE` | `/todos/:id` | Delete; return 200 with **empty body**. Client uses `hx-swap="delete"`. |

## Error responses are 200 + OOB banner, not 4xx

HTMX swallows 4xx responses by default unless `hx-target-4xx` is configured globally. Simpler and more consistent: respond with HTTP 200 containing both the unchanged row HTML AND a `<div id="error-banner" hx-swap-oob="true">…</div>` fragment.

The base layout (`views/layout.html`) MUST contain `<div id="error-banner" aria-live="polite"></div>` as the OOB target.

## Concurrency: trust the optimistic UPDATE

For status changes, do not do `SELECT` → check FSM → `UPDATE`. That's a TOCTOU race.

Instead: call `UpdateTodoStatus(ctx, newStatus, id, expectedCurrentStatus)` directly. The sqlc query is `UPDATE ... WHERE id=? AND status=?`. Check `rowsAffected`:
- `== 1` → transition succeeded. Reload (or construct) the updated row and render.
- `== 0` → either the row doesn't exist OR another request beat us (or the FSM precondition fails). Reload the current row, render it unchanged + OOB error banner.

This makes invalid transitions impossible at the DB level, not just the handler level.

## Input handling

- Echo's `c.FormValue("title")` is fine. Trim whitespace. Reject empty titles with a 200 + OOB error.
- No CSRF middleware (see Non-Goals). If this ever runs publicly, revisit.
- Sanitization: `html/template` auto-escapes by default. Don't disable it. Don't use `template.HTML` on user input.

## Handler struct, not free functions

Use a small `Handlers` struct holding `*db.Queries` and the parsed templates. Methods on it become routes. This keeps dependencies explicit without introducing DI machinery.

```go
type Handlers struct {
    Q     *db.Queries
    Views *Views // from render.go
}

func (h *Handlers) ListTodos(c echo.Context) error { ... }
```

## Logging

`log` from stdlib only. One line on server start (`log.Println("listening on :8080")`), error logs on unrecoverable DB/template errors. No request-level logging, no structured logger, no zap/zerolog.
