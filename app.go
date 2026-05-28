package todolist

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/kingychiu/no-js-todolist/db"
	"github.com/labstack/echo/v4"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed static/*
var staticFS embed.FS

// NewApp wires migrations, templates, handlers, and routes into a ready-to-Start Echo instance.
func NewApp(sqldb *sql.DB) (*echo.Echo, error) {
	if err := RunMigrations(sqldb); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}

	views, err := LoadViews()
	if err != nil {
		return nil, fmt.Errorf("load views: %w", err)
	}

	h := &Handlers{
		Q:     db.New(sqldb),
		Views: views,
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.StaticFS("/static", echo.MustSubFS(staticFS, "static"))
	e.GET("/", h.ListTodos)
	e.POST("/todos", h.CreateTodo)
	e.PUT("/todos/:id/progress", h.ProgressTodo)
	e.DELETE("/todos/:id", h.DeleteTodo)

	return e, nil
}

// RunMigrations applies all pending Goose migrations from the embedded FS.
func RunMigrations(sqldb *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(sqldb, "migrations")
}
