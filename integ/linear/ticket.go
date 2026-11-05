package linear

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/registry"
)

// Ticket is one ticket in full: what the launcher's card shows, and what the agent is given.
type Ticket struct {
	Key           string       `json:"key"`
	Title         string       `json:"title"`
	URL           string       `json:"url"`
	Description   string       `json:"description"`
	State         string       `json:"state"`
	StateType     string       `json:"stateType"`
	PriorityLabel string       `json:"priorityLabel,omitempty"`
	Project       string       `json:"project,omitempty"`
	Assignee      string       `json:"assignee,omitempty"`
	Mine          bool         `json:"mine"` // the API key's owner is the assignee
	Comments      []Comment    `json:"comments,omitempty"`
	Attachments   []Attachment `json:"attachments,omitempty"`
	PR            *PR          `json:"pr,omitempty"` // the newest pull request among the attachments

	ID     string  `json:"-"`
	States []State `json:"-"` // the team's workflow
	viewer string  // the API key's owner
}

// Comment is one comment on a ticket, oldest first.
type Comment struct {
	Author string    `json:"author"`
	Body   string    `json:"body"`
	At     time.Time `json:"at"`
}

// Attachment is a link on a ticket: a pull request, a file, a Slack thread.
type Attachment struct {
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle,omitempty"`
	URL      string    `json:"url"`
	At       time.Time `json:"at"`
}

const ticketQuery = `query($id: String!) {
	viewer { id }
	issue(id: $id) { id identifier title url description priorityLabel
		state { name type }
		project { name }
		assignee { id name }
		comments(first: 50) { nodes { body createdAt user { name displayName } } }
		attachments(first: 20) { nodes { title subtitle url createdAt } }
		team { states(first: 50) { nodes { id name type position } } } } }`

// Ticket reads one ticket by key, whoever it's assigned to.
func (c Client) Ticket(ctx context.Context, key string) (Ticket, error) {
	type person struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	}
	var out struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
		Issue *struct {
			ID            string `json:"id"`
			Identifier    string `json:"identifier"`
			Title         string `json:"title"`
			URL           string `json:"url"`
			Description   string `json:"description"`
			PriorityLabel string `json:"priorityLabel"`
			State         struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"state"`
			Project struct {
				Name string `json:"name"`
			} `json:"project"`
			Assignee *person `json:"assignee"`
			Comments struct {
				Nodes []struct {
					Body      string    `json:"body"`
					CreatedAt time.Time `json:"createdAt"`
					User      *person   `json:"user"`
				} `json:"nodes"`
			} `json:"comments"`
			Attachments struct {
				Nodes []struct {
					Title     string    `json:"title"`
					Subtitle  string    `json:"subtitle"`
					URL       string    `json:"url"`
					CreatedAt time.Time `json:"createdAt"`
				} `json:"nodes"`
			} `json:"attachments"`
			Team struct {
				States struct {
					Nodes []State `json:"nodes"`
				} `json:"states"`
			} `json:"team"`
		} `json:"issue"`
	}
	if err := c.Do(ctx, ticketQuery, map[string]any{"id": key}, &out); err != nil {
		return Ticket{}, err
	}
	is := out.Issue
	if is == nil {
		return Ticket{}, fmt.Errorf("linear has no ticket %s", key)
	}
	t := Ticket{Key: is.Identifier, Title: is.Title, URL: is.URL, Description: is.Description, State: is.State.Name,
		StateType: is.State.Type, PriorityLabel: is.PriorityLabel, Project: is.Project.Name, ID: is.ID, States: is.Team.States.Nodes,
		viewer: out.Viewer.ID}
	if is.Assignee != nil {
		t.Assignee, t.Mine = is.Assignee.Name, is.Assignee.ID == out.Viewer.ID
	}
	for _, n := range is.Comments.Nodes {
		who := "someone"
		if n.User != nil {
			who = cmpOr(n.User.DisplayName, n.User.Name)
		}
		t.Comments = append(t.Comments, Comment{Author: who, Body: n.Body, At: n.CreatedAt})
	}
	slices.SortStableFunc(t.Comments, func(a, b Comment) int { return a.At.Compare(b.At) })
	var newest time.Time
	for _, n := range is.Attachments.Nodes {
		t.Attachments = append(t.Attachments, Attachment{Title: n.Title, Subtitle: n.Subtitle, URL: n.URL, At: n.CreatedAt})
		if p := prFromURL(n.URL); p != nil && (t.PR == nil || n.CreatedAt.After(newest)) {
			t.PR, newest = p, n.CreatedAt
		}
	}
	slices.SortStableFunc(t.Attachments, func(a, b Attachment) int { return a.At.Compare(b.At) })
	return t, nil
}

// Summary is the description's first paragraph, as the card shows it.
func (t Ticket) Summary() string {
	d := strings.TrimSpace(t.Description)
	if i := strings.Index(d, "\n\n"); i >= 0 {
		d = d[:i]
	}
	return strings.Join(strings.Fields(d), " ")
}

// Facts are the card's footnotes: its address, attachments, linked PR and comments.
func (t Ticket) Facts() []string {
	addr := strings.TrimPrefix(strings.TrimPrefix(t.URL, "https://"), "http://")
	if i := strings.Index(addr, "/"+t.Key+"/"); i >= 0 {
		addr = addr[:i+1+len(t.Key)] // the slug after the key is its title again
	}
	out := []string{addr}
	if n := len(t.Attachments); n > 0 {
		out = append(out, plural(n, "attachment"))
	}
	if t.PR != nil {
		out = append(out, fmt.Sprintf("linked PR #%d", t.PR.Number))
	}
	if n := len(t.Comments); n > 0 {
		out = append(out, plural(n, "comment"))
	}
	return out
}

// Detail is the ticket as Oriel's card and the agent get it.
func (t Ticket) Detail() registry.Ticket {
	out := registry.Ticket{Key: t.Key, Title: t.Title, URL: t.URL, State: t.State, Stage: stage(t.State, t.StateType),
		Project: t.Project, PriorityLabel: t.PriorityLabel, Summary: t.Summary(), Description: t.Description, Facts: t.Facts(), Context: t.Context()}
	if t.PR != nil {
		out.PR = &registry.TicketPR{Number: t.PR.Number, URL: t.PR.URL, Repo: t.PR.Repo}
	}
	return out
}

// Context is the ticket for the agent: its text, then every comment and attachment, in markdown. The comments are
// people's words, quoted as theirs.
func (t Ticket) Context() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Linear ticket %s: %s\n%s\n", t.Key, t.Title, t.URL)
	var meta []string
	for _, m := range [][2]string{{"State", t.State}, {"Priority", t.PriorityLabel}, {"Project", t.Project}, {"Assignee", t.Assignee}} {
		if m[1] != "" {
			meta = append(meta, m[0]+": "+m[1])
		}
	}
	if len(meta) > 0 {
		b.WriteString(strings.Join(meta, " · ") + "\n")
	}
	if d := strings.TrimSpace(t.Description); d != "" {
		b.WriteString("\n" + d + "\n")
	}
	if len(t.Comments) > 0 {
		fmt.Fprintf(&b, "\n## Comments (%d)\n", len(t.Comments))
		for _, c := range t.Comments {
			fmt.Fprintf(&b, "\n**%s** · %s:\n\n> %s\n", c.Author, c.At.UTC().Format("2006-01-02 15:04"),
				strings.ReplaceAll(strings.TrimSpace(c.Body), "\n", "\n> "))
		}
	}
	if len(t.Attachments) > 0 {
		fmt.Fprintf(&b, "\n## Attachments (%d)\n\n", len(t.Attachments))
		for _, a := range t.Attachments {
			line := "- " + cmpOr(a.Title, a.URL)
			if a.Subtitle != "" {
				line += " (" + a.Subtitle + ")"
			}
			b.WriteString(line + ": " + a.URL + "\n")
		}
	}
	return b.String()
}

// Start moves a ticket into its team's in-progress state, if it hasn't started yet (crit's Start work). It says
// whether it moved, and the state the ticket is in.
func (c Client) Start(ctx context.Context, key string) (moved bool, state string, err error) {
	t, err := c.Ticket(ctx, key)
	if err != nil {
		return false, "", err
	}
	switch t.StateType {
	case "triage", "backlog", "unstarted":
	default:
		return false, t.State, nil
	}
	st := pickState(t.States, "started", "in progress")
	if st == nil {
		return false, t.State, nil
	}
	return c.move(ctx, t, *st)
}

// Review moves a ticket into its team's In Review state once its PR is raised, from any state before it; a team
// with no In Review state, or a ticket already past it, stays where it is.
func (c Client) Review(ctx context.Context, key string) (moved bool, state string, err error) {
	t, err := c.Ticket(ctx, key)
	if err != nil {
		return false, "", err
	}
	st := reviewState(t.States)
	if st == nil || t.State == st.Name {
		return false, t.State, nil
	}
	switch t.StateType {
	case "triage", "backlog", "unstarted":
	case "started":
		for _, cur := range t.States {
			if cur.Name == t.State && cur.Type == "started" && cur.Position > st.Position {
				return false, t.State, nil
			}
		}
	default:
		return false, t.State, nil
	}
	return c.move(ctx, t, *st)
}

// Done moves a ticket into its team's completed state once its PR is merged (crit's done on merge); a ticket already
// completed or canceled stays where it is.
func (c Client) Done(ctx context.Context, key string) (moved bool, state string, err error) {
	t, err := c.Ticket(ctx, key)
	if err != nil {
		return false, "", err
	}
	st := doneState(t.States)
	if st == nil || t.StateType == "completed" || t.StateType == "canceled" {
		return false, t.State, nil
	}
	return c.move(ctx, t, *st)
}

func (c Client) move(ctx context.Context, t Ticket, st State) (bool, string, error) {
	ok, err := c.UpdateIssue(ctx, t.ID, map[string]any{"stateId": st.ID})
	if err != nil {
		return false, t.State, err
	}
	if !ok {
		return false, t.State, fmt.Errorf("linear refused to move %s to %s", t.Key, st.Name)
	}
	return true, st.Name, nil
}

// LinkPR attaches a pull request to its ticket, titled "#12 its title".
func (c Client) LinkPR(ctx context.Context, key, url string, number int, title string) error {
	t, err := c.Ticket(ctx, key)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("Pull request #%d", number)
	if title = strings.TrimSpace(title); title != "" {
		name = fmt.Sprintf("#%d %s", number, title)
	}
	ok, err := c.Attach(ctx, t.ID, url, name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("linear refused to link the PR to %s", t.Key)
	}
	return nil
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
