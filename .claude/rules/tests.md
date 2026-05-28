---
paths:
  - "**/*_test.go"
---

# Testing Rules

Applies to all `*_test.go` files. The whole project is designed to be testable without a browser — these rules preserve that.

## Toolchain — pure Go only

- `net/http/httptest` for the HTTP layer.
- `github.com/PuerkitoBio/goquery` (or `golang.org/x/net/html`) for parsing response bodies.
- `database/sql` with `_journal=MEMORY` or `:memory:` for per-test SQLite instances.
- Goose for running migrations in test setup.
- Standard `testing` package — no testify, no ginkgo, no any third-party test framework.

**Forbidden in tests:**
- `chromedp`, `go-rod`, `playwright-go`, any browser-driving library.
- Node, JSDOM, happy-dom, any JS runtime — even if it's "just for one test."
- Real network calls. Real filesystem access outside of t.TempDir().

If a test seems to require any of these, the design is wrong — surface it to the user rather than reaching for a browser.

## Three test categories

### 1. FSM unit tests (`TestFSM_*`)

Direct calls to `CanTransitionTo`. Cover all 9 ordered pairs of (current, next) states, asserting `true` for the two valid edges and `false` for the other seven. No HTTP, no DB.

```go
func TestFSM_CanTransitionTo(t *testing.T) {
    cases := []struct {
        from, to TodoState
        want     bool
    }{
        {Pending, InProgress, true},
        {InProgress, Completed, true},
        {Pending, Completed, false},
        {Completed, InProgress, false},
        // ... all 9 pairs
    }
    for _, c := range cases {
        if got := c.from.CanTransitionTo(c.to); got != c.want {
            t.Errorf("%s -> %s: got %v, want %v", c.from, c.to, got, c.want)
        }
    }
}
```

### 2. Handler + template contract tests

Boot a real in-memory SQLite with migrations, build the real handler struct, fire HTTP requests with `httptest.NewRecorder` / `httptest.NewRequest`, parse the response body with goquery, and assert on selectors and attributes.

```go
func TestPut_PendingToInProgress_RendersCompleteButton(t *testing.T) {
    t.Parallel()
    h := newTestHandlers(t)            // helper that builds in-memory DB + handlers
    id := mustCreateTodo(t, h, "buy milk")

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPut, "/todos/"+fmt.Sprint(id)+"/progress", nil)
    h.Echo.ServeHTTP(rec, req)

    if rec.Code != 200 { t.Fatalf("status = %d", rec.Code) }

    doc, _ := goquery.NewDocumentFromReader(rec.Body)
    btn := doc.Find("button[hx-put]").First()
    if got := strings.TrimSpace(btn.Text()); got != "Complete" {
        t.Errorf("button text = %q, want %q", got, "Complete")
    }
    if got := btn.AttrOr("hx-put", ""); got != fmt.Sprintf("/todos/%d/progress", id) {
        t.Errorf("hx-put = %q", got)
    }
}
```

Tests in this category to write:
- POST `/todos` with valid title → response contains a new `<li>` with title and "Start Work" button.
- POST `/todos` with empty title → response contains the OOB error banner.
- PUT on Pending → "Complete" button, no "Start Work".
- PUT on InProgress → no action button, completed-styling.
- PUT on Completed → unchanged row + OOB error banner (invalid transition).
- DELETE → status 200 with empty body.
- Concurrent PUT race: simulate two PUTs by calling `UpdateTodoStatus` directly with stale `expectedCurrentStatus` → second one returns 0 rowsAffected, handler renders the error banner.

### 3. Cross-reference test — every `hx-target` resolves in the shell

This is the test that substitutes for browser-based DOM verification.

```go
func TestHxTargets_ResolveInShell(t *testing.T) {
    h := newTestHandlers(t)
    // Seed one todo in each state so all handler branches render.
    mustCreateTodo(t, h, "p")
    inProgressID := mustCreateTodo(t, h, "ip")
    mustProgressToInProgress(t, h, inProgressID)

    shell := fetchHTML(t, h, http.MethodGet, "/")
    shellIDs := collectIDs(shell)

    // Collect every hx-target and hx-swap-oob ID from every handler response.
    responses := []*goquery.Document{
        shell,
        fetchHTML(t, h, http.MethodPut, fmt.Sprintf("/todos/%d/progress", inProgressID)),
        // ... one fetch per relevant route × state
    }
    for _, doc := range responses {
        doc.Find("[hx-target], [hx-swap-oob]").Each(func(_ int, s *goquery.Selection) {
            target := s.AttrOr("hx-target", "")
            if strings.HasPrefix(target, "#") {
                id := strings.TrimPrefix(target, "#")
                if !shellIDs[id] {
                    t.Errorf("hx-target=%q has no matching id in shell", target)
                }
            }
            // Similar check for hx-swap-oob="true" elements that carry an id.
        })
    }
}
```

This catches: typoed target IDs, IDs that exist in the shell but get removed by an OOB swap, OOB fragments referencing non-existent containers — the most common silent HTMX bugs.

## Test setup helpers

Put helpers in `main_test.go` next to the tests. Don't create a `testhelpers` package — there's only one test file.

Required helpers:
- `newTestHandlers(t)` — opens `:memory:` SQLite, runs goose, builds Views and Handlers, returns the struct ready to ServeHTTP.
- `mustCreateTodo(t, h, title)` → returns the new ID.
- `mustProgressToInProgress(t, h, id)` — drives the state machine for setup.
- `fetchHTML(t, h, method, path)` → `*goquery.Document`. Fails the test on non-200 unless explicitly checking otherwise.

## Test naming

- `TestFunction_Scenario_Expected`. Example: `TestPut_OnCompleted_ReturnsErrorBanner`.
- Use `t.Parallel()` whenever the test owns its own DB. Don't parallelize tests that share state.

## Coverage

Commands:
```
go test -cover ./...                                              # ad-hoc summary
go test -coverpkg=./... -coverprofile=coverage.out ./...          # for CI (instruments all pkgs)
go tool cover -html=coverage.out -o coverage.html                 # visual
go tool cover -func=coverage.out                                  # per-function table
```

`-coverpkg=./...` is important: without it, code in `db/` (sqlc-generated) isn't measured even when called by tests in the main package.

**No enforced percentage threshold.** Gaming "must be > 80%" is worse than honest gaps.

Informal target:
- **100% of `fsm.go`** — trivial, achieved by one table-driven test over all 9 (current, next) pairs.
- **100% of state-transition paths in `handlers.go`** — each valid transition AND the rejected-invalid path (which renders the OOB error banner).
- **100% of error-returning paths in `handlers.go`** — every handler tested for both success and induced failure (e.g., closed DB, malformed input).
- **`main.go` wiring is acceptable to remain uncovered** — `goose.Up`, `db.Open`, `srv.Start` are framework-level wiring. Testing them tests the framework, not our code.

Achieving 100% of handler error paths may require injected failures: a deliberately-closed `*sql.DB`, a Todo ID that doesn't exist, a `title` longer than any plausible column constraint. Those tests are worth writing.

`coverage.out` and `coverage.html` are git-ignored — local-view-only artifacts.

## What tests don't cover (acknowledge, don't fix with a browser)

These bugs would only surface in a real browser. Document them in a `TODO` at the top of `main_test.go` so they're not forgotten:
- `hx-trigger` event timing edge cases.
- DOM state where an `hx-target` exists in the shell but is hidden / replaced before the swap fires.
- History/back-button restoration of HTMX-swapped content.

Acceptable trade-off: this is a small app, the design minimizes browser-only surface, and the cross-reference test catches the most likely class of these bugs at the contract level.
