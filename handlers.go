package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/kingychiu/no-js-todolist/db"
	"github.com/labstack/echo/v4"
)

type Handlers struct {
	Q     *db.Queries
	Views *Views
}

func (h *Handlers) ListTodos(c echo.Context) error {
	todos, err := h.Q.ListTodos(c.Request().Context())
	if err != nil {
		return err
	}
	return h.Views.Render(c, "layout", todos)
}

func (h *Handlers) CreateTodo(c echo.Context) error {
	title := strings.TrimSpace(c.FormValue("title"))
	if title == "" {
		return h.renderError(c, "Title cannot be empty.")
	}
	todo, err := h.Q.CreateTodo(c.Request().Context(), title)
	if err != nil {
		return err
	}
	return h.Views.Render(c, "todo_item.html", todo)
}

func (h *Handlers) ProgressTodo(c echo.Context) error {
	id, err := parseID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	ctx := c.Request().Context()
	current, err := h.Q.GetTodo(ctx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "todo not found")
	}
	next, ok := TodoState(current.Status).Next()
	if !ok {
		return h.renderRejection(c, current, "Todo already completed.")
	}
	rows, err := h.Q.UpdateTodoStatus(ctx, db.UpdateTodoStatusParams{
		NewStatus:      string(next),
		ID:             id,
		ExpectedStatus: current.Status,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		latest, err := h.Q.GetTodo(ctx, id)
		if err != nil {
			return err
		}
		return h.renderRejection(c, latest, "Transition rejected — state changed concurrently.")
	}
	current.Status = string(next)
	return h.Views.Render(c, "todo_item.html", current)
}

func (h *Handlers) DeleteTodo(c echo.Context) error {
	id, err := parseID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	if err := h.Q.DeleteTodo(c.Request().Context(), id); err != nil {
		return err
	}
	return c.NoContent(http.StatusOK)
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

// renderError writes ONLY the OOB error banner (used when there's no row to refresh).
func (h *Handlers) renderError(c echo.Context, msg string) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	return h.Views.tmpl.ExecuteTemplate(c.Response().Writer, "error_banner.html", msg)
}

// renderRejection writes the unchanged row + OOB error banner in a single response.
func (h *Handlers) renderRejection(c echo.Context, row db.Todo, msg string) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	w := c.Response().Writer
	if err := h.Views.tmpl.ExecuteTemplate(w, "todo_item.html", row); err != nil {
		return err
	}
	return h.Views.tmpl.ExecuteTemplate(w, "error_banner.html", msg)
}
