-- name: ListTodos :many
SELECT * FROM todos ORDER BY id;

-- name: GetTodo :one
SELECT * FROM todos WHERE id = ?;

-- name: CreateTodo :one
INSERT INTO todos (title) VALUES (?)
RETURNING *;

-- name: UpdateTodoStatus :execrows
UPDATE todos SET status = sqlc.arg('new_status')
WHERE id = sqlc.arg('id') AND status = sqlc.arg('expected_status');

-- name: DeleteTodo :exec
DELETE FROM todos WHERE id = ?;
