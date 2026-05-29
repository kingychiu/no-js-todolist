package arcade

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/kingychiu/no-js-arcade/db"
	"github.com/labstack/echo/v4"
)

type Handlers struct {
	Q     *db.Queries
	Views *Views
	rng   *rand.Rand
	Snake *SnakeRuntime
}

const sessionCookieName = "arcade_session"

// --- session helpers ---

func (h *Handlers) sessionFor(c echo.Context) (db.Session, error) {
	ctx := c.Request().Context()
	var id string
	if cookie, err := c.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		id = cookie.Value
	} else {
		id = newSessionID()
		c.SetCookie(&http.Cookie{
			Name:     sessionCookieName,
			Value:    id,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400 * 30,
		})
	}
	if err := h.Q.UpsertSession(ctx, id); err != nil {
		return db.Session{}, err
	}
	return h.Q.GetSession(ctx, id)
}

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}

// buildViewData populates the per-step data the templates expect.
func (h *Handlers) buildViewData(c echo.Context, sess db.Session) (*ViewData, error) {
	data := &ViewData{Session: sess}
	ctx := c.Request().Context()

	switch WizardState(sess.WizardState) {
	case WizardPlaying:
		gs, err := h.Q.GetGameState(ctx, sess.ID)
		if err != nil {
			return data, nil // game not initialized yet — render what we can
		}
		switch Game(gs.Game) {
		case Game2048:
			var b T48Board
			if err := json.Unmarshal([]byte(gs.Board), &b); err == nil {
				data.Board = b
			}
		case GameMinesweeper:
			var b MSBoard
			if err := json.Unmarshal([]byte(gs.Board), &b); err == nil {
				data.Board = b
			}
		case GameSnake:
			if sg, ok := h.Snake.Get(sess.ID); ok {
				board, _, _ := sg.Snapshot()
				data.Board = NewSnakeBoardView(board)
			}
		}
	case WizardFinished:
		gs, err := h.Q.GetGameState(ctx, sess.ID)
		if err == nil {
			data.FinalScore = gs.Score
		}
		entries, err := h.Q.ListLeaderboard(ctx, db.ListLeaderboardParams{
			Game:       sess.ChosenGame,
			Difficulty: sess.ChosenDiff,
		})
		if err != nil {
			return nil, err
		}
		rows := make([]LeaderboardRow, len(entries))
		for i, e := range entries {
			rows[i] = LeaderboardRow{Entry: e, IsCurrent: e.Name == sess.Name}
		}
		data.Leaderboard = rows
	}
	return data, nil
}

// renderFrame renders the wizard_frame for the current session state.
func (h *Handlers) renderFrame(c echo.Context, sess db.Session) error {
	data, err := h.buildViewData(c, sess)
	if err != nil {
		return err
	}
	return h.Views.Render(c, "wizard_frame", data)
}

// renderFrameWithError renders wizard_frame + the OOB error banner.
func (h *Handlers) renderFrameWithError(c echo.Context, sess db.Session, msg string) error {
	data, err := h.buildViewData(c, sess)
	if err != nil {
		return err
	}
	return h.Views.RenderWithError(c, "wizard_frame", data, msg)
}

// transitionSession runs the standard wizard-state UPDATE with optimistic lock.
// Returns (sessionAfter, true) on success, (currentSession, false) on rejection.
func (h *Handlers) transitionSession(
	c echo.Context,
	sess db.Session,
	target WizardState,
	game, diff string,
) (db.Session, bool, error) {
	if !WizardState(sess.WizardState).CanTransitionTo(target) {
		return sess, false, nil
	}
	ctx := c.Request().Context()
	rows, err := h.Q.UpdateSessionWizardState(ctx, db.UpdateSessionWizardStateParams{
		NewState:      string(target),
		ChosenGame:    game,
		ChosenDiff:    diff,
		ID:            sess.ID,
		ExpectedState: sess.WizardState,
	})
	if err != nil {
		return sess, false, err
	}
	if rows == 0 {
		latest, _ := h.Q.GetSession(ctx, sess.ID)
		return latest, false, nil
	}
	latest, err := h.Q.GetSession(ctx, sess.ID)
	if err != nil {
		return sess, false, err
	}
	return latest, true, nil
}

// --- handlers ---

func (h *Handlers) GetIndex(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	data, err := h.buildViewData(c, sess)
	if err != nil {
		return err
	}
	return h.Views.Render(c, "page", data)
}

func (h *Handlers) PostWizardName(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" || len(name) > 32 {
		return h.renderFrameWithError(c, sess, "Name must be 1–32 characters.")
	}
	ctx := c.Request().Context()
	if _, err := h.Q.UpdateSessionName(ctx, db.UpdateSessionNameParams{Name: name, ID: sess.ID}); err != nil {
		return err
	}
	sess.Name = name
	after, ok, err := h.transitionSession(c, sess, WizardNamed, sess.ChosenGame, sess.ChosenDiff)
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "Step already passed.")
	}
	return h.renderFrame(c, after)
}

func (h *Handlers) PostWizardGame(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	game := Game(c.FormValue("game"))
	if !ValidGame(game) {
		return h.renderFrameWithError(c, sess, "Unknown game.")
	}
	if game != Game2048 && game != GameMinesweeper && game != GameSnake {
		return h.renderFrameWithError(c, sess, "That game isn't available yet.")
	}
	after, ok, err := h.transitionSession(c, sess, WizardGameChosen, string(game), "")
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "Pick from the current step.")
	}
	return h.renderFrame(c, after)
}

func (h *Handlers) PostWizardDifficulty(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	diff := Difficulty(c.FormValue("difficulty"))
	if !ValidDifficulty(diff) {
		return h.renderFrameWithError(c, sess, "Unknown difficulty.")
	}
	after, ok, err := h.transitionSession(c, sess, WizardDifficultyChosen, sess.ChosenGame, string(diff))
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "Pick from the current step.")
	}
	return h.renderFrame(c, after)
}

func (h *Handlers) PostWizardStart(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	// Initialize a fresh game state for the chosen game+difficulty.
	if err := h.initGame(c, sess); err != nil {
		return err
	}
	after, ok, err := h.transitionSession(c, sess, WizardPlaying, sess.ChosenGame, sess.ChosenDiff)
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "Cannot start now.")
	}
	return h.renderFrame(c, after)
}

func (h *Handlers) PostWizardBack(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	var target WizardState
	var game, diff string
	switch WizardState(sess.WizardState) {
	case WizardGameChosen:
		target = WizardNamed
		game, diff = "", ""
	case WizardDifficultyChosen:
		target = WizardGameChosen
		game, diff = sess.ChosenGame, ""
	default:
		return h.renderFrameWithError(c, sess, "Nothing to go back to from here.")
	}
	after, ok, err := h.transitionSession(c, sess, target, game, diff)
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "State changed.")
	}
	return h.renderFrame(c, after)
}

func (h *Handlers) PostWizardQuit(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	// If a Snake goroutine is running for this session, shut it down.
	if Game(sess.ChosenGame) == GameSnake {
		h.Snake.Stop(sess.ID)
	}
	// Playing → Finished, no leaderboard entry (player chose to quit).
	after, ok, err := h.transitionSession(c, sess, WizardFinished, sess.ChosenGame, sess.ChosenDiff)
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "Not currently playing.")
	}
	return h.renderFrame(c, after)
}

func (h *Handlers) PostWizardReplay(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	if err := h.initGame(c, sess); err != nil {
		return err
	}
	after, ok, err := h.transitionSession(c, sess, WizardPlaying, sess.ChosenGame, sess.ChosenDiff)
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "Cannot replay from here.")
	}
	return h.renderFrame(c, after)
}

func (h *Handlers) PostWizardChangeDifficulty(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	h.Snake.Stop(sess.ID)
	after, ok, err := h.transitionSession(c, sess, WizardGameChosen, sess.ChosenGame, "")
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "State changed.")
	}
	return h.renderFrame(c, after)
}

func (h *Handlers) PostWizardDifferentGame(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	h.Snake.Stop(sess.ID)
	after, ok, err := h.transitionSession(c, sess, WizardNamed, "", "")
	if err != nil {
		return err
	}
	if !ok {
		return h.renderFrameWithError(c, after, "State changed.")
	}
	return h.renderFrame(c, after)
}

// --- game-specific ---

// initGame creates or replaces the game_states row for this session, seeded
// with a fresh board for the chosen game+difficulty.
func (h *Handlers) initGame(c echo.Context, sess db.Session) error {
	ctx := c.Request().Context()
	if sess.ChosenGame == "" || sess.ChosenDiff == "" {
		return errors.New("game/difficulty not chosen")
	}
	switch Game(sess.ChosenGame) {
	case Game2048:
		board := NewT48Board(T48SizeFor(Difficulty(sess.ChosenDiff)), h.rng)
		boardJSON, err := json.Marshal(board)
		if err != nil {
			return err
		}
		return h.Q.UpsertGameState(ctx, db.UpsertGameStateParams{
			SessionID:  sess.ID,
			Game:       sess.ChosenGame,
			Difficulty: sess.ChosenDiff,
			FsmState:   string(T48Playing),
			Board:      string(boardJSON),
			Score:      0,
		})
	case GameMinesweeper:
		w, h2, mines := MSDimensions(Difficulty(sess.ChosenDiff))
		board := NewMSBoard(w, h2, mines)
		boardJSON, err := json.Marshal(board)
		if err != nil {
			return err
		}
		return h.Q.UpsertGameState(ctx, db.UpsertGameStateParams{
			SessionID:  sess.ID,
			Game:       sess.ChosenGame,
			Difficulty: sess.ChosenDiff,
			FsmState:   string(MSPlaying),
			Board:      string(boardJSON),
			Score:      0,
		})
	case GameSnake:
		w, ht, tick := SnakeDimensions(Difficulty(sess.ChosenDiff))
		snakeRng := rand.New(rand.NewSource(h.rng.Int63()))
		board := NewSnakeBoard(w, ht, snakeRng)
		boardJSON, err := json.Marshal(board)
		if err != nil {
			return err
		}
		if err := h.Q.UpsertGameState(ctx, db.UpsertGameStateParams{
			SessionID:  sess.ID,
			Game:       sess.ChosenGame,
			Difficulty: sess.ChosenDiff,
			FsmState:   string(SnakePlaying),
			Board:      string(boardJSON),
			Score:      0,
		}); err != nil {
			return err
		}
		// Replace any existing goroutine and start a fresh one. The onEnd
		// callback persists the leaderboard entry and transitions the
		// wizard to finished from the background goroutine.
		h.Snake.Start(sess.ID, board, tick, snakeRng, func(sid string, score int) {
			h.onSnakeGameOver(sid, score)
		})
		return nil
	}
	return errors.New("unsupported game")
}

func (h *Handlers) PostT48Move(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	if WizardState(sess.WizardState) != WizardPlaying || Game(sess.ChosenGame) != Game2048 {
		return h.renderFrameWithError(c, sess, "Not playing 2048 right now.")
	}
	dir := T48Direction(c.FormValue("dir"))
	if !ValidT48Direction(dir) {
		return h.RenderBoardWithError(c, sess, "Unknown direction.")
	}

	ctx := c.Request().Context()
	gs, err := h.Q.GetGameState(ctx, sess.ID)
	if err != nil {
		return err
	}
	var board T48Board
	if err := json.Unmarshal([]byte(gs.Board), &board); err != nil {
		return err
	}

	currentFSM := T48State(gs.FsmState)
	after, newFSM, changed := ApplyMove(board, dir, h.rng)
	if !changed {
		return h.Views.RenderWithError(c, "twenty48_board", board, "No move in that direction.")
	}
	if newFSM != currentFSM {
		if !currentFSM.CanTransitionTo(newFSM) {
			return h.Views.RenderWithError(c, "twenty48_board", board, "Invalid transition.")
		}
	}

	afterJSON, err := json.Marshal(after)
	if err != nil {
		return err
	}
	rows, err := h.Q.UpdateGameState(ctx, db.UpdateGameStateParams{
		NewState:      string(newFSM),
		Board:         string(afterJSON),
		Score:         int64(after.Score),
		SessionID:     sess.ID,
		ExpectedState: string(currentFSM),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		// Lost a race; reload and reject.
		return h.Views.RenderWithError(c, "twenty48_board", board, "State changed concurrently.")
	}

	// If the game ended (Won or Lost), record leaderboard + transition wizard.
	if newFSM == T48Won || newFSM == T48Lost {
		if _, err := h.Q.InsertLeaderboardEntry(ctx, db.InsertLeaderboardEntryParams{
			Name:       sess.Name,
			Game:       sess.ChosenGame,
			Difficulty: sess.ChosenDiff,
			Score:      int64(after.Score),
		}); err != nil {
			return err
		}
		after2, ok, err := h.transitionSession(c, sess, WizardFinished, sess.ChosenGame, sess.ChosenDiff)
		if err != nil {
			return err
		}
		if !ok {
			return h.Views.RenderWithError(c, "twenty48_board", after, "Could not transition to finished.")
		}
		// Retarget the swap to the wizard frame so the whole step view changes.
		c.Response().Header().Set("HX-Retarget", "#wizard-frame")
		c.Response().Header().Set("HX-Reswap", "innerHTML")
		return h.renderFrame(c, after2)
	}

	// Game continues: respond with the updated board only.
	return h.Views.Render(c, "twenty48_board", after)
}

// --- Snake handlers ---

// GetSnakeBoard is the long-poll endpoint. It blocks until the goroutine
// produces another frame, then returns the new board fragment. If the game
// ended, it retargets the swap to the wizard frame so the player sees the
// finished step.
func (h *Handlers) GetSnakeBoard(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	if WizardState(sess.WizardState) != WizardPlaying || Game(sess.ChosenGame) != GameSnake {
		c.Response().Header().Set("HX-Retarget", "#wizard-frame")
		c.Response().Header().Set("HX-Reswap", "innerHTML")
		return h.renderFrame(c, sess)
	}

	sg, ok := h.Snake.Get(sess.ID)
	if !ok {
		c.Response().Header().Set("HX-Retarget", "#wizard-frame")
		c.Response().Header().Set("HX-Reswap", "innerHTML")
		return h.renderFrame(c, sess)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 25*time.Second)
	defer cancel()
	sg.WaitNextFrame(ctx)

	board, state, _ := sg.Snapshot()
	if state == SnakeGameOver {
		latest, err := h.Q.GetSession(c.Request().Context(), sess.ID)
		if err != nil {
			return err
		}
		c.Response().Header().Set("HX-Retarget", "#wizard-frame")
		c.Response().Header().Set("HX-Reswap", "innerHTML")
		return h.renderFrame(c, latest)
	}

	return h.Views.Render(c, "snake_board", NewSnakeBoardView(board))
}

// PostSnakeDirection forwards a direction change into the goroutine's input
// channel. Fire-and-forget: returns 204 so the client's hx-swap="none"
// doesn't change anything visible.
func (h *Handlers) PostSnakeDirection(c echo.Context) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	if WizardState(sess.WizardState) != WizardPlaying || Game(sess.ChosenGame) != GameSnake {
		return c.NoContent(http.StatusNoContent)
	}
	dir := SnakeDirection(c.FormValue("dir"))
	if !ValidSnakeDirection(dir) {
		return c.NoContent(http.StatusNoContent)
	}
	if sg, ok := h.Snake.Get(sess.ID); ok {
		sg.PushDirection(dir)
	}
	return c.NoContent(http.StatusNoContent)
}

// onSnakeGameOver runs from inside the snake goroutine when it detects a
// collision. It inserts a leaderboard entry, persists the final board state,
// and transitions the session's wizard to Finished. Errors are intentionally
// swallowed (the long-poll handler will re-check the session and recover).
func (h *Handlers) onSnakeGameOver(sessionID string, score int) {
	ctx := context.Background()
	sess, err := h.Q.GetSession(ctx, sessionID)
	if err != nil {
		return
	}
	_, _ = h.Q.InsertLeaderboardEntry(ctx, db.InsertLeaderboardEntryParams{
		Name:       sess.Name,
		Game:       sess.ChosenGame,
		Difficulty: sess.ChosenDiff,
		Score:      int64(score),
	})

	if sg, ok := h.Snake.Get(sessionID); ok {
		board, _, _ := sg.Snapshot()
		if boardJSON, err := json.Marshal(board); err == nil {
			_ = h.Q.UpsertGameState(ctx, db.UpsertGameStateParams{
				SessionID:  sessionID,
				Game:       sess.ChosenGame,
				Difficulty: sess.ChosenDiff,
				FsmState:   string(SnakeGameOver),
				Board:      string(boardJSON),
				Score:      int64(score),
			})
		}
	}

	_, _ = h.Q.UpdateSessionWizardState(ctx, db.UpdateSessionWizardStateParams{
		NewState:      string(WizardFinished),
		ChosenGame:    sess.ChosenGame,
		ChosenDiff:    sess.ChosenDiff,
		ID:            sess.ID,
		ExpectedState: string(WizardPlaying),
	})
}

// --- Minesweeper handlers ---

// PostMSReveal handles a reveal click on a Minesweeper cell.
func (h *Handlers) PostMSReveal(c echo.Context) error {
	return h.postMSAction(c, "reveal")
}

// PostMSFlag handles a flag/unflag toggle on a Minesweeper cell.
func (h *Handlers) PostMSFlag(c echo.Context) error {
	return h.postMSAction(c, "flag")
}

func (h *Handlers) postMSAction(c echo.Context, action string) error {
	sess, err := h.sessionFor(c)
	if err != nil {
		return err
	}
	if WizardState(sess.WizardState) != WizardPlaying || Game(sess.ChosenGame) != GameMinesweeper {
		return h.renderFrameWithError(c, sess, "Not playing Minesweeper right now.")
	}
	x, err := parseIntField(c, "x")
	if err != nil {
		return h.renderMSBoardWithError(c, sess, "Invalid cell coordinate.")
	}
	y, err := parseIntField(c, "y")
	if err != nil {
		return h.renderMSBoardWithError(c, sess, "Invalid cell coordinate.")
	}

	ctx := c.Request().Context()
	gs, err := h.Q.GetGameState(ctx, sess.ID)
	if err != nil {
		return err
	}
	var board MSBoard
	if err := json.Unmarshal([]byte(gs.Board), &board); err != nil {
		return err
	}

	currentFSM := MSState(gs.FsmState)
	var after MSBoard
	var newFSM MSState
	if action == "reveal" {
		after, newFSM = RevealCell(board, x, y, h.rng)
	} else {
		after, newFSM = FlagCell(board, x, y)
	}

	if newFSM != currentFSM {
		if !currentFSM.CanTransitionTo(newFSM) {
			return h.Views.RenderWithError(c, "minesweeper_board", board, "Invalid transition.")
		}
	}

	afterJSON, err := json.Marshal(after)
	if err != nil {
		return err
	}
	rows, err := h.Q.UpdateGameState(ctx, db.UpdateGameStateParams{
		NewState:      string(newFSM),
		Board:         string(afterJSON),
		Score:         int64(MSScore(after)),
		SessionID:     sess.ID,
		ExpectedState: string(currentFSM),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return h.Views.RenderWithError(c, "minesweeper_board", board, "State changed concurrently.")
	}

	if newFSM == MSWon || newFSM == MSLost {
		if _, err := h.Q.InsertLeaderboardEntry(ctx, db.InsertLeaderboardEntryParams{
			Name:       sess.Name,
			Game:       sess.ChosenGame,
			Difficulty: sess.ChosenDiff,
			Score:      int64(MSScore(after)),
		}); err != nil {
			return err
		}
		after2, ok, err := h.transitionSession(c, sess, WizardFinished, sess.ChosenGame, sess.ChosenDiff)
		if err != nil {
			return err
		}
		if !ok {
			return h.Views.RenderWithError(c, "minesweeper_board", after, "Could not transition to finished.")
		}
		c.Response().Header().Set("HX-Retarget", "#wizard-frame")
		c.Response().Header().Set("HX-Reswap", "innerHTML")
		return h.renderFrame(c, after2)
	}

	return h.Views.Render(c, "minesweeper_board", after)
}

func (h *Handlers) renderMSBoardWithError(c echo.Context, sess db.Session, msg string) error {
	ctx := c.Request().Context()
	gs, err := h.Q.GetGameState(ctx, sess.ID)
	if err != nil {
		return h.Views.RenderError(c, msg)
	}
	var board MSBoard
	if err := json.Unmarshal([]byte(gs.Board), &board); err == nil {
		return h.Views.RenderWithError(c, "minesweeper_board", board, msg)
	}
	return h.Views.RenderError(c, msg)
}

func parseIntField(c echo.Context, name string) (int, error) {
	v := strings.TrimSpace(c.FormValue(name))
	if v == "" {
		return 0, errors.New("empty")
	}
	n := 0
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int(ch-'0')
		if n > 1024 {
			return 0, errors.New("too large")
		}
	}
	return n, nil
}

// RenderBoardWithError renders the current 2048 board (unchanged) + OOB error.
// Used when the move was invalid input but the game state is unchanged.
func (h *Handlers) RenderBoardWithError(c echo.Context, sess db.Session, msg string) error {
	ctx := c.Request().Context()
	gs, err := h.Q.GetGameState(ctx, sess.ID)
	if err != nil {
		return h.Views.RenderError(c, msg)
	}
	var board T48Board
	if err := json.Unmarshal([]byte(gs.Board), &board); err == nil {
		return h.Views.RenderWithError(c, "twenty48_board", board, msg)
	}
	return h.Views.RenderError(c, msg)
}
