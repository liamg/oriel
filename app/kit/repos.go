package kit

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/theme"
)

// TRepoFound is a found repo's chip (RepoFound).
var TRepoFound = theme.Declare("repos.found", "amber")

// RepoFound says whether discovery added r and no session has used it yet.
func RepoFound(r core.Repo) bool { return r.Source == core.RepoDiscovered && r.Used.IsZero() }
