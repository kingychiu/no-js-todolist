---
paths:
  - "wizard.go"
  - "game_*.go"
---

# Pure Functions, Strongly-Typed Data, and the Functional Core

Applies to `wizard.go` and `game_*.go`. The project's architectural principle:

> **The backend is mostly FSMs + pure functions over strongly-typed data structures. Handlers are the imperative shell — everything else aspires to be pure.**

See `.claude/rules/fsm.md` for the FSM layer (lifecycle phases + `CanTransitionTo`). This rule covers the *logic* layer that sits alongside the FSM in each file: data structs, helpers, and the pure computational functions that compute "given this board + input, what's the new board?"

## The three layers in each game/wizard file

| Layer | Examples | Discipline |
|---|---|---|
| **Strongly-typed data structs** | `SnakeBoard`, `T48Board`, `MSBoard`, `Cell`, `Direction` (typed string), `WizardState` (typed string) | Named types everywhere. No `any` / `interface{}` for game data. |
| **Pure functions** | `Tick(board)`, `ApplyMove(board, dir, rng)`, `RevealCell(board, x, y, rng)`, `Score(board)`, `IsTerminal(state)` | No side effects, all inputs as parameters, return new value. |
| **FSM** | `WizardState`, `SnakeState`, `T48State`, `MSState` + `CanTransitionTo` | (See fsm.md.) |

The lifecycle FSM tells you *which phase*. Pure functions compute *what the new board looks like and which phase comes next*. The two compose at the handler boundary.

## Pure-function discipline

A function in these files is "pure" if it satisfies all of:

1. **No side effects.** No DB calls, no HTTP, no file I/O, no logging, no `panic` for control flow, no global-state mutation, no goroutine launches.
2. **All inputs as parameters.** No reading from package-level vars, `os.Getenv`, `time.Now()`, or `math/rand` globals.
3. **Randomness is explicit.** Functions that need randomness take a `*rand.Rand` parameter. The caller (handler or goroutine) creates the source. Tests pass a seeded `rand.New(rand.NewSource(42))` for determinism.
4. **Time is explicit.** Functions that need timestamps take a `time.Time` parameter. Don't call `time.Now()` inside.
5. **Return new value; don't mutate.** Either take input by value (Go copies the struct), or document explicitly when a pointer-receiver method mutates. Default to by-value.
6. **One conceptual operation per function.** `Tick` advances one step. `ApplyMove` applies one move. If a function does two things, split it.

## Strongly-typed data discipline

1. **Named types for enums.** `type Direction string` with `const (North Direction = "N"; ...)`. Not bare `string` with magic values scattered through the code.
2. **Named types for FSM constants.** `type WizardState string`, `type SnakeState string`, etc. (Covered in fsm.md.)
3. **Named types for coordinates and IDs.** `type Cell struct{ X, Y int }`, not `[2]int`. `type SessionID string` if you want to avoid string-shaped-confusion at the handler boundary.
4. **No `any` / `interface{}` for game data.** Boards have known shapes — model them as structs. JSON serialization for `game_states.board` happens at the DB boundary; in-memory representation stays typed.
5. **Prefer constructors when zero-values aren't meaningful.** `NewSnakeBoard(width, height int, rng *rand.Rand) SnakeBoard`. Tests can still construct directly via struct literals for table-driven cases.

## What this enables — the architectural payoff

- **Microsecond-fast unit tests** with no DB, no HTTP, no templates. Hundreds of table-driven cases per game.
- **Same code paths in production and tests.** No mocks, no fakes for game logic.
- **The harness wins.** Bugs in game logic surface in pure unit tests, not in flaky e2e — exactly what this project is designed to optimize.
- **Composability.** Pure functions chain trivially: `Tick(Tick(Tick(board)))`. Tests can simulate long move sequences cheaply.

## Examples

```go
// game_snake.go

type Direction string
const (
    North Direction = "N"
    South Direction = "S"
    East  Direction = "E"
    West  Direction = "W"
)

type Cell struct{ X, Y int }

type SnakeBoard struct {
    Width, Height int
    Snake         []Cell    // head at index 0
    Direction     Direction
    Food          Cell
    Score         int
}

// Pure. No DB, no HTTP, no globals. Same input → same output.
func Tick(board SnakeBoard) (SnakeBoard, SnakeState) { ... }

// Pure. Rejects reverse-into-self by returning the input unchanged.
func SetDirection(board SnakeBoard, dir Direction) SnakeBoard { ... }

// Pure. Takes RNG explicitly so tests are deterministic.
func PlaceFood(board SnakeBoard, rng *rand.Rand) SnakeBoard { ... }
```

```go
// game_2048.go

type T48Direction string
const (
    T48Left  T48Direction = "left"
    T48Right T48Direction = "right"
    T48Up    T48Direction = "up"
    T48Down  T48Direction = "down"
)

type T48Board struct {
    Size  int      // 3, 4, or 5 depending on difficulty
    Cells [][]int  // [Size][Size]
    Score int
}

// Pure. Returns new board, the FSM state after the move, and a flag for whether anything changed.
func ApplyMove(board T48Board, dir T48Direction, rng *rand.Rand) (T48Board, T48State, bool) { ... }

// Pure. Checks whether any move would change the board (used to detect Lost).
func HasValidMoves(board T48Board) bool { ... }
```

```go
// game_minesweeper.go

type MSCell struct {
    HasMine   bool
    Revealed  bool
    Flagged   bool
    Neighbors int    // count of adjacent mines (computed once)
}

type MSBoard struct {
    Width, Height int
    Cells         [][]MSCell
    MinesPlaced   bool   // true after first reveal places mines
    MineCount     int
}

// Pure. First reveal places mines (using rng), guaranteeing the first click is safe.
func RevealCell(board MSBoard, x, y int, rng *rand.Rand) (MSBoard, MSState) { ... }

// Pure.
func FlagCell(board MSBoard, x, y int) (MSBoard, MSState) { ... }
```

## Where pure functions fit in the handler flow

The handler is the only impure layer. The pure call sits in the middle:

```go
func (h *Handlers) PostT48Move(c echo.Context) error {
    sess := h.session(c)
    state, board := h.loadT48(c, sess)
    dir := parseDirection(c)

    // PURE: compute new board + state from current state + input
    after, newState, changed := ApplyMove(board, dir, h.rng)
    if !changed {
        return h.renderRejection(c, "twenty48_board.html", board, "no valid move that direction")
    }
    if newState != T48State(state) {
        if !T48State(state).CanTransitionTo(newState) {
            return h.renderRejection(c, "twenty48_board.html", board, "invalid transition")
        }
    }

    // IMPURE: persist with optimistic lock
    if err := h.persistT48(c, sess, after, newState); err != nil { return err }

    return h.Views.Render(c, "twenty48_board.html", after)
}
```

The pure layer is unit-tested without any of the impure context. The handler is integration-tested.

## Snake's goroutine is the impure shell

Snake's lifecycle needs a long-running loop (tick-by-tick movement). The goroutine is the impure shell wrapping pure functions:

- `Tick(board)`, `SetDirection(board, dir)`, `PlaceFood(board, rng)` are pure.
- `RunSnakeLoop(ctx, sessionID, input, persist, notify, rng)` is the impure shell: it owns the `time.Ticker`, reads from the input channel, persists, and notifies long-poll waiters. It calls the pure functions; it doesn't reimplement them.
- The shell is tested via the e2e suite. The pure functions are tested directly. They never overlap.

## What pure functions MUST NOT do

- Read or write the database.
- Make HTTP calls.
- Touch the filesystem.
- Call `time.Now()`, `math/rand` globals, `os.Getenv`, or any package-level mutable state.
- Launch goroutines (the shell launches goroutines that CALL pure functions).
- Log or print at runtime (debug prints during development are fine; ship without them).
- Panic for control flow. Panics for genuine "this can't happen" invariants are OK; panics as expected error paths are not.

## What pure functions also avoid (softer rules)

- Don't use `error` returns to signal expected outcomes. Prefer `(value, bool)` for "this might not produce a result" cases (e.g., `Next() (TodoState, bool)`). Reserve `error` for genuinely unexpected failures.
- Don't take `context.Context`. Pure functions are computation; cancellation is the shell's concern.
- Don't return interfaces from concrete pure functions unless you genuinely need polymorphism (you almost never do here — the games are independent, not interchangeable).

## How testing follows from purity

See `.claude/rules/tests.md` for the test categories. The relevant one here is **category 2 — pure game-logic unit tests** — table-driven cases that construct boards as struct literals, call the pure function, and assert on the returned struct. No `httptest`, no DB setup, no goroutines, no mocks. The whole game-logic test suite for all three games should run in well under 100ms.
