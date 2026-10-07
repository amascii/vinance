// Package web holds the HTTP router and handlers.
package web

import (
	"database/sql"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v2"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
)

//go:embed static
var staticFS embed.FS

// Server holds the dependencies handlers share.
type Server struct {
	logger *slog.Logger
	db     *sql.DB
	q      *gen.Queries
	ledger *ledger.Service
	now    func() time.Time
}

// Option customises NewRouter.
type Option func(*Server)

// WithClock sets the clock used for "today" (tests inject a fixed time).
func WithClock(now func() time.Time) Option { return func(s *Server) { s.now = now } }

// NewRouter builds the application router. The logger receives JSON request logs.
func NewRouter(logger *slog.Logger, conn *sql.DB, opts ...Option) http.Handler {
	s := &Server{logger: logger, db: conn, q: gen.New(conn), ledger: ledger.NewService(conn), now: time.Now}
	for _, o := range opts {
		o(s)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	// httplog builds its own logger; swap in ours so level and JSON format stay consistent.
	reqLog := httplog.NewLogger("vinance", httplog.Options{JSON: true, Concise: true})
	reqLog.Logger = logger
	r.Use(httplog.RequestLogger(reqLog, []string{"/healthz"}))
	r.Use(middleware.Recoverer)
	// Reject cross-site state-changing requests (a hostile web page POSTing to localhost).
	r.Use(http.NewCrossOriginProtection().Handler)
	r.Use(auth)

	static, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServerFS(static)))

	r.Get("/healthz", s.healthz)
	r.Get("/", s.home)
	r.Get("/accounts", s.accounts)
	r.Get("/accounts/manage", s.accountManageGet)
	r.Post("/accounts/manage/create", s.accountCreate)
	r.Post("/accounts/manage/{id}/update", s.accountUpdate)
	r.Post("/accounts/manage/{id}/archive", s.accountArchive)
	r.Post("/accounts/manage/{id}/delete", s.accountDelete)
	r.Get("/recurring", s.recurringGet)
	r.Post("/recurring/create", s.recurringCreate)
	r.Post("/recurring/{id}/update", s.recurringUpdate)
	r.Post("/recurring/{id}/active", s.recurringActive)
	r.Post("/recurring/{id}/delete", s.recurringDelete)
	r.Post("/recurring/{id}/accept", s.recurringAccept)
	r.Post("/recurring/{id}/skip", s.recurringSkip)
	r.Get("/budgets", s.budgetsGet)
	r.Post("/budgets/set", s.budgetSet)
	r.Post("/budgets/delete", s.budgetDelete)
	r.Get("/tags", s.tags)
	r.Get("/tags/manage", s.tagManageGet)
	r.Post("/tags/manage/rename", s.tagRename)
	r.Post("/tags/manage/delete", s.tagDelete)
	r.Get("/transactions", s.transactions)
	r.Post("/transactions/bulk", s.bulkReview)
	r.Post("/transactions/bulk/apply", s.bulkApply)
	r.Get("/transactions/{id}", s.editorGet)
	r.Post("/transactions/{id}", s.editorPost)
	r.Post("/transactions/{id}/remaining", s.editorRemaining)
	r.Post("/transactions/{id}/delete", s.editorDelete)
	r.Post("/quickadd", s.quickAddSubmit)
	r.Post("/quickadd/suggest/description", s.suggestDescription)
	r.Post("/quickadd/suggest/tags", s.suggestTags)
	return r
}

// auth is a placeholder for future authentication (Tailscale / login). No-op in v1.
func auth(next http.Handler) http.Handler { return next }

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := s.db.PingContext(r.Context()); err != nil {
		s.logger.Error("health check failed", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"db unavailable"}`))
		return
	}
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// render writes a templ component with the given status.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		s.logger.Error("render failed", "path", r.URL.Path, "error", err)
	}
}

// serverError logs err and sends a plain 500.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
