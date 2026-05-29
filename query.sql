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
  SET game = excluded.game,
      difficulty = excluded.difficulty,
      fsm_state = excluded.fsm_state,
      board = excluded.board,
      score = excluded.score,
      started_at = CURRENT_TIMESTAMP;

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
