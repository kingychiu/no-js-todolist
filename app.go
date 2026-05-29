package arcade

import (
	"database/sql"
	"embed"
	"fmt"
	"math/rand"
	"time"

	"github.com/kingychiu/no-js-arcade/db"
	"github.com/labstack/echo/v4"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

//go:embed static/*
var staticFS embed.FS

// NewApp wires migrations, templates, handlers, and routes into a
// ready-to-Start Echo instance.
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
		rng:   rand.New(rand.NewSource(time.Now().UnixNano())),
		Snake: NewSnakeRuntime(),
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.StaticFS("/static", echo.MustSubFS(staticFS, "static"))

	e.GET("/", h.GetIndex)
	e.POST("/wizard/name", h.PostWizardName)
	e.POST("/wizard/game", h.PostWizardGame)
	e.POST("/wizard/difficulty", h.PostWizardDifficulty)
	e.POST("/wizard/start", h.PostWizardStart)
	e.POST("/wizard/back", h.PostWizardBack)
	e.POST("/wizard/quit", h.PostWizardQuit)
	e.POST("/wizard/replay", h.PostWizardReplay)
	e.POST("/wizard/change-difficulty", h.PostWizardChangeDifficulty)
	e.POST("/wizard/different-game", h.PostWizardDifferentGame)
	e.POST("/game/2048/move", h.PostT48Move)
	e.POST("/game/minesweeper/reveal", h.PostMSReveal)
	e.POST("/game/minesweeper/flag", h.PostMSFlag)
	e.GET("/game/snake/board", h.GetSnakeBoard)
	e.POST("/game/snake/direction", h.PostSnakeDirection)

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
