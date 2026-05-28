package main

import (
	"embed"
	"html/template"

	"github.com/labstack/echo/v4"
)

//go:embed views/*.html
var viewsFS embed.FS

type Views struct {
	tmpl *template.Template
}

func LoadViews() (*Views, error) {
	t, err := template.New("").ParseFS(viewsFS, "views/*.html")
	if err != nil {
		return nil, err
	}
	return &Views{tmpl: t}, nil
}

func (v *Views) Render(c echo.Context, name string, data any) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	return v.tmpl.ExecuteTemplate(c.Response().Writer, name, data)
}
