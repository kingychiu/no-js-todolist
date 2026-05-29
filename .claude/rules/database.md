---
paths:
  - "query.sql"
  - "sqlc.yaml"
  - "migrations/**"
  - "db/**"
---

# Database Layer Rules

Applies to `query.sql`, `sqlc.yaml`, `migrations/**/*.sql`, and `db/**/*.go`. SQLite via `mattn/go-sqlite3`, schema via Goose, typed queries via sqlc.

## Schema lives in migrations

- `migrations/` is the authoritative source. sqlc reads it to type-check `query.sql`.
- No separate `schema.sql`. Point `sqlc.yaml` at the migrations folder.

## Tables

**`migrations/001_init.sql`** — sessions:

```sql
-- +goose Up
CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,                              -- UUID from cookie
  name          TEXT NOT NULL DEFAULT '',
  wizard_state  TEXT NOT NULL DEFAULT 'unnamed',
  chosen_game   TEXT NULL,                                     -- 'snake' / '2048' / 'minesweeper'
  chosen_diff   TEXT NULL,                                     -- 'easy' / 'medium' / 'hard'
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE sessions;
```

**`migrations/002_games.sql`** — active game state per session:

```sql
-- +goose Up
CREATE TABLE game_states (
  session_id  TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  game        TEXT NOT NULL,
  difficulty  TEXT NOT NULL,
  fsm_state   TEXT NOT NULL,                                   -- per-game FSM constant
  board       TEXT NOT NULL,                                   -- JSON-encoded board (per-game shape)
  score       INTEGER NOT NULL DEFAULT 0,
  started_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE game_states;
```

**`migrations/003_leaderboard.sql`** — final scores partitioned by (game, difficulty):

```sql
-- +goose Up
CREATE TABLE leaderboard (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL,
  game        TEXT NOT NULL,
  difficulty  TEXT NOT NULL,
  score       INTEGER NOT NULL,
  played_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_leaderboard_game_diff_score ON leaderboard (game, difficulty, score DESC);

-- +goose Down
DROP INDEX idx_leaderboard_game_diff_score;
DROP TABLE leaderboard;
```

- State columns stored as TEXT, matching the FSM constants exactly.
- No `CHECK` constraints — FSMs at the handler boundary already enforce validity.
- `board` is JSON-encoded because each game's runtime state shape differs. **JSON-in-SQLite is implementation detail; we never serve JSON to the client.**

## Migration rules

- Sequential numbering: `001_*.sql`, `002_*.sql`, … Pad to three digits.
- Both `-- +goose Up` and `-- +goose Down` are required.
- Down migrations reverse the up cleanly (tests rely on this).
- **Never edit a committed migration.** Add a new one.
- Migrations are embedded with `//go:embed migrations/*.sql` and run via `goose.Up(db, "migrations")` in `app.go` on startup.

## Required sqlc queries (`query.sql`)

```sql
-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

-- name: UpsertSession :exec
INSERT INTO sessions (id) VALUES (?) ON CONFLICT(id) DO NOTHING;

-- name: UpdateSessionName :execrows
UPDATE sessions SET name = sqlc.arg('name') WHERE id = sqlc.arg('id');

-- name: UpdateSessionWizardState :execrows
UPDATE sessions
SET wizard_state = sqlc.arg('new_state'),
    chosen_game  = sqlc.arg('chosen_game'),
    chosen_diff  = sqlc.arg('chosen_diff')
WHERE id = sqlc.arg('id') AND wizard_state = sqlc.arg('expected_state');

-- name: GetGameState :one
SELECT * FROM game_states WHERE session_id = ?;

-- name: UpsertGameState :exec
INSERT INTO game_states (session_id, game, difficulty, fsm_state, board, score)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE
  SET game = excluded.game, difficulty = excluded.difficulty,
      fsm_state = excluded.fsm_state, board = excluded.board, score = excluded.score;

-- name: UpdateGameState :execrows
UPDATE game_states
SET fsm_state = sqlc.arg('new_state'),
    board     = sqlc.arg('board'),
    score     = sqlc.arg('score')
WHERE session_id = sqlc.arg('session_id')
  AND fsm_state  = sqlc.arg('expected_state');

-- name: DeleteGameState :exec
DELETE FROM game_states WHERE session_id = ?;

-- name: InsertLeaderboardEntry :one
INSERT INTO leaderboard (name, game, difficulty, score) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: ListLeaderboard :many
SELECT * FROM leaderboard
WHERE game = ? AND difficulty = ?
ORDER BY score DESC, played_at ASC
LIMIT 20;
```

### Critical: optimistic locking with `sqlc.arg('name')`

Both `UpdateSessionWizardState` and `UpdateGameState` use the optimistic-lock pattern:

```sql
UPDATE … SET state = sqlc.arg('new_state'), …
WHERE id = sqlc.arg('id') AND state = sqlc.arg('expected_state');
```

**Use `sqlc.arg('name')` for repeated columns.** Without it, sqlc would generate ugly field names like `WizardState` and `WizardState_2`. Named args produce clean `NewState` / `ExpectedState` fields on the params struct.

The `AND state = ?` clause is **not optional** — it's the database-level FSM enforcement:
- Caller passes `(newState, expectedCurrentState)`.
- If the row's current state doesn't match, zero rows are affected.
- `:execrows` returns `int64`; caller checks `rowsAffected == 0` and treats as a rejected transition.

This closes the TOCTOU race between "handler reads state" and "handler writes new state."

## `sqlc.yaml` config

```yaml
version: "2"
sql:
  - engine: "sqlite"
    schema: "migrations/"
    queries: "query.sql"
    gen:
      go:
        package: "db"
        out: "db"
        sql_package: "database/sql"
        emit_json_tags: false
        emit_prepared_queries: false
        emit_interface: false
        emit_exact_table_names: false
        emit_empty_slices: true
```

- `emit_json_tags: false` — never serialize over the wire.
- `emit_interface: false` — no DI machinery.
- `emit_empty_slices: true` — empty leaderboard returns `[]LeaderboardEntry{}`, not nil.

After editing `query.sql` or migrations: run `sqlc generate`. Commit the regenerated `db/*.go`.

## `db/` is generated

Never hand-edited. If a query is wrong, fix `query.sql` and regenerate.

## SQLite connection settings

```go
db, err := sql.Open("sqlite3", "file:arcade.db?_journal=WAL&_busy_timeout=5000&_sync=NORMAL&_fk=on")
```

- `_journal=WAL` — readers don't block writers.
- `_busy_timeout=5000` — wait 5s for write locks.
- `_sync=NORMAL` — significantly faster than `FULL`; corruption-safe **only** with WAL.
- `_fk=on` — foreign keys enforced (matters here: `game_states.session_id` references `sessions.id` with ON DELETE CASCADE).

`mattn/go-sqlite3` maps these DSN params to PRAGMAs on connection.

## Not included

- No ORM. sqlc + raw SQL only.
- No connection pool tuning. SQLite + one process needs nothing.
- No retry logic. Errors propagate; handlers turn them into HTML error banners.
- No multi-database support. SQLite is the database.
