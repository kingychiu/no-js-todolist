.PHONY: fmt lint test test-unit test-e2e cover check run build clean sqlc

fmt:
	goimports -w .

lint:
	@test -z "$$(goimports -l .)" || { echo "Run 'make fmt' — files need formatting:"; goimports -l .; exit 1; }
	golangci-lint run --enable-only=errcheck,staticcheck,govet,ineffassign ./...
	govulncheck ./...

test:
	go test ./...

test-unit:
	go test . ./db/...

test-e2e:
	go test ./e2e/...

cover:
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out | tail -20

check: fmt lint test

run:
	go run ./cmd/server

build:
	go build -o no-js-arcade ./cmd/server

sqlc:
	sqlc generate

clean:
	rm -f no-js-arcade coverage.out coverage.html arcade.db arcade.db-wal arcade.db-shm
