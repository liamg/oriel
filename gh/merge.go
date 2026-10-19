package gh

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// MergeState is what merging a pull request now would meet.
type MergeState struct {
	Methods    []string // allowed: merge, squash, rebase
	Default    string   // the viewer's last, else the repo's: "" when GitHub doesn't say
	SHA        string   // the head commit
	Draft      bool
	Mergeable  string   // MERGEABLE, CONFLICTING or UNKNOWN
	Status     string   // mergeStateStatus: CLEAN, BEHIND, BLOCKED, DIRTY, UNSTABLE…
	Decision   string   // reviewDecision: APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
	Unresolved int      // review threads not resolved
	Changes    []string // who's asking for changes, still
}

// Merge is how to merge: Method merge, squash or rebase, at SHA (refused if the head has moved on).
type Merge struct {
	Method, SHA, Title, Message string
}

const mergeQuery = `query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed viewerDefaultMergeMethod pullRequest(number: $number) { headRefOid isDraft mergeable mergeStateStatus reviewDecision reviewThreads(first: 100) { nodes { isResolved } } latestReviews(first: 50) { nodes { state author { login } } } } } }`

func (a API) MergeState(ctx context.Context, repo Repo, number int) (MergeState, error) {
	var out struct {
		Data struct {
			Repository struct {
				Merge   bool   `json:"mergeCommitAllowed"`
				Squash  bool   `json:"squashMergeAllowed"`
				Rebase  bool   `json:"rebaseMergeAllowed"`
				Default string `json:"viewerDefaultMergeMethod"`
				PR      struct {
					Head      string `json:"headRefOid"`
					Draft     bool   `json:"isDraft"`
					Mergeable string `json:"mergeable"`
					Status    string `json:"mergeStateStatus"`
					Decision  string `json:"reviewDecision"`
					Threads   struct {
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
					Reviews struct {
						Nodes []struct {
							State  string `json:"state"`
							Author struct {
								Login string `json:"login"`
							} `json:"author"`
						} `json:"nodes"`
					} `json:"latestReviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{"query": mergeQuery, "variables": map[string]any{"owner": repo.Owner, "name": repo.Name, "number": number}}
	if err := a.T.Do(ctx, "POST", "graphql", in, &out); err != nil {
		return MergeState{}, err
	}
	if len(out.Errors) > 0 {
		return MergeState{}, &Error{Status: 422, Message: out.Errors[0].Message}
	}
	r, p := out.Data.Repository, out.Data.Repository.PR
	m := MergeState{Default: strings.ToLower(r.Default), SHA: p.Head, Draft: p.Draft, Mergeable: p.Mergeable, Status: p.Status, Decision: p.Decision}
	for _, x := range []struct {
		ok   bool
		name string
	}{{r.Merge, "merge"}, {r.Squash, "squash"}, {r.Rebase, "rebase"}} {
		if x.ok {
			m.Methods = append(m.Methods, x.name)
		}
	}
	for _, t := range p.Threads.Nodes {
		if !t.IsResolved {
			m.Unresolved++
		}
	}
	for _, rv := range p.Reviews.Nodes {
		if rv.State == ReviewChangesRequested {
			m.Changes = append(m.Changes, rv.Author.Login)
		}
	}
	return m, nil
}

func (a API) MergePR(ctx context.Context, repo Repo, number int, m Merge) error {
	in := map[string]any{"merge_method": m.Method}
	for k, v := range map[string]string{"sha": m.SHA, "commit_title": m.Title, "commit_message": m.Message} {
		if v != "" {
			in[k] = v
		}
	}
	return a.T.Do(ctx, "PUT", repoPath(repo, fmt.Sprintf("pulls/%d/merge", number)), in, nil)
}

func (a API) DeleteBranch(ctx context.Context, repo Repo, branch string) error {
	parts := strings.Split(branch, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return a.T.Do(ctx, "DELETE", repoPath(repo, "git/refs/heads/"+strings.Join(parts, "/")), nil, nil)
}
