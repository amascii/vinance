package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/quickadd"
	"github.com/amascii/vinance/internal/web/views"
)

const isoDate = "2006-01-02"

// signed formats a resolved entry's amount the way lists show it: -$ for spending, +$ for income.
func signed(kind quickadd.Kind, amount int64, cur string) string {
	s := ledger.Format(amount, cur)
	switch kind {
	case quickadd.KindExpense:
		return "-" + s
	case quickadd.KindIncome:
		return "+" + s
	}
	return s
}

// resolveRuleText interprets a rule's text for a given due date.
func resolveRuleText(text, date string, accts []quickadd.Account) (quickadd.Result, quickadd.Parsed, error) {
	d, err := time.Parse(isoDate, date)
	if err != nil {
		return quickadd.Result{}, quickadd.Parsed{}, err
	}
	parsed := quickadd.Parse(text, d)
	res, err := quickadd.Resolve(parsed, accts, 0)
	return res, parsed, err
}

// checkRuleText validates text for a new/edited rule: no date (the schedule supplies it) and an
// explicit account (recurring entries don't use a moving "last used" default).
func checkRuleText(text, anchor string, accts []quickadd.Account) (string, error) {
	text = strings.TrimSpace(text)
	d, err := time.Parse(isoDate, anchor)
	if err != nil {
		d = time.Now()
	}
	parsed := quickadd.Parse(text, d)
	if parsed.DateGiven {
		return "", errors.New("leave the date out of the text: due dates come from the schedule")
	}
	if len(parsed.Accounts) == 0 && parsed.OK() {
		return "", errors.New("include the @account (recurring entries don't use a default account)")
	}
	if _, err := quickadd.Resolve(parsed, accts, 0); err != nil {
		return "", err
	}
	return text, nil
}

func (s *Server) recurringModel(ctx context.Context) (views.RecurringPage, []quickadd.Account, error) {
	accts, _, err := s.loadAccounts(ctx)
	if err != nil {
		return views.RecurringPage{}, nil, err
	}
	today := s.now().Format(isoDate)
	m := views.RecurringPage{Form: views.RuleForm{Freq: "monthly", Every: "1", Anchor: today}}

	due, err := s.ledger.Due(ctx, today)
	if err != nil {
		return m, nil, err
	}
	for _, d := range due {
		row := views.DueRow{RuleID: d.Rule.ID, Date: d.Date, Overdue: d.Date < today}
		res, parsed, rerr := resolveRuleText(d.Rule.Text, d.Date, accts)
		if rerr != nil {
			row.Description, row.Problem = d.Rule.Text, "This can't be added as written: "+rerr.Error()+". Edit the rule below."
		} else {
			p := previewOK(res, parsed)
			row.Description, row.Amount, row.Accounts, row.Tags = p.Description, signed(res.Kind, res.Amount, res.Currency), p.Accounts, p.Tags
		}
		m.Due = append(m.Due, row)
	}

	rules, err := s.ledger.ListRules(ctx)
	if err != nil {
		return m, nil, err
	}
	for _, r := range rules {
		row := views.RuleRow{
			ID: r.ID, Text: r.Text, Schedule: r.Schedule.Describe(), Active: r.Active,
			Form: views.RuleForm{Text: r.Text, Freq: string(r.Schedule.Freq), Every: strconv.Itoa(r.Schedule.Every), Anchor: r.Schedule.Anchor, End: r.Schedule.End},
		}
		switch next := r.NextDue(); {
		case !r.Active:
			row.Next = "Paused"
		case next == "":
			row.Next = "Ended"
		default:
			row.Next = "Next: " + next
		}
		res, parsed, rerr := resolveRuleText(r.Text, firstNonEmpty(r.NextDue(), r.Schedule.Anchor), accts)
		if rerr != nil {
			row.Problem = "This can't be added as written: " + rerr.Error() + "."
		} else {
			row.Preview = previewOK(res, parsed)
			row.Preview.Amount = signed(res.Kind, res.Amount, res.Currency)
		}
		m.Rules = append(m.Rules, row)
	}
	return m, accts, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Server) recurringGet(w http.ResponseWriter, r *http.Request) {
	m, _, err := s.recurringModel(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	m.Flash = takeFlash(w, r)
	s.render(w, r, http.StatusOK, views.RecurringPageView(m))
}

// recurringProblem re-renders the page with a message attached to a rule (0 = the add form).
func (s *Server) recurringProblem(w http.ResponseWriter, r *http.Request, status int, id int64, form *views.RuleForm, msg string) {
	m, _, err := s.recurringModel(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	m.Problem, m.ProblemID = msg, id
	if form != nil {
		if id == 0 {
			m.Form = *form
		} else {
			for i := range m.Rules {
				if m.Rules[i].ID == id {
					m.Rules[i].Form = *form
				}
			}
		}
	}
	s.render(w, r, status, views.RecurringPageView(m))
}

// ruleFromForm reads and validates the add/edit form.
func (s *Server) ruleFromForm(r *http.Request, accts []quickadd.Account) (string, ledger.Schedule, views.RuleForm, error) {
	form := views.RuleForm{
		Text: strings.TrimSpace(r.PostFormValue("text")), Freq: r.PostFormValue("freq"), Every: strings.TrimSpace(r.PostFormValue("every")),
		Anchor: r.PostFormValue("anchor"), End: r.PostFormValue("end"),
	}
	every, err := strconv.Atoi(form.Every)
	if err != nil {
		return "", ledger.Schedule{}, form, errors.New("\"every\" must be a whole number")
	}
	sch := ledger.Schedule{Freq: ledger.Freq(form.Freq), Every: every, Anchor: form.Anchor, End: form.End}
	if err := sch.Validate(); err != nil {
		return "", sch, form, err
	}
	text, err := checkRuleText(form.Text, form.Anchor, accts)
	if err != nil {
		return "", sch, form, err
	}
	return text, sch, form, nil
}

func ruleMessage(err error) string {
	return strings.TrimPrefix(strings.TrimSuffix(err.Error(), "."), ledger.ErrInvalidRule.Error()+": ")
}

func (s *Server) recurringCreate(w http.ResponseWriter, r *http.Request) {
	accts, _, err := s.loadAccounts(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	text, sch, form, err := s.ruleFromForm(r, accts)
	if err != nil {
		s.recurringProblem(w, r, http.StatusUnprocessableEntity, 0, &form, capitalize(ruleMessage(err))+".")
		return
	}
	rule, err := s.ledger.CreateRule(r.Context(), text, sch)
	if err != nil {
		if errors.Is(err, ledger.ErrInvalidRule) {
			s.recurringProblem(w, r, http.StatusUnprocessableEntity, 0, &form, capitalize(ruleMessage(err))+".")
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("recurring rule created", "id", rule.ID, "freq", string(sch.Freq), "every", sch.Every, "anchor", sch.Anchor)
	setFlash(w, "Added “"+rule.Text+"” ("+sch.Describe()+").")
	http.Redirect(w, r, "/recurring", http.StatusSeeOther)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (s *Server) ruleID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Server) recurringUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := s.ruleID(w, r)
	if !ok {
		return
	}
	accts, _, err := s.loadAccounts(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	text, sch, form, err := s.ruleFromForm(r, accts)
	if err != nil {
		s.recurringProblem(w, r, http.StatusUnprocessableEntity, id, &form, capitalize(ruleMessage(err))+".")
		return
	}
	rule, err := s.ledger.UpdateRule(r.Context(), id, text, sch, s.now().Format(isoDate))
	switch {
	case errors.Is(err, ledger.ErrRuleNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, ledger.ErrInvalidRule):
		s.recurringProblem(w, r, http.StatusUnprocessableEntity, id, &form, capitalize(ruleMessage(err))+".")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("recurring rule updated", "id", id)
	setFlash(w, "Saved “"+rule.Text+"”.")
	http.Redirect(w, r, "/recurring", http.StatusSeeOther)
}

func (s *Server) recurringActive(w http.ResponseWriter, r *http.Request) {
	id, ok := s.ruleID(w, r)
	if !ok {
		return
	}
	active := r.PostFormValue("active") == "1"
	switch err := s.ledger.SetRuleActive(r.Context(), id, active); {
	case errors.Is(err, ledger.ErrRuleNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	setFlash(w, map[bool]string{true: "Resumed.", false: "Paused."}[active])
	http.Redirect(w, r, "/recurring", http.StatusSeeOther)
}

func (s *Server) recurringDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.ruleID(w, r)
	if !ok {
		return
	}
	switch err := s.ledger.DeleteRule(r.Context(), id); {
	case errors.Is(err, ledger.ErrRuleNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("recurring rule deleted", "id", id)
	setFlash(w, "Deleted. Transactions already added from it were kept.")
	http.Redirect(w, r, "/recurring", http.StatusSeeOther)
}

func (s *Server) recurringAccept(w http.ResponseWriter, r *http.Request) {
	id, ok := s.ruleID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	forDate := r.PostFormValue("date")
	rule, err := s.ledger.GetRule(ctx, id)
	if errors.Is(err, ledger.ErrRuleNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	accts, _, err := s.loadAccounts(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	res, _, rerr := resolveRuleText(rule.Text, forDate, accts)
	if rerr != nil {
		s.recurringProblem(w, r, http.StatusUnprocessableEntity, id, nil, "This can't be added as written: "+rerr.Error()+". Edit the rule first.")
		return
	}
	txnID, err := s.ledger.AcceptDue(ctx, id, forDate, res.Input)
	switch {
	case errors.Is(err, ledger.ErrNotDue), errors.Is(err, ledger.ErrAlreadyAdded):
		setFlash(w, "That one was already handled.")
		http.Redirect(w, r, "/recurring", http.StatusSeeOther)
		return
	case err != nil:
		var ve *ledger.ValidationError
		if errors.As(err, &ve) {
			s.recurringProblem(w, r, http.StatusUnprocessableEntity, id, nil, strings.Join(ve.Problems, "; "))
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("recurring transaction added", "rule", id, "transaction", txnID, "for", forDate)
	setFlash(w, "Added "+res.Input.Description+" "+ledger.Format(res.Amount, res.Currency)+" for "+forDate+".")
	http.Redirect(w, r, "/recurring", http.StatusSeeOther)
}

func (s *Server) recurringSkip(w http.ResponseWriter, r *http.Request) {
	id, ok := s.ruleID(w, r)
	if !ok {
		return
	}
	forDate := r.PostFormValue("date")
	switch err := s.ledger.SkipDue(r.Context(), id, forDate); {
	case errors.Is(err, ledger.ErrRuleNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, ledger.ErrNotDue):
		setFlash(w, "That one was already handled.")
	case err != nil:
		s.serverError(w, r, err)
		return
	default:
		s.logger.Info("recurring occurrence skipped", "rule", id, "for", forDate)
		setFlash(w, "Skipped "+forDate+".")
	}
	http.Redirect(w, r, "/recurring", http.StatusSeeOther)
}
