package web

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

func (s *Server) accountManageModel(r *http.Request) (views.AccountManage, error) {
	all, err := s.ledger.ListAccounts(r.Context())
	if err != nil {
		return views.AccountManage{}, err
	}
	m := views.AccountManage{Form: views.AccountForm{Type: "asset", Currency: "USD", OpeningDate: s.now().Format("2006-01-02")}}
	for _, a := range all {
		row := views.AccountManageRow{
			ID: a.ID, Name: a.Name, Slug: a.Slug, Type: a.Type, Currency: a.Currency, Splits: a.Splits, Archived: a.Archived,
			Href: "/transactions?account=" + url.QueryEscape(a.Slug),
		}
		if a.Archived {
			m.Archived = append(m.Archived, row)
		} else {
			m.Active = append(m.Active, row)
		}
	}
	return m, nil
}

func (s *Server) accountManageGet(w http.ResponseWriter, r *http.Request) {
	m, err := s.accountManageModel(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	m.Flash = takeFlash(w, r)
	s.render(w, r, http.StatusOK, views.AccountManagePage(m))
}

// accountProblem re-renders the page with a message (for the add form when id is 0).
func (s *Server) accountProblem(w http.ResponseWriter, r *http.Request, status int, id int64, form *views.AccountForm, msg string) {
	m, err := s.accountManageModel(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	m.Problem, m.ProblemID = msg, id
	if form != nil {
		m.Form = *form
	}
	s.render(w, r, status, views.AccountManagePage(m))
}

// friendly turns a ledger error into a message a person can act on.
func friendly(err error) (string, bool) {
	switch {
	case errors.Is(err, ledger.ErrInvalidAccount):
		return strings.TrimPrefix(err.Error(), ledger.ErrInvalidAccount.Error()+": "), true
	case errors.Is(err, ledger.ErrAccountExists):
		return "An account named that (or one that would type the same way in quick-add) already exists: " + strings.TrimPrefix(err.Error(), ledger.ErrAccountExists.Error()+" "), true
	case errors.Is(err, ledger.ErrAccountInUse):
		return strings.TrimPrefix(err.Error(), ledger.ErrAccountInUse.Error()+": "), true
	case errors.Is(err, ledger.ErrAccountNotFound):
		return "That account doesn't exist (or can't be edited).", true
	}
	return "", false
}

func (s *Server) accountCreate(w http.ResponseWriter, r *http.Request) {
	form := views.AccountForm{
		Name: strings.TrimSpace(r.PostFormValue("name")), Type: r.PostFormValue("type"),
		Currency: strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency"))),
		Opening:  strings.TrimSpace(r.PostFormValue("opening")), OpeningDate: r.PostFormValue("opening_date"),
	}
	in := ledger.NewAccount{Name: form.Name, Type: form.Type, Currency: form.Currency, OpeningDate: form.OpeningDate}
	if form.Opening != "" {
		amt, err := ledger.ParseAmount(form.Opening, form.Currency)
		if err != nil {
			s.accountProblem(w, r, http.StatusUnprocessableEntity, 0, &form, "The opening balance isn't a valid amount for "+form.Currency+" (use a number like 1500.00).")
			return
		}
		in.OpeningBalance = amt
	}
	acct, err := s.ledger.CreateAccount(r.Context(), in)
	if err != nil {
		if msg, ok := friendly(err); ok {
			s.accountProblem(w, r, http.StatusUnprocessableEntity, 0, &form, msg)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("account created", "id", acct.ID, "name", acct.Name, "type", acct.Type, "currency", acct.Currency, "opening_balance", in.OpeningBalance)
	setFlash(w, "Added "+acct.Name+" (quick-add: @"+acct.Slug+").")
	http.Redirect(w, r, "/accounts/manage", http.StatusSeeOther)
}

func (s *Server) accountID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Server) accountUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := s.accountID(w, r)
	if !ok {
		return
	}
	u := ledger.AccountUpdate{
		Name: r.PostFormValue("name"), Type: r.PostFormValue("type"),
		Currency: r.PostFormValue("currency"),
	}
	acct, err := s.ledger.UpdateAccount(r.Context(), id, u)
	if err != nil {
		if msg, ok := friendly(err); ok {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, ledger.ErrAccountNotFound) {
				status = http.StatusNotFound
			}
			s.accountProblem(w, r, status, id, nil, msg)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("account updated", "id", id, "name", acct.Name, "type", acct.Type, "currency", acct.Currency)
	setFlash(w, "Saved "+acct.Name+" (quick-add: @"+acct.Slug+").")
	http.Redirect(w, r, "/accounts/manage", http.StatusSeeOther)
}

func (s *Server) accountArchive(w http.ResponseWriter, r *http.Request) {
	id, ok := s.accountID(w, r)
	if !ok {
		return
	}
	archived := r.PostFormValue("archived") == "1"
	switch err := s.ledger.SetAccountArchived(r.Context(), id, archived); {
	case errors.Is(err, ledger.ErrAccountNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("account archived", "id", id, "archived", archived)
	setFlash(w, map[bool]string{true: "Archived.", false: "Restored."}[archived])
	http.Redirect(w, r, "/accounts/manage", http.StatusSeeOther)
}

func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.accountID(w, r)
	if !ok {
		return
	}
	switch err := s.ledger.DeleteAccount(r.Context(), id); {
	case errors.Is(err, ledger.ErrAccountNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, ledger.ErrAccountInUse):
		s.accountProblem(w, r, http.StatusConflict, id, nil, "This account has transactions, so it can't be deleted. Archive it instead.")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("account deleted", "id", id)
	setFlash(w, "Deleted.")
	http.Redirect(w, r, "/accounts/manage", http.StatusSeeOther)
}
