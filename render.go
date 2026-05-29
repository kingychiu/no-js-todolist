package arcade

import (
	"embed"
	"html/template"

	"github.com/kingychiu/no-js-todolist/db"
	"github.com/labstack/echo/v4"
)

//go:embed views/*.html
var viewsFS embed.FS

// LeaderboardRow pairs a stored entry with a flag marking the current player.
type LeaderboardRow struct {
	Entry     db.Leaderboard
	IsCurrent bool
}

// ViewData is the universal template input. Fields irrelevant to the current
// step have their zero value and templates simply don't render them.
type ViewData struct {
	Session     db.Session
	Board       any
	FinalScore  int64
	Leaderboard []LeaderboardRow
}

type Views struct {
	tmpl *template.Template
}

func LoadViews() (*Views, error) {
	t, err := template.New("").Funcs(funcMap()).ParseFS(viewsFS, "views/*.html")
	if err != nil {
		return nil, err
	}
	return &Views{tmpl: t}, nil
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"inc": func(i int) int { return i + 1 },
	}
}

// Render writes the named template to the response body.
func (v *Views) Render(c echo.Context, name string, data any) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	return v.tmpl.ExecuteTemplate(c.Response().Writer, name, data)
}

// RenderWithError writes the named template AND an OOB error banner in one body.
// Used for invalid-transition responses: the view stays unchanged, the banner
// is swapped into #error-banner.
func (v *Views) RenderWithError(c echo.Context, name string, data any, msg string) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	w := c.Response().Writer
	if err := v.tmpl.ExecuteTemplate(w, name, data); err != nil {
		return err
	}
	return v.tmpl.ExecuteTemplate(w, "error_banner", msg)
}

// RenderError writes only the OOB error banner (no other content).
// Used when there's nothing to refresh in the main view.
func (v *Views) RenderError(c echo.Context, msg string) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	return v.tmpl.ExecuteTemplate(c.Response().Writer, "error_banner", msg)
}
