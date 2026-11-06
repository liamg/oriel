package linear

import (
	"context"
	"fmt"
	"strings"
)

// Project is a Linear project, as an agent lists them: what to name it by when filing or moving a ticket.
type Project struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Slug   string   `json:"slug"`
	URL    string   `json:"url"`
	Status string   `json:"status"`
	Teams  []string `json:"teams"` // the teams' keys
}

// Projects are the workspace's projects that aren't archived; of a team (its key or name) when team isn't "", and
// with search in their names when it isn't "".
func (c Client) Projects(ctx context.Context, team, search string) ([]Project, error) {
	all, err := c.projects(ctx)
	if err != nil {
		return nil, err
	}
	var out []Project
	for _, p := range all {
		if search != "" && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(strings.TrimSpace(search))) {
			continue
		}
		if team != "" && !p.inTeam(team) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (p Project) inTeam(team string) bool {
	team = strings.TrimSpace(team)
	for _, t := range p.Teams {
		if strings.EqualFold(t, team) {
			return true
		}
	}
	return false
}

func (c Client) projects(ctx context.Context) ([]Project, error) {
	const query = `{ projects(first: 250) { nodes { id name slugId url status { name } teams(first: 20) { nodes { key name } } } } }`
	var out struct {
		Projects struct {
			Nodes []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				SlugID string `json:"slugId"`
				URL    string `json:"url"`
				Status struct {
					Name string `json:"name"`
				} `json:"status"`
				Teams struct {
					Nodes []struct {
						Key  string `json:"key"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"teams"`
			} `json:"nodes"`
		} `json:"projects"`
	}
	if err := c.Do(ctx, query, nil, &out); err != nil {
		return nil, err
	}
	ps := make([]Project, len(out.Projects.Nodes))
	for i, n := range out.Projects.Nodes {
		p := Project{ID: n.ID, Name: n.Name, Slug: n.SlugID, URL: n.URL, Status: n.Status.Name}
		for _, t := range n.Teams.Nodes {
			p.Teams = append(p.Teams, t.Key)
		}
		ps[i] = p
	}
	return ps, nil
}

// project is the id of the project called name, as a person says it (its name, any case; its slug; its URL), with
// its name; "" for "none".
func (c Client) project(ctx context.Context, name string) (id, called string, err error) {
	name = strings.TrimSpace(name)
	switch strings.ToLower(name) {
	case "none", "no project":
		return "", "", nil
	}
	all, err := c.projects(ctx)
	if err != nil {
		return "", "", err
	}
	var names []string
	for _, p := range all {
		if strings.EqualFold(p.Name, name) || strings.EqualFold(p.Slug, name) || p.URL != "" && strings.TrimSuffix(name, "/") == p.URL ||
			p.Slug != "" && strings.HasSuffix(strings.ToLower(name), "-"+strings.ToLower(p.Slug)) {
			return p.ID, p.Name, nil
		}
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return "", "", fmt.Errorf("linear has no projects")
	}
	return "", "", fmt.Errorf("no project %q in Linear: %s", name, strings.Join(names, ", "))
}
