package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

// bulkRequest is a bulk tag change as submitted: the operation plus which transactions it covers,
// either ticked ids or "everything matching the filter in return".
type bulkRequest struct {
	change ledger.BulkChange
	ids    []int64
	all    bool
	back   string
}

// parseBulk reads the submitted form. problem is a message for the person ("" when fine).
func (s *Server) parseBulk(r *http.Request) (req bulkRequest, problem string, err error) {
	if err := r.ParseForm(); err != nil {
		return req, "", err
	}
	req.back = nonEmptyStr(safeNext(r.PostFormValue("return")), "/transactions")
	req.change = ledger.BulkChange{Op: ledger.BulkOp(r.PostFormValue("op")), Tag: r.PostFormValue("bulk_tag"), To: r.PostFormValue("bulk_to")}
	req.all = r.PostFormValue("scope") == "all"
	if req.all {
		u, perr := url.Parse(req.back)
		if perr != nil {
			return req, "", perr
		}
		p, err := s.parseFilter(r.Context(), u.Query())
		if err != nil {
			return req, "", err
		}
		if !p.empty {
			if req.ids, err = s.ledger.MatchingIDs(r.Context(), p.filter); err != nil {
				return req, bulkProblem(err), nilIfProblem(err)
			}
		}
	} else {
		for _, v := range r.PostForm["id"] {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				req.ids = append(req.ids, id)
			}
		}
	}
	if len(req.ids) == 0 {
		return req, "Tick at least one transaction first.", nil
	}
	return req, "", nil
}

func bulkProblem(err error) string {
	switch {
	case errors.Is(err, ledger.ErrInvalidTag):
		return "Enter a tag name (and, to move, a different tag to move it to)."
	case errors.Is(err, ledger.ErrTooManyTransactions):
		return fmt.Sprintf("That is more than %d transactions at once; narrow the filter first.", ledger.MaxBulk)
	}
	return ""
}

// nilIfProblem passes through errors that are not the person's to fix.
func nilIfProblem(err error) error {
	if bulkProblem(err) != "" {
		return nil
	}
	return err
}

// bulkReview is the confirm step: it says what would change and changes nothing.
func (s *Server) bulkReview(w http.ResponseWriter, r *http.Request) {
	req, problem, err := s.parseBulk(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if problem == "" {
		var res ledger.BulkResult
		if res, err = s.ledger.BulkPreview(r.Context(), req.ids, req.change); err != nil {
			if problem = bulkProblem(err); problem == "" {
				s.serverError(w, r, err)
				return
			}
		} else if res.Transactions == 0 {
			problem = "Nothing to change: no selected line " + map[ledger.BulkOp]string{
				ledger.BulkAdd: "needs that tag.", ledger.BulkRemove: "has that tag.", ledger.BulkMove: "has that tag.",
			}[req.change.Op]
		} else {
			c := views.BulkConfirm{
				Op: string(req.change.Op), Tag: ledger.TagSlug(req.change.Tag), To: ledger.TagSlug(req.change.To),
				Transactions: res.Transactions, Lines: res.Lines, All: req.all, Return: req.back,
			}
			if !req.all {
				c.IDs = req.ids
			}
			s.render(w, r, http.StatusOK, views.BulkConfirmPage(c))
			return
		}
	}
	setFlash(w, problem)
	http.Redirect(w, r, req.back, http.StatusSeeOther)
}

// bulkApply makes the confirmed change in one database transaction.
func (s *Server) bulkApply(w http.ResponseWriter, r *http.Request) {
	req, problem, err := s.parseBulk(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if problem == "" {
		res, err := s.ledger.BulkApply(r.Context(), req.ids, req.change)
		switch {
		case err == nil:
			tag := "#" + ledger.TagSlug(req.change.Tag)
			what := fmt.Sprintf("%s %s (%s)", plural2(res.Transactions, "transaction"), "updated", plural2(res.Lines, "line"))
			switch req.change.Op {
			case ledger.BulkAdd:
				problem = fmt.Sprintf("Added %s: %s.", tag, what)
			case ledger.BulkRemove:
				problem = fmt.Sprintf("Removed %s: %s.", tag, what)
			default:
				problem = fmt.Sprintf("Moved %s → #%s: %s.", tag, ledger.TagSlug(req.change.To), what)
			}
		default:
			if problem = bulkProblem(err); problem == "" {
				s.serverError(w, r, err)
				return
			}
		}
	}
	setFlash(w, problem)
	http.Redirect(w, r, req.back, http.StatusSeeOther)
}

func plural2(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strings.TrimSpace(views.Thousands(n)) + " " + noun + "s"
}
