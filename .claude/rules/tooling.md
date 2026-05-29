---
paths:
  - "Makefile"
  - ".github/**"
  - ".golangci.yml"
  - ".gitignore"
---

# Tooling: lint, static analysis, vulnerability scan, coverage

Applies to `Makefile`, `.github/**`, and any linter config that lands. The whole stack is intentionally minimal: **two installed binaries** (`golangci-lint`, `govulncheck`) plus the Go toolchain.

## The Makefile

```makefile
.PHONY: fmt lint test cover check

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
```

Run `make check` before any commit. `make cover` when you want to see gaps.

**Note on goimports:** golangci-lint v2 moved formatters (`goimports`, `gofmt`, etc.) out of the linters list into a separate `formatters` category. To avoid adding a `.golangci.yml`, we keep `goimports` as a standalone binary: `fmt` runs `goimports -w .`, and `lint` enforces correct formatting via `goimports -l .` (fails if any file needs reformatting).

## Why this exact linter set

| Linter | What it catches | Why we need it |
|---|---|---|
| `errcheck` | Unchecked error returns | DB `rowsAffected`, template `Execute`, `goose.Up` — silent dropped errors corrupt FSM state. |
| `staticcheck` | ~200 checks (nil derefs, unused code, idiom violations) | The de-facto Go static analyzer. |
| `govet` | Stdlib-blessed checks (Go 1.24+ includes `tests` and improved `printf`) | Free, official. |
| `ineffassign` | Ineffectual assignments | Catches `err := a(); x, err := b()` where the first error is silently overwritten. |
| `goimports` (standalone) | Import formatting + unused-import removal | Runs outside golangci-lint because v2 split formatters off. |

**No config file.** CLI flags are the documented happy path. Don't add `.golangci.yml`. If you ever need to suppress a specific check, use an inline `//nolint:linter-name` comment, surgically.

## Linters explicitly NOT enabled

- **`gosec`** — source-code security patterns (SQL injection, hardcoded secrets). Not relevant: sqlc parameterizes queries, no auth, no TLS code. Add later if the threat model changes.
- **`nilaway`** — deeper nil analysis. staticcheck covers the common cases.
- **`revive` / `gocyclo` / `dupl` / `gocognit`** — style/complexity linters. The project is too small to need them.
- **Default golangci-lint preset** (i.e., running without `--disable-all`). Pulls in dozens of linters; many produce noise on a small project. Always run `--disable-all --enable=...`.

## govulncheck

Run alongside the linter. It scans dependencies and the standard library for known CVEs, using call-graph analysis to only flag *reachable* vulnerabilities (not just present-in-go.sum). Required even for non-public apps in 2026 — supply chain compromise is the primary attack vector.

Different from `gosec`: govulncheck = known CVEs in deps; gosec = source-code logic flaws. We use govulncheck; we skip gosec.

### Remediation when govulncheck flags stdlib CVEs

Bump the `go` directive in `go.mod` to a patched minor:

```
go get go@1.25.10            # whatever the latest patched version is
```

**This is an application (`package main`), so we bump the `go` directive directly.** The `toolchain` directive is for libraries that want to compile with a newer Go than they require from importers — we have no importers. When `go` and `toolchain` are equal, Go normalizes them to a single `go` line anyway.

Never suppress govulncheck findings on stdlib CVEs. Bump and re-run.

## Deferred close patterns

Use the right pattern for the resource. Three forms, in order of preference:

**1. Closure-discard (preferred for read-only or shutdown closes).**
```go
defer func() { _ = sqldb.Close() }() // sql.DB on orderly shutdown
defer func() { _ = rows.Close() }()  // read-only iteration
```
No `//nolint` comment, no suppressed signal — explicit discard.

**2. Capture-and-check (required for writes and transactions).**
```go
func writeFile(path string, data []byte) (err error) {
    f, err := os.Create(path)
    if err != nil { return err }
    defer func() {
        if cerr := f.Close(); cerr != nil && err == nil {
            err = cerr // surface close error only if no earlier error
        }
    }()
    _, err = f.Write(data)
    return err
}
```
For `*os.File` writes, `tx.Commit()`, or anywhere a failed close means data loss. **Never** discard close errors on writers.

**3. `//nolint:errcheck` (fallback).**
Only when neither pattern fits — e.g., inside a non-statement defer chain. Don't blanket-disable across a file. Don't use it to silence `Exec`, `Execute`, `Decode`, or other live error returns.

## Coverage policy

- **No enforced numeric threshold.** Gaming an 80% rule is worse than honest gaps.
- Informal target documented in `.claude/rules/tests.md`.
- HTML coverage report: `coverage.html` is generated for local viewing only. Gitignore it.

## .gitignore essentials

```
# Coverage outputs
coverage.out
coverage.html

# SQLite database files (created at runtime)
*.db
*.db-wal
*.db-shm

# Build outputs
/no-js-arcade
```

## CI (when it lands)

A single GitHub Actions job is enough:

```yaml
- run: make check
- run: make cover
```

Don't introduce a separate codecov/coveralls integration unless a second contributor joins. For now, `go tool cover -func=coverage.out` printing to stdout is sufficient.

## What this layer does NOT include

- No `pre-commit` framework (Python-based, wrong toolchain for a Go project).
- No `lefthook` / `husky` hook manager.
- No coverage badge in README — premature.
- No `gotestsum` for JUnit XML — no CI dashboard consuming it.
- No `air` / `reflex` / hot-reload tools — they're nice for development but speculative until the project is bigger.
