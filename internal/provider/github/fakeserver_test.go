package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// fakeIssue is the subset of the GitHub REST issue shape the fake
// server round-trips.
type fakeIssue struct {
	Number      int              `json:"number"`
	NodeID      string           `json:"node_id"`
	Title       string           `json:"title"`
	Body        string           `json:"body,omitempty"`
	State       string           `json:"state"`
	Labels      []fakeLabel      `json:"labels"`
	HTMLURL     string           `json:"html_url"`
	UpdatedAt   time.Time        `json:"updated_at"`
	PullRequest *fakePullRequest `json:"pull_request,omitempty"`
}

type fakeLabel struct {
	Name string `json:"name"`
}

type fakePullRequest struct {
	URL string `json:"url"`
}

// fakeGitHub is an in-memory emulator of the small slice of the
// GitHub REST API the Provider uses, built on httptest.Server. It
// serves both "/repos/..." (as hit by newWithClient, which points
// directly at the server) and "/api/v3/repos/..." (as hit by New
// with a baseUrl option, since go-github's WithEnterpriseURLs appends
// that prefix).
type fakeGitHub struct {
	mu        sync.Mutex
	owner     string
	repo      string
	issues    map[int]*fakeIssue
	nextID    int
	unauth    bool
	rateLimit bool
	reqCount  map[string]int // path, with any "/api/v3" prefix stripped -> request count
	rawCount  map[string]int // exact request path, prefix included -> request count

	project *fakeProject   // the GitHub Project served over GraphQL; nil = none
	opCount map[string]int // GraphQL operationName -> request count

	srv *httptest.Server
}

// Client returns an *http.Client suitable for talking to the fake
// server (no TLS cert verification needed since it's plain HTTP).
func (f *fakeGitHub) Client() *http.Client { return f.srv.Client() }

// SetRateLimited makes every request fail with 403 and a zero rate
// limit remaining, which go-github surfaces as *gh.RateLimitError.
func (f *fakeGitHub) SetRateLimited(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rateLimit = v
}

func newFakeGitHub(owner, repo string) *fakeGitHub {
	f := &fakeGitHub{
		owner:    owner,
		repo:     repo,
		issues:   make(map[int]*fakeIssue),
		nextID:   1,
		reqCount: make(map[string]int),
		rawCount: make(map[string]int),
		opCount:  make(map[string]int),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeGitHub) Close() { f.srv.Close() }

func (f *fakeGitHub) URL() string { return f.srv.URL }

// RequestCount returns how many requests hit path exactly (ignoring
// query string and any "/api/v3" prefix).
func (f *fakeGitHub) RequestCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqCount[path]
}

// RawRequestCount returns how many requests hit path exactly
// (ignoring query string, but keeping any "/api/v3" prefix as sent).
func (f *fakeGitHub) RawRequestCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rawCount[path]
}

// Seed adds an issue directly, bypassing the HTTP API.
func (f *fakeGitHub) Seed(iss *fakeIssue) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if iss.NodeID == "" {
		iss.NodeID = fmt.Sprintf("I_%d", iss.Number)
	}
	f.issues[iss.Number] = iss
	if iss.Number+1 > f.nextID {
		f.nextID = iss.Number + 1
	}
}

// SetUnauthorized makes every request fail with 401.
func (f *fakeGitHub) SetUnauthorized(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unauth = v
}

var issuesCollectionRe = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues$`)
var issueItemRe = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)$`)
var issueLabelsRe = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)/labels$`)
var issueLabelRe = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)/labels/([^/]+)$`)

func (f *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	// Route on the escaped path, not r.URL.Path: net/http decodes
	// percent-escapes into r.URL.Path (so "kind%2Fbug" there is
	// indistinguishable from a literal "/"), which would silently
	// mis-route a label containing "/", "#" or "?". EscapedPath
	// preserves the on-the-wire encoding, so a single path segment
	// like "kind%2Fbug" stays one segment for the regexes below.
	escapedPath := r.URL.EscapedPath()
	path := strings.TrimPrefix(escapedPath, "/api/v3")

	f.mu.Lock()
	f.reqCount[path]++
	f.rawCount[escapedPath]++
	unauth := f.unauth
	rateLimited := f.rateLimit
	f.mu.Unlock()

	if unauth {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	if rateLimited {
		reset := time.Now().Add(time.Hour).Unix()
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "API rate limit exceeded"})
		return
	}

	switch {
	case escapedPath == "/graphql" || escapedPath == "/api/graphql":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.graphql(w, r)
		return

	case issuesCollectionRe.MatchString(path):
		m := issuesCollectionRe.FindStringSubmatch(path)
		if m[1] != f.owner || m[2] != f.repo {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			f.listIssues(w, r)
		case http.MethodPost:
			f.createIssue(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return

	case issueLabelsRe.MatchString(path):
		m := issueLabelsRe.FindStringSubmatch(path)
		number, _ := strconv.Atoi(m[3])
		if r.Method == http.MethodPost {
			f.addLabels(w, r, number)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return

	case issueLabelRe.MatchString(path):
		m := issueLabelRe.FindStringSubmatch(path)
		number, _ := strconv.Atoi(m[3])
		label, err := url.PathUnescape(m[4])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
			return
		}
		if r.Method == http.MethodDelete {
			f.removeLabel(w, number, label)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return

	case issueItemRe.MatchString(path):
		m := issueItemRe.FindStringSubmatch(path)
		number, _ := strconv.Atoi(m[3])
		switch r.Method {
		case http.MethodGet:
			f.getIssue(w, number)
		case http.MethodPatch:
			f.patchIssue(w, r, number)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}

func (f *fakeGitHub) listIssues(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	state := r.URL.Query().Get("state")
	if state == "" {
		state = "open"
	}
	perPage := 30
	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			perPage = n
		}
	}
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}

	var matched []*fakeIssue
	for _, iss := range f.issues {
		if state == "all" || iss.State == state {
			matched = append(matched, iss)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Number < matched[j].Number })

	start := (page - 1) * perPage
	end := start + perPage
	if start > len(matched) {
		start = len(matched)
	}
	if end > len(matched) {
		end = len(matched)
	}
	pageItems := matched[start:end]

	if end < len(matched) {
		nextURL := fmt.Sprintf("<%s/repos/%s/%s/issues?page=%d&per_page=%d>; rel=\"next\"", f.srv.URL, f.owner, f.repo, page+1, perPage)
		w.Header().Set("Link", nextURL)
	}

	writeJSON(w, http.StatusOK, pageItems)
}

func (f *fakeGitHub) getIssue(w http.ResponseWriter, number int) {
	f.mu.Lock()
	iss, ok := f.issues[number]
	f.mu.Unlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeJSON(w, http.StatusOK, iss)
}

func (f *fakeGitHub) createIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	number := f.nextID
	f.nextID++

	labels := make([]fakeLabel, 0, len(body.Labels))
	for _, l := range body.Labels {
		labels = append(labels, fakeLabel{Name: l})
	}

	iss := &fakeIssue{
		Number:    number,
		NodeID:    fmt.Sprintf("I_%d", number),
		Title:     body.Title,
		Body:      body.Body,
		State:     "open",
		Labels:    labels,
		HTMLURL:   fmt.Sprintf("https://github.example/%s/%s/issues/%d", f.owner, f.repo, number),
		UpdatedAt: time.Now().UTC(),
	}
	f.issues[number] = iss

	writeJSON(w, http.StatusCreated, iss)
}

func (f *fakeGitHub) patchIssue(w http.ResponseWriter, r *http.Request, number int) {
	var body struct {
		Title *string `json:"title"`
		State *string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	iss, ok := f.issues[number]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}

	if body.Title != nil {
		iss.Title = *body.Title
	}
	if body.State != nil {
		iss.State = *body.State
	}
	iss.UpdatedAt = time.Now().UTC()

	writeJSON(w, http.StatusOK, iss)
}

func (f *fakeGitHub) addLabels(w http.ResponseWriter, r *http.Request, number int) {
	var names []string
	if err := json.NewDecoder(r.Body).Decode(&names); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	iss, ok := f.issues[number]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}

	existing := map[string]bool{}
	for _, l := range iss.Labels {
		existing[l.Name] = true
	}
	for _, n := range names {
		if !existing[n] {
			iss.Labels = append(iss.Labels, fakeLabel{Name: n})
			existing[n] = true
		}
	}

	writeJSON(w, http.StatusOK, iss.Labels)
}

func (f *fakeGitHub) removeLabel(w http.ResponseWriter, number int, label string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	iss, ok := f.issues[number]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}

	kept := make([]fakeLabel, 0, len(iss.Labels))
	found := false
	for _, l := range iss.Labels {
		if l.Name == label {
			found = true
			continue
		}
		kept = append(kept, l)
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Label does not exist"})
		return
	}
	iss.Labels = kept

	writeJSON(w, http.StatusOK, kept)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
