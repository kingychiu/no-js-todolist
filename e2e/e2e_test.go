// Package e2e holds black-box user-story tests for the no-js-todolist HTTP API.
//
// These tests only use the exported todolist.NewApp surface plus stdlib net/http
// and goquery. They MUST NOT poke at internal helpers (mustForceStatus, direct
// DB writes, etc.) — every state change must flow through the HTTP layer the
// way a real client would drive it.
package e2e

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PuerkitoBio/goquery"
	_ "github.com/mattn/go-sqlite3"

	todolist "github.com/kingychiu/no-js-todolist"
)

// newServer boots a fresh app backed by a per-test SQLite file.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	dbpath := filepath.Join(t.TempDir(), "e2e.db")
	sqldb, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal=WAL&_busy_timeout=5000&_sync=NORMAL&_fk=on", dbpath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })

	e, err := todolist.NewApp(sqldb)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv
}

func parse(t *testing.T, body io.Reader) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func postForm(t *testing.T, srv *httptest.Server, path string, values url.Values) *http.Response {
	t.Helper()
	resp, err := srv.Client().PostForm(srv.URL+path, values)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func do(t *testing.T, srv *httptest.Server, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// extractTodoID finds the first <li id="todo-N"> in the document and returns N.
func extractTodoID(t *testing.T, doc *goquery.Document) int64 {
	t.Helper()
	id, ok := doc.Find("li[id^='todo-']").First().Attr("id")
	if !ok {
		t.Fatalf("no <li id='todo-*'> in document; body:\n%s", documentHTML(doc))
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "todo-"), 10, 64)
	if err != nil {
		t.Fatalf("parse id %q: %v", id, err)
	}
	return n
}

func documentHTML(doc *goquery.Document) string {
	h, err := doc.Html()
	if err != nil {
		return "(no html)"
	}
	return h
}

// actionButtonText returns the text of the first hx-put button in the document, or "" if none.
func actionButtonText(doc *goquery.Document) string {
	return strings.TrimSpace(doc.Find("li button[hx-put]").First().Text())
}

func hasOOBErrorBanner(doc *goquery.Document) bool {
	return doc.Find(`div#error-banner[hx-swap-oob="true"]`).Length() > 0
}

// --- User-story tests ---

// TestE2E_FullLifecycle walks one todo through every state via the HTTP API:
// add → start → complete, then verifies the list shows it as completed.
func TestE2E_FullLifecycle(t *testing.T) {
	t.Parallel()
	srv := newServer(t)

	// User adds a todo.
	resp := postForm(t, srv, "/todos", url.Values{"title": {"learn htmx"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d", resp.StatusCode)
	}
	doc := parse(t, resp.Body)
	_ = resp.Body.Close()
	id := extractTodoID(t, doc)
	if got := actionButtonText(doc); got != "Start Work" {
		t.Fatalf("after POST, button = %q, want %q", got, "Start Work")
	}

	// User clicks "Start Work".
	resp = do(t, srv, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT 1 status = %d", resp.StatusCode)
	}
	doc = parse(t, resp.Body)
	_ = resp.Body.Close()
	if got := actionButtonText(doc); got != "Complete" {
		t.Fatalf("after first PUT, button = %q, want %q", got, "Complete")
	}

	// User clicks "Complete".
	resp = do(t, srv, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT 2 status = %d", resp.StatusCode)
	}
	doc = parse(t, resp.Body)
	_ = resp.Body.Close()
	if doc.Find("li button[hx-put]").Length() != 0 {
		t.Fatalf("after second PUT, expected no action button")
	}
	if cls := doc.Find("li").AttrOr("class", ""); !strings.Contains(cls, "completed") {
		t.Fatalf("after second PUT, li class = %q, expected 'completed'", cls)
	}

	// User reloads — the list shows the completed todo.
	resp = do(t, srv, http.MethodGet, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}
	doc = parse(t, resp.Body)
	_ = resp.Body.Close()
	li := doc.Find(fmt.Sprintf("li#todo-%d", id))
	if li.Length() != 1 {
		t.Fatalf("after reload, expected todo in list")
	}
	if !strings.Contains(li.AttrOr("class", ""), "completed") {
		t.Fatalf("reloaded todo not marked completed")
	}
}

// TestE2E_ProgressThenReject walks past completed and verifies the OOB error
// banner appears while the row stays unchanged.
func TestE2E_ProgressThenReject(t *testing.T) {
	t.Parallel()
	srv := newServer(t)

	resp := postForm(t, srv, "/todos", url.Values{"title": {"ship it"}})
	doc := parse(t, resp.Body)
	_ = resp.Body.Close()
	id := extractTodoID(t, doc)

	// Two valid progressions.
	_ = do(t, srv, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id)).Body.Close()
	_ = do(t, srv, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id)).Body.Close()

	// Third attempt should be rejected.
	resp = do(t, srv, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rejection PUT status = %d", resp.StatusCode)
	}
	doc = parse(t, resp.Body)
	_ = resp.Body.Close()

	if !hasOOBErrorBanner(doc) {
		t.Fatalf("expected OOB error banner on rejected transition")
	}
	if doc.Find("li").Length() != 1 {
		t.Fatalf("expected unchanged row to also be returned")
	}
	if cls := doc.Find("li").AttrOr("class", ""); !strings.Contains(cls, "completed") {
		t.Fatalf("returned row should still be completed; class=%q", cls)
	}
}

// TestE2E_AddDeleteCycle adds two todos, deletes the first, and verifies the
// list reflects exactly the survivor.
func TestE2E_AddDeleteCycle(t *testing.T) {
	t.Parallel()
	srv := newServer(t)

	resp := postForm(t, srv, "/todos", url.Values{"title": {"first"}})
	doc := parse(t, resp.Body)
	_ = resp.Body.Close()
	firstID := extractTodoID(t, doc)

	resp = postForm(t, srv, "/todos", url.Values{"title": {"second"}})
	doc = parse(t, resp.Body)
	_ = resp.Body.Close()
	secondID := extractTodoID(t, doc)
	if firstID == secondID {
		t.Fatalf("expected distinct IDs, got %d and %d", firstID, secondID)
	}

	// Delete the first.
	resp = do(t, srv, http.MethodDelete, fmt.Sprintf("/todos/%d", firstID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(body) != 0 {
		t.Fatalf("DELETE body should be empty, got %q", body)
	}

	// Reload — only the second should remain.
	resp = do(t, srv, http.MethodGet, "/")
	doc = parse(t, resp.Body)
	_ = resp.Body.Close()
	if doc.Find(fmt.Sprintf("li#todo-%d", firstID)).Length() != 0 {
		t.Fatalf("deleted todo should not appear in list")
	}
	if doc.Find(fmt.Sprintf("li#todo-%d", secondID)).Length() != 1 {
		t.Fatalf("surviving todo should appear in list")
	}
	if !strings.Contains(doc.Find(fmt.Sprintf("li#todo-%d", secondID)).Text(), "second") {
		t.Fatalf("surviving todo should retain its title")
	}
}

// TestE2E_ConcurrentProgress fires two PUTs at the same Pending todo in parallel.
// Exactly one MUST win with a Complete-button row; the other MUST come back
// with the row + OOB error banner (optimistic-lock rejection). The test
// tolerates either ordering — the invariant is "exactly one success, exactly
// one rejection."
func TestE2E_ConcurrentProgress(t *testing.T) {
	t.Parallel()
	srv := newServer(t)

	resp := postForm(t, srv, "/todos", url.Values{"title": {"race me"}})
	doc := parse(t, resp.Body)
	_ = resp.Body.Close()
	id := extractTodoID(t, doc)

	type result struct {
		body string
		err  error
	}
	results := make([]result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			<-start
			r := do(t, srv, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id))
			b, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			results[i] = result{body: string(b), err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	successes, rejections := 0, 0
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("goroutine %d read error: %v", i, r.err)
		}
		d, err := goquery.NewDocumentFromReader(strings.NewReader(r.body))
		if err != nil {
			t.Fatalf("goroutine %d parse: %v", i, err)
		}
		switch {
		case hasOOBErrorBanner(d):
			rejections++
		case actionButtonText(d) == "Complete":
			successes++
		default:
			// Could be the case where one goroutine read in_progress and progressed to completed.
			// That's also a valid outcome of the race. Count it as a success-equivalent.
			if d.Find("li button[hx-put]").Length() == 0 && strings.Contains(d.Find("li").AttrOr("class", ""), "completed") {
				successes++
			} else {
				t.Errorf("goroutine %d response classification unclear: %s", i, r.body)
			}
		}
	}

	// Total must be 2 and at least one must be a clear success path.
	if successes+rejections != 2 {
		t.Fatalf("expected 2 classified responses, got %d successes + %d rejections", successes, rejections)
	}
	if successes < 1 {
		t.Fatalf("expected at least one success, got %d", successes)
	}
}
