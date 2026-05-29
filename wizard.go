package arcade

// WizardState is the user's position in the step-form lobby.
type WizardState string

const (
	WizardUnnamed          WizardState = "unnamed"
	WizardNamed            WizardState = "named"
	WizardGameChosen       WizardState = "game_chosen"
	WizardDifficultyChosen WizardState = "difficulty_chosen"
	WizardPlaying          WizardState = "playing"
	WizardFinished         WizardState = "finished"
)

// CanTransitionTo enforces the lobby's allowed moves. Forward steps are
// linear; backward navigation is allowed from each "chosen" step to the
// previous one; "finished" fans out to Replay (Playing), Change game
// (GameChosen), and Restart (Named).
func (s WizardState) CanTransitionTo(next WizardState) bool {
	switch s {
	case WizardUnnamed:
		return next == WizardNamed
	case WizardNamed:
		return next == WizardGameChosen
	case WizardGameChosen:
		return next == WizardDifficultyChosen || next == WizardNamed
	case WizardDifficultyChosen:
		return next == WizardPlaying || next == WizardGameChosen
	case WizardPlaying:
		return next == WizardFinished
	case WizardFinished:
		return next == WizardPlaying || next == WizardGameChosen || next == WizardNamed
	}
	return false
}

// Game is the set of games the arcade exposes. The string values are the
// stable identifiers stored in the database and used in routes.
type Game string

const (
	GameSnake       Game = "snake"
	Game2048        Game = "2048"
	GameMinesweeper Game = "minesweeper"
)

// Difficulty is per-game, mapped to game-specific knobs in the runtime layer.
type Difficulty string

const (
	DiffEasy   Difficulty = "easy"
	DiffMedium Difficulty = "medium"
	DiffHard   Difficulty = "hard"
)

// ValidGame reports whether g is a known game identifier.
func ValidGame(g Game) bool {
	switch g {
	case GameSnake, Game2048, GameMinesweeper:
		return true
	}
	return false
}

// ValidDifficulty reports whether d is a known difficulty.
func ValidDifficulty(d Difficulty) bool {
	switch d {
	case DiffEasy, DiffMedium, DiffHard:
		return true
	}
	return false
}
