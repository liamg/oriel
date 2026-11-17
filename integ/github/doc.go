// Package github talks to GitHub (its importers name it gh): the Client the Ship page needs (a branch's PR, opening and editing one, reviewers, ready
// for review, checks and an Actions job's log) and the review mirror (a PR's review comments, reviews and
// conversation, and replying there as the user) over the REST API, through the user's gh (CLI) or a token (HTTP); a
// repo's PR template and CODEOWNERS; and ghtest, a fake GitHub for tests. Unlike integ's other code hosts it isn't a
// process plugin: the built-in GitHub code host (app/github) uses it in process.
package github
