package web

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

const flashCookie = "vinance_flash"

// setFlash stores a one-shot message to show after a redirect.
func setFlash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: url.QueryEscape(msg), Path: "/", MaxAge: 30, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// takeFlash returns and clears the pending flash message, if any.
func takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	msg, err := url.QueryUnescape(c.Value)
	if err != nil {
		return ""
	}
	return msg
}

func sortKey(s string) string {
	if s == "uses" {
		return "uses"
	}
	return "name"
}

// tagManageModel builds the page model from the current filter.
func (s *Server) tagManageModel(r *http.Request, q, sortBy string) (views.TagManage, error) {
	all, err := s.ledger.ListTags(r.Context())
	if err != nil {
		return views.TagManage{}, err
	}
	m := views.TagManage{Q: q, Sort: sortKey(sortBy), Total: len(all)}
	needle := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), "#")))
	for _, t := range all {
		if needle != "" && !strings.Contains(t.Name, needle) {
			continue
		}
		m.Tags = append(m.Tags, views.TagManageRow{Name: t.Name, Splits: t.Splits, Href: "/transactions?tag=" + url.QueryEscape(t.Name)})
	}
	if m.Sort == "uses" {
		sort.SliceStable(m.Tags, func(i, j int) bool { return m.Tags[i].Splits > m.Tags[j].Splits })
	}
	return m, nil
}

func (s *Server) tagManageGet(w http.ResponseWriter, r *http.Request) {
	m, err := s.tagManageModel(r, r.URL.Query().Get("q"), r.URL.Query().Get("sort"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	m.Flash = takeFlash(w, r)
	s.render(w, r, http.StatusOK, views.TagManagePage(m))
}

func manageRedirect(r *http.Request) string {
	return views.ManageURL(r.PostFormValue("q"), r.PostFormValue("sort"))
}

func (s *Server) tagRename(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	from := strings.TrimSpace(r.PostFormValue("from"))
	to := ledger.TagSlug(r.PostFormValue("to"))
	confirmed := r.PostFormValue("confirm") == "1"

	problem := func(status int, msg string) {
		m, err := s.tagManageModel(r, r.PostFormValue("q"), r.PostFormValue("sort"))
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		m.Problem = msg
		s.render(w, r, status, views.TagManagePage(m))
	}

	if to == "" {
		problem(http.StatusUnprocessableEntity, "Enter a tag name (letters, numbers and dashes).")
		return
	}
	fromSplits, exists, err := s.ledger.TagUsage(ctx, from)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !exists {
		problem(http.StatusNotFound, "That tag no longer exists.")
		return
	}
	if to == from {
		http.Redirect(w, r, manageRedirect(r), http.StatusSeeOther)
		return
	}
	if toSplits, toExists, err := s.ledger.TagUsage(ctx, to); err != nil {
		s.serverError(w, r, err)
		return
	} else if toExists && !confirmed {
		m, err := s.tagManageModel(r, r.PostFormValue("q"), r.PostFormValue("sort"))
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		m.Confirm = &views.MergeConfirm{From: from, To: to, FromSplits: fromSplits, ToSplits: toSplits}
		s.render(w, r, http.StatusOK, views.TagManagePage(m))
		return
	}

	res, err := s.ledger.RenameTag(ctx, from, to)
	switch {
	case errors.Is(err, ledger.ErrTagNotFound):
		problem(http.StatusNotFound, "That tag no longer exists.")
		return
	case errors.Is(err, ledger.ErrInvalidTag):
		problem(http.StatusUnprocessableEntity, "Enter a tag name (letters, numbers and dashes).")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("tag renamed", "from", from, "to", res.To, "merged", res.Merged, "lines", res.Splits)
	if res.Merged {
		setFlash(w, "Merged #"+from+" into #"+res.To+" ("+views.Thousands(res.Splits)+" "+plural(res.Splits, "line", "lines")+").")
	} else {
		setFlash(w, "Renamed #"+from+" to #"+res.To+".")
	}
	http.Redirect(w, r, manageRedirect(r), http.StatusSeeOther)
}

func (s *Server) tagDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	switch err := s.ledger.DeleteTag(r.Context(), name); {
	case errors.Is(err, ledger.ErrTagInUse):
		m, merr := s.tagManageModel(r, r.PostFormValue("q"), r.PostFormValue("sort"))
		if merr != nil {
			s.serverError(w, r, merr)
			return
		}
		m.Problem = "#" + name + " is still used by transactions. Rename or merge it instead."
		s.render(w, r, http.StatusConflict, views.TagManagePage(m))
		return
	case errors.Is(err, ledger.ErrTagNotFound):
		http.Redirect(w, r, manageRedirect(r), http.StatusSeeOther)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("tag deleted", "name", name)
	setFlash(w, "Deleted #"+name+".")
	http.Redirect(w, r, manageRedirect(r), http.StatusSeeOther)
}
