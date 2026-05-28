.PHONY: fmt lint test cover check run build clean sqlc

fmt:
	goimports -w .

lint:
	@test -z "$$(goimports -l .)" || { echo "Run 'make fmt' — files need formatting:"; goimports -l .; exit 1; }
	golangci-lint run --enable-only=errcheck,staticcheck,govet,ineffassign ./...
	govulncheck ./...

test:
	go test ./...

cover:
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out | tail -20

check: fmt lint test

run:
	go run .

build:
	go build -o no-js-todolist .

sqlc:
	sqlc generate

clean:
	rm -f no-js-todolist coverage.out coverage.html todos.db todos.db-wal todos.db-shm
