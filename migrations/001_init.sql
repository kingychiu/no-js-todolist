-- +goose Up
CREATE TABLE todos (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  title  TEXT    NOT NULL,
  status TEXT    NOT NULL DEFAULT 'pending'
);

-- +goose Down
DROP TABLE todos;
