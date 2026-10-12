package views

import (
	"context"
	"net/http"
	"strings"
)

type pathKey struct{}

// WithPath is middleware that records the request path so the layout can mark the current page in the nav.
func WithPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), pathKey{}, r.URL.Path)))
	})
}

type loggedInKey struct{}

// WithLoggedIn marks the request as authenticated, so the nav offers Log out.
func WithLoggedIn(ctx context.Context) context.Context {
	return context.WithValue(ctx, loggedInKey{}, true)
}

func loggedIn(ctx context.Context) bool { v, _ := ctx.Value(loggedInKey{}).(bool); return v }

type navLink struct {
	Href, Label, Icon string
}

// navMain is always visible; navMore lives in the More menu.
var (
	navMain = []navLink{{"/accounts", "Accounts", "wallet"}, {"/transactions", "Transactions", "list"}}
	navMore = []navLink{
		{"/budgets", "Budgets", "pie"}, {"/recurring", "Recurring", "repeat"}, {"/tags", "Tags", "tag"},
		{"/accounts/manage", "Manage accounts", "pencil"}, {"/tags/manage", "Manage tags", "pencil"},
	}
)

// navSection is the nav href the path belongs to: the longest matching prefix, so
// /transactions/42 is Transactions and /accounts/manage/3/update is Manage accounts.
func navSection(path string) string {
	best := ""
	if path == "/" {
		return "/"
	}
	for _, links := range [][]navLink{navMain, navMore} {
		for _, l := range links {
			if (path == l.Href || strings.HasPrefix(path, l.Href+"/")) && len(l.Href) > len(best) {
				best = l.Href
			}
		}
	}
	return best
}

func currentSection(ctx context.Context) string {
	p, _ := ctx.Value(pathKey{}).(string)
	return navSection(p)
}

func inMore(section string) bool {
	for _, l := range navMore {
		if l.Href == section {
			return true
		}
	}
	return false
}
