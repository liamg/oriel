// Package gh talks to GitHub: the Client the Ship page needs (a branch's PR, opening and editing one, reviewers, ready
// for review, checks and an Actions job's log) over the REST API, through the user's gh (CLI) or a token (HTTP); a
// repo's PR template and CODEOWNERS; and ghtest, a fake GitHub for tests.
package gh
