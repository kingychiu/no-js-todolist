-- +goose Up
CREATE TABLE game_states (
  session_id  TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  game        TEXT NOT NULL,
  difficulty  TEXT NOT NULL,
  fsm_state   TEXT NOT NULL,
  board       TEXT NOT NULL,
  score       INTEGER NOT NULL DEFAULT 0,
  started_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE game_states;
