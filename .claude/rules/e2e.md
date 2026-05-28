---
paths:
  - "e2e/**"
---

# End-to-End User-Story Test Rules

Applies to `e2e/**/*_test.go`. These are **black-box** tests: they drive the system the way a real client would, via HTTP only, with no access to internal helpers.

## Why this folder exists

Black-box discipline is enforced by the Go package boundary. `e2e/` is `package e2e`, which physically cannot import `package todolist`'s unexported identifiers. There's no way to call `mustForceStatus`, no way to poke `Handlers.Q` directly, no shortcut to set up state. Every state transition must flow through the HTTP layer.

This is the project's user-story harness. If you can't write the test as "POST → parse response → PUT → parse response → assert," the test belongs in `main_test.go`, not here.

## What e2e tests MUST do

1. **Boot the app via `todolist.NewApp(sqldb)` and wrap it in `httptest.NewServer`.** Use the `newServer(t)` helper.
2. **Drive state changes through HTTP only.** No `db.Queries`, no SQL, no direct calls to handler methods.
3. **Extract IDs from response HTML.** The pattern: parse with goquery, find `li[id^='todo-']`, strip the prefix, parse to int64. Use `extractTodoID(t, doc)`.
4. **Chain at least two HTTP requests per test.** If your test is a single request, it belongs in `main_test.go`. E2E means a flow.
5. **Assert on selectors and `hx-*` attributes**, not on raw HTML strings. Use goquery.
6. **Use `t.Parallel()`.** Each test owns its own SQLite file (per-test `t.TempDir()`).

## What e2e tests MUST NOT do

- Import `todolist`'s unexported identifiers. They literally can't — but don't try to work around it by exporting things just for tests.
- Use `os/exec` to launch a separate binary. `httptest.NewServer(echoApp)` is the contract; the binary is tested by virtue of using the same `NewApp` code path.
- Set up state via direct DB writes. Even via `database/sql` — that defeats the point of black-box.
- Depend on test ordering. Each test must be self-contained.
- Run slower than ~200ms each. If they get slow, look at the wiring, not the design.

## Standard helpers (in `e2e_test.go`)

- `newServer(t) *httptest.Server` — fresh DB at `t.TempDir() + "/e2e.db"`, migrations applied, Echo wrapped.
- `parse(t, body) *goquery.Document` — parse a response body.
- `postForm(t, srv, path, values)` — POST `application/x-www-form-urlencoded`.
- `do(t, srv, method, path)` — GET/PUT/DELETE without a body.
- `extractTodoID(t, doc)` — find the first `<li id="todo-N">` and return N.
- `actionButtonText(doc)` — get the text of the first `hx-put` button (or "" if none).
- `hasOOBErrorBanner(doc)` — true if `div#error-banner[hx-swap-oob="true"]` is present.

When adding a new test, reuse helpers. Add new helpers only when more than one test needs them.

## What makes a good e2e test

A test that fails this question doesn't belong here:

> If a non-technical product owner read this test, would they recognize it as a thing a user does?

Examples that pass:
- "User adds a todo, starts work on it, completes it, then reloads the page and sees it as completed." ✓
- "User submits an empty title, sees an error, then submits a valid one and sees it appear." ✓
- "User adds two todos, deletes one, and only the other remains." ✓
- "Two concurrent clicks on Start Work — exactly one wins; the other is told the state changed." ✓ (this is a user-perceived behavior, not an internal implementation detail)

Examples that don't (belong in `main_test.go` instead):
- "GET / returns 200." ✗ — single endpoint, not a flow.
- "Invalid id returns 400." ✗ — error contract, not a user story.
- "FSM rejects InProgress → Pending." ✗ — unit-level invariant.

## Race-tolerant assertions

The concurrent-progress test cannot deterministically predict ordering, because SQLite WAL allows two readers to both see Pending before either UPDATE lands. The invariant the test enforces is:

- Exactly 2 responses classified.
- At least 1 success (a Complete button OR a completed-state row, depending on which goroutine was faster).
- The other is either a second success (valid: G1 progressed to InProgress, G2 then progressed to Completed) or a rejection (valid: optimistic lock kicked in).

Don't tighten this to "exactly one rejection" — that would be flaky and would lie about the actual valid outcomes.

## When to add an e2e test vs an integration test

| Question | Add e2e | Keep in `main_test.go` |
|---|---|---|
| Does it chain ≥2 HTTP requests? | yes | no |
| Does it model what a user does? | yes | no |
| Does it need internal state shortcuts? | no — it can't have them | yes |
| Does it exercise a single endpoint contract? | no | yes |
| Does it test FSM logic in isolation? | no | yes |
| Does it cross-reference attributes between responses? | could go either way | also fine |
