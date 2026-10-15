// Package linear is the Linear plugin's process: the viewer's assigned tickets, one ticket in detail, the moves and
// links made on them, and the workspace's recent activity, over Linear's GraphQL API (crit-reference.md → Linear).
// It never holds the API key: each request goes through the host's http.request, which adds the key only for
// api.linear.app (security.md).
package linear

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Endpoint is Linear's GraphQL API.
const Endpoint = "https://api.linear.app/graphql"

// Secret is the plugin's secret: a Linear personal API key, sent as the Authorization header as it is.
const Secret = "LINEAR_TOKEN"

// Post sends a GraphQL request's body to Endpoint with the key, and gives back the response's status and body.
type Post func(ctx context.Context, body []byte) (status int, resp []byte, err error)

// Client talks to Linear as the owner of the API key.
type Client struct {
	Post Post
}

// Do runs one request and unmarshals the response's data into out (which may be nil).
func (c Client) Do(ctx context.Context, query string, vars map[string]any, out any) error {
	in := map[string]any{"query": query}
	if len(vars) > 0 {
		in["variables"] = vars
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	status, resp, err := c.Post(ctx, body)
	if err != nil {
		return err
	}
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		if status == 401 {
			return errors.New("linear: the API key was refused")
		}
		return fmt.Errorf("linear: %d: %w", status, err)
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("linear: %s", env.Errors[0].Message)
	}
	if status >= 400 {
		return fmt.Errorf("linear: %d", status)
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// UpdateIssue applies an IssueUpdateInput to one issue. ok is Linear's own success flag: false means it refused.
func (c Client) UpdateIssue(ctx context.Context, id string, input map[string]any) (ok bool, err error) {
	const mutation = `mutation($id: String!, $input: IssueUpdateInput!) {
		issueUpdate(id: $id, input: $input) { success } }`
	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.Do(ctx, mutation, map[string]any{"id": id, "input": input}, &out); err != nil {
		return false, err
	}
	return out.IssueUpdate.Success, nil
}

// Attach links a URL to an issue. Linear keys attachments by URL per issue, so the same URL sent twice updates the
// one attachment.
func (c Client) Attach(ctx context.Context, issueID, url, title string) (ok bool, err error) {
	const mutation = `mutation($input: AttachmentCreateInput!) {
		attachmentCreate(input: $input) { success } }`
	var out struct {
		AttachmentCreate struct {
			Success bool `json:"success"`
		} `json:"attachmentCreate"`
	}
	vars := map[string]any{"input": map[string]any{"issueId": issueID, "url": url, "title": title}}
	if err := c.Do(ctx, mutation, vars, &out); err != nil {
		return false, err
	}
	return out.AttachmentCreate.Success, nil
}
