package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/HayKor/yi/internal/domain"
)

// statusFieldName is the project field yi syncs board columns with.
const statusFieldName = "Status"

// project is the GitHub Project (v2) a Provider keeps issue status in:
// its Status field and that field's options, resolved once by
// loadProject, plus the repository's items on it, loaded lazily.
type project struct {
	ref     string // "owner/number", for messages
	id      string
	fieldID string
	options map[string]projectOption // statusKey(name) -> option

	// items maps an issue number of this repository to its item on
	// the project; nil until loadItems runs.
	items map[int]projectItem
}

type projectOption struct{ id, name string }

type projectItem struct {
	id     string
	status string // the Status option name; "" if unset
}

// statusKey is how option names are compared (domain.SameStatusName).
func statusKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// parseProjectRef parses the "project" option: a project number, owned
// by the repository's owner, or "owner/number".
func parseProjectRef(value, repoOwner string) (owner string, number int, err error) {
	owner, num, found := strings.Cut(value, "/")
	if !found {
		owner, num = repoOwner, value
	}
	number, convErr := strconv.Atoi(num)
	if owner == "" || convErr != nil || number <= 0 || strings.Contains(num, "/") {
		return "", 0, fmt.Errorf("github: project must be a project number or owner/number, got %q", value)
	}
	return owner, number, nil
}

// graphqlURL returns the GraphQL endpoint next to a REST base URL:
// "<base>graphql" for api.github.com, "/api/graphql" for an Enterprise
// "/api/v3/" base.
func graphqlURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(u.Path, "/api/v3/") {
		u.Path = strings.TrimSuffix(u.Path, "v3/") + "graphql"
	} else {
		u.Path += "graphql"
	}
	return u.String(), nil
}

type graphqlRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// graphql runs one GraphQL operation (named name in query) through the
// REST client, so it shares its auth, rate-limit and HTTP error
// handling, and decodes the response's data into out. GraphQL errors
// (reported with a 200) become an error naming op.
func (p *Provider) graphql(ctx context.Context, op, name, query string, vars map[string]any, out any) error {
	endpoint, err := graphqlURL(p.client.BaseURL())
	if err != nil {
		return fmt.Errorf("github: %s: %w", op, err)
	}
	req, err := p.client.NewRequest(ctx, "POST", endpoint, graphqlRequest{Query: query, OperationName: name, Variables: vars})
	if err != nil {
		return fmt.Errorf("github: %s: %w", op, err)
	}
	var resp graphqlResponse
	r, err := p.client.Do(req, &resp)
	if err != nil {
		return mapErr(op, r, err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, len(resp.Errors))
		for i, e := range resp.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("github: %s: %s", op, strings.Join(msgs, "; "))
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return fmt.Errorf("github: %s: decode response: %w", op, err)
	}
	return nil
}

const projectQuery = `query yiProject($login: String!, $number: Int!) {
  repositoryOwner(login: $login) {
    ... on ProjectV2Owner {
      projectV2(number: $number) {
        id
        field(name: "` + statusFieldName + `") {
          ... on ProjectV2SingleSelectField { id options { id name } }
        }
      }
    }
  }
}`

// loadProject resolves the project (owner, number): its id, its Status
// field and options. Every status must map to an option (by
// ProjectStatus); extra options are fine.
func (p *Provider) loadProject(ctx context.Context, owner string, number int, statuses []domain.Status) error {
	ref := fmt.Sprintf("%s/%d", owner, number)
	op := "load project " + ref
	var data struct {
		RepositoryOwner *struct {
			ProjectV2 *struct {
				ID    string `json:"id"`
				Field *struct {
					ID      string `json:"id"`
					Options []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"options"`
				} `json:"field"`
			} `json:"projectV2"`
		} `json:"repositoryOwner"`
	}
	if err := p.graphql(ctx, op, "yiProject", projectQuery, map[string]any{"login": owner, "number": number}, &data); err != nil {
		return fmt.Errorf("%w (check \"github.project\" in .issue-sync.yml; the token needs the project scope: gh auth refresh -s project)", err)
	}
	if data.RepositoryOwner == nil {
		return fmt.Errorf("github: project %s: no user or organization %q", ref, owner)
	}
	proj := data.RepositoryOwner.ProjectV2
	if proj == nil {
		return fmt.Errorf("github: project %s not found", ref)
	}
	if proj.Field == nil || proj.Field.ID == "" {
		return fmt.Errorf("github: project %s has no single-select field %q", ref, statusFieldName)
	}

	pr := &project{ref: ref, id: proj.ID, fieldID: proj.Field.ID, options: map[string]projectOption{}}
	var names []string
	for _, o := range proj.Field.Options {
		pr.options[statusKey(o.Name)] = projectOption{id: o.ID, name: o.Name}
		names = append(names, o.Name)
	}

	var missing []string
	for _, s := range statuses {
		if _, ok := pr.options[statusKey(s.ProjectStatus)]; !ok {
			missing = append(missing, fmt.Sprintf("statuses.%s (%q)", s.Key, s.ProjectStatus))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("github: project %s has no %s option for %s; its options: %s (set projectStatus in .issue-sync.yml to one of them)",
			ref, statusFieldName, strings.Join(missing, ", "), strings.Join(names, ", "))
	}

	p.project = pr
	return nil
}

const projectItemsQuery = `query yiProjectItems($id: ID!, $after: String) {
  node(id: $id) {
    ... on ProjectV2 {
      items(first: 100, after: $after) {
        nodes {
          id
          content { ... on Issue { number repository { nameWithOwner } } }
          fieldValueByName(name: "` + statusFieldName + `") {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

// loadItems fetches, once per Provider, the project's items that are
// issues of this repository.
func (p *Provider) loadItems(ctx context.Context) error {
	if p.project.items != nil {
		return nil
	}
	repo := p.owner + "/" + p.repo
	items := map[int]projectItem{}
	vars := map[string]any{"id": p.project.id, "after": nil}
	for {
		var data struct {
			Node struct {
				Items struct {
					Nodes []struct {
						ID      string `json:"id"`
						Content struct {
							Number     int `json:"number"`
							Repository struct {
								NameWithOwner string `json:"nameWithOwner"`
							} `json:"repository"`
						} `json:"content"`
						FieldValue *struct {
							Name string `json:"name"`
						} `json:"fieldValueByName"`
					} `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"items"`
			} `json:"node"`
		}
		if err := p.graphql(ctx, "list project items of "+p.project.ref, "yiProjectItems", projectItemsQuery, vars, &data); err != nil {
			return err
		}
		for _, n := range data.Node.Items.Nodes {
			// Draft issues and pull requests have no issue content.
			if n.Content.Number == 0 || !strings.EqualFold(n.Content.Repository.NameWithOwner, repo) {
				continue
			}
			it := projectItem{id: n.ID}
			if n.FieldValue != nil {
				it.status = n.FieldValue.Name
			}
			items[n.Content.Number] = it
		}
		if !data.Node.Items.PageInfo.HasNextPage {
			break
		}
		vars["after"] = data.Node.Items.PageInfo.EndCursor
	}
	p.project.items = items
	return nil
}

// withStatus fills in iss's status field from the project, if the
// Provider has one.
func (p *Provider) withStatus(ctx context.Context, iss domain.Issue) (domain.Issue, error) {
	if p.project == nil {
		return iss, nil
	}
	if err := p.loadItems(ctx); err != nil {
		return domain.Issue{}, err
	}
	iss.HasStatusField = true
	iss.Status = p.project.items[iss.ID].status
	return iss, nil
}

const addItemMutation = `mutation yiAddItem($project: ID!, $content: ID!) {
  addProjectV2ItemById(input: {projectId: $project, contentId: $content}) { item { id } }
}`

const setStatusMutation = `mutation yiSetStatus($project: ID!, $item: ID!, $field: ID!, $option: String!) {
  updateProjectV2ItemFieldValue(input: {projectId: $project, itemId: $item, fieldId: $field, value: {singleSelectOptionId: $option}}) {
    projectV2Item { id }
  }
}`

// setStatus sets issue number's Status on the project to the option
// named name, first adding the issue (by its GraphQL node id) to the
// project if it isn't known to be there. It returns the option's own
// spelling of the name.
func (p *Provider) setStatus(ctx context.Context, number int, nodeID, name string) (string, error) {
	op := fmt.Sprintf("set status of #%d on project %s", number, p.project.ref)
	opt, ok := p.project.options[statusKey(name)]
	if !ok {
		names := make([]string, 0, len(p.project.options))
		for _, o := range p.project.options {
			names = append(names, o.name)
		}
		sort.Strings(names)
		return "", fmt.Errorf("github: %s: no %s option %q (options: %s)", op, statusFieldName, name, strings.Join(names, ", "))
	}

	item, known := p.project.items[number]
	if !known {
		// Adding an issue that's already on the project is a no-op
		// returning its item, so this is safe whether or not items
		// were loaded.
		var data struct {
			Add struct {
				Item struct {
					ID string `json:"id"`
				} `json:"item"`
			} `json:"addProjectV2ItemById"`
		}
		if err := p.graphql(ctx, op, "yiAddItem", addItemMutation, map[string]any{"project": p.project.id, "content": nodeID}, &data); err != nil {
			return "", err
		}
		item = projectItem{id: data.Add.Item.ID}
	}

	var data json.RawMessage
	vars := map[string]any{"project": p.project.id, "item": item.id, "field": p.project.fieldID, "option": opt.id}
	if err := p.graphql(ctx, op, "yiSetStatus", setStatusMutation, vars, &data); err != nil {
		return "", err
	}

	if p.project.items != nil {
		item.status = opt.name
		p.project.items[number] = item
	}
	return opt.name, nil
}
