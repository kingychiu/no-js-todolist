---
paths:
  - "e2e/**"
---

# End-to-End User-Story Test Rules

Applies to `e2e/**/*_test.go`. These are **black-box** tests: they drive the system the way a real client would, via HTTP only, with no access to internal helpers.

## Why this folder exists

Black-box discipline is enforced by the Go package boundary. `e2e/` is `package e2e`, which physically cannot import `package arcade`'s unexported identifiers. There's no way to call `forceGameFSM`, no way to poke `db.Queries` directly, no shortcut. Every state transition must flow through the HTTP layer.

This is the project's user-story harness. If you can't write the test as "POST → parse response → POST → parse response → assert," it belongs in `main_test.go`, not here.

## What e2e tests MUST do

1. **Boot the app via `arcade.NewApp(sqldb)` wrapped in `httptest.NewServer`.** Use the `newServer(t)` helper.
2. **Drive state changes through HTTP only.** No `db.Queries`, no SQL, no direct calls to handler methods.
3. **Carry the session cookie across requests.** Use a `*http.Client` with a `cookiejar.Jar` so the session persists within a test.
4. **Chain at least two HTTP requests per test.** A single request belongs in `main_test.go`. E2E means a user flow.
5. **Assert on selectors and `hx-*` attributes**, not on raw HTML strings. Use goquery.
6. **Use `t.Parallel()`.** Each test owns its own SQLite file (per-test `t.TempDir()`).

## What e2e tests MUST NOT do

- Import `arcade`'s unexported identifiers (they can't — but don't try to export things just for tests).
- Use `os/exec` to launch a separate binary. `httptest.NewServer(echoApp)` is the contract.
- Set up state via direct DB writes. Even via `database/sql` — that defeats the point of black-box.
- Depend on test ordering.
- Run slower than ~500ms each.

## Standard helpers (in `e2e_test.go`)

- `newServer(t) *httptest.Server` — fresh DB at `t.TempDir() + "/e2e.db"`, migrations applied, Echo wrapped.
- `newClient(t) *http.Client` — HTTP client with a fresh cookie jar.
- `parse(t, body) *goquery.Document` — goquery parse.
- `postForm(t, client, srv, path, values)` — POST `application/x-www-form-urlencoded`, returns response + parsed doc.
- `get(t, client, srv, path)` — GET, returns response + parsed doc.
- `hasOOBErrorBanner(doc)` — true if `div#error-banner[hx-swap-oob="true"]` is present.
- `currentWizardStep(doc)` — reads a `data-step` attribute on `#wizard-frame` to know which step the user is on.

When adding a new test, reuse helpers. Add new helpers only when more than one test needs them.

## What makes a good e2e test

> Could a non-technical product owner read this test and recognize it as a thing a user does?

Examples that pass:
- "User enters name 'Alice', picks 2048, picks Easy, plays a few moves to win, sees their score on the leaderboard." ✓
- "User picks Snake, plays, dies, clicks Replay, lands back in a fresh Snake game with the same difficulty." ✓
- "User submits an empty name, sees an error banner, then submits 'Bob' and proceeds to the game picker." ✓
- "User reaches the finished step, clicks Change Game, ends up back at the game picker with their name preserved." ✓
- "User tries to POST a 2048 move while still in the name step — gets an OOB error banner." ✓ (this is a user-perceived behavior, not an internal implementation detail)

Examples that don't (belong in `main_test.go` instead):
- "GET / returns 200." ✗ — single endpoint.
- "Invalid difficulty returns 400." ✗ — error contract, not a user story.
- "WizardFSM rejects unnamed → playing." ✗ — unit-level invariant.
- "Tick advances the snake's head." ✗ — pure-function unit test.

## Race-tolerant assertions (Snake's long-poll)

The Snake long-poll endpoint blocks server-side until the goroutine ticks or until the context times out. E2E tests can use it like this:

```go
// Player starts Snake.
postForm(t, client, srv, "/wizard/start", nil)

// Open a long-poll for the next frame; expect a fresh board within ~500ms.
resp := get(t, client, srv, "/game/snake/board")
doc := parse(t, resp.Body)
if doc.Find("#snake-board").Length() == 0 { t.Fatalf("expected snake board") }

// Push a direction; the next long-poll cycle should reflect it.
postForm(t, client, srv, "/game/snake/direction", url.Values{"dir": {"up"}})
// ... repeat the long-poll, assert the snake moved up
```

Don't assert exact frame counts or timings. Assert that the user-visible state has changed in the expected direction within a generous deadline.

## When to add an e2e test vs an integration test

| Question | Add e2e | Keep in `main_test.go` |
|---|---|---|
| Does it chain ≥2 HTTP requests? | yes | no |
| Does it model what a user does? | yes | no |
| Does it need internal state shortcuts? | no — it can't have them | yes |
| Does it test a single endpoint contract? | no | yes |
| Does it test FSM logic in isolation? | no | yes |
| Does it test a pure game-logic function? | no | yes |
| Does it cross-reference HTML attributes across responses? | could go either way | also fine |
