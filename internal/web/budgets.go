package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

// monthParam returns a valid YYYY-MM from the request, defaulting to the current month.
func (s *Server) monthParam(v string) string {
	if _, _, err := ledger.MonthBounds(v); err == nil {
		return v
	}
	return s.now().Format("2006-01")
}

func shiftMonth(month string, delta int) string {
	t, _ := time.Parse("2006-01", month)
	return t.AddDate(0, delta, 0).Format("2006-01")
}

func budgetsURL(month string) string { return "/budgets?month=" + url.QueryEscape(month) }

// budgetState maps usage to severity: ok, warn from 85% of the budget, over once exceeded.
func budgetState(l ledger.BudgetLine) (state, icon string) {
	switch {
	case l.SpentUSD > l.MonthlyUSD:
		return "over", "⚠"
	case l.Percent() >= 85:
		return "warn", "!"
	}
	return "ok", "✓"
}

func (s *Server) budgetsModel(r *http.Request, month string) (views.BudgetsPage, error) {
	ctx := r.Context()
	st, err := s.ledger.BudgetStatus(ctx, month)
	if err != nil {
		return views.BudgetsPage{}, err
	}
	first, _ := time.Parse("2006-01", month)
	m := views.BudgetsPage{
		Month: month, MonthLabel: first.Format("January 2006"),
		PrevHref: budgetsURL(shiftMonth(month, -1)), NextHref: budgetsURL(shiftMonth(month, 1)),
		TotalBudget: ledger.Format(st.TotalBudget, "USD"), TotalSpending: ledger.Format(st.TotalSpending, "USD"),
		Missing: st.Missing,
	}
	today := s.now().Format(isoDate)
	pace := -1
	if month == s.now().Format("2006-01") {
		pace = int(ledger.MonthProgress(month, today)*100 + 0.5)
		m.PaceNote = fmt.Sprintf("day %d of %d", s.now().Day(), daysInMonth(first))
	}
	for _, l := range st.Lines {
		state, icon := budgetState(l)
		label := ledger.Format(l.Remaining(), "USD") + " left"
		if l.Remaining() < 0 {
			label = ledger.Format(-l.Remaining(), "USD") + " over"
		}
		fill := l.Percent()
		if fill > 100 {
			fill = 100
		}
		v := url.Values{"tag": {l.Tag}, "from": {st.From}, "to": {st.To}}
		m.Bars = append(m.Bars, views.BudgetBar{
			Tag: l.Tag, Href: "/transactions?" + v.Encode(), Budget: ledger.Format(l.MonthlyUSD, "USD"),
			Spent: ledger.Format(l.SpentUSD, "USD"), Label: label, Icon: icon, State: state, Percent: l.Percent(),
			Fill: fill, Pace: pace, AmountVal: ledger.FormatInput(l.MonthlyUSD, "USD"),
		})
	}
	tags, err := s.ledger.ListTags(ctx)
	if err != nil {
		return m, err
	}
	for _, t := range tags {
		m.TagOptions = append(m.TagOptions, t.Name)
	}
	return m, nil
}

func daysInMonth(first time.Time) int { return first.AddDate(0, 1, -1).Day() }

func (s *Server) budgetsGet(w http.ResponseWriter, r *http.Request) {
	m, err := s.budgetsModel(r, s.monthParam(r.URL.Query().Get("month")))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	m.Flash = takeFlash(w, r)
	s.render(w, r, http.StatusOK, views.BudgetsPageView(m))
}

// budgetsProblem re-renders the page (422) with a message, attached to the row being edited
// (the per-row form posts edit=1) or to the add form, which keeps what was typed.
func (s *Server) budgetsProblem(w http.ResponseWriter, r *http.Request, month, tag string, form views.BudgetForm, msg string) {
	m, err := s.budgetsModel(r, month)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	m.Problem = msg
	if r.PostFormValue("edit") == "1" {
		m.ProblemTag = ledger.TagSlug(tag)
	} else {
		m.Form = form
	}
	s.render(w, r, http.StatusUnprocessableEntity, views.BudgetsPageView(m))
}

func (s *Server) budgetSet(w http.ResponseWriter, r *http.Request) {
	month := s.monthParam(r.PostFormValue("month"))
	tag := strings.TrimSpace(r.PostFormValue("tag"))
	amountText := strings.TrimSpace(r.PostFormValue("amount"))
	form := views.BudgetForm{Tag: tag, Amount: amountText}

	amount, err := ledger.ParseAmount(amountText, "USD")
	if err != nil || amount <= 0 {
		s.budgetsProblem(w, r, month, tag, form, "Enter a monthly amount in dollars, like 400.00.")
		return
	}
	b, err := s.ledger.SetBudget(r.Context(), tag, amount)
	switch {
	case errors.Is(err, ledger.ErrInvalidBudget):
		s.budgetsProblem(w, r, month, tag, form, strings.TrimPrefix(err.Error(), ledger.ErrInvalidBudget.Error()+": ")+".")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("budget set", "tag", b.Tag, "monthly_usd_cents", b.MonthlyUSD)
	setFlash(w, "Budget for #"+b.Tag+" is "+ledger.Format(b.MonthlyUSD, "USD")+" a month.")
	http.Redirect(w, r, budgetsURL(month), http.StatusSeeOther)
}

func (s *Server) budgetDelete(w http.ResponseWriter, r *http.Request) {
	month := s.monthParam(r.PostFormValue("month"))
	tag := strings.TrimSpace(r.PostFormValue("tag"))
	switch err := s.ledger.DeleteBudget(r.Context(), tag); {
	case errors.Is(err, ledger.ErrBudgetNotFound):
		// already gone: nothing to do
	case err != nil:
		s.serverError(w, r, err)
		return
	default:
		s.logger.Info("budget removed", "tag", tag)
		setFlash(w, "Removed the budget for #"+tag+".")
	}
	http.Redirect(w, r, budgetsURL(month), http.StatusSeeOther)
}
