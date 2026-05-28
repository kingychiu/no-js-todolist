---
paths:
  - "views/**"
  - "render.go"
  - "static/**"
---

# Templates, Rendering, and Styling Rules

Applies to `views/*.html`, `render.go`, and `static/*`. Templates are the API surface of this app — HTMX consumes the HTML directly.

## Template engine: `html/template`

Standard library only. Not `templ`. Reasons:
- `templ` adds a second code-gen step on top of sqlc, which the project's simplicity ethic rejects.
- `html/template` auto-escapes user input by default. Don't disable it.
- Auto-escape works correctly with HTMX attribute values.

## Template composition pattern

The full-page render uses Go's standard `define` / `template` composition. It's not obvious if you haven't seen it before, so always follow this shape:

**`views/layout.html`** — the entry template. Wraps everything in `{{ define "layout" }}…{{ end }}` and calls `{{ template "content" . }}` where the inner block goes:

```
{{ define "layout" }}
<!DOCTYPE html>
<html>
  <head>...</head>
  <body>
    <div id="error-banner" aria-live="polite"></div>
    {{ template "content" . }}
  </body>
</html>
{{ end }}
```

**`views/index.html`** — provides the inner block via `{{ define "content" }}…{{ end }}`. The file itself doesn't render anything on its own:

```
{{ define "content" }}
<form ...>...</form>
<ul id="todo-list">
  {{ range . }}{{ template "todo_item.html" . }}{{ end }}
</ul>
{{ end }}
```

**Render via the named template, not the filename:**

```go
v.tmpl.ExecuteTemplate(w, "layout", todos)  // entry is "layout", not "layout.html"
```

Fragments (`todo_item.html`, `error_banner.html`) are NOT wrapped in `define` — the file content IS the template body, and the filename IS the template name. Render via `ExecuteTemplate(w, "todo_item.html", todo)`.

## `render.go` — load once, render fast

```go
type Views struct {
    tmpl *template.Template
}

func LoadViews(fs embed.FS) (*Views, error) {
    t, err := template.New("").Funcs(funcMap()).ParseFS(fs, "views/*.html")
    return &Views{tmpl: t}, err
}

func (v *Views) Render(c echo.Context, name string, data any) error {
    c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
    return v.tmpl.ExecuteTemplate(c.Response().Writer, name, data)
}
```

Parse all templates once at startup. Don't re-parse per request.

**Funcs map:** only what's actually used. Most likely just one helper to render the action button per state. Don't add helpers speculatively.

## OOB error response: render two fragments into one body

For invalid-transition responses, write the unchanged row template AND the error banner template to the same response. Order matters less than presence — HTMX scans for `hx-swap-oob` anywhere in the body.

```go
func (h *Handlers) renderRejection(c echo.Context, row db.Todo, msg string) error {
    c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
    if err := h.Views.tmpl.ExecuteTemplate(c.Response().Writer, "todo_item.html", row); err != nil { return err }
    return h.Views.tmpl.ExecuteTemplate(c.Response().Writer, "error_banner.html", msg)
}
```

## Template file inventory

| File | Purpose | Rules |
|---|---|---|
| `layout.html` | Base shell — `<html>`, `<head>`, `<body>`, Pico.css link, HTMX CDN script. | **Must contain** `<div id="error-banner" aria-live="polite"></div>`. **Must contain** the form, list container, and any `id` that handler responses reference via `hx-target` or `hx-swap-oob`. |
| `index.html` | Full page. Composes `layout.html` with the initial todo list. | Add-todo form has `<input name="title" autofocus required>`. |
| `todo_item.html` | Single `<li>` partial. | **Canonical row template.** Used for list rendering AND for PUT responses. One source of truth. |
| `error_banner.html` | OOB error fragment. | Renders `<div id="error-banner" hx-swap-oob="true">…message…</div>`. |

Reusing `todo_item.html` for both list rendering and PUT responses is what keeps the UI consistent. Don't create a separate "row update" template.

## Action button per state — the only conditional in `todo_item.html`

```html
{{ if eq .Status "pending" }}
  <button hx-put="/todos/{{.ID}}/progress" hx-target="closest li" hx-swap="outerHTML">Start Work</button>
{{ else if eq .Status "in_progress" }}
  <button hx-put="/todos/{{.ID}}/progress" hx-target="closest li" hx-swap="outerHTML">Complete</button>
{{ else }}
  <span class="visually-hidden">completed</span>
{{ end }}
```

- `closest li` keeps the swap target correct even if list ordering changes.
- `outerHTML` replaces the row entirely so state-dependent attributes refresh.
- Completed rows include visually-hidden status text for screen readers — strikethrough alone is not accessible.

## Styling: classless CSS via Pico.css v2 (vendored)

Pico.css v2 is committed to `static/pico.css` and embedded with `//go:embed`. Served by `main.go` via Echo's static file handler. Do NOT fetch from a CDN at runtime — Pico's upstream slowed after v2.1.1, and vendoring insulates us.

### Templates use semantic HTML only

- `<button>`, not `<div class="btn">`.
- `<ul><li>`, not `<div class="list-item">`.
- `<form>`, `<input>`, `<label>` — Pico styles them automatically.
- **No utility classes for visual purposes.** No `class="text-red-500"`, no `class="mt-4 flex"`, none of that. The only acceptable classes are structural/semantic (e.g., `class="visually-hidden"` for a11y).

### Fragment hierarchy must match what it replaces

Classless CSS relies on parent-child selectors (`ul > li`, `form > input`). If an HTMX swap returns a fragment with a different structure, styles collapse. Rules:
- A PUT response for a `<li>` must return a `<li>` with the same tag structure.
- A POST `/todos` response, if it returns a single new row, must return a `<li>` (not a `<div>`).
- Don't wrap fragments in extra `<div>` containers "for safety."

### When you need custom styles

If a single declaration is genuinely necessary (e.g., strikethrough for completed todos), add it inline via a `<style>` block in `layout.html`. Don't introduce a second CSS file unless customization grows past ~10 lines.

```html
<style>
  .completed { text-decoration: line-through; opacity: 0.6; }
  .visually-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0,0,0,0); }
</style>
```

## HTMX attributes — the protocol

- `hx-get`, `hx-post`, `hx-put`, `hx-delete` → match the route's HTTP method.
- `hx-target` → CSS selector (often `closest li` or `#todo-list`). The referenced ID must exist somewhere in the rendered shell.
- `hx-swap` → `outerHTML` for row replacement, `beforeend` for appending to a list, `delete` for DELETE.
- `hx-swap-oob="true"` on a fragment with an `id` → out-of-band swap into the matching element in the current page.
- Never use `hx-vals` or `hx-include` to ship JSON — forms post form-encoded data, which is what Echo's `c.FormValue` reads.

## What templates may NOT do

- No `<script>` tags except the HTMX CDN in `layout.html`, exactly once.
- **No HTMX extensions** (`htmx-ext-sse`, `htmx-ext-ws`, `htmx-ext-debug`, etc.). HTMX 2.0 (June 2024) moved all extensions out of core, so any extension is a second `<script>` tag. The "HTMX CDN only" rule is strict.
- **No Alpine.js. Not even one `x-data` attribute.** This is non-negotiable — the whole project's value comes from the server-rendered-only invariant.
- No hyperscript (`_="on click ..."`).
- No jQuery, no any other client-side framework or sprinkle library.
- No inline JS event handlers (`onclick=`, `onsubmit=`, `onchange=`, etc.).
- No JSON in template output.
- No client-side computation. If a value is derived, derive it server-side and render the result.

If a UI need *feels* like it requires client-side JS (modal toggles, tab switching, accordions, autocompletes), the answer is one of:
1. Express it as an HTMX request that returns the updated fragment.
2. Use `<details>` / `<summary>` (native HTML) for disclosure widgets.
3. Use CSS-only state (`:checked`, `:focus-within`, `:has()`) for stateless interactions.
4. Decline the feature.

## DOM visibility — render-only, never CSS-toggle

If an interactive element should not be visible, the server **must not render its HTML** in the response. Do NOT use `.hidden` or `display:none` to hide buttons, forms, modals, or any interactive node.

**Why:** `goquery` tests cannot compute CSS layout. By physically omitting the node from the response, tests can definitively assert visibility via `doc.Find("…").Length() == 0`. CSS-hidden elements would falsely register as present.

Applies to all conditional UI: action buttons that depend on state (already done in `todo_item.html`), modals, error banners (already done via OOB swap), tab panels, accordions. The shell may declare permanent containers (e.g., `<div id="error-banner">`) but their content must come from the server, never from CSS visibility toggles.

## Debouncing: `hx-disabled-elt="this"` on every state-mutating button

All buttons that trigger `hx-post`, `hx-put`, or `hx-delete` **must** include `hx-disabled-elt="this"`. This is native HTMX (core, not an extension) — it disables the button from the moment of click until the request completes, preventing double-clicks and the race conditions they cause.

```html
<button hx-put="/todos/{{ .ID }}/progress" hx-target="closest li" hx-swap="outerHTML" hx-disabled-elt="this">Start Work</button>
```

The optimistic-lock guard in the DB (`UPDATE … WHERE status=?`) is still the source of truth — `hx-disabled-elt` is the cheap UI-level defense, not the correctness mechanism.

## Real-time UI is out of scope

This project does not need real-time. If a future requirement appears:
- **First choice:** native HTMX polling with `hx-trigger="every Ns"`. No extra script tag, automatically pauses when the tab is backgrounded, standard HTTP semantics.
- **Not allowed:** SSE (`htmx-ext-sse`), WebSockets (`htmx-ext-ws`), or any other extension. All are second script tags. Adding one would relax the project's core constraint and trigger a separate proposal.
