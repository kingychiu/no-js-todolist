---
paths:
  - "fsm.go"
---

# Finite State Machine Rules

Applies to `fsm.go`. This file is the single source of truth for the Todo lifecycle. Keep it tiny.

## The complete FSM

```go
package main

type TodoState string

const (
    Pending    TodoState = "pending"
    InProgress TodoState = "in_progress"
    Completed  TodoState = "completed"
)

// CanTransitionTo reports whether the FSM allows moving from s to next.
func (s TodoState) CanTransitionTo(next TodoState) bool {
    switch s {
    case Pending:
        return next == InProgress
    case InProgress:
        return next == Completed
    }
    return false
}

// Next returns the next state in the linear progression, if any.
// Used by the "progress" handler — the only valid forward step from each state.
func (s TodoState) Next() (TodoState, bool) {
    switch s {
    case Pending:
        return InProgress, true
    case InProgress:
        return Completed, true
    }
    return "", false
}
```

That is the entire FSM. Two valid edges:
- `Pending → InProgress`
- `InProgress → Completed`

Everything else (including same-state and any backward transition) returns `false`. `Next()` exists because the `/todos/:id/progress` endpoint asks "what is the single valid forward step from here?" — duplicating that switch in the handler would be worse than exposing it as a method.

## Resist these "improvements"

The CLAUDE.md "Simplicity First" rule applies hardest here. Do NOT add:

- **Transition tables / `map[TodoState][]TodoState`.** Two cases in a switch is exactly enough.
- **A registry pattern** with `RegisterTransition(from, to)`. There are no plugins.
- **Event hooks / observers** (`OnEnter`, `OnExit`, callbacks). Side effects happen in the handler.
- **A `Transition()` method that mutates a Todo.** Mutation is the handler's job after `CanTransitionTo` returns true. Keep the FSM pure.
- **A `States()` or `AllStates()` reflection helper.** If you need to enumerate states, list them at the call site.
- **An `Error` type for invalid transitions.** The handler decides what to do with `false`; the FSM doesn't need its own error type.
- **A `String()` method.** `TodoState` is already a string.
- **JSON or DB scanner methods on `TodoState`.** sqlc generates the DB scan; rendering uses raw string equality.

## What goes in this file

Only the type, the three constants, `CanTransitionTo`, and `Next`. If you're tempted to add a helper beyond these two, ask first whether the call site can do it in one line.

## When the FSM grows (don't pre-empt this)

If a future requirement adds a 4th state (e.g., `Cancelled`) or a 5th transition, extend the switch by adding the new case. Don't refactor to a table in anticipation — wait until the switch becomes genuinely unreadable, which won't happen below ~6 states.

## Validation lives at the handler boundary

The FSM only decides "is this transition allowed?" It does not:
- Read or write the database.
- Render HTML.
- Know about HTTP.
- Know about HTMX.

That separation is what makes the FSM trivially unit-testable (see `.claude/rules/tests.md`).
