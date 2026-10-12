package views

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNavSection(t *testing.T) {
	cases := map[string]string{
		"/":                          "/",
		"/accounts":                  "/accounts",
		"/accounts/manage":           "/accounts/manage",
		"/accounts/manage/3/archive": "/accounts/manage",
		"/transactions":              "/transactions",
		"/transactions/42":           "/transactions",
		"/tags":                      "/tags",
		"/tags/manage/rename":        "/tags/manage",
		"/recurring/7/accept":        "/recurring",
		"/budgets":                   "/budgets",
		"/quickadd":                  "",
		"/accountsx":                 "",
	}
	for path, want := range cases {
		assert.Equal(t, want, navSection(path), path)
	}
}
