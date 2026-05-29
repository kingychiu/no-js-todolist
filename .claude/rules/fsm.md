---
paths:
  - "wizard.go"
  - "game_*.go"
---

# Finite State Machine Rules

Applies to `wizard.go` and `game_*.go`. The arcade has **four composed FSMs**: a Wizard FSM that orchestrates the lobby, and one FSM per game (Snake, 2048, Minesweeper). Each lives in its own file alongside the code that mutates and reads it.

## One file per FSM, kept with its consumers

| File | FSM | Consumers in this file |
|---|---|---|
| `wizard.go` | `WizardState` — onboarding lobby | the wizard transition helpers |
| `game_snake.go` | `SnakeState` — Snake lifecycle | the per-session goroutine, board ticker, collision check |
| `game_2048.go` | `Twenty48State` — 2048 lifecycle | board merge logic, win/lose detection |
| `game_minesweeper.go` | `MinesweeperState` — Minesweeper lifecycle | cell reveal, mine layout, win/lose detection |

Each file owns its FSM type, constants, and `CanTransitionTo`. Each file also owns the *runtime* data structures the game needs (board, snake position, food cells, etc.). The FSM is the lifecycle; the runtime data is the snapshot.

**Don't** create a shared `GameEngine` interface or a unified `Game` struct holding all three. Each game's state shape is different; the abstraction would be cost without payoff.

## Wizard FSM (in `wizard.go`)

```go
type WizardState string

const (
    WizardUnnamed          WizardState = "unnamed"
    WizardNamed            WizardState = "named"
    WizardGameChosen       WizardState = "game_chosen"
    WizardDifficultyChosen WizardState = "difficulty_chosen"
    WizardPlaying          WizardState = "playing"
    WizardFinished         WizardState = "finished"
)

func (s WizardState) CanTransitionTo(next WizardState) bool {
    switch s {
    case WizardUnnamed:           return next == WizardNamed
    case WizardNamed:             return next == WizardGameChosen
    case WizardGameChosen:        return next == WizardDifficultyChosen || next == WizardNamed
    case WizardDifficultyChosen:  return next == WizardPlaying || next == WizardGameChosen
    case WizardPlaying:           return next == WizardFinished
    case WizardFinished:          return next == WizardPlaying || next == WizardGameChosen || next == WizardNamed
    }
    return false
}
```

This is richer than the Todo FSM (multiple valid edges per state, backward transitions, fan-out from `finished`). The switch still fits in well under 30 lines.

## Per-game FSMs

Each game's FSM is a small switch. Examples (final shape):

```go
// game_snake.go
type SnakeState string
const (
    SnakeIdle     SnakeState = "idle"
    SnakePlaying  SnakeState = "playing"
    SnakeGameOver SnakeState = "game_over"
)
// CanTransitionTo: Idle→Playing, Playing→GameOver

// game_2048.go
type Twenty48State string
const (
    T48Playing   Twenty48State = "playing"
    T48Won       Twenty48State = "won"
    T48Continued Twenty48State = "continued"  // chose to keep playing past 2048
    T48Lost      Twenty48State = "lost"
)
// CanTransitionTo: Playing→Won, Playing→Lost, Won→Continued, Won→Lost, Continued→Lost

// game_minesweeper.go
type MinesweeperState string
const (
    MSPlaying MinesweeperState = "playing"
    MSWon     MinesweeperState = "won"
    MSLost    MinesweeperState = "lost"
)
// CanTransitionTo: Playing→Won, Playing→Lost
```

## Composition: wizard and game FSMs

The wizard's `playing` state is the **container** for an active game FSM, not a duplicate of it:
- Wizard transition `difficulty_chosen → playing` initializes a fresh game-FSM in its initial state.
- Per-move requests trigger game-FSM transitions; wizard stays in `playing`.
- When a game-FSM hits a terminal state (Snake `game_over`, 2048 `lost`, Minesweeper `won`/`lost`), the handler triggers wizard `playing → finished` and persists the score.

The two layers don't know each other's internals. `handlers.go` is the bridge.

## Resist these "improvements"

Same discipline as the Todo's FSM, just multiplied across files:

- **No transition tables / `map[State][]State`.** A switch with N cases is fine; tables are abstraction-for-its-own-sake.
- **No event hooks / observers.** Side effects happen in the handler after the FSM says "valid."
- **No `Transition()` method that mutates a session/game struct.** Mutation is the handler's job after `CanTransitionTo` returns true.
- **No shared interface between game FSMs.** They look similar but are independent.
- **No "Next()" generalized helper unless the call site genuinely benefits.** The Todo's `Next()` made sense because the API was "progress to next." The arcade has no equivalent — wizard transitions are user-driven choices, not linear progressions. Don't add `Next()` to Wizard or game FSMs.

## When the FSMs grow

If a future game needs a 4th or 5th state, extend the switch. Don't refactor to a registry. The threshold for "this switch is too long to read" is roughly 8-10 states; we are nowhere near that.

## Game logic is pure functions, not handler-embedded

Each game's file holds **three layers**, all in the same `package arcade`:

1. **Lifecycle FSM** — type + constants + `CanTransitionTo`. (This rule.)
2. **Strongly-typed runtime data structures** — `SnakeBoard`, `T48Board`, `MSBoard`, `Cell`, `Direction`. (See `.claude/rules/pure_functions.md`.)
3. **Pure game-logic functions** — `Tick`, `ApplyMove`, `RevealCell`, `SetDirection`. They take board + input, return (new board, new FSM state). (See `.claude/rules/pure_functions.md`.)

The FSM rule (this file) handles *which phase the game is in and whether a transition is valid*. The pure-functions rule handles *the computation that decides what the next phase should be and what the new board looks like*. Both rules co-load on the same files because they describe complementary concerns.

The handler is the only impure layer — it loads state, calls the pure function, validates the FSM transition the function reports, persists optimistically, renders.

## Validation lives at the handler boundary

Every state-mutating handler follows this exact shape:

```
1. Load wizard state from sessions table (by session cookie).
2. If the request implies a wizard transition (forward step, back, replay):
   a. Compute the intended target wizard state from the user's action.
   b. Check WizardState.CanTransitionTo(target). If false → OOB error banner.
3. If the request is a game move:
   a. Load the active game's FSM state + board from DB.
   b. Call the pure game function: (newBoard, newState) = ApplyMove(board, input)
   c. If newState != currentState, check GameState.CanTransitionTo(newState).
      If false → OOB error banner.
4. Apply the change via optimistic UPDATE (WHERE state = expected_state),
   check rowsAffected. If 0 → OOB error banner.
5. Render the new view.
```

The FSMs and pure game functions themselves never:
- Read or write the database.
- Render HTML.
- Know about HTTP, HTMX, or Echo.
- Know about each other.

That separation is what makes the architecture testable in milliseconds without a browser.

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
