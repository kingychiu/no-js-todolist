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
