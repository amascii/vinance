package quickadd

// Past is what the app knows about an earlier transaction: enough to pre-fill a new entry.
type Past struct {
	Description string
	Income      bool     // money came in
	Amount      string   // positive, plain digits as a text field holds them ("45.20")
	Accounts    []string // slugs: [from] for spending, [to] for income, [from, to] for a transfer; empty if unknown
	Tags        []string // kebab-case, no "#"
}
