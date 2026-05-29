package arcade

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"strings"

	"github.com/kingychiu/no-js-todolist/db"
	"github.com/labstack/echo/v4"
)

type Handlers struct {
	Q     *db.Queries
	Views *Views
	rng   *rand.Rand
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
		if Game(gs.Game) == Game2048 {
			var b T48Board
			if err := json.Unmarshal([]byte(gs.Board), &b); err == nil {
				data.Board = b
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
	if game != Game2048 {
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
