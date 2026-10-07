# CLAUDE.md

**Read `PROJECT.md` first.** It holds the decisions, data model, import spec and task list. Pick up at the first unchecked task.

## Working rules

- **Trunk-based git:** commit small, working increments directly to `main`. Remote `origin` is github.com/amascii/vinance (private for now); push `main` once the build and tests pass.
  `go build ./... && go test ./...` must pass before each commit.
- After each task, tick it in `PROJECT.md` → Tasks and add a dated line to → Log **in the same commit**.
- If you make or change a design decision, record it under `PROJECT.md` → Decisions. If you're unsure and the
  choice is the user's to make, ask instead of guessing.
- **All logs are JSON** (`log/slog` JSON handler). No `fmt.Println` or `log.Printf` for diagnostics.
- **Money is integer minor units.** Never use float64 for amounts.
- **`data/` is personal financial data and is gitignored.** Never commit it, never paste its contents into fixtures.
  Tests use small synthetic fixtures.
- Prefer standard ecosystem libraries (chi, templ, sqlc, goose, testify) over hand-rolled code.
- Every task ships with tests: unit for logic, HTTP feature tests (httptest + goquery) for handlers, and a playwright E2E journey for browser-only UI behavior. See PROJECT.md → Testing.
- Generated code (`*_templ.go`, sqlc output) is committed. Run `make generate` after editing `.templ` or query files.

## Hard-won gotchas (each one cost a debugging session)

- **htmx attribute inheritance:** an element inside a form silently inherits the form's `hx-target`/`hx-swap`. Always set both explicitly on nested htmx elements.
- **Out-of-order responses:** debounced live-update inputs (`delay:`) need `hx-sync="this:replace"`, or an older response can overwrite a newer one (flaky "stale" UI).
- **htmx `changed` ignores synthetic `input` events** whose value hasn't changed; kick a preview with `htmx.trigger(el, 'focus')` instead. `htmx:load` (not `afterSettle`) is the hook for freshly swapped content.
- **Pico redefines `--pico-color` inside `<button>`** (white, for filled buttons): custom-styled buttons must use `--pico-contrast` for text, or the text vanishes on a transparent background.
- **Browser-side offsets are UTF-16 units**, Go's are bytes: convert (`utf16Offset`) before sending text selections to JS.
- **macOS Chromium: `Control+A` moves to line start**; use `Locator.Fill("")` in Playwright to clear an input.
- **htmx `from:#id` triggers** bind to whichever element has that id *when processed*; during an `outerHTML` swap that can be the old, soon-removed element, so live updates silently
  die after a re-render. Put the trigger on a stable ancestor and let events bubble.
- **htmx history snapshots don't contain typed input values.** Pages whose state lives in the URL (the filter form) use `hx-history="false"` so Back re-fetches.
- **htmx only wires up swapped-in content after a ~20ms settle**, so every form also has `method="post"`/`action` and the server returns a full page for non-`HX-Request` posts.
- **htmx discards 4xx by default;** the layout's `htmx-config` meta lets 422 swap in (validation errors).
- **Feature tests (httptest + goquery) cannot see htmx behaviour.** Anything involving swaps, triggers, focus, history or layout needs a Playwright journey in `e2e/`.
- **sqlc:** use `sqlc.arg(name)` for *every* parameter in a query that mixes named and bare `?` (positional numbering otherwise breaks). `SELECT *`/`RETURNING *` queries must be regenerated after
  any migration that adds columns (`make generate`).
- **templ:** `@{` is parsed as a component call; write a literal `@` as `{ "@" + x }`.
- **goquery** always synthesises `<html>`; to test "fragment vs full page" assert on `<title>`, not `html`.
- **go vet** (composites) rejects unkeyed struct literals from other packages, including in tests.
- **zsh:** an unmatched glob aborts the whole `&&` chain; avoid bare globs in shell one-liners.
- **Verification habit that paid off:** for anything that computes money (import, balances, tag report, budgets), compare the page's numbers on the real DB against an independent
  SQL + Python (`fractions.Fraction`) computation, not against the code under test.
- **Entry form (not a text bar):** e2e picks type and direction with `selectValue(t, page, "kind"|"direction", X)` (they are dropdowns), and wait for `.htmx-added` to clear (`settled`) after any swap before typing. Autocomplete fills tags only when the tags field is empty.
- **Pico input height ignores font-size/padding overrides** (it is `1rem × line-height + padding`): compact forms need `height: auto; line-height: 1.3` too, and Pico's root font is 125% on wide screens.
