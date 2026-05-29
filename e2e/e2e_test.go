// Package e2e holds black-box user-story tests for no-js-arcade.
//
// These tests can only drive the system through HTTP. They cannot import the
// arcade package's unexported helpers, so every state change must go through
// the public API the way a real client does.
package e2e

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	_ "github.com/mattn/go-sqlite3"

	arcade "github.com/kingychiu/no-js-todolist"
)

func newServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	dbpath := filepath.Join(t.TempDir(), "e2e.db")
	sqldb, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal=WAL&_busy_timeout=5000&_sync=NORMAL&_fk=on", dbpath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	e, err := arcade.NewApp(sqldb)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	return srv, client
}

func parse(t *testing.T, body io.Reader) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func postForm(t *testing.T, client *http.Client, urlStr string, values url.Values) *http.Response {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	resp, err := client.PostForm(urlStr, values)
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	return resp
}

func get(t *testing.T, client *http.Client, urlStr string) *http.Response {
	t.Helper()
	resp, err := client.Get(urlStr)
	if err != nil {
		t.Fatalf("GET %s: %v", urlStr, err)
	}
	return resp
}

func parseAndClose(t *testing.T, resp *http.Response) *goquery.Document {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	return parse(t, resp.Body)
}

func dataStep(doc *goquery.Document) string {
	return doc.Find("[data-step]").AttrOr("data-step", "")
}

func hasOOBErrorBanner(doc *goquery.Document) bool {
	return doc.Find(`#error-banner[hx-swap-oob="true"]`).Length() > 0
}

// --- User-story tests ---

// TestE2E_FullArcadeFlow walks the user from a fresh visit all the way to
// the finished step via Quit, and verifies the leaderboard view appears.
func TestE2E_FullArcadeFlow(t *testing.T) {
	t.Parallel()
	srv, client := newServer(t)

	// Land on the homepage — should be the name step.
	doc := parseAndClose(t, get(t, client, srv.URL+"/"))
	if dataStep(doc) != "name" {
		t.Fatalf("step = %q, want name", dataStep(doc))
	}

	// Submit name.
	doc = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/name", url.Values{"name": {"Alice"}}))
	if dataStep(doc) != "game" {
		t.Fatalf("after name, step = %q, want game", dataStep(doc))
	}
	if !strings.Contains(doc.Text(), "Alice") {
		t.Errorf("expected name 'Alice' in game-step view")
	}

	// Pick 2048.
	doc = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/game", url.Values{"game": {"2048"}}))
	if dataStep(doc) != "difficulty" {
		t.Fatalf("after game, step = %q, want difficulty", dataStep(doc))
	}

	// Pick easy.
	doc = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/difficulty", url.Values{"difficulty": {"easy"}}))
	if dataStep(doc) != "ready" {
		t.Fatalf("after difficulty, step = %q, want ready", dataStep(doc))
	}

	// Start.
	doc = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/start", nil))
	if dataStep(doc) != "playing" {
		t.Fatalf("after start, step = %q, want playing", dataStep(doc))
	}
	if doc.Find("#twenty48-board").Length() == 0 {
		t.Errorf("expected 2048 board after start")
	}

	// Make a move.
	doc = parseAndClose(t, postForm(t, client, srv.URL+"/game/2048/move", url.Values{"dir": {"left"}}))
	if doc.Find("#twenty48-board").Length() == 0 {
		t.Errorf("expected board fragment in move response")
	}

	// Quit.
	doc = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/quit", nil))
	if dataStep(doc) != "finished" {
		t.Fatalf("after quit, step = %q, want finished", dataStep(doc))
	}
	if doc.Find("table").Length() == 0 {
		t.Errorf("expected leaderboard table on finished step")
	}
}

// TestE2E_WizardSkipAhead_Rejected confirms that posting a later-step form
// while the session is still on step 1 returns the OOB error banner.
func TestE2E_WizardSkipAhead_Rejected(t *testing.T) {
	t.Parallel()
	srv, client := newServer(t)
	doc := parseAndClose(t, postForm(t, client, srv.URL+"/wizard/game", url.Values{"game": {"2048"}}))
	if !hasOOBErrorBanner(doc) {
		t.Errorf("expected OOB error banner on skip-ahead")
	}
}

// TestE2E_BackNav_DifficultyToGame walks to the difficulty step and uses
// the back button to return to the game picker.
func TestE2E_BackNav_DifficultyToGame(t *testing.T) {
	t.Parallel()
	srv, client := newServer(t)

	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/name", url.Values{"name": {"Bob"}}))
	doc := parseAndClose(t, postForm(t, client, srv.URL+"/wizard/game", url.Values{"game": {"2048"}}))
	if dataStep(doc) != "difficulty" {
		t.Fatalf("setup: expected difficulty, got %q", dataStep(doc))
	}

	doc = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/back", nil))
	if dataStep(doc) != "game" {
		t.Errorf("after back, step = %q, want game", dataStep(doc))
	}
}

// TestE2E_ReplayFromFinished plays a game, quits, then clicks Replay and
// confirms the session lands back in the playing state with a fresh board.
func TestE2E_ReplayFromFinished(t *testing.T) {
	t.Parallel()
	srv, client := newServer(t)

	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/name", url.Values{"name": {"Carol"}}))
	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/game", url.Values{"game": {"2048"}}))
	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/difficulty", url.Values{"difficulty": {"medium"}}))
	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/start", nil))
	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/quit", nil))

	doc := parseAndClose(t, postForm(t, client, srv.URL+"/wizard/replay", nil))
	if dataStep(doc) != "playing" {
		t.Errorf("after replay, step = %q, want playing", dataStep(doc))
	}
	if doc.Find("#twenty48-board").Length() == 0 {
		t.Errorf("expected fresh board after replay")
	}
}

// TestE2E_DifferentGame_FromFinished returns the user to the game picker
// from the finished step.
func TestE2E_DifferentGame_FromFinished(t *testing.T) {
	t.Parallel()
	srv, client := newServer(t)

	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/name", url.Values{"name": {"Dora"}}))
	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/game", url.Values{"game": {"2048"}}))
	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/difficulty", url.Values{"difficulty": {"hard"}}))
	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/start", nil))
	_ = parseAndClose(t, postForm(t, client, srv.URL+"/wizard/quit", nil))

	doc := parseAndClose(t, postForm(t, client, srv.URL+"/wizard/different-game", nil))
	if dataStep(doc) != "game" {
		t.Errorf("after different-game, step = %q, want game", dataStep(doc))
	}
	// Name should be preserved.
	if !strings.Contains(doc.Text(), "Dora") {
		t.Errorf("expected name to persist across different-game")
	}
}
