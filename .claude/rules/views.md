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
- `templ` adds a second code-gen step on top of sqlc — the simplicity ethic rejects it.
- `html/template` auto-escapes user input by default. Don't disable it.
- Auto-escape works correctly with HTMX attribute values.

## Template composition pattern

The full-page render uses Go's `define` / `template` composition. Always follow this shape:

**`views/layout.html`** — the entry template. Wraps everything in `{{ define "layout" }}…{{ end }}` and calls `{{ template "content" . }}`:

```
{{ define "layout" }}
<!DOCTYPE html>
<html>
  <head>...</head>
  <body>
    <div id="error-banner" aria-live="polite"></div>
    <main id="wizard-frame">
      {{ template "content" . }}
    </main>
  </body>
</html>
{{ end }}
```

**Step templates** (`wizard_name.html`, `wizard_game.html`, `wizard_difficulty.html`, etc.) — each defines `{{ define "content" }}…{{ end }}`. When rendered as the page entry, the layout fills the inner block.

**Render via the named template, not the filename:**

```go
v.tmpl.ExecuteTemplate(w, "layout", data)
```

Fragments (game board partials, error banner, leaderboard rows) are NOT wrapped in `define` — the file content IS the template body, the filename IS the template name. Render via `ExecuteTemplate(w, "snake_board.html", board)`.

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

**Funcs map:** only what's actually used. Don't add helpers speculatively.

## OOB error response: render two fragments into one body

For invalid-transition responses, write the unchanged view AND the error banner template to the same response. Order doesn't matter — HTMX scans for `hx-swap-oob` anywhere.

```go
func (h *Handlers) renderRejection(c echo.Context, viewName string, viewData any, msg string) error {
    c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
    w := c.Response().Writer
    if err := h.Views.tmpl.ExecuteTemplate(w, viewName, viewData); err != nil { return err }
    return h.Views.tmpl.ExecuteTemplate(w, "error_banner.html", msg)
}
```

## Template file inventory

| File | Purpose |
|---|---|
| `layout.html` | Base shell — `<html>`, `<head>`, `<body>`, Pico.css link, HTMX CDN script. **Must contain** `<div id="error-banner">` and `<main id="wizard-frame">` (the OOB / wizard swap targets). |
| `wizard_name.html` | Step 1 — name input form. |
| `wizard_game.html` | Step 2 — game picker. |
| `wizard_difficulty.html` | Step 3 — difficulty picker (content varies by chosen game). |
| `wizard_finished.html` | Step 5 — game-over view with Replay / Change game / Restart links + leaderboard preview. |
| `snake_board.html` | Snake board fragment — returned by the long-poll endpoint. |
| `twenty48_board.html` | 2048 grid fragment — returned per move. |
| `minesweeper_board.html` | Minesweeper grid fragment — returned per reveal/flag. |
| `leaderboard.html` | Top-N table for a given (game, difficulty). |
| `error_banner.html` | OOB error fragment: `<div id="error-banner" hx-swap-oob="true">…</div>`. |

The current wizard step is rendered as the content of `#wizard-frame`. Game board templates live inside `#wizard-frame` while `wizard_state = playing`. The wizard step IS the game UI in that phase.

## Action button examples

```html
<!-- Wizard step transition -->
<button hx-post="/wizard/game" hx-vals='{"game":"snake"}' hx-target="#wizard-frame" hx-swap="outerHTML" hx-disabled-elt="this">Play Snake</button>

<!-- 2048 move triggered by arrow key, no JS needed -->
<div id="twenty48-board"
     hx-post="/game/2048/move"
     hx-trigger="keydown[key=='ArrowLeft'] from:body, keydown[key=='ArrowRight'] from:body, keydown[key=='ArrowUp'] from:body, keydown[key=='ArrowDown'] from:body"
     hx-vals='js:{dir: event.key}'  <!-- NOTE: we do NOT use hx-vals js. Use four separate buttons with hx-vals instead. -->
     hx-target="this" hx-swap="outerHTML">
  ...
</div>
```

For keyboard input without JS, prefer multiple bindings that each emit a fixed direction:

```html
<div id="twenty48-board" hx-target="this" hx-swap="outerHTML">
  <button hidden hx-post="/game/2048/move" hx-vals='{"dir":"left"}'  hx-trigger="keydown[key=='ArrowLeft']  from:body" hx-disabled-elt="this"></button>
  <button hidden hx-post="/game/2048/move" hx-vals='{"dir":"right"}' hx-trigger="keydown[key=='ArrowRight'] from:body" hx-disabled-elt="this"></button>
  <button hidden hx-post="/game/2048/move" hx-vals='{"dir":"up"}'    hx-trigger="keydown[key=='ArrowUp']    from:body" hx-disabled-elt="this"></button>
  <button hidden hx-post="/game/2048/move" hx-vals='{"dir":"down"}'  hx-trigger="keydown[key=='ArrowDown']  from:body" hx-disabled-elt="this"></button>
  <!-- board cells here -->
</div>
```

`hx-vals` with a static JSON object is fine — it's not JS, it's HTMX attribute syntax. We avoid the `js:` prefix because that DOES evaluate JavaScript.

## Styling: classless CSS via Pico.css v2 (vendored)

Pico v2 is committed to `static/pico.css` and embedded via `//go:embed`. Served by Echo's static file handler. **Do not fetch from a CDN at runtime** — Pico's upstream slowed after v2.1.1; vendoring insulates us.

### Templates use semantic HTML only

- `<button>`, not `<div class="btn">`.
- `<form>`, `<input>`, `<label>` — Pico styles them.
- **No utility classes for visual purposes.** No `class="text-red-500"`, no `class="mt-4 flex"`. Only structural/semantic classes if absolutely required (e.g., `class="visually-hidden"` for a11y, `class="completed"` for game-over styling).

### Fragment hierarchy must match what it replaces

Classless CSS relies on parent-child selectors. If a swap returns a fragment with a different shape, styles collapse. Rules:
- A move response that replaces `#twenty48-board` must return a div with the same id and structure.
- A reveal response for one cell returns a button/element with the same parent-child relationship.
- Don't wrap fragments in extra `<div>` containers "for safety."

### Inline `<style>` in `layout.html` for small custom needs

Game boards need grid layout that Pico doesn't supply by default. Inline what's needed:

```html
<style>
  #twenty48-board { display: grid; grid-template-columns: repeat(var(--cols, 4), 1fr); gap: 0.5rem; }
  .ms-cell { width: 28px; height: 28px; display: inline-flex; align-items: center; justify-content: center; }
  .ms-cell.revealed { background: var(--pico-muted-color, #eee); }
  #snake-board { font-family: monospace; line-height: 1; white-space: pre; }
  .completed { opacity: 0.6; }
  .visually-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0,0,0,0); }
</style>
```

Don't introduce a second CSS file unless customization grows past ~30 lines.

## HTMX attributes — the protocol

- `hx-get`, `hx-post`, `hx-put`, `hx-delete` → match the route's HTTP method.
- `hx-target` → CSS selector (often `#wizard-frame`, `#twenty48-board`, `closest .ms-cell`).
- `hx-swap` → `outerHTML` is the typical fragment replacement.
- `hx-swap-oob="true"` on a fragment with an `id` → out-of-band swap.
- `hx-trigger="keydown[key=='ArrowLeft'] from:body"` → native HTMX keyboard support, no JS needed.
- `hx-vals='{"dir":"left"}'` → static JSON values sent with the request. **No `js:` prefix** (that evaluates JS).
- Never use `hx-vals` for arbitrary JS evaluation.

## What templates may NOT do

- No `<script>` tags except the HTMX CDN in `layout.html`, exactly once.
- **No HTMX extensions** (`htmx-ext-sse`, `htmx-ext-ws`, `htmx-ext-debug`, etc.). HTMX 2.0 moved them out of core — any extension is a second `<script>` tag. The "HTMX CDN only" rule is strict.
- **No Alpine.js. Not even one `x-data` attribute.** Non-negotiable.
- No hyperscript (`_="on click ..."`).
- No jQuery, no any other client-side framework or sprinkle library.
- No inline JS event handlers (`onclick=`, `onsubmit=`, `onchange=`, etc.).
- No `hx-vals='js:...'` — that evaluates JavaScript.
- No JSON in template output.
- No client-side computation. If a value is derived, derive it server-side.

If a UI need *feels* like it requires client-side JS (modal toggles, tab switching, accordions, autocompletes), the answer is one of:
1. Express it as an HTMX request that returns the updated fragment.
2. Use `<details>` / `<summary>` (native HTML) for disclosure widgets.
3. Use CSS-only state (`:checked`, `:focus-within`, `:has()`) for stateless interactions.
4. Decline the feature.

## DOM visibility — render-only, never CSS-toggle

If an interactive element should not be visible, the server **must not render its HTML**. Do NOT use `.hidden` or `display:none` to hide buttons, panels, modals, game-over screens.

**Why:** `goquery` tests cannot compute CSS layout. By physically omitting the node from the response, tests can definitively assert visibility via `doc.Find("…").Length() == 0`. CSS-hidden elements would falsely register as present.

Concrete: when the wizard is in step 2, the server returns only the step-2 fragment. Step 1's name input is gone from the response. When 2048 reaches Won state, the move-trigger buttons are no longer rendered.

## Debouncing: `hx-disabled-elt="this"` on every state-mutating button

All buttons/triggers that fire `hx-post`, `hx-put`, or `hx-delete` **must** include `hx-disabled-elt="this"`. Native HTMX (core, not an extension). Prevents double-click races.

```html
<button hx-post="/wizard/start" hx-target="#wizard-frame" hx-swap="outerHTML" hx-disabled-elt="this">Start Game</button>
```

The optimistic-lock guard in the DB is the source of truth; `hx-disabled-elt` is the cheap UI-level defense.

## Real-time UI: only Snake uses long-polling

Snake's `#snake-board` element uses `hx-trigger="load delay:0"` to long-poll for the next frame. The server-side handler blocks on the goroutine's broadcast channel with `context.WithTimeout`. After the swap, the new `#snake-board` re-issues the request — a continuous cycle of complete HTTP requests, no extensions, no JSON.

```html
<div id="snake-board"
     hx-get="/game/snake/board"
     hx-trigger="load delay:0"
     hx-target="this" hx-swap="outerHTML">
  <!-- pre-formatted ASCII or grid of cells -->
</div>
```

For 2048, Minesweeper, and the wizard: pure turn-based HTMX. Click / keypress → POST → fragment swap.

If a future need requires push:
- **First choice:** `hx-trigger="every Ns"` polling. No extra script tag.
- **Not allowed:** SSE, WebSockets, or any other extension. Adding one would relax the project's core constraint and trigger a separate proposal.
