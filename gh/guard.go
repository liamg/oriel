package gh

import (
	"regexp"
	"slices"
	"strings"
)

// An agent never posts to a pull request's review or conversation: it drafts, and the user forwards. PostsReview and
// ReviewTool are how the guards around an agent recognise an attempt, whatever the shell or the MCP server it comes
// through.

// reviewTools are the MCP tools, on any server, that write to a PR's review or conversation (GitHub's own server
// names them so).
var reviewTools = []string{
	"add_comment_to_pending_review", "add_issue_comment", "add_pull_request_review_comment",
	"add_pull_request_review_comment_to_pending_review", "add_reply_to_pull_request_comment",
	"create_and_submit_pull_request_review", "create_pending_pull_request_review", "create_pull_request_review",
	"pull_request_review_write", "reply_to_pull_request_review_comment", "resolve_review_thread",
	"submit_pending_pull_request_review", "update_issue_comment", "update_pull_request_comment",
}

// ReviewTool reports whether an MCP tool, by its full name on any server (mcp__github__add_issue_comment,
// github__add_issue_comment), writes to a pull request's review or conversation.
func ReviewTool(name string) bool {
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	return slices.Contains(reviewTools, strings.ToLower(name))
}

var (
	reviewPath     = regexp.MustCompile(`(^|/)(pulls|issues)/[^/]+/(comments|reviews)(/|$)|(^|/)(pulls|issues)/comments/|/replies$`)
	reviewMutation = regexp.MustCompile(`\b(addPullRequestReview\w*|submitPullRequestReview|addComment|updateIssueComment|updatePullRequestReview\w*|resolveReviewThread|deletePullRequestReview\w*)\b`)
)

// ghValueFlags are gh api's flags that take a value.
var ghValueFlags = []string{"-X", "--method", "-f", "--raw-field", "-F", "--field", "--input", "-H", "--header", "-q", "--jq",
	"-t", "--template", "--hostname", "--cache", "-p", "--preview"}

// PostsReview reports whether a shell command posts to a pull request's review or conversation through gh: `gh pr
// review`, `gh pr comment`, `gh issue comment`, closing with --comment, or `gh api` writing to a PR's comments,
// reviews or replies (or a GraphQL mutation that does). A script run by sh -c or eval is looked at too.
func PostsReview(command string) bool {
	for _, seg := range segments(command) {
		for i, w := range seg {
			if (i > 0 && seg[i-1] == "-c" || seg[0] == "eval") && PostsReview(w) {
				return true
			}
		}
		i := slices.IndexFunc(seg, func(w string) bool { return w == "gh" || strings.HasSuffix(w, "/gh") })
		if i < 0 || i+1 >= len(seg) {
			continue
		}
		if ghPosts(seg[i+1:]) {
			return true
		}
	}
	return false
}

func ghPosts(args []string) bool {
	sub := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch sub(0) {
	case "pr", "issue":
		switch sub(1) {
		case "comment", "review":
			return true
		case "close", "reopen", "merge":
			return slices.ContainsFunc(args[2:], func(a string) bool {
				return a == "-c" || a == "--comment" || strings.HasPrefix(a, "--comment=")
			})
		}
	case "api":
		return apiPosts(args[1:])
	}
	return false
}

func apiPosts(args []string) bool {
	method, path, fields, mutation := "", "", false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, inline := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "-") {
			if path == "" {
				path = a
			}
			continue
		}
		if strings.HasPrefix(a, "-X") && len(a) > 2 {
			name, val, inline = "-X", a[2:], true
		}
		if !slices.Contains(ghValueFlags, name) {
			continue
		}
		if !inline && i+1 < len(args) {
			i++
			val = args[i]
		}
		switch name {
		case "-X", "--method":
			method = strings.ToUpper(val)
		case "-f", "--raw-field", "-F", "--field", "--input":
			fields = true
			mutation = mutation || reviewMutation.MatchString(val)
		}
	}
	if method == "" {
		method = "GET"
		if fields {
			method = "POST"
		}
	}
	path = strings.TrimPrefix(strings.SplitN(path, "?", 2)[0], "/")
	if path == "graphql" {
		return mutation
	}
	return method != "GET" && method != "HEAD" && reviewPath.MatchString(path)
}

// segments splits a shell command into its simple commands' words, quotes taken off: at ; & | newlines,
// parentheses and backticks outside quotes.
func segments(cmd string) [][]string {
	var out [][]string
	var seg []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			seg = append(seg, word.String())
			word.Reset()
			inWord = false
		}
	}
	endSeg := func() {
		endWord()
		if len(seg) > 0 {
			out = append(out, seg)
			seg = nil
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '\'':
			inWord = true
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				j = len(cmd) - i - 1
			}
			word.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			inWord = true
			for i++; i < len(cmd) && cmd[i] != '"'; i++ {
				if cmd[i] == '\\' && i+1 < len(cmd) {
					i++
				}
				word.WriteByte(cmd[i])
			}
		case c == '\\' && i+1 < len(cmd):
			inWord = true
			i++
			word.WriteByte(cmd[i])
		case c == ' ' || c == '\t':
			endWord()
		case strings.IndexByte(";&|\n()`", c) >= 0 || c == '$' && i+1 < len(cmd) && cmd[i+1] == '(':
			endSeg()
		default:
			inWord = true
			word.WriteByte(c)
		}
	}
	endSeg()
	return out
}
