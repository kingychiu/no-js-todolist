package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/kingychiu/no-js-todolist/db"
	"github.com/labstack/echo/v4"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

// --- FSM unit tests ---

func TestFSM_CanTransitionTo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, to TodoState
		want     bool
	}{
		{Pending, Pending, false},
		{Pending, InProgress, true},
		{Pending, Completed, false},
		{InProgress, Pending, false},
		{InProgress, InProgress, false},
		{InProgress, Completed, true},
		{Completed, Pending, false},
		{Completed, InProgress, false},
		{Completed, Completed, false},
	}
	for _, c := range cases {
		if got := c.from.CanTransitionTo(c.to); got != c.want {
			t.Errorf("%s -> %s: got %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestFSM_Next(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from   TodoState
		want   TodoState
		wantOK bool
	}{
		{Pending, InProgress, true},
		{InProgress, Completed, true},
		{Completed, "", false},
	}
	for _, c := range cases {
		got, ok := c.from.Next()
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s.Next() = (%v, %v), want (%v, %v)", c.from, got, ok, c.want, c.wantOK)
		}
	}
}

// --- Handler + template contract tests ---

type testEnv struct {
	e *echo.Echo
	q *db.Queries
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dbpath := filepath.Join(t.TempDir(), "test.db")
	sqldb, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal=WAL&_busy_timeout=5000&_fk=on", dbpath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })

	if err := runMigrations(sqldb); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	views, err := LoadViews()
	if err != nil {
		t.Fatalf("load views: %v", err)
	}
	q := db.New(sqldb)
	h := &Handlers{Q: q, Views: views}

	e := echo.New()
	e.HideBanner = true
	e.GET("/", h.ListTodos)
	e.POST("/todos", h.CreateTodo)
	e.PUT("/todos/:id/progress", h.ProgressTodo)
	e.DELETE("/todos/:id", h.DeleteTodo)

	return &testEnv{e: e, q: q}
}

func mustCreate(t *testing.T, env *testEnv, title string) int64 {
	t.Helper()
	todo, err := env.q.CreateTodo(context.Background(), title)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return todo.ID
}

func mustForceStatus(t *testing.T, env *testEnv, id int64, from, to TodoState) {
	t.Helper()
	rows, err := env.q.UpdateTodoStatus(context.Background(), db.UpdateTodoStatusParams{
		NewStatus:      string(to),
		ID:             id,
		ExpectedStatus: string(from),
	})
	if err != nil {
		t.Fatalf("force status: %v", err)
	}
	if rows == 0 {
		t.Fatalf("force status: 0 rows affected (expected %s, got something else)", from)
	}
}

func fetchDoc(t *testing.T, env *testEnv, method, path string, body io.Reader) (*goquery.Document, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	return doc, rec
}

func formBody(values url.Values) io.Reader {
	return strings.NewReader(values.Encode())
}

func TestGet_RendersShellWithTodos(t *testing.T) {
	env := newTestEnv(t)
	mustCreate(t, env, "buy milk")
	doc, rec := fetchDoc(t, env, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if doc.Find("#error-banner").Length() == 0 {
		t.Errorf("shell missing #error-banner")
	}
	if doc.Find("#todo-list").Length() == 0 {
		t.Errorf("shell missing #todo-list")
	}
	if !strings.Contains(doc.Find("#todo-list").Text(), "buy milk") {
		t.Errorf("list does not contain seeded todo")
	}
	if doc.Find("input[name='title'][autofocus]").Length() == 0 {
		t.Errorf("input missing autofocus")
	}
}

func TestPost_CreatesTodo_ReturnsLi(t *testing.T) {
	env := newTestEnv(t)
	doc, rec := fetchDoc(t, env, http.MethodPost, "/todos", formBody(url.Values{"title": {"learn htmx"}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if doc.Find("li").Length() != 1 {
		t.Errorf("expected 1 li, got %d", doc.Find("li").Length())
	}
	if got := strings.TrimSpace(doc.Find("li button[hx-put]").First().Text()); got != "Start Work" {
		t.Errorf("button = %q, want %q", got, "Start Work")
	}
}

func TestPost_EmptyTitle_ReturnsOOBError(t *testing.T) {
	env := newTestEnv(t)
	doc, rec := fetchDoc(t, env, http.MethodPost, "/todos", formBody(url.Values{"title": {"   "}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if doc.Find(`div#error-banner[hx-swap-oob="true"]`).Length() == 0 {
		t.Errorf("missing OOB error banner; got body:\n%s", rec.Body.String())
	}
}

func TestPut_PendingToInProgress_RendersCompleteButton(t *testing.T) {
	env := newTestEnv(t)
	id := mustCreate(t, env, "x")
	doc, rec := fetchDoc(t, env, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	btn := doc.Find("li button[hx-put]").First()
	if got := strings.TrimSpace(btn.Text()); got != "Complete" {
		t.Errorf("button = %q, want %q", got, "Complete")
	}
	if got := btn.AttrOr("hx-put", ""); got != fmt.Sprintf("/todos/%d/progress", id) {
		t.Errorf("hx-put = %q", got)
	}
}

func TestPut_InProgressToCompleted_NoActionButton(t *testing.T) {
	env := newTestEnv(t)
	id := mustCreate(t, env, "x")
	mustForceStatus(t, env, id, Pending, InProgress)
	doc, rec := fetchDoc(t, env, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if doc.Find("li button[hx-put]").Length() != 0 {
		t.Errorf("expected no hx-put button on completed todo")
	}
	if !strings.Contains(doc.Find("li").AttrOr("class", ""), "completed") {
		t.Errorf("expected li to have 'completed' class")
	}
	// OOB banner should NOT be present for a successful transition
	if doc.Find(`#error-banner[hx-swap-oob="true"]`).Length() != 0 {
		t.Errorf("unexpected OOB error banner on successful transition")
	}
}

func TestPut_AlreadyCompleted_ReturnsRowAndOOBError(t *testing.T) {
	env := newTestEnv(t)
	id := mustCreate(t, env, "x")
	mustForceStatus(t, env, id, Pending, InProgress)
	mustForceStatus(t, env, id, InProgress, Completed)
	doc, rec := fetchDoc(t, env, http.MethodPut, fmt.Sprintf("/todos/%d/progress", id), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if doc.Find(`div#error-banner[hx-swap-oob="true"]`).Length() == 0 {
		t.Errorf("missing OOB error banner")
	}
	if doc.Find("li").Length() != 1 {
		t.Errorf("expected the unchanged row to also be present")
	}
	if !strings.Contains(doc.Find("li").AttrOr("class", ""), "completed") {
		t.Errorf("row should still be marked completed")
	}
}

func TestDelete_RemovesTodo_EmptyBody(t *testing.T) {
	env := newTestEnv(t)
	id := mustCreate(t, env, "x")
	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/todos/%d", id), nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", rec.Body.String())
	}
	if _, err := env.q.GetTodo(context.Background(), id); err == nil {
		t.Errorf("todo should have been deleted")
	}
}

// --- Optimistic locking (DB-level FSM enforcement) ---

func TestUpdateTodoStatus_StaleExpected_ZeroRowsAffected(t *testing.T) {
	env := newTestEnv(t)
	id := mustCreate(t, env, "x") // pending
	rows, err := env.q.UpdateTodoStatus(context.Background(), db.UpdateTodoStatusParams{
		NewStatus:      string(Completed),
		ID:             id,
		ExpectedStatus: string(InProgress), // stale — actual is pending
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if rows != 0 {
		t.Errorf("rowsAffected = %d, want 0", rows)
	}
}

// --- Cross-reference test ---
// Every hx-target (#ID) and hx-swap-oob id referenced by any handler response
// must point to an element that exists in the rendered page shell.

func TestHxTargets_ResolveInShell(t *testing.T) {
	env := newTestEnv(t)
	// Seed two todos so the list isn't empty.
	mustCreate(t, env, "pending-todo")
	progressID := mustCreate(t, env, "ip-todo")
	mustForceStatus(t, env, progressID, Pending, InProgress)

	shell, _ := fetchDoc(t, env, http.MethodGet, "/", nil)
	shellIDs := map[string]bool{}
	shell.Find("[id]").Each(func(_ int, s *goquery.Selection) {
		if id := s.AttrOr("id", ""); id != "" {
			shellIDs[id] = true
		}
	})
	if !shellIDs["error-banner"] || !shellIDs["todo-list"] {
		t.Fatalf("shell missing required ids; got %v", shellIDs)
	}

	// Exercise each route × interesting state and gather the responses.
	docs := []*goquery.Document{shell}
	r1, _ := fetchDoc(t, env, http.MethodPost, "/todos", formBody(url.Values{"title": {"new"}}))
	docs = append(docs, r1)
	r2, _ := fetchDoc(t, env, http.MethodPost, "/todos", formBody(url.Values{"title": {"  "}})) // error banner
	docs = append(docs, r2)
	r3, _ := fetchDoc(t, env, http.MethodPut, fmt.Sprintf("/todos/%d/progress", progressID), nil) // completes ip-todo
	docs = append(docs, r3)
	r4, _ := fetchDoc(t, env, http.MethodPut, fmt.Sprintf("/todos/%d/progress", progressID), nil) // already completed → error banner
	docs = append(docs, r4)

	checked := 0
	for i, doc := range docs {
		doc.Find("[hx-target]").Each(func(_ int, s *goquery.Selection) {
			target := s.AttrOr("hx-target", "")
			if !strings.HasPrefix(target, "#") {
				return // selectors like "closest li" are not id refs
			}
			id := strings.TrimPrefix(target, "#")
			if !shellIDs[id] {
				t.Errorf("doc[%d]: hx-target=%q has no matching id in shell", i, target)
			}
			checked++
		})
		doc.Find(`[hx-swap-oob="true"]`).Each(func(_ int, s *goquery.Selection) {
			id := s.AttrOr("id", "")
			if id == "" || shellIDs[id] {
				checked++
				return
			}
			t.Errorf("doc[%d]: hx-swap-oob id=%q has no matching id in shell", i, id)
		})
	}
	if checked == 0 {
		t.Errorf("cross-reference test exercised 0 references — test is ineffective")
	}
}

// Silence the goose package-level logger in any test path that might log.
var _ = goose.NopLogger
