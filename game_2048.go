package arcade

import (
	"math/rand"
)

// T48State is the lifecycle phase of a 2048 game. Continued is reserved for a
// future "keep playing past 2048" UX; Phase 1 only exercises Playing/Won/Lost.
type T48State string

const (
	T48Playing   T48State = "playing"
	T48Won       T48State = "won"
	T48Continued T48State = "continued"
	T48Lost      T48State = "lost"
)

// CanTransitionTo enforces the legal phases the 2048 game can pass through.
func (s T48State) CanTransitionTo(next T48State) bool {
	switch s {
	case T48Playing:
		return next == T48Won || next == T48Lost
	case T48Won:
		return next == T48Continued || next == T48Lost
	case T48Continued:
		return next == T48Lost
	}
	return false
}

// T48Direction is the move direction. Stable string values are used in routes.
type T48Direction string

const (
	T48Left  T48Direction = "left"
	T48Right T48Direction = "right"
	T48Up    T48Direction = "up"
	T48Down  T48Direction = "down"
)

// ValidT48Direction reports whether the value is a recognized move.
func ValidT48Direction(d T48Direction) bool {
	switch d {
	case T48Left, T48Right, T48Up, T48Down:
		return true
	}
	return false
}

// T48Board is the in-memory game state, serialized to JSON for storage.
type T48Board struct {
	Size  int     `json:"size"`
	Cells [][]int `json:"cells"`
	Score int     `json:"score"`
}

// T48SizeFor returns the board size for the given difficulty.
//
//	Easy   → 5×5 (more room to maneuver)
//	Medium → 4×4 (classic)
//	Hard   → 3×3 (cramped)
func T48SizeFor(d Difficulty) int {
	switch d {
	case DiffEasy:
		return 5
	case DiffHard:
		return 3
	}
	return 4 // medium / default
}

// NewT48Board returns a fresh board with two starting tiles placed.
func NewT48Board(size int, rng *rand.Rand) T48Board {
	b := T48Board{
		Size:  size,
		Cells: make([][]int, size),
	}
	for i := range b.Cells {
		b.Cells[i] = make([]int, size)
	}
	b = placeRandomTile(b, rng)
	b = placeRandomTile(b, rng)
	return b
}

// ApplyMove computes the result of shifting and merging in direction d, then
// spawning one new tile if the board changed. Returns the new board, the
// implied FSM state (Playing / Won / Lost), and whether anything moved. If
// changed=false, the caller should treat the move as a no-op.
func ApplyMove(board T48Board, d T48Direction, rng *rand.Rand) (T48Board, T48State, bool) {
	after, scored := shiftAndMerge(board, d)
	if boardsEqual(board.Cells, after.Cells) {
		return board, classifyT48State(board), false
	}
	after.Score = board.Score + scored
	after = placeRandomTile(after, rng)
	return after, classifyT48State(after), true
}

// Hit2048 reports whether the board contains a 2048 tile (or higher).
func Hit2048(board T48Board) bool {
	for _, row := range board.Cells {
		for _, v := range row {
			if v >= 2048 {
				return true
			}
		}
	}
	return false
}

// HasValidMoves reports whether any move would change the board. False means
// the game is over (every cell is filled and no two adjacent tiles match).
func HasValidMoves(board T48Board) bool {
	n := board.Size
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if board.Cells[r][c] == 0 {
				return true
			}
			if c+1 < n && board.Cells[r][c] == board.Cells[r][c+1] {
				return true
			}
			if r+1 < n && board.Cells[r][c] == board.Cells[r+1][c] {
				return true
			}
		}
	}
	return false
}

// classifyT48State returns the lifecycle phase implied by the current board.
// Phase 1 treats Won as terminal (no Continued path yet).
func classifyT48State(board T48Board) T48State {
	if Hit2048(board) {
		return T48Won
	}
	if !HasValidMoves(board) {
		return T48Lost
	}
	return T48Playing
}

// shiftAndMerge returns a board with tiles shifted toward d and equal
// adjacent pairs merged (each tile merges at most once per move). The
// returned score is the sum of merged values for this move only.
func shiftAndMerge(board T48Board, d T48Direction) (T48Board, int) {
	n := board.Size
	cells := copyCells(board.Cells)
	totalScore := 0

	switch d {
	case T48Left:
		for r := 0; r < n; r++ {
			cells[r], totalScore = compactAndMergeRow(cells[r], totalScore)
		}
	case T48Right:
		for r := 0; r < n; r++ {
			reverseInts(cells[r])
			cells[r], totalScore = compactAndMergeRow(cells[r], totalScore)
			reverseInts(cells[r])
		}
	case T48Up:
		for c := 0; c < n; c++ {
			col := extractCol(cells, c)
			col, totalScore = compactAndMergeRow(col, totalScore)
			setCol(cells, c, col)
		}
	case T48Down:
		for c := 0; c < n; c++ {
			col := extractCol(cells, c)
			reverseInts(col)
			col, totalScore = compactAndMergeRow(col, totalScore)
			reverseInts(col)
			setCol(cells, c, col)
		}
	}
	return T48Board{Size: n, Cells: cells, Score: board.Score}, totalScore
}

// compactAndMergeRow shifts non-zeros to the left, merges equal adjacent
// pairs once, and returns the row padded with zeros to its original length,
// plus the running score after this row's merges are added.
func compactAndMergeRow(row []int, runningScore int) ([]int, int) {
	n := len(row)
	compact := make([]int, 0, n)
	for _, v := range row {
		if v != 0 {
			compact = append(compact, v)
		}
	}
	merged := make([]int, 0, n)
	for i := 0; i < len(compact); {
		if i+1 < len(compact) && compact[i] == compact[i+1] {
			sum := compact[i] * 2
			merged = append(merged, sum)
			runningScore += sum
			i += 2
		} else {
			merged = append(merged, compact[i])
			i++
		}
	}
	out := make([]int, n)
	copy(out, merged)
	return out, runningScore
}

// placeRandomTile drops a 2 (90%) or 4 (10%) into a random empty cell.
// If the board is full, returns it unchanged.
func placeRandomTile(board T48Board, rng *rand.Rand) T48Board {
	empties := make([][2]int, 0, board.Size*board.Size)
	for r := 0; r < board.Size; r++ {
		for c := 0; c < board.Size; c++ {
			if board.Cells[r][c] == 0 {
				empties = append(empties, [2]int{r, c})
			}
		}
	}
	if len(empties) == 0 {
		return board
	}
	pick := empties[rng.Intn(len(empties))]
	value := 2
	if rng.Intn(10) == 0 {
		value = 4
	}
	board.Cells[pick[0]][pick[1]] = value
	return board
}

func copyCells(cells [][]int) [][]int {
	out := make([][]int, len(cells))
	for i := range cells {
		out[i] = make([]int, len(cells[i]))
		copy(out[i], cells[i])
	}
	return out
}

func boardsEqual(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func reverseInts(s []int) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func extractCol(cells [][]int, c int) []int {
	n := len(cells)
	col := make([]int, n)
	for r := 0; r < n; r++ {
		col[r] = cells[r][c]
	}
	return col
}

func setCol(cells [][]int, c int, col []int) {
	for r := 0; r < len(cells); r++ {
		cells[r][c] = col[r]
	}
}
