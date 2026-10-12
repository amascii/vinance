package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/httprate"
	"golang.org/x/crypto/bcrypt"

	"github.com/amascii/vinance/internal/web/views"
)

const (
	sessionLifetime = 30 * 24 * time.Hour
	sessionKey      = "authenticated"
	// loginAttempts caps POST /login across all clients: with one user, a global cap beats a per-IP one
	// (X-Forwarded-For is client-controlled behind a proxy) and costs nothing.
	loginAttempts = 10
)

// HashPassword returns the bcrypt hash to put in VINANCE_PASSWORD_HASH.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// WithAuth turns on password login: every page except /login, /healthz and /static needs a session.
// hash is a bcrypt hash (see HashPassword); secureCookies marks the session cookie Secure (set it when served over HTTPS).
func WithAuth(hash string, secureCookies bool) Option {
	return func(s *Server) { s.passwordHash, s.secureCookies = []byte(hash), secureCookies }
}

// newSessions builds the session manager, storing sessions in the app database.
func (s *Server) newSessions() *scs.SessionManager {
	sm := scs.New()
	sm.Store = sqlite3store.NewWithCleanupInterval(s.db, time.Hour)
	sm.Lifetime = sessionLifetime
	sm.Cookie.Name = "vinance_session"
	sm.Cookie.Secure = s.secureCookies // HttpOnly and SameSite=Lax are scs defaults
	return sm
}

// requireLogin sends visitors without a session to the login page.
func (s *Server) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authed := s.sessions.GetBool(r.Context(), sessionKey)
		switch {
		case authed:
			r = r.WithContext(views.WithLoggedIn(r.Context()))
		case r.URL.Path == "/login" || r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/"):
		default:
			s.redirectToLogin(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		next.ServeHTTP(w, r)
	})
}

// redirectToLogin remembers where the visitor was going. For htmx requests (a swap whose session expired) it asks htmx
// for a full-page redirect, returning to the page the user is on rather than the fragment URL that was requested.
func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	dest := "/login"
	switch {
	case r.Header.Get("HX-Request") == "true":
		if cur, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil {
			dest = loginURL(cur.RequestURI())
		}
		w.Header().Set("HX-Redirect", dest)
		w.WriteHeader(http.StatusUnauthorized)
		return
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		dest = loginURL(r.URL.RequestURI())
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func loginURL(next string) string {
	if next = loginNext(next); next == "/" {
		return "/login"
	}
	return "/login?next=" + url.QueryEscape(next)
}

// loginNext returns next if it is a same-site path worth returning to after login, else "/" (never back to /login).
func loginNext(next string) string {
	next = safeNext(next)
	u, err := url.Parse(next)
	if next == "" || err != nil || u.Path == "/login" || u.Path == "/logout" {
		return "/"
	}
	return next
}

func (s *Server) loginGet(w http.ResponseWriter, r *http.Request) {
	next := loginNext(r.URL.Query().Get("next"))
	if s.sessions.GetBool(r.Context(), sessionKey) {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, views.LoginPage(next, ""))
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	next := loginNext(r.PostFormValue("next"))
	if bcrypt.CompareHashAndPassword(s.passwordHash, []byte(r.PostFormValue("password"))) != nil {
		s.logger.Warn("login failed", "remote", r.RemoteAddr)
		s.render(w, r, http.StatusUnauthorized, views.LoginPage(next, "Wrong password."))
		return
	}
	// A fresh token on privilege change defeats session fixation.
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.sessions.Put(r.Context(), sessionKey, true)
	s.logger.Info("login", "remote", r.RemoteAddr)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) logoutPost(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Destroy(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// loginLimit throttles password guessing.
func loginLimit() func(http.Handler) http.Handler {
	return httprate.LimitAll(loginAttempts, time.Minute)
}
