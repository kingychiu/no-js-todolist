-- +goose Up
CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL DEFAULT '',
  wizard_state  TEXT NOT NULL DEFAULT 'unnamed',
  chosen_game   TEXT NOT NULL DEFAULT '',
  chosen_diff   TEXT NOT NULL DEFAULT '',
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE sessions;
