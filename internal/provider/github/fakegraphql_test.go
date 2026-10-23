package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// fakeProject is the GitHub Project (v2) the fake server serves over
// GraphQL: the operations project.go sends (yiProject, yiProjectItems,
// yiAddItem, yiSetStatus), nothing else.
type fakeProject struct {
	login       string
	number      int
	noStatus    bool     // the project has no Status field
	options     []string // Status option names; option id = "OPT_" + name
	items       []*fakeItem
	pageSize    int // items per yiProjectItems page
	nextItemNum int
}

type fakeItem struct {
	id     string
	number int    // issue number; 0 = a draft issue
	repo   string // "owner/repo" of the issue
	status string // option name, "" = unset
}

// SetProject makes the server host project login/number with a Status
// field holding options.
func (f *fakeGitHub) SetProject(login string, number int, options ...string) *fakeProject {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.project = &fakeProject{login: login, number: number, options: options, pageSize: 100}
	return f.project
}

// AddItem puts issue number of repo on the project with status.
func (f *fakeGitHub) AddItem(number int, repo, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.project.addItem(number, repo).status = status
}

// ItemStatus returns the Status of this repo's issue number on the
// project, and whether it's on the project at all.
func (f *fakeGitHub) ItemStatus(number int) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.project.items {
		if it.number == number && it.repo == f.owner+"/"+f.repo {
			return it.status, true
		}
	}
	return "", false
}

// OpCount returns how many GraphQL requests named op the server saw.
func (f *fakeGitHub) OpCount(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opCount[op]
}

func (p *fakeProject) addItem(number int, repo string) *fakeItem {
	for _, it := range p.items {
		if it.number == number && it.repo == repo && number != 0 {
			return it
		}
	}
	p.nextItemNum++
	it := &fakeItem{id: fmt.Sprintf("PVTI_%d", p.nextItemNum), number: number, repo: repo}
	p.items = append(p.items, it)
	return it
}

func (p *fakeProject) id() string { return fmt.Sprintf("PVT_%s_%d", p.login, p.number) }

func (f *fakeGitHub) graphql(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.opCount[req.OperationName]++

	str := func(k string) string { s, _ := req.Variables[k].(string); return s }
	data := func(v any) { writeJSON(w, http.StatusOK, map[string]any{"data": v}) }
	gqlErr := func(msg string) {
		writeJSON(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]string{{"message": msg}}})
	}
	p := f.project

	switch req.OperationName {
	case "yiProject":
		login := str("login")
		number, _ := req.Variables["number"].(float64)
		if p == nil || !strings.EqualFold(login, p.login) {
			if strings.EqualFold(login, f.owner) || (p != nil && strings.EqualFold(login, p.login)) {
				gqlErr(fmt.Sprintf("Could not resolve to a ProjectV2 with the number %d.", int(number)))
				return
			}
			data(map[string]any{"repositoryOwner": nil})
			return
		}
		if int(number) != p.number {
			gqlErr(fmt.Sprintf("Could not resolve to a ProjectV2 with the number %d.", int(number)))
			return
		}
		var field any
		if !p.noStatus {
			opts := make([]map[string]string, 0, len(p.options))
			for _, o := range p.options {
				opts = append(opts, map[string]string{"id": "OPT_" + o, "name": o})
			}
			field = map[string]any{"id": "FIELD_status", "options": opts}
		}
		data(map[string]any{"repositoryOwner": map[string]any{"projectV2": map[string]any{"id": p.id(), "field": field}}})

	case "yiProjectItems":
		if p == nil || str("id") != p.id() {
			gqlErr("Could not resolve to a node with the global id of '" + str("id") + "'")
			return
		}
		start := 0
		if after := str("after"); after != "" {
			start, _ = strconv.Atoi(after)
		}
		end := min(start+p.pageSize, len(p.items))
		nodes := []map[string]any{}
		for _, it := range p.items[start:end] {
			n := map[string]any{"id": it.id, "content": map[string]any{}, "fieldValueByName": nil}
			if it.number != 0 {
				n["content"] = map[string]any{"number": it.number, "repository": map[string]any{"nameWithOwner": it.repo}}
			}
			if it.status != "" {
				n["fieldValueByName"] = map[string]any{"name": it.status}
			}
			nodes = append(nodes, n)
		}
		data(map[string]any{"node": map[string]any{"items": map[string]any{
			"nodes":    nodes,
			"pageInfo": map[string]any{"hasNextPage": end < len(p.items), "endCursor": strconv.Itoa(end)},
		}}})

	case "yiAddItem":
		if p == nil || str("project") != p.id() {
			gqlErr("Could not resolve to a node with the global id of '" + str("project") + "'")
			return
		}
		number, err := strconv.Atoi(strings.TrimPrefix(str("content"), "I_"))
		if err != nil || f.issues[number] == nil {
			gqlErr("Could not resolve to a node with the global id of '" + str("content") + "'")
			return
		}
		it := p.addItem(number, f.owner+"/"+f.repo)
		data(map[string]any{"addProjectV2ItemById": map[string]any{"item": map[string]string{"id": it.id}}})

	case "yiSetStatus":
		if p == nil || str("project") != p.id() || str("field") != "FIELD_status" {
			gqlErr("bad project or field")
			return
		}
		name := strings.TrimPrefix(str("option"), "OPT_")
		for _, it := range p.items {
			if it.id == str("item") {
				it.status = name
				data(map[string]any{"updateProjectV2ItemFieldValue": map[string]any{"projectV2Item": map[string]string{"id": it.id}}})
				return
			}
		}
		gqlErr("Could not resolve to a node with the global id of '" + str("item") + "'")

	default:
		gqlErr("fake: unknown operation " + req.OperationName)
	}
}
