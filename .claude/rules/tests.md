---
paths:
  - "main_test.go"
  - "**/*_test.go"
---

# Testing Rules

Applies to all `*_test.go` files. The whole project is designed to be testable without a browser — these rules preserve that.

## Toolchain — pure Go only

- `net/http/httptest` for the HTTP layer.
- `github.com/PuerkitoBio/goquery` for parsing response bodies.
- `database/sql` with a per-test SQLite file in `t.TempDir()` — **not** `:memory:` (see below).
- Goose for running migrations in test setup.
- Standard `testing` package — no testify, no ginkgo, no third-party framework.

**Forbidden in tests:**
- `chromedp`, `go-rod`, `playwright-go`, any browser-driving library.
- Node, JSDOM, happy-dom, any JS runtime — even "just for one test."
- Real network calls outside of `httptest.NewServer`. Real filesystem outside of `t.TempDir()`.

If a test seems to require any of these, the design is wrong — surface it to the user rather than reaching for a browser.

## Four test categories

### 1. FSM unit tests

Direct calls to each FSM's `CanTransitionTo`. Table-driven over (current, next) pairs. No HTTP, no DB.

```go
func TestWizardFSM_CanTransitionTo(t *testing.T) {
    t.Parallel()
    cases := []struct {
        from, to WizardState
        want     bool
    }{
        {WizardUnnamed, WizardNamed, true},
        {WizardUnnamed, WizardGameChosen, false}, // can't skip ahead
        {WizardGameChosen, WizardNamed, true},    // back-nav
        {WizardFinished, WizardPlaying, true},    // replay
        {WizardFinished, WizardNamed, true},      // restart
        // ... cover the full matrix
    }
    for _, c := range cases {
        if got := c.from.CanTransitionTo(c.to); got != c.want {
            t.Errorf("%s → %s: got %v, want %v", c.from, c.to, got, c.want)
        }
    }
}

func TestSnakeFSM_CanTransitionTo(t *testing.T) { ... }
func TestT48FSM_CanTransitionTo(t *testing.T)   { ... }
func TestMSFSM_CanTransitionTo(t *testing.T)    { ... }
```

### 2. Pure game-logic unit tests

The game functions (`Tick`, `ApplyMove`, `RevealCell`, `SetDirection`, etc.) are pure — test them directly with constructed board values. Microsecond fast, zero infrastructure.

```go
func TestSnake_TickAdvancesHead(t *testing.T) {
    t.Parallel()
    b := SnakeBoard{Snake: []Cell{{2,2}}, Direction: East, Food: Cell{9,9}, Width: 10, Height: 10}
    after, state := Tick(b)
    if after.Snake[0] != (Cell{3,2}) { t.Fatalf("head didn't advance: %v", after.Snake[0]) }
    if state != SnakePlaying { t.Fatalf("state = %s, want playing", state) }
}

func TestSnake_TickIntoWallEndsGame(t *testing.T) {
    t.Parallel()
    b := SnakeBoard{Snake: []Cell{{9,2}}, Direction: East, Width: 10, Height: 10}
    _, state := Tick(b)
    if state != SnakeGameOver { t.Fatalf("expected game_over, got %s", state) }
}

func TestT48_ApplyLeft_MergesEqualAdjacentTiles(t *testing.T) {
    t.Parallel()
    b := T48Board{ /* {2,2,0,0} on first row */ }
    after, _ := ApplyMove(b, DirLeft)
    if after.Cells[0][0] != 4 { t.Fatalf("expected merge to 4, got %d", after.Cells[0][0]) }
}

func TestMS_RevealCell_FloodFillsEmptyRegion(t *testing.T) { ... }
func TestMS_RevealCell_OnMineLoses(t *testing.T)           { ... }
func TestMS_FlagCell_TogglesAndDoesntReveal(t *testing.T)  { ... }
```

Use table-driven cases liberally. These tests have **zero infrastructure** — no DB, no HTTP, no templates. If a game function needs randomness, it takes a `*rand.Rand` parameter and tests pass a deterministic seed.

### 3. Handler + template contract tests

Boot a per-test SQLite, run migrations, build the real `Handlers` struct, fire HTTP requests, parse responses with goquery, assert on selectors.

```go
func TestPostWizardName_TransitionsToNamed(t *testing.T) {
    t.Parallel()
    env := newTestEnv(t)
    rec := postForm(t, env, "/wizard/name", url.Values{"name": {"Alice"}})
    if rec.Code != http.StatusOK { t.Fatalf("status = %d", rec.Code) }
    doc := parse(t, rec.Body)
    if doc.Find("[data-step='game']").Length() == 0 {
        t.Errorf("expected game-picker step in response")
    }
}

func TestPost2048Move_RejectedAfterLoss(t *testing.T) {
    t.Parallel()
    env := newTestEnv(t)
    env.startGameViaWizard(t, "2048", "easy")
    env.forceGameFSM(t, T48Lost)  // white-box shortcut for setup
    rec := postForm(t, env, "/game/2048/move", url.Values{"dir": {"left"}})
    doc := parse(t, rec.Body)
    if !hasOOBErrorBanner(doc) {
        t.Errorf("expected OOB error on move after loss")
    }
}
```

Coverage targets in this category:
- Wizard step submissions: each valid forward transition; each rejection (skip-ahead, invalid back-nav).
- Per-game move handlers: valid move, game-ending move, post-game-over move (rejected).
- Optimistic-lock rejection: stale `expected_state` returns 0 rowsAffected.
- Leaderboard rendering with (game, difficulty) filters.

### 4. Cross-reference test — every `hx-target` resolves in the shell

Substitutes for browser-based DOM verification.

```go
func TestHxTargets_ResolveInShell(t *testing.T) {
    env := newTestEnv(t)
    // Walk enough of the state space that every response branch renders.

    shell := fetchDoc(t, env, http.MethodGet, "/")
    shellIDs := collectIDs(shell)
    if !shellIDs["error-banner"] || !shellIDs["wizard-frame"] {
        t.Fatalf("shell missing required ids; got %v", shellIDs)
    }

    responses := []*goquery.Document{ /* fetch each major response shape */ }
    for _, doc := range responses {
        doc.Find("[hx-target], [hx-swap-oob]").Each(func(_ int, s *goquery.Selection) {
            if t := s.AttrOr("hx-target", ""); strings.HasPrefix(t, "#") {
                if !shellIDs[strings.TrimPrefix(t, "#")] {
                    t.Errorf("hx-target=%q has no matching id in shell", t)
                }
            }
        })
    }
}
```

Catches typoed IDs, OOB targets that don't exist, and the most common silent HTMX bugs without needing a real DOM.

## Test setup helpers

Put helpers in `main_test.go` next to the tests. Don't create a `testhelpers` package.

Required:
- `newTestEnv(t)` — opens per-test SQLite at `filepath.Join(t.TempDir(), "test.db")`, runs goose, builds Views + Handlers, returns the env with a fresh `*echo.Echo`.
- `postForm(t, env, path, values)` — POST `application/x-www-form-urlencoded`.
- `parse(t, body) *goquery.Document` — goquery parse.
- `(*testEnv).startGameViaWizard(t, game, difficulty)` — drives the wizard via real HTTP up to `playing` state. Used by tests that need a game-in-progress.
- `(*testEnv).forceGameFSM(t, state)` — white-box shortcut to set the game's FSM state directly. **Forbidden in e2e tests** (separate package, can't import this).

### Critical: do NOT use `:memory:` SQLite in tests

`database/sql` opens multiple connections in a pool. `sqlite3` with `:memory:` creates an **isolated, blank database per connection** — so `goose.Up` applies migrations on one connection, and queries hit a different connection where tables don't exist. `cache=shared` partially works around it but has locking edge cases with goose.

Per-test file in `t.TempDir()`:

```go
dbpath := filepath.Join(t.TempDir(), "test.db")
sqldb, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal=WAL&_busy_timeout=5000&_sync=NORMAL&_fk=on", dbpath))
```

`t.TempDir()` is auto-cleaned. Each test gets a fresh DB.

## Test naming

- `TestFunction_Scenario_Expected`. Examples: `TestPostWizardName_RejectsEmpty`, `TestSnake_TickEatsFood_ExtendsAndScores`.
- `t.Parallel()` when the test owns its own DB.

## Coverage

```
go test -cover ./...                                              # ad-hoc summary
go test -coverpkg=./... -coverprofile=coverage.out ./...          # CI (instruments all pkgs)
go tool cover -html=coverage.out -o coverage.html                 # visual
go tool cover -func=coverage.out                                  # per-function table
```

`-coverpkg=./...` is important: without it, code in `db/` (sqlc-generated) isn't measured even when called by tests.

**No enforced percentage threshold.** Gaming `> 80%` is worse than honest gaps.

Informal target:
- **100% of `wizard.go`** (`CanTransitionTo`) — table-driven test over the matrix.
- **100% of each `game_*.go` FSM** — same.
- **100% of pure game-logic functions** (`Tick`, `ApplyMove`, `RevealCell`, etc.) — these are the load-bearing logic and they're trivial to cover at the pure level.
- **100% of state-transition paths in `handlers.go`** — each valid transition AND the rejection path.
- **100% of error-returning paths in `handlers.go`** — induced failures (closed DB, bogus session ID).
- **`cmd/server/main.go` wiring is acceptable to remain uncovered** — `goose.Up`, `db.Open`, `srv.Start` are framework-level wiring.

`coverage.out` and `coverage.html` are git-ignored.

## What tests don't cover (acknowledge, don't fix with a browser)

These bugs would only surface in a real browser. Document them in a `TODO` at the top of `main_test.go`:
- `hx-trigger` event timing edge cases (keyboard repeat-fire, focus quirks).
- DOM state where an `hx-target` exists in the shell but is hidden / replaced before the swap fires.
- History/back-button restoration of HTMX-swapped content.
- Snake long-polling reconnection behavior on network flap.

Accept these as documented risk. The cross-reference test catches the most likely class.
