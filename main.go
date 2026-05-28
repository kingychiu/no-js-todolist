package main

import (
	"database/sql"
	"embed"
	"log"

	"github.com/kingychiu/no-js-todolist/db"
	"github.com/labstack/echo/v4"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed static/*
var staticFS embed.FS

func main() {
	sqldb, err := sql.Open("sqlite3", "file:todos.db?_journal=WAL&_busy_timeout=5000&_fk=on")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() { _ = sqldb.Close() }()

	if err := runMigrations(sqldb); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	views, err := LoadViews()
	if err != nil {
		log.Fatalf("load views: %v", err)
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

	log.Println("listening on :8080")
	if err := e.Start(":8080"); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func runMigrations(sqldb *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(sqldb, "migrations")
}
