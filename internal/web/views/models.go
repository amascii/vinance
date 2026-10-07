package views

// TxnRow is one transaction as shown in a list.
type TxnRow struct {
	ID           int64
	Date         string
	Description  string
	Direction    string // "out" | "in" | "transfer" | ""
	Amount       string // formatted with currency symbol and sign for in/out
	Accounts     []string
	Tags         []string
	HasImbalance bool
	Splits       []SplitRow // shown in a collapsible detail when ShowSplits
	EditURL      string     // where this transaction is edited
	Running      string     // account view: "Balance $1,234.56" / "Owed $5,733.74" after this transaction
	ShowSplits   bool       // more than one item line, or any memo
	Pick         bool       // show a selection checkbox (bulk tag editing)
}

// SplitRow is one split line inside a transaction's detail.
type SplitRow struct {
	Label  string // account name for real accounts, "" for built-in category splits
	Memo   string
	Tags   []string
	Amount string
	Warn   bool // sits in the Imbalance account
}

// Scope names the account a quick-add bar is locked to.
type Scope struct {
	Slug     string
	Name     string
	Type     string // asset | liability
	Currency string
}

// Suggestion is a clickable autocomplete entry for the token under the cursor.
type Suggestion struct {
	Insert string // text that replaces the token, e.g. "#snacks" or "@brisk"
	Label  string // what to show
	Hint   string // secondary text, e.g. an account's currency
}

// PreviewOK describes a successfully understood entry.
type PreviewOK struct {
	Kind        string // Expense | Income | Transfer
	Accounts    []string
	Amount      string
	Description string
	Tags        []string
	Date        string
	DateNote    string // "today" when the date was defaulted
}

// DescSuggestion is a past transaction offered as a completion. Accepting it fills the form's
// fields from Fill and selects the amount so it can be typed over.
type DescSuggestion struct {
	Description string
	Detail      string // "-$12.50 · BRISK · #fast-food · 3×"
	Fill        DescFill
}

// DescFill is what accepting a suggestion sets. An empty field leaves the form's value alone.
type DescFill struct {
	Kind      string // expense | income | transfer
	Account   string // slug: the account (from, for a transfer)
	To        string // slug: the other account of a transfer
	Direction string // on a register: "out" or "in" relative to the register's account
	Amount    string
	Tags      string // space-separated, no "#"
}

// EntryAccount is an account choice in the entry form.
type EntryAccount struct {
	Slug     string
	Name     string
	Currency string
	Type     string // asset | liability
}

// EntryForm holds the entry form's field values.
type EntryForm struct {
	Kind        string // expense | income | transfer
	Account     string // slug (unused on a register: the account is the Scope)
	To          string // slug of the other account of a transfer
	Direction   string // register transfers: "out" (money leaves this account) or "in"
	Date        string // YYYY-MM-DD
	Description string
	Amount      string
	Tags        string
}

// QuickAdd is everything the quick-add panel renders.
type QuickAdd struct {
	Form     EntryForm
	Today    string         // YYYY-MM-DD on the server's clock: what the Today button sets
	Accounts []EntryAccount // real, unarchived accounts
	Problems []string       // shown after a rejected submit
	Flash    string         // shown after a successful add
	Recent   []TxnRow
	// NeedsFixing counts transactions with a line in the Imbalance account.
	NeedsFixing int
	// NoAccounts is true on a fresh database: quick-add needs an account to put money in.
	NoAccounts bool
	// DueCount is how many recurring transactions are waiting to be added or skipped.
	DueCount int
	// Scope locks the entry to one account (an account's register); nil on the home page.
	Scope *Scope
	// Return is where to go after a scoped add (the register's own URL, filters included).
	Return string
}

// AccountOption is an entry in the account filter dropdown.
type AccountOption struct {
	Slug string
	Name string
}

// TxnFilterForm echoes the filter inputs back into the form.
type TxnFilterForm struct {
	Q         string
	Account   string // slug
	Tags      string // space-separated, as typed
	From      string
	To        string
	Imbalance bool
	Untagged  bool
}

// TxnList is everything the transactions page renders.
type TxnList struct {
	Form       TxnFilterForm
	Accounts   []AccountOption
	TagOptions []string
	Notices    []string // problems with the filter inputs
	// AccountHeader describes the account being viewed and its current balance ("" when not filtered by one).
	AccountHeader string
	// Entry is the scoped quick-add bar shown on an account's register (nil elsewhere).
	Entry *QuickAdd
	// EntryNote explains why there is no entry bar (e.g. the account is archived).
	EntryNote string
	Flash     string
	Count     int
	// Totals is the net movement of the real accounts over every matching transaction, one entry per
	// currency (empty when no filter is active).
	Totals    []TxnTotal
	Items     []TxnRow
	NextURL   string // "" when there is no next page
	FilterURL string // the canonical URL of this filtered view (where a bulk change returns to)
}

// TxnTotal is one currency's net over the filtered transactions.
type TxnTotal struct {
	Currency  string
	Amount    string // formatted, signed ("-$50.00", "+$20.00")
	Direction string // "out", "in" or "" (zero)
}

// ---- transaction editor ----

// AccountOpt is a choice in a split line's account dropdown.
type AccountOpt struct {
	ID   string
	Name string
}

// EditRow is one split line in the editor form.
type EditRow struct {
	Account    string // account id
	Amount     string // in Currency
	Currency   string
	Value      string // in the transaction currency; only shown when NeedsValue
	Memo       string
	Tags       string // "#a #b"
	Reconciled string
	Builtin    bool   // a built-in category account: its currency is selectable
	NeedsValue bool   // Currency differs from the transaction currency
	Warn       bool   // in the Imbalance account
	Error      string // problem with this line's inputs
}

// Remaining is the live "left to allocate" indicator.
type Remaining struct {
	Text     string // e.g. "$55.00 left to allocate"
	State    string // "ok" | "under" | "over"
	Balanced bool
}

// Editor is everything the transaction editor renders.
type Editor struct {
	ID          int64
	Date        string
	Description string
	Notes       string
	Currency    string // transaction currency (read-only)
	Rows        []EditRow
	Categories  []AccountOpt // built-in accounts
	Accounts    []AccountOpt // real accounts
	Currencies  []string     // choices for category lines
	Remaining   Remaining
	Problems    []string
	Flash       string
	Next        string // where to go after save/delete (a local path)
	Meta        string // "Imported from GnuCash · created …"
	Imbalance   bool   // some line sits in the Imbalance account
	FocusLast   bool   // "+ Add line" was just pressed: focus the new line's first field
}

// ---- balances ----

// BalanceRow is one account on the balances page.
type BalanceRow struct {
	Name     string
	Href     string // filtered transaction list for the account
	Balance  string // in the account's own currency
	USD      string // "≈ $275.00" for non-USD accounts with a known rate
	NoRate   bool
	Zero     bool
	Archived bool
}

// Balances is everything the balances page renders.
type Balances struct {
	NetWorth      string
	NetNegative   bool
	Assets        string
	Owed          string
	AssetRows     []BalanceRow
	LiabilityRows []BalanceRow
	MissingRates  []string
	RateNotes     []string // "MXN rate as of 2026-09-24"
}

// ---- tag report ----

// Choice is a link in a row of options (kind toggle, date presets).
type Choice struct {
	Label  string
	Href   string
	Active bool
}

// TagBar is one row of the tag report.
type TagBar struct {
	Tag    string // "Untagged" for the untagged row
	Href   string // transactions behind the number
	Amount string // USD
	Detail string // other-currency amounts and the item count
	Pct    int    // bar length, relative to the largest row
	Title  string // hover text
}

// TagsPage is everything the tag report renders.
type TagsPage struct {
	Notices    []string
	Kind       string // "expense" | "income"
	KindLabel  string // "spending" | "income"
	Kinds      []Choice
	Ranges     []Choice
	From, To   string // echoed into the custom-range form
	RangeLabel string
	Total      string
	TotalNote  string
	Bars       []TagBar
	Untagged   *TagBar
	Missing    []string
	Empty      bool
}

// ---- tag management ----

// TagManageRow is one tag in the management list.
type TagManageRow struct {
	Name   string
	Href   string // transactions carrying the tag
	Splits int
}

// MergeConfirm asks before combining two tags.
type MergeConfirm struct {
	From, To             string
	FromSplits, ToSplits int
}

// TagManage is everything the tag management page renders.
type TagManage struct {
	Q       string
	Sort    string // "name" | "uses"
	Tags    []TagManageRow
	Total   int // tags before filtering
	Flash   string
	Problem string
	Confirm *MergeConfirm
}

// ---- account management ----

// AccountManageRow is one account on the management page.
type AccountManageRow struct {
	ID       int64
	Name     string
	Slug     string
	Type     string
	Currency string
	Splits   int
	Archived bool
	Href     string // its transactions
}

// AccountForm echoes the "add account" form.
type AccountForm struct {
	Name        string
	Type        string
	Currency    string
	Opening     string
	OpeningDate string
}

// AccountManage is everything the account management page renders.
type AccountManage struct {
	Active   []AccountManageRow
	Archived []AccountManageRow
	Form     AccountForm
	Flash    string
	Problem  string
	// ProblemID is the account whose edit form produced Problem (0 = the add form).
	ProblemID int64
}

// ---- recurring ----

// DueRow is one occurrence waiting to be added or skipped.
type DueRow struct {
	RuleID      int64
	Date        string
	Overdue     bool
	Description string
	Amount      string
	Accounts    []string
	Tags        []string
	Problem     string // the rule's text no longer resolves (e.g. its account was archived)
}

// RuleRow is one recurring rule.
type RuleRow struct {
	ID       int64
	Text     string
	Schedule string // "Monthly on the 1st"
	Next     string // "Next: 2026-11-01", "Paused", "Ended"
	Active   bool
	Preview  *PreviewOK
	Problem  string
	Form     RuleForm // current values for the edit form
}

// RuleForm holds the add/edit form fields.
type RuleForm struct {
	Text   string
	Freq   string
	Every  string
	Anchor string
	End    string
}

// RecurringPage is everything the recurring page renders.
type RecurringPage struct {
	Due       []DueRow
	Rules     []RuleRow
	Form      RuleForm
	Flash     string
	Problem   string
	ProblemID int64 // the rule (or due occurrence's rule) the problem belongs to; 0 = the add form
}

// ---- budgets ----

// BudgetBar is one budget measured against the month.
type BudgetBar struct {
	Tag       string
	Href      string // the transactions behind the spending
	Budget    string // "$300.00"
	Spent     string
	Label     string // "$50.00 left" / "$10.00 over"
	Icon      string // text icon so state never relies on color alone
	State     string // ok | warn | over
	Percent   int    // for the label, may exceed 100
	Fill      int    // bar width, 0-100
	Pace      int    // even-pace marker position, -1 when not shown
	AmountVal string // budget as a plain number for the edit field
}

// BudgetForm echoes the add form.
type BudgetForm struct {
	Tag    string
	Amount string
}

// BudgetsPage is everything the budgets page renders.
type BudgetsPage struct {
	Month         string
	MonthLabel    string
	PrevHref      string
	NextHref      string
	Bars          []BudgetBar
	TotalBudget   string
	TotalSpending string
	PaceNote      string
	Missing       []string
	Flash         string
	Problem       string
	ProblemTag    string // tag whose edit produced Problem ("" = the add form)
	Form          BudgetForm
	TagOptions    []string
}

// BulkConfirm is the "are you sure" page for a bulk tag change.
type BulkConfirm struct {
	Op           string // "add" | "remove" | "move"
	Tag, To      string // normalised, no '#'
	Transactions int
	Lines        int
	IDs          []int64 // the explicit selection (empty when All)
	All          bool    // every transaction matching the filter in Return
	Return       string
}
