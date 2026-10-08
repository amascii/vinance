# vinance

A simple, fast personal finance tracker — a lighter alternative to GnuCash, for one user.

> **This file is the source of truth for the project.** Sessions don't share memory, so everything needed
> to continue the work lives here. Keep **Tasks** and **Log** current: tick tasks and add a log line in the same
> commit as the work.

## Why

- GnuCash is clunky; entering a transaction should take seconds (including from a phone).
- One account per expense type is clunky. Use **tags** instead (`#snacks`, `#bakery`, `#food-truck`).
- Accounts stay only for real places money lives: banks, cards, wallets, investments.

## Stack

| Concern | Choice | Notes |
|---|---|---|
| Language | Go 1.27 | Toolchain installed: `go1.27.1 darwin/arm64` |
| HTTP router | `github.com/go-chi/chi/v5` | Standard middleware: RequestID, RealIP, Recoverer |
| Templates | `github.com/a-h/templ` | Type-safe components; the standard pairing with htmx in Go |
| Frontend | htmx 2.x (vendored in `internal/web/static/`, no CDN) | Plus a small classless CSS (Pico CSS) for mobile-friendly defaults |
| DB | SQLite via `modernc.org/sqlite` | Pure Go, no cgo. Open with `_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)` |
| Queries | `sqlc` (`sqlc.yaml`, queries in `internal/db/queries/*.sql`) | Generated code committed |
| Migrations | `github.com/pressly/goose/v3` | SQL files embedded via `embed`, run automatically at startup |
| Logging | `log/slog` + `slog.NewJSONHandler` (Go's built-in structured logger, its equivalent of Python's structlog); `github.com/go-chi/httplog/v2` for request logs | **All logs are JSON.** Pass loggers via context / `logger.With(...)` for bound fields |
| Tests | stdlib `testing` + `testify` (assertions), `net/http/httptest` + `github.com/PuerkitoBio/goquery` (HTML assertions), `github.com/playwright-community/playwright-go` (browser E2E) | See **Testing** section |
| Dev tools | Pin `templ`, `sqlc`, `goose` with `go get -tool` (the `tool` directive in go.mod) | Run via `go tool templ generate`, etc. |
| Build | `Makefile`: `generate`, `build`, `run`, `test`, `lint` | |
| VCS | git, local only (no remote yet), **trunk-based** | Small commits straight to `main`; tests must pass before committing |

Config is read from environment variables: `VINANCE_DB` (default `./data/vinance.db`, gitignored), `VINANCE_ADDR` (default `127.0.0.1:8080`), `LOG_LEVEL` (default `info`).

## Decisions

1. **Tags replace expense accounts.** GnuCash `Expenses:Groceries:Snacks` → tags `#groceries #snacks` (flat, one per path segment).
   Tags are lowercase kebab-case: `Outside Food` → `outside-food`, `PC+Tech` → `pc-tech`. A parent-level report is the sum of the parent tag.
2. **Double-entry under the hood.** Every transaction has ≥2 splits whose **values sum to zero**. The UI hides this:
   quick-add asks for amount, account and tags, and the other side goes to a built-in category account
   (`Expenses` for spending, `Income` for money in). Transfers and card payments are account→account.
3. **Multi-currency.** Each real account has one currency (USD, MXN, JPY, KRW so far). Each split stores `amount` (in the
   split's currency) and `value` (in the transaction's currency), like GnuCash. Store integers in minor units, never floats:
   USD/MXN have 2 decimals, JPY/KRW have 0. Reports show per currency, and USD conversion uses rates from the `prices` table.
4. **Single user, phone access eventually, hosting undecided.** Build for localhost first. Keep auth as a chi middleware
   that does nothing in v1, so we can add Tailscale or a VPS login later without restructuring.
5. **Quick add is a structured form** (replaced the original one-line text bar on 2026-10-02): **two consistent rows** (row 1: type, account(s), date; row 2: description, amount, tags and a **+** button; laptop/desktop first, wraps on phones): Spend/Income/Transfer *dropdown*, account select(s) (a scoped register's transfer direction is a dropdown too), labels are visually hidden (placeholders/aria); there is no header/summary/"today" hint text, only the post-save confirmation, a calendar date picker only (no typing dates; the ‹ › / Today buttons were removed 2026-10-02), description with history autocomplete, amount, one tags text field with suggestions. The text grammar below survives only for **recurring rules**, which still store a quick-add line.
6. **Split transactions are first-class.** A transaction mirrors one statement line (e.g. Walmart $65.00 on BRISK) and can be
   broken into item lines (splits), each with its own amount, memo and tags (`#drinks`, `#snacks`, `#stationary`).
   Item lines must add up to the statement total, and the editor shows the live "remaining to allocate".
   Splitting is the preferred style going forward. The old style (one transaction per grocery item, e.g. `Milk`, `Egg Whites`)
   is **imported as-is, not merged**.
7. **Adjustments are kept.** `Expenses:Adjustment` imports as an expense split tagged `#adjustment`.
8. **Imbalances import as-is and are fixed in the app.** GnuCash `Imbalance-USD` splits go into a built-in `Imbalance` account.
   The dashboard flags any transaction with an `Imbalance` split, and the user fixes it in the transaction editor.
   So v1 needs a **full transaction editor**, not just quick-add.
9. **Libraries:** prefer the standard, widely used Go libraries (see Stack) over hand-rolling. Don't reinvent routing, templating or migrations.
10. **Bulk tag editing (decided 2026-10-03):** *Add* tags every expense/income (category) line of each selected transaction; *Remove*/*Move* touch only lines that carry the tag. The selection is **cleared whenever the filter or search changes** (the bar and rows are swapped together); "load more" only appends rows that are still on screen, so earlier ticks stay. A **confirm step** states how many transactions/lines will change; there is no undo.
11. **Filtered total (decided 2026-10-05):** whenever a search/filter is active, `/transactions` shows a sticky bottom line with the **net movement of the real (asset/liability) accounts over the whole matching set**, one amount per transaction currency (never mixed), signed (negative = money went out), no "owes you" wording. Same net as each row's `Summary`, so own-account transfers net to zero. Summed in SQL (`ledger.Service.Totals`), so it covers rows past the first page. Use case: lending money, `meal (john)` -100 then `transfer (john)` +50 → -50.
12. **CI and branch protection (decided 2026-10-07):** every change goes through a PR. `.github/workflows/ci.yml` runs on each PR and on `main`: a `changes` job skips the heavy jobs for docs-only diffs, then `test` (generated code is current, build, vet, `go test ./...`) and `e2e` (Playwright on Ubuntu). Branch protection on `main` requires `test` and `e2e`, requires a PR (0 approvals, since the owner cannot approve their own), blocks force-pushes and deletion, and applies to admins. Merges are squash-only. No workflow-level `paths:` filter: it would leave required checks pending forever on docs-only PRs; skipped jobs count as passing instead.
## Roadmap

- **v1:** skeleton, schema, GnuCash import, quick-add, transaction editor with splits, transaction list and filters, balances, tag report, imbalance fixing.
- **v1.5:** suggestions. Typing "McDonald's" proposes `#fast-food @brisk` from history. Start with "copy the most recent transaction
  with the same description". Nice-to-have; don't over-engineer it.
- **v2:** budgets and recurring transactions (rent, subscriptions).

## Data model (v1)

```sql
accounts(
  id INTEGER PK, name TEXT UNIQUE NOT NULL,       -- "BRISK", "Wharf Bank"
  slug TEXT UNIQUE NOT NULL,                       -- "brisk", "wharf-bank" (used as @slug in quick-add)
  type TEXT NOT NULL CHECK (type IN ('asset','liability','income','expense','equity','imbalance')),
  currency TEXT,                                   -- ISO code; NULL for built-in category accounts (any currency)
  builtin INTEGER NOT NULL DEFAULT 0,              -- Expenses, Income, Equity, Imbalance
  archived INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL)

transactions(
  id INTEGER PK, date TEXT NOT NULL,               -- YYYY-MM-DD
  description TEXT NOT NULL,
  currency TEXT NOT NULL,                          -- currency that split `value`s are in
  notes TEXT NOT NULL DEFAULT '',
  gnucash_id TEXT UNIQUE,                          -- import idempotency; NULL for native transactions
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL)

splits(
  id INTEGER PK, transaction_id INTEGER NOT NULL REFERENCES transactions ON DELETE CASCADE,
  account_id INTEGER NOT NULL REFERENCES accounts,
  position INTEGER NOT NULL,                       -- display order
  memo TEXT NOT NULL DEFAULT '',
  currency TEXT NOT NULL,                          -- = account currency, or any for built-ins
  amount INTEGER NOT NULL,                         -- minor units of splits.currency; debit-positive (GnuCash sign convention)
  value INTEGER NOT NULL,                          -- minor units of transactions.currency; SUM(value) per txn = 0
  reconciled TEXT NOT NULL DEFAULT 'n')            -- 'n' | 'c' | 'y'

tags(id INTEGER PK, name TEXT UNIQUE NOT NULL)     -- kebab-case, no '#'
split_tags(split_id REFERENCES splits ON DELETE CASCADE, tag_id REFERENCES tags, PRIMARY KEY(split_id, tag_id))
prices(currency TEXT, date TEXT, usd_per_unit TEXT, PRIMARY KEY(currency, date))  -- decimal string; seeded from imported FX txns
```

**Sign convention (same as GnuCash):** positive = debit. Spending $12.50 on a credit card means a BRISK split of `-1250`
and an Expenses split of `+1250`. Asset and expense balances are positive. Liability, income and equity balances are negative
(display them flipped).

The zero-sum invariant is enforced in Go (`internal/ledger`) on every write, inside a DB transaction. A transaction that
doesn't balance can't be saved, except that the importer may write `Imbalance` splits, which are what make it balance.

## Quick-add form (current UI)

Fields: **kind** (Spend / Income / Transfer; CSS `:has()` shows/hides fields), **account** (+ **to** for transfers), **date** (`<input type=date>`; ‹ › step a day, Today; hint says "7 days ago"), **description**, **amount**, **tags** (`#a #b`, suggestions as you type).
On a register (`/transactions?account=slug`) the account is a hidden `scope` field (never chosen), and a transfer has "Other account" + direction radios (Out of X / Into X; default Into for a card, Out for a bank).
Account and date are remembered per browser in cookies (`vinance_acct`, `vinance_date`; the date only when it isn't today) and pre-set on the next entry. Description autocomplete (3+ letters; `POST /quickadd/suggest/description`): **Tab**/click fills *every* field
(kind, account if you haven't touched it, to, direction, tags if empty, amount) and selects the amount; ↑↓ choose, Enter accepts, Esc dismisses. Tags: `POST /quickadd/suggest/tags`. Submit is `POST /quickadd` → `entryFromForm` → `quickadd.Resolve` (the same resolver recurring rules use).
Code: `internal/web/quickadd.go`, `entry.go`, `views/quickadd.templ`, `static/app.js`/`app.css`. Tests: `quickadd_test.go`, `entry_test.go`, `scoped_test.go`; e2e helpers `fill`/`quickAdd`/`settled` in `e2e/quickadd_test.go`.

## Quick-add text grammar (recurring rules only now)

```
@account  DESCRIPTION...  [+]AMOUNT  [#tag ...]  [YYMMDD]        (any order; this is the natural one)
```

The input is **pre-filled with the remembered account and date** (`@wharf-bank 260924 `), so day to day you only type a description and an amount.

- **Account first, remembered.** After each add the account used (and the date, if one was typed) is stored in a per-browser cookie (`vinance_acct`, `vinance_date`) and pre-filled into the next entry.
  Fallback when nothing is remembered: the real account most recently spent from. An entry with no date clears the remembered date (= "today").
- **Amount** is the first token if the line starts with a number (the original `12.50 coffee @brisk` order still works), otherwise the last number-like word. `7-11` and `2 for 1` stay description.
  A leading `+` means money coming in (income); `-5` is rejected.
- **Date** is `YYMMDD` (`261001` = 2026-10-01; years 2020-2039), or `today`, `yesterday`, `9/24`, `9/24/26`, `9/24/2026`, `2026-09-24`. `9/24` without a year uses the current year, or last year if that
  would be more than 30 days ahead. A six-digit number that *looks* like a date but isn't real (`261032`) next to another number is refused ("isn't a real date") instead of being booked as a $261,032 amount;
  alone it is an amount (a ¥300,000 withdrawal), and amounts written `300,000`/`300000.00` are never dates.
- `@a` = spent from / `+` income arrives in; `@a @b` = transfer from a to b. `@x` matches account slugs by unique prefix; archived accounts are skipped. `#tags` are created on first use.
- The amount is in the account's currency.
- `12.50 McDonald's #fast-food @brisk` creates BRISK `-1250` and Expenses `+1250` tagged `fast-food`; `@wharf-bank Salary +2000 #salary` creates Wharf Bank `+200000` and Income `-200000`.

**Autocomplete from history.** Once 3+ letters of the description are typed (and the caret is *in* the description), the preview lists past transactions whose description starts with, or has a word starting
with, what you typed (case-insensitive; most-used first). **Tab** (or tap) fills the line from the most recent one: its description, `+` if it was income, its account(s) — replacing only the
*remembered* account, never one you chose on purpose — its tags, and its last amount, which is left **selected** so typing overwrites it. Typed tags/amount/date are kept (a bare amount gains the `+`
if that description was income last time). Up/Down choose, Enter accepts a chosen one (otherwise it submits), Esc dismisses. Pure logic: `quickadd.Complete`; lookup: `ledger.SuggestDescriptions`.

- The parser lives in `internal/quickadd`, is pure (no DB), and returns a struct. Unit-test it heavily.

## GnuCash import (removed)

A one-time GnuCash CSV importer (`vinance import-gnucash`) seeded the first database and was removed on 2026-10-07 once it was no longer needed
(it is in the git history only if you kept it; this repo's history was squashed). Imported transactions keep their `gnucash_id`; the
editor labels them "Imported from GnuCash". The `prices` table was seeded by the importer and now has no writer other than the `UpsertPrice` query.

## Testing

Three layers. Every feature task ships with tests in the relevant layers.

| Layer | What it covers | How | Run with |
|---|---|---|---|
| **Unit** | Pure logic: money parse/format, quick-add parser, zero-sum validation, CSV row parsing, tag slugging | Table-driven tests, `testify/require` + `assert`. templ components can be rendered directly to a buffer and checked | `make test` |
| **Feature (HTTP)** | Handlers + DB end to end, no browser: "POST quick-add → transaction exists → list fragment shows it", "editor rejects an unbalanced save", "HX-Request gets a fragment, not a full page" | Build the real chi router against a fresh temp SQLite DB (migrated). Drive it with `httptest`, set `HX-Request: true` for htmx calls, parse responses with `goquery` and assert on elements (`#txn-list tr`, `.remaining`) | `make test` (same suite, fast) |
| **E2E (browser)** | Things only a real browser shows: htmx swaps, live quick-add preview, `#`/`@` autocomplete, "remaining to allocate" updating as you type, mobile viewport | `playwright-go` (module path `github.com/mxschmitt/playwright-go`) with headless Chromium against the app on a random port with a temp DB. Keep it to a handful of critical user journeys | `make e2e` (behind build tag `e2e`; one-time setup `make e2e-install`) |

**Browsers for E2E don't come from Homebrew** (Homebrew's `chromium` cask is deprecated). Playwright downloads its own
browser builds, pinned to the playwright-go version, into `~/Library/Caches/ms-playwright`. The install command above handles it,
and running it again after a playwright-go upgrade fetches the matching build. Use headless Chromium by default. WebKit is also
available if we want a Safari/iPhone-like check later. This machine already has the playwright cache (`chromium-1117`, `webkit-2003`,
`firefox-1449`) from another project; playwright-go may need a different build number, and the install command fetches it.

Shared helpers live in `internal/testutil`: `NewDB(t)` (temp migrated DB), `NewServer(t)`, and `Seed*` fixtures. Use synthetic data only; never real `data/`.
The feature layer is the main workhorse because most UI behavior is server-rendered HTML. E2E is for the parts that only happen in the browser.

**CI:** the same layers run on GitHub Actions for every PR (see Decisions #12). Run `make test` and `make e2e` locally before pushing; CI also fails if `make generate` would change committed generated code.

## Running it

```
make run                                  # http://127.0.0.1:8080, DB at ./data/vinance.db (auto-migrated)
make run-lan                              # same, listening on 0.0.0.0:8080 (phone/other computers on the LAN; no login!)
make test                                 # unit + HTTP feature tests (fast)
make e2e-install && make e2e              # one-time browser setup, then Playwright journeys
make generate                             # after editing *.templ or internal/db/queries/*.sql (generated code is committed)
```

Env: `VINANCE_DB`, `VINANCE_ADDR`, `LOG_LEVEL` (see Stack). Logs are JSON on stdout.

## Pages and routes

| Route | What |
|---|---|
| `/` | Entry form (kind, account, date picker, description autocomplete, amount, tags), recent list, "N need fixing" banner, first-run welcome |
| `/transactions` | Filterable list (text, account, tags, dates, needs-fixing, untagged), infinite scroll, split detail |
| `/transactions/{id}` | Editor: split lines, live remaining, add/remove/reorder, delete |
| `/accounts`, `/accounts/manage` | Balances + USD net worth; create / rename / archive / delete accounts |
| `/budgets` | Monthly budgets per tag for any month: severity meters (color + icon + label), even-pace tick, month navigation |
| `/recurring` | Due recurring entries (Add / Skip), rules with schedules (add, edit, pause, delete) |
| `/tags`, `/tags/manage` | Spending/income by tag with date ranges and bars; rename / merge / delete tags |
| `/healthz` | JSON health check (pings the DB) |

POSTs are protected by `http.CrossOriginProtection`; every state-changing form also works without JS.

## Project layout

```
cmd/vinance/main.go          subcommands: serve (default), migrate
cmd/screenshots/main.go      regenerates docs/screenshots from demo data (`make screenshots`)
internal/config              env config
internal/logging             slog JSON logger
internal/db                  Open (SQLite pragmas), Migrate (embedded goose migrations)
internal/db/gen             sqlc generated code
internal/db/migrations/      0001_init, 0002_description_index, 0003_recurring, 0004_budgets
internal/demo                synthetic demo ledger (README screenshots; never real data)
internal/db/queries/         sqlc query files (accounts, tags, transactions, suggest, reports)
internal/ledger              money + currency conversion + rates, slugs, validation, Service (create/update/delete/get/list/count,
                             tags, accounts, balances, tag report, LastLike), Transaction.Summary
internal/quickadd            quick-add parser + account resolver (pure, no DB)
internal/web                 chi router, handlers (quickadd, transactions, editor, accounts, accountmanage, tags, tagmanage), flash cookie
internal/web/views           templ components + view models
internal/web/static          htmx.min.js, pico.min.css, app.css, app.js
internal/testutil            temp migrated DB / test server (fixed clock) / seed helpers
e2e/                         playwright-go browser journeys (build tag e2e)
data/                        gitignored: personal CSV export and the local DB
```

## Tasks

Do these in order. One commit per task (or smaller). Tick the box and add a Log line in the same commit.

- [x] Move GnuCash export into repo (`data/`, gitignored)
- [x] Survey export contents
- [x] `git init`, PROJECT.md, CLAUDE.md
- [x] Settle architecture decisions
- [x] **Skeleton:** `go mod init github.com/amascii/vinance` (local-only path is fine), deps + `tool` directives, Makefile, `cmd/vinance` with
      subcommands, config, slog JSON logger, chi server with `/healthz`, a templ hello page, vendored htmx + pico, `.gitignore` updated,
      `internal/testutil` + one feature test (GET `/` returns 200 and contains the page title, via goquery)
- [x] **E2E harness:** playwright-go setup, `make e2e`, one smoke test (page loads, htmx is present). Each later UI task adds its journey here
- [x] **Schema:** goose migration `0001_init.sql` (data model above, plus indexes on `splits(transaction_id)`, `splits(account_id)`,
      `transactions(date)`), seed the built-in accounts (Expenses, Income, Equity, Imbalance), sqlc config + queries, auto-migrate on startup
- [x] **Ledger core:** money type + parse/format per currency, transaction create/update/delete with zero-sum validation, tests
- [x] **GnuCash importer** (done 2026-10-01; removed 2026-10-07, see "GnuCash import (removed)").
- [x] **Quick-add parser** (`internal/quickadd`) + table tests
- [x] **Quick-add UI:** input at the top of the home page, `hx-post` on submit, live preview of the parse (`hx-trigger="keyup changed delay:200ms"`),
      autocomplete for `#`/`@` (`/suggest/tags?q=`, `/suggest/accounts?q=`)
- [x] **Transaction list:** newest first, paginated or infinite scroll, filter by account, tag, date range and text. Show splits/tags inline
- [x] **Transaction editor:** edit date, description and notes. Add/remove/reorder split lines with per-line amount, account, memo and tags.
      Show the live "remaining to allocate". Save is blocked unless it balances. Delete transaction. Mobile-friendly
- [x] **Imbalance indicator:** banner/count on the home page linking to transactions with `Imbalance` splits
- [x] **Account balances page:** per account in its own currency, with assets/liabilities totals and net worth in USD via `prices`
- [x] **Tag report:** spending by tag over a date range (per currency + USD total), with drill-down to transactions
- [x] **Tag management:** rename/merge tags (e.g. fix a misspelled tag carried over from GnuCash)
- [x] **Account management:** create/rename/archive accounts
- [x] v1.5: description-based suggestions
- [x] v2a: recurring transactions
- [x] v2b: budgets
- [x] **Compact UI pass:** entry form on one line with dropdowns (no giant Spend/Income/Transfer buttons, smaller date picker); transactions filter bar on one wrapping line; tighter spacing
- [x] **Compact transaction editor:** each split line is currently a tall card, so only ~3 fit on a laptop screen. Make a line **one row** (account, memo, amount, tags, remove; even simpler than the entry form and filter bar),
      so a whole receipt fits without scrolling. Keep "remaining to allocate" visible, header fields (date, description, notes) on one line, and "Add line" focus the new row's first field. Update feature + e2e tests (`editor_test.go`).
- [x] **Editor action buttons on one line:** Save, Cancel and Delete currently stack up. Put them on one row: **Save** the largest (primary, wide), **Cancel** a small ✕ icon button, **Delete** a trash-can icon button (keep the confirm prompt;
      `aria-label`s + tooltips since they have no text). Update `editor_test.go` / e2e selectors that use the button text.
- [x] **Tag suggestions on editor lines:** the per-line tags field gets the same suggestions as the entry form. Feasible: `POST /quickadd/suggest/tags` already takes the field's text + cursor position and returns suggestion chips, so the work is
      (1) each line's tags input gets `hx-post`/`hx-trigger`/`hx-sync` and its own suggestion container (an inline strip under *that* line; `hx-target` set explicitly per line, since the editor form has its own `hx-target`), (2) generalise `app.js`, which hard-codes `#qa-tags`
      (the `pos` parameter and the chip-click handler), to work on any tags input (e.g. a `data-tag-input` attribute) and keep the entry form working, (3) make sure the suggestion strip doesn't push a line past one row or trip the `.lines` `input` → remaining preview.
      Tests: feature test for the endpoint response in the editor context, e2e journey (type `#gro` on line 2, click a chip, only that line's tags change).
- [x] **Bulk tag editing on selected transactions:** on the transaction list (and an account's register) each row gets a checkbox; a bar appears once something is selected ("N selected") with actions **Add tag**, **Remove tag** and **Move tag → other tag**
      (the move covers "migrate this tag's transactions to a new tag" but only for the ticked rows; whole-tag rename/merge already exists under Tags → Manage). Also: "select all matching the current filter" (with the count shown, since the list is paginated) and a
      confirm step that says how many transactions/lines will change. Open design points to settle first and record under Decisions: tags live on **split lines**, so decide whether a bulk action applies to every expense/income line of the transaction
      or only matching ones (decided, see Decision 10: add → all category lines, remove/move → only lines that have the tag); selection is cleared on any filter/search/load-more change; one DB transaction for the whole change, confirm step, no undo.
      Tests: unit for the ledger bulk operation (add/remove/move, idempotent when the tag is already there, tags created on demand), feature test for the endpoint, e2e journey (tick 2 of 3 rows, add a tag, only those two change; select-all-matching).
- [x] **Filtered total:** net of the matching transactions (signed, per currency) at the bottom of the list whenever a filter is active; see Decision 11. Tests: ledger unit, feature, e2e (spans pages, updates live).
- [x] **README with screenshots** from synthetic demo data (`internal/demo`, `cmd/screenshots`, `make screenshots`), MIT license, PR-only workflow
- [x] **`make run-lan`:** run bound to `0.0.0.0:8080` for access from other devices on the same network

## Log

- **2026-10-01** — Project kickoff. Moved the GnuCash export into `data/`, surveyed it, and settled decisions 1–9, the roadmap, the data model,
  the quick-add grammar and the import spec. Library choice revised from "mostly stdlib" to standard ecosystem libs (chi, templ, sqlc, goose, httplog).
  Added the three-layer testing strategy (unit / HTTP feature with goquery / playwright-go E2E).
- **2026-10-01** — Skeleton done. Go module `github.com/amascii/vinance`, chi router (RequestID/RealIP/Recoverer/httplog, no-op `auth`
  middleware), `/healthz`, templ home page, vendored htmx 2.0.11 + Pico CSS 2, config + JSON slog logger, `serve` subcommand with graceful
  shutdown (`migrate`/`import-gnucash` stubbed), Makefile, `internal/testutil.NewServer`, feature + unit tests passing.
  Notes: httplog's logger is replaced with ours so all logs share one JSON format; `/healthz` is excluded from request logs;
  default DB path moved to `./data/vinance.db` so it stays gitignored. Dev tools (templ, sqlc, goose) are pinned as `tool` deps;
  run `make generate` after editing `.templ` files (generated `*_templ.go` is committed).
- **2026-10-01** — E2E harness done. `e2e/` (build tag `e2e`) with `TestMain` launching headless Chromium via playwright-go and a `newPage` helper;
  smoke tests: page title + htmx loaded, and no horizontal overflow at 390px. `make e2e` runs them; `make e2e-install` sets up the browser.
  **Gotcha:** the Go Playwright bindings now live at module path `github.com/mxschmitt/playwright-go` (the old
  `github.com/playwright-community/playwright-go` path stopped working after v0.6000.0, and v0.6000.0's driver download from Microsoft's CDN returns
  404/400). Use the new path at v0.6201.1+; it fetches the driver from npm/nodejs.org and `make e2e-install` works as-is. (An earlier commit briefly
  carried an install-script workaround for this; it was removed.)
- **2026-10-01** — Schema done. `internal/db/migrations/0001_init.sql` (accounts, transactions, splits, tags, split_tags, prices + indexes, seeded
  built-ins `expenses`/`income`/`equity`/`imbalance`, with a working Down). `db.Open` sets foreign_keys, WAL, busy_timeout and `_txlock=immediate`;
  `db.Migrate` runs goose's Provider API and logs each migration as JSON. `serve` and `migrate` both auto-migrate; `/healthz` now pings the DB
  (503 if down). sqlc configured (`sqlc.yaml`, queries in `internal/db/queries/`, output `internal/db/gen`) with account + tag queries only;
  later tasks add queries as they need them. `testutil.NewDB` / `NewServerWithDB` give tests a temp migrated DB.
  Schema choices beyond the spec: CHECKs on `slug` (`[a-z0-9-]` only), `tags.name` (no spaces, `#` or A-Z; non-ASCII allowed), `transactions.date`
  (YYYY-MM-DD shape), `reconciled` (`n|c|y`), and built-in accounts must have NULL currency while real accounts must have one.
  `created_at`/`updated_at` default to UTC ISO-8601 (code must bump `updated_at` itself on edit). Tags can't be deleted while in use.
- **2026-10-01** — Ledger core done (`internal/ledger`). `money.go`: `ParseAmount`/`Format`/`FormatPlain` over int64 minor units (USD/MXN 2 decimals,
  JPY/KRW 0; accepts `$`, `Mex$`, `JP¥`, `₩` prefixes, commas, trailing `.`). `slug.go`: `TagSlug` (unicode-aware kebab-case) and `AccountSlug` (ASCII).
  `service.go`: `Service.Create/CreateIn/Update/Delete/Get`. `Validate` collects *all* problems into a `*ValidationError` (wraps `ErrUnbalanced` when
  values don't sum to zero) and checks date, description, ≥2 splits, split-vs-account currency, amount==value for same-currency splits, reconciled flag.
  `Update` replaces all splits and tags atomically and bumps `updated_at`. `CreateIn(ctx, tx, in)` lets the importer make the whole import one DB
  transaction. `Remaining(splits)` is the editor's "left to allocate". New sqlc queries in `transactions.sql`.
  No special case for Imbalance splits: they're ordinary splits whose values make the transaction sum to zero.
- **2026-10-01** — GnuCash importer done (`internal/importer/gnucash`, `vinance import-gnucash <csv>`). One DB transaction (all-or-nothing), idempotent by
  `gnucash_id`, collects *all* invalid transactions before failing, seeds `prices` (USD per unit, from JPY/KRW/MXN cross-currency splits), and verifies
  each real account's imported total against the CSV's own sum. Number quirks handled: `-713937.00` for KRW, trailing `.`, commas, BOM/CRLF.
  Synthetic fixture in `testdata/sample.csv`. **Real run:** all transactions, splits, accounts, tags and prices imported;
  all balance checks passed; an independent SQL + Python check confirmed every transaction sums to zero, only the known few have an `Imbalance` split, and
  balances match the CSV. Re-running skips every transaction. Imported into `data/vinance.db` (gitignored; safe to delete and re-import any time).
  Liabilities:Neo became "Neo Credit" (Assets:Neo clash). Observation: flat tags mean some leaf names are generic or shared across parents
  (generic leaf names such as `misc` and `travel` that exist under several parents) — see Open questions (#1).

- **2026-10-01** — Quick-add parser done (`internal/quickadd`). Two pure stages: `Parse(text, today) Parsed` (keeps partial results plus `Problems`, so a live
  preview can show half-typed input) and `Resolve(parsed, accounts, defaultAccountID) (Result, error)` which maps `@refs` (exact slug, else unique prefix;
  archived/built-in accounts excluded) and returns a balanced `ledger.TxnInput` plus `Kind` (expense/income/transfer). Rules chosen where the spec was silent:
  description is required; amounts must be positive (`+` = money in; refunds aren't supported yet); one account uses the default (last-used) when none is typed;
  transfers need two *different* accounts in the *same* currency (conversion goes through the editor); tags on a transfer attach to the destination split.
- **2026-10-01** — Quick-add UI done. Home page (`/`) = quick-add panel (`#qa-panel`: input, live preview, suggestions, recent-20 list). Routes:
  `POST /quickadd/preview` (debounced 150ms on `input`; renders the understood entry or hints, plus `#tag`/`@account` suggestions for the token under the
  caret — the browser sends `pos` in UTF-16 units via a `htmx:configRequest` hook in `static/app.js`), `POST /quickadd` (creates; 200 + fresh panel, or
  **422** + panel with problems and the text kept). htmx is configured (`<meta name="htmx-config">`) to swap 422 responses. The form is a normal
  `method=post` form, so without htmx (or inside htmx's ~20ms settle window after a swap, which a real bug in my first E2E run exposed) it posts normally and
  non-`HX-Request` requests get a full page. `http.NewCrossOriginProtection()` middleware rejects cross-site POSTs. Default account = the real account most
  recently spent from (negative split). `ledger.Transaction.Summary()` (direction, headline amount, accounts, tags, imbalance flag) feeds list rows.
  Clock is injectable (`web.WithClock`; `testutil.Today` = 2026-10-01). Gotcha found: sqlc turned `LIKE ?2 ... LIMIT ?` into mismatched positional params, so
  use `sqlc.arg(name)` for *every* parameter in queries that mix named and bare `?`. Tests: feature tests (preview, suggestions incl. LIKE-wildcard safety,
  submit, 422, defaults, cross-origin, no-htmx) and 3 Playwright journeys (type → suggest → click → Enter; error then resubmit; phone width).
- **2026-10-01** — Transaction list done. `ledger.Service.List(Filter)` (hand-written parameterised SQL; filters: text over description/notes/split memos with LIKE
  wildcards escaped, one account, tags ANDed, inclusive date range, `Imbalance`; keyset pagination on `(date DESC, id DESC)` via `Cursor`, page size 50, fetches
  limit+1 to know if there's a next page) and `Count`. `loadMany` loads a page's splits/tags in 3 queries; `Get` and `ListRecent` now use it (the three sqlc
  queries they replaced were deleted). Page `GET /transactions`: filter form (search, account, tags as `#a #b`, from/to, "needs fixing only"), live-updating via
  htmx (`input delay:300ms`), infinite scroll via an `hx-trigger="revealed"` sentinel row whose URL carries the active filters + `before=<cursor>`, multi-split
  receipts as a collapsible `<details>` (per-line account/memo/tags/amount). One URL, three shapes by header: full page, `TxnResults` fragment (filter change),
  `TxnItems` (scroll); `Vary: HX-Request`; `HX-History-Restore-Request` gets the full page. Server sends `HX-Push-Url` with the canonical URL (no empty params).
  **Gotcha:** htmx history snapshots don't capture typed input values, so the filter form has `hx-history="false"` and Back re-fetches the URL (URL = source of
  truth). Tests: ledger filter/pagination/cursor tests, 15+ feature tests, 3 Playwright journeys (infinite scroll to 120, live filter + URL + Back + Clear, split
  detail at phone width). Also added `testutil.SeedSpend`.
- **2026-10-01** — Transaction editor done. Routes: `GET /transactions/{id}?next=…`, `POST /transactions/{id}` (one endpoint for every button: `save`, `add`,
  `remove-N`, `up-N`, `down-N`, `recalc`), `POST /transactions/{id}/remaining` (live indicator + out-of-band Save button state), `POST /transactions/{id}/delete`.
  List/home rows link to it with `next=<the list URL>` so Save/Delete return you where you came from (`safeNext` only allows local paths: no open redirect).
  Design: generic split-line editor (each line = account, signed amount, currency, memo, tags); all form state lives in the posted form (lines named
  `r-<n>-<field>`, ordered numerically), no server session. **"+ Add line" pre-fills the amount still to allocate** and reuses the previous category, so splitting
  a receipt needs no arithmetic. Completely empty lines are ignored and dropped on save; a line with a memo but no amount is an error. Real accounts fix a line's
  currency; category lines have a currency select; lines whose currency differs from the transaction's show a "Worth in <txn currency>" field (cross-currency,
  e.g. imported rows). Reconciled flags are preserved. Imbalance lines are flagged with a banner so they can be reassigned to Expenses + tags.
  Save is blocked in the UI until balanced *and* enforced by `ledger.Service.Update`. Delete asks via `hx-confirm`.
  **Two htmx gotchas the browser tests caught (feature tests can't):** (1) an element inside a form silently inherits the form's `hx-target`/`hx-swap` — always
  set both explicitly on nested htmx elements; (2) `hx-trigger="… from:#id"` binds to whichever element has that id *at processing time*, which during an outerHTML
  swap can be the old, soon-removed element, so live updates died after any re-render — put the trigger on a stable ancestor and let events bubble instead.
  Tests: ~25 feature tests (reads the rendered form back like a browser) + 3 Playwright journeys (split a $65 receipt end to end incl. live indicator and Save
  state; delete with dismiss/accept of the confirm; fix an Imbalance from the needs-fixing list).
- **2026-10-01** — Imbalance indicator + balances page done. Home shows "N transactions need fixing →" (links to `/transactions?imbalance=1`; disappears at 0).
  `/accounts`: `ledger.Service.BalanceSheet` sums split amounts per real account (own currency, debit-positive; liabilities displayed as amount owed),
  converts with the *latest* rate per currency from `prices` using exact `big.Rat` math (`ledger.ConvertToUSD`, half away from zero), totals assets/owed/net worth in
  USD, flags currencies with a balance but no rate (excluded from totals, shown as a notice), hides archived accounts with zero balance, and links each account to its
  filtered transaction list. **Verified on the real DB against an independent SQL+Python/Fraction computation: net worth = assets − owed
  (exact match).** Notes: `prices` only holds rates seeded by the importer (a few rows per currency), so they get stale; consider a manual "add rate" screen later.
- **2026-10-01** — Tag report done: page `/tags` (nav "Tags"). `ledger.Service.TagReport` totals the built-in Expenses (or Income, positive) splits per tag over an inclusive
  date range; a split with several tags counts toward each (so bars overlap and exceed the total — the page says so); an Untagged row and a Total that counts every
  split once. USD conversion uses the rate in effect on each transaction's date (`ledger.Rates.At`: latest rate on/before the date, else the earliest later one; exact
  `big.Rat`); currencies with no rate are excluded from USD and flagged. Range presets (this month [default], last month, last 30 days, YTD, all time) or custom
  from/to; Spending/Income toggle. Each bar links to `/transactions?tag=…&from=…&to=…`; Untagged links to the new `untagged=1` list filter ("Untagged spending only"
  checkbox). Bars follow the dataviz guidance: single hue (`--series-1`, light/dark variants), ≤24px thick (10px), 4px rounded data end / square baseline, values in text
  ink beside the bar, no legend (single series), the list is the table view. **Verified on real data: the all-time spending total; for 2026 the report's total and
  every per-tag figure match an independent SQL+Python computation exactly.** Browser test asserts bars are proportional/thin and that the page (incl. the range form)
  fits a 390px phone (it caught an overflow, then passed after the fix).
- **2026-10-01** — Tag management done: `/tags/manage` (linked from the tag report). `ledger.Service` `ListTags` (with usage), `TagUsage`, `RenameTag` (normalises the new name with
  `TagSlug`; if it already exists the tags are *merged* in one DB transaction — `INSERT OR IGNORE` so a line carrying both keeps one copy — then the old tag is removed; renaming to
  itself is a no-op), `DeleteTag` (only when unused). UI: filter + sort (name / most used), per-tag "Rename or merge" disclosure, **merge requires an explicit confirmation page**
  (shows both usage counts; Cancel changes nothing), unused tags get "Delete unused tag". Success uses post-redirect-get with a one-shot flash message in a short-lived HttpOnly
  cookie (`vinance_flash`), so reload doesn't resubmit or repeat the message. Plain HTML forms (work without JS). Tag changes don't touch transactions' `updated_at`.
  Tests: ledger (collision merge, edge cases) + feature (list/filter/sort, flash cookie, confirm flow, validation, delete rules, cross-origin) + Playwright journey (fix a typo,
  cancel then confirm a merge, delete unused, reload clears flash, phone width).
- **2026-10-01** — Account management done: `/accounts/manage` (linked from the balances page). `ledger.Service` `ListAccounts`, `CreateAccount` (name → `@slug`; unique by
  name case-insensitively *and* by slug, so `Brisk!` clashes with `BRISK`; asset or liability; ISO currency; optional **opening balance** creates an "Opening balance"
  transaction vs Equity tagged `opening-balance`, atomically with the account — a liability's opening balance is what you owe), `UpdateAccount` (rename — slug follows —,
  asset↔liability, currency only while the account has no transactions), `SetAccountArchived`, `DeleteAccount` (only never-used accounts). Built-in accounts can't be edited,
  archived or deleted. Archived accounts disappear from quick-add (resolution + suggestions) but keep history and still show on the balances page while they hold a balance.
  UI: add form (stays open and keeps input on error), per-account Edit disclosure with errors shown beside the account that caused them, Archive/Restore, Delete for unused.
  Flash via the same cookie mechanism as tag management. Tests: ledger (validation, duplicates, opening-balance signs, atomic rollback, currency lock) + feature +
  Playwright journey (start with no accounts → add → quick-add into it → archive).

- **2026-10-01** — v1.5 suggestions done. (Superseded by the autocomplete above.) Typing an amount + a description you've used before shows a "Last time (McDonald's on 2026-09-21): [#fast-food @brisk]" chip; one
  click appends the tokens. Rules: exact description match, case-insensitive (`ledger.Service.LastLike`, backed by migration `0002` index `COLLATE NOCASE`); newest match of the
  *same direction* (expense vs `+` income); only fills in what the user hasn't typed (their own tags/account are never overridden); skips archived accounts; stays quiet for
  transfers, unknown descriptions, or when nothing is missing; works even when the entry is otherwise unresolvable (fresh DB with no default account). Not done (deliberately,
  per "don't over-engineer"): prefix/fuzzy description matching and amount suggestions. Also added a first-run welcome on `/` for an empty database, and this Running/Routes/Layout
  refresh.

- **2026-10-01** — v2a recurring transactions done: `/recurring` (nav "Recurring"; home banner "N recurring transactions are due →"). A rule = a quick-add line
  *without a date but with an explicit @account* (validated through the real `quickadd.Parse/Resolve`) + a schedule (`ledger.Schedule`: weekly/monthly/yearly, every N, anchor date,
  optional end date; occurrences are computed from the anchor so "the 31st" clamps to short months without drifting; Feb 29 yearly handled). **Nothing is created automatically:**
  due occurrences (overdue ones each listed, max 12 per rule at a time) get **Add** (creates an ordinary transaction dated on the due date) or **Skip**. Adding is atomic and idempotent:
  migration `0003` adds `transactions.recurring_id` + `recurring_for` (the due *date*, not an occurrence number, so editing a schedule can't collide) with a partial UNIQUE index, plus the
  rule's `next_index` check, so double-clicks/stale pages are harmless ("That one was already handled."). Editing text/end date keeps your place; changing frequency/interval/start
  restarts at the first date on/after today (old dates aren't resurrected). Pause/resume (missed occurrences are offered on resume), delete (created transactions are kept, link cleared).
  A rule whose account is later archived shows a problem and a disabled Add until fixed. Header nav now wraps on phones (a 5th link had caused horizontal scroll; caught by the browser
  test). Tests: schedule math table tests (clamping, leap years, monotonic), service tests (atomic accept, stale/double accept, pause, end, update, delete), ~12 feature tests, Playwright journey.
- **2026-10-01** — v2b budgets done: `/budgets?month=YYYY-MM` (nav "Budgets"). A budget is a monthly USD limit on one tag (`budgets` table, migration `0004`, one per tag). Spending is
  measured by `TagReport` for the month, so the budgets page and the tag report always agree (**verified on real data: every budgeted tag and the month total match the tag report to the
  cent**). Overlapping tags behave as in the report: a line carrying two budgeted tags counts toward both. Severity: ok (<85%), warn (85% up to and including exactly 100%), over
  (any amount above); shown as meter fill color **plus** an icon (✓ ! ⚠) and wording ("$50.00 left" / "$10.00 over"), so it never relies on color alone; the bar is capped at the track;
  current month has a thin even-pace tick (day N of M). Set/change (upsert) and remove per row; setting a budget for a tag that doesn't exist creates it (unused) so a budget can precede
  its first use. Merging tags moves a budget to the surviving tag unless it already has one; deleting an unused tag removes its budget. Dataviz: single hue + status ramps defined as CSS
  variables with light/dark values. Tests: ledger (status math, boundaries, month bounds/progress, merge behaviour), ~10 feature tests, Playwright journey (set two budgets, over vs left,
  fill proportions, change, previous month, drill-down, remove, no horizontal scroll). Header links slimmed on phones (6 links wrapped to 3 rows).

- **2026-10-02** — Quick-add reworked from user feedback (no data migration; existing transactions untouched): (1) **account-first, remembered** — input pre-filled with `@account` (+ date) kept per browser in cookies;
  (2) **amount anywhere** (first token or last number); (3) **`YYMMDD` dates** with typo protection (a non-date six-digit number beside a real amount is refused, never booked as an amount) and a remembered date for
  working through past days; (4) **autocomplete from history** after 3 letters (Tab/tap fills description, `+`, account, tags and last amount with the amount pre-selected; arrows/Enter/Esc), replacing the v1.5
  "Last time" chip (code and tests removed). Verified on real data: a short prefix fills the usual description, account, amount and tags. Details worth knowing:
  selection offsets are sent as **UTF-16 units** (browser) not Go bytes (tested with é and an emoji); suggestions only show while the caret is in the description so editing the filled amount doesn't reopen them;
  the live preview/indicator inputs now use `hx-sync="this:replace"` (an out-of-order response had made the editor's remaining indicator flaky ~1 in 5 runs). Test-suite notes: `Control+A` is "start of line"
  on macOS Chromium (use `Fill("")`); htmx's `changed` filter ignores synthetic input events with an unchanged value (trigger `focus` via `htmx.trigger` instead).

- **2026-10-02** — Accounts page: a visual-only "done" checkbox on each account row (pure HTML/CSS, `:has(:checked)` dims and strikes the row; no name/form so nothing is sent or stored;
  `autocomplete="off"` stops browsers restoring ticks on reload). Browser test confirms a reload clears them.

- **2026-10-02** — Account register: clicking an account (`/transactions?account=slug`) now shows, per row, *that account's* change (a card payment is -$500 on the bank and +$500 on the card, instead of the
  same gross number on both) and its running balance after the transaction ("Balance $…" for assets, "Owed $…" for liabilities, as on the Accounts page), plus a header with the current balance.
  `ledger.Service.RunningBalances` uses a SQL window function over the account's *whole* history in the list's `(date, id)` order, so balances are right under any filter (date/tag/search) and on every
  infinite-scroll page; back-dated entries slot into the middle. Real accounts only (not Expenses/Income). **Verified on real data against an independent Python cumulative sum: every account's header matches the
  Accounts page and every row matches exactly.**

- **2026-10-02** — Add from an account's register: `/transactions?account=slug` (real, unarchived accounts) now has the home page's quick-add bar at the top, locked to that account. It is the *same* component
  (`views.QuickAddForm`, shared with the home panel), the same endpoints (`/quickadd`, `/quickadd/preview`) and the same JS (autocomplete, Tab, remembered date); the only difference is a hidden `account` field.
  The input holds just `description amount #tags [YYMMDD]` (no `@account`): the server prepends `@slug` itself, so nothing typed can send the entry elsewhere; an `@other` (so also a transfer) is refused with
  "Entries here go to X. Use the Home page for transfers" (both live in the preview and on submit); account suggestions are off; autocomplete never changes the account and its fill/selection offsets are
  relative to the bar's text. Success redirects (`HX-Redirect`/303, `return` URL checked with `safeNext`) back to the same filtered register with a flash, so the running balances and header refresh; the account
  and any typed date are remembered for the home page too. Archived accounts show a note instead of a bar. Also restyled the suggestion rows (quiet outline, clear keyboard-selected state; note Pico redefines
  `--pico-color` inside buttons, use `--pico-contrast`). Tests: ~12 feature tests (bar presence, lock, refusal, validation, return URL, cross-origin, scoped autocomplete incl. UTF-16) + a Playwright journey.

- **2026-10-03** — Editor fixes after a user report ("paid a credit card, it got recorded as an expense; the editor says Over-allocated by MX$X"). Replayed on a copy of the real DB (one affected transaction):
  the app was right — the message means both lines were positive (+N +N); changing a line's *account* never flips its *sign*, so a card payment recorded as a charge needs both signs flipped *and* the
  other line pointed at the bank. Two genuine papercuts found while replaying: (1) a leading `+` in an amount (`+100.00`, which the editor's own hint encourages) was rejected as not-a-number — `ledger.ParseAmount`
  now accepts one leading `+` or `-`; (2) a malformed amount was silently left out of the total, so the indicator blamed the *other* line ("Over-allocated by MX$X") — it now says "Line 1: that amount isn't a number
  (like -12.50 or +12.50)", flags that line immediately (not only after a failed save) and keeps Save off. The hint now says that changing a line's account does not flip its sign. Tests: the exact story as feature
  tests and as a Playwright journey (record on the card's register → editor → flip both signs → both registers and the reports are right). Test-suite note: htmx wires up swapped-in content ~20ms after
  insertion (it marks it `htmx-added` meanwhile), so scripted typing right after a re-render can be dropped — wait for `#editor.htmx-added` to clear; humans can't hit this.

- **2026-10-03** — Transfers from an account's register (card payments etc.). The register's quick-add bar now accepts ONE other `@account`, making a transfer between the register's account and that one (previously
  refused). Direction follows account type (`quickadd.IntoScope`): on a **card**, naming a bank pays the card (money into the card); on a **bank**, naming anything pays out of it; a leading **`+` reverses** it (cash advance /
  money arriving). The preview shows the direction before saving ("Transfer: Neo → Neo Credit"); the fixed account is never offered as the "other"; currency mismatches, naming the same account, two others, or unknown
  accounts are refused with a clear message. `quickadd.ScopedLine` rewrites the *typed text* (drops the @other, strips the +) so mistakes like a bad date still surface. History autocomplete on a register fills the other
  account for past transfers (with a `+` only when the past transfer went the unusual way). Entering from the home page is unchanged (`@from @to`). Tests: unit (direction table, resolvable lines, pass-through of mistakes),
  ~10 feature tests (both registers, both directions, +, bank→bank, refusals, preview, autocomplete round trip) and a Playwright journey of "Neo Credit Card Payment 300.00 261003 @neo".

- **2026-10-07** — Added `make run-lan` (`VINANCE_ADDR=0.0.0.0:8080`) so the app can be opened from a phone or another computer on the same Wi-Fi. `make run` is unchanged (localhost only). No auth exists, so README keeps the trusted-network warning.

## Status

- **2026-10-02** — **Free-text bar replaced by a structured entry form** (user request; answers: replace entirely, calendar date picker with no typed dates, one tags field). Same autocomplete, now filling every field. Routes: `/quickadd` (POST), `/quickadd/suggest/description`, `/quickadd/suggest/tags` (`/quickadd/preview` removed). `quickadd.Parse/Resolve` stay for recurring rules; `Complete`/`ScopedLine`/`IntoScope` removed. Fixed a bug found by the rewrite: with no history the form's `Account` was empty so a transfer's `To` defaulted to the same first account. Tests rewritten (feature + e2e; e2e green x3). No data changes.

**Everything on the roadmap is built:** v1 (import, quick-add, editor, list, balances, tag report, tag + account management), v1.5 (suggestions), v2 (recurring, budgets).
All tests are green: `make test` (unit + HTTP feature tests) and `make e2e` (Playwright journeys). Your real data is imported into `data/vinance.db`.

## Open questions

Nothing is blocked. These are choices I made on your behalf; each is easy to change. Answer whenever, or just say "fine".

**Data / import**
1. **Generic tags after flattening.** Leaf categories with the same name under different parents (e.g. two different `misc` or `travel` leaves) became one tag, so
   different things share a name. Left as is; the tag manager can rename/merge. Want a one-off script to disambiguate them?
2. **Misspelled tags** carried over from GnuCash can be renamed in Tags → Manage.
3. **The Imbalance transactions** are waiting to be fixed (banner on the home page).
4. **Exchange rates.** `prices` only holds rates the (removed) importer seeded from cross-currency transactions. Balances use the latest rate;
   reports use the rate nearest each transaction's date. There is no screen to add/edit rates and no importer to seed them, so conversions will drift stale as you keep spending
   foreign currency. Want a small "Rates" page (or auto-fetch from a free FX API)?

**Quick-add / editor**
5. **Description is required** (`12.50 #food @brisk` is rejected). Alternative: default the description to the first tag.
6. **Refunds** aren't supported in quick-add (amounts must be positive; `+` means income). Refund = negative expense line, available via the editor only.
7. **Tags on a transfer** attach to the destination line; the tag report only counts Expenses/Income lines, so they don't affect spending totals.
8. **Default account** for quick-add = the account used in your last entry (remembered per browser), falling back to the real account most recently *spent from*.
9. **Editor uses signed, GnuCash-style amounts** (− out of an account, + into it) with "Add line" pre-filling the remainder. A friendlier "statement mode" (total + items, no signs) is possible if
   the signed lines feel clunky.
10. **Renaming an account changes its `@slug`** (the quick-add token and `?account=` URLs). Alternative: keep slugs stable and only change the display name.

**Recurring / budgets**
11. **Recurring entries are never auto-created**; due ones wait for Add/Skip (statement amounts vary). Want an "auto-add" checkbox for fixed ones (rent, subscriptions)?
12. **Recurring rules need an explicit `@account`** (no moving default), and each overdue occurrence is listed separately (max 12 per rule at a time).
13. **Budgets** are monthly, USD-only, one per tag, no rollover of unspent amounts; the warning threshold is 85%. Say if you want other periods, other currencies, or a different threshold.

**Running it for real**
14. **Hosting/auth.** Still localhost-only with no login (the `auth` middleware is a no-op; `http.CrossOriginProtection` blocks cross-site POSTs). To use it from your phone you'll need a login and
    HTTPS, or Tailscale to your home machine. The server uses its own clock for "today", so a server in another time zone would date quick-adds differently than you'd expect.
15. **Backups.** The whole database is one file (`data/vinance.db`, WAL mode). There's no backup feature yet. A nightly `sqlite3 data/vinance.db ".backup 'backup.db'"` (or Litestream) is the usual answer.
- **2026-10-02** — Compact UI pass. The desktop view was too tall (entry form + filters pushed transactions below the fold). Entry form is now one flex line:
  kind and transfer-direction are `<select>`s (e2e uses `selectValue`), labels are screen-reader-only, date input is 8.6rem with small ‹ › Today, hint + summary + flash share one small line below.
  Scope name/currency for the JS summary moved onto the form (`data-scope-name`/`data-scope-currency`). Filter bar (search, account, tags, from/to, two checkboxes) is one wrapping line via CSS;
  markup unchanged. Pico form spacing reduced locally (`--pico-form-element-spacing-*`), not globally.
- **2026-10-02** — Added task: compact transaction editor (one row per split line). Prompted by a real grocery receipt split: the card-style lines fit only ~3 per screen.
- **2026-10-02** — Compact transaction editor done. A split line is one row (account, memo, tags, amount, currency, ↑ ↓ ✕); tab order follows the visual order. Header is date + description + notes on one line, the sign explanation
  is a collapsed "how the signs work" disclosure, and "+ Add line", the "left to allocate" indicator and Save share one row. "+ Add line" focuses the new line's memo (`Editor.FocusLast`). Layout is CSS-only (`.line-main` is `display: contents`).
  e2e `TestEditorLinesAreOneRowEach`: six lines are each under 52px and Save is still on an 800px-high screen.
  **Gotcha:** Pico sizes inputs as `1rem × line-height + padding`, ignoring a smaller font-size, so shrinking font/padding alone did nothing (inputs stayed 47px). Compact forms must also set `height: auto; line-height: 1.3`
  (shared rule for the entry form, filters and editor). Also, Pico's root font is 125% on wide screens, so `rem` is ~20px there.
- **2026-10-02** — Entry form: removed the ‹ › and Today buttons (just the date picker; the hint still says how far back the date is) and moved Add to its own centred row below the fields. e2e date test now fills the date directly.
- **2026-10-02** — Entry form: Add is back at the end of the one line (the centred-own-row experiment was a misread; the form had been wrapping Add onto a second line only because it was too wide).
  Narrower fields, and the page container is no longer capped at Pico's 700px on mid-width windows (`max-width: 1400px`). e2e `TestEntryFormIsOneLineOnALaptop`: spend/income are one row at 1000px, transfer at 1100px+.
- **2026-10-03** — Page side margins: container is `max-width: 1200px` with `padding-inline: clamp(1rem, 3vw, 2.5rem)` (the 1400px, near-zero-margin version felt cramped). Entry fields shrink a little more so the transfer row still fits on one line at 1100px.
- **2026-10-03** — Entry form revised after feedback: not forced to one line. Two consistent rows (choose / type), Add is a `+` button (aria-label "Add"), and the "Add to X" header, "Spending from X" summary and "6 days ago" hint were removed (server `DateHint`, JS and tests too).
  The amount placeholder carries the currency ("Amount MXN"). Replaced the one-line e2e test with `TestEntryFormIsTwoConsistentRows`.
- **2026-10-03** — Added task: editor action buttons on one line (big Save, ✕ Cancel, trash-can Delete).
- **2026-10-03** — Added task: tag suggestions on editor lines (reuse the entry form's suggest endpoint; generalise the `#qa-tags`-only JS).
- **2026-10-03** — Added task: bulk tag editing on selected transactions (checkbox rows; add / remove / move a tag; select-all-matching).
- **2026-10-03** — Editor action buttons done: one row with the "+ Add line" button and remaining indicator on the left, then a wide **Save**, a small ✕ **Cancel** and a trash-can **Delete** (inline SVG; `aria-label` + tooltip; the confirm prompt is kept). Also records Decision 10 (bulk tag editing rules). e2e `TestEditorActionsAreOneRow`.
- **2026-10-03** — Tag suggestions on editor lines done. Each line's tags input has `data-tag-input` + htmx wiring to the existing `/quickadd/suggest/tags` (explicit `hx-include="this"`, `hx-target`, `hx-swap`, `hx-sync`) and its own `.line-suggestions` strip (`data-tags-for` names the input). `app.js` no longer hard-codes `#qa-tags`: `configRequest` sends the field as `tags` + caret `pos`, and the chip click edits the input its list belongs to. e2e `TestEditorLineTagSuggestions`.
- **2026-10-03** — Bulk tag editing done. `/transactions` rows have checkboxes (`form="bulk"`); a bar appears when something is ticked: **Add / Remove / Move tag**, plus "Select all N matching" (re-resolved server-side from the filter in `return`, so it covers rows past the first page; capped at `ledger.MaxBulk` = 5000).
  `POST /transactions/bulk` is the confirm page (counts transactions + lines, changes nothing); `POST /transactions/bulk/apply` does it in one DB transaction and flashes a summary. `ledger.BulkPreview/BulkApply/MatchingIDs` (`internal/ledger/bulk.go`); add → category lines only, remove/move → lines carrying the tag, idempotent, tags created on demand.
  Refined Decision 10: ticks survive "load more" (rows stay on screen) and are cleared when the filter/search changes. Tests: ledger unit, `bulk_test.go` feature, e2e `TestBulkTagSelectedTransactions` + `TestBulkMoveTagForEverythingMatching`.
  **Gotcha:** the bulk bar sits next to the filter form, whose fields are `tag` and `to` (date): bulk fields are `bulk_tag`/`bulk_to` so selectors and params don't collide.
- **2026-10-05** — Filtered total done. `ledger.Service.Totals(Filter)` sums `splits.value` of asset/liability lines per `transactions.currency` over the whole filtered set (ignores paging); `Filter.Active()` gates it. The list's `TxnResults` renders a sticky `#txn-total` line (green/red, per currency) after the rows, so it also arrives with live filter swaps. Tests: `TestTotals`, `TestTransactionsTotalNetsMatchingRows`, e2e `TestTransactionsTotalCoversWholeResultSet`.
- **2026-10-06** — Accounts: compact edit form. Name, Type, Currency and Save share one wrapping line (scoped to `ul.account-list .account-form`; the shared `.account-form` on budgets/recurring is untouched). Pico height reset as for the other compact forms. e2e `TestAccountEditFormIsCompact`.
- **2026-10-06** — Accounts: the Edit link is now a pencil icon at the right of each row (the form still opens beneath it). e2e asserts its position in `TestAccountEditFormIsCompact`.
- **2026-10-06** — Accounts: clicking the pencil focuses the name with its text selected (typing replaces it). Click handler in `app.js`, not `toggle`, so a form reopened by a validation error does not steal focus. e2e `TestAccountEditSelectsName`.
- **2026-10-06** — Quick-add: the description suggestion list closes when focus moves to the amount, tags or any other field without accepting one (and a response still in flight is dropped), so the tag buttons are reachable. `focusin` + `htmx:beforeSwap` in `app.js`. e2e `TestSuggestionsDismissedWhenMovingToAnotherField`.
- **2026-10-06** — Tags: Tab completes the word under the caret when the suggestion list has exactly one entry (entry form and editor lines); with several, Tab moves on as before. `acceptTag` shared with the click handler in `app.js`. e2e `TestTabCompletesTheOnlyTagSuggestion`.
- **2026-10-07** — Pre-publication scrub for a public repo: removed real balances, totals, counts, merchant/payee names and the owner's name from PROJECT.md, tests and the fixture (synthetic amounts instead); history squashed to one commit. `data/` was never committed. Account/card institution names remain in the importer notes.
- **2026-10-07** — Removed the GnuCash importer (`internal/importer`, `import-gnucash`, `import.sql`) and its spec, which also contained real counts and merchant names. Kept the `gnucash_id` column (dropping it would need a migration on the real DB) and `UpsertPrice` (moved to `reports.sql`; the only way to write FX rates). Follow-up: there is now no way to add exchange rates (open question 4).
- **2026-10-07** — Test fixtures: replaced the remaining real-looking payees and amounts with generic ones ("Internet bill" MX$500, "Clothing store" $120).
- **2026-10-07** — Fixtures and docs: replaced real institution and fund names with fictitious ones of the same shape (e.g. the card account is now "Brisk", the bank "Wharf Bank"), keeping slug, prefix and ordering behaviour.
- **2026-10-07** — Go module renamed to `github.com/amascii/vinance` to match the GitHub account; commit author email set to the GitHub noreply address.
- **2026-10-07** — First push: private GitHub repo `amascii/vinance`. Final pre-publication pass: generalised the category-tree and subscription examples; CLAUDE.md now documents the remote.
- **2026-10-07** — Added an MIT `LICENSE` (copyright holder: the GitHub handle).
- **2026-10-07** — README with screenshots. `internal/demo` seeds a deterministic synthetic ledger (4 accounts, ~140 transactions, a split receipt, a peso trip, one imbalance, 5 budgets, 4 recurring rules) into a *temp* DB; `cmd/screenshots` serves the app against it with a fixed clock (2026-09-24) and captures 7 pages with Playwright into `docs/screenshots`. `data/vinance.db` is never opened (checked: size and mtime unchanged). Workflow change: changes now go through a branch + PR, not straight to `main`.
- **2026-10-07** — CI: `.github/workflows/ci.yml` (change detection, generated-code check, build/vet/test, Playwright e2e), README badge, and the branch-protection + squash-only decision (#12).
