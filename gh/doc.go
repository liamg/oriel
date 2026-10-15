// Package gh talks to GitHub: the Client the Ship page needs (a branch's PR, opening and editing one, reviewers, ready
// for review, checks and an Actions job's log) and the review mirror (a PR's review comments, reviews and
// conversation, and replying there as the user) over the REST API, through the user's gh (CLI) or a token (HTTP); a
// repo's PR template and CODEOWNERS; what counts as posting to a review (PostsReview, ReviewTool), which agents are
// never allowed; and ghtest, a fake GitHub for tests.
package gh
