package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// How long a fetched CI status stays fresh before it may be re-fetched.
const ciTTL = 30 * time.Second

// ciEntry is one cached CI status for a project slug + branch.
type ciEntry struct {
	Status string
	URL    string // deep link to the latest workflow, when known
	At     time.Time
}

// ciTarget is one visible session the CI column needs a status for.
type ciTarget struct {
	CWD    string
	Branch string
	Slug   string // "" when not derived yet
}

// ciMsg delivers fetched statuses and newly derived project slugs.
type ciMsg struct {
	slugs   map[string]string  // cwd -> slug ("" = underivable)
	entries map[string]ciEntry // slug@branch -> status
}

// remoteRe extracts host and org/repo from a git remote URL, both
// git@host:org/repo(.git) and https://host/org/repo(.git) forms.
var remoteRe = regexp.MustCompile(`(?:@|://)([^/:]+)[/:]([^/]+/[^/]+?)(?:\.git)?$`)

// ciVCS maps git hosts to CircleCI project-slug prefixes.
var ciVCS = map[string]string{
	"github.com":    "gh",
	"bitbucket.org": "bb",
}

// deriveSlug turns a project directory's origin remote into a CircleCI
// project slug, or "" when there is no usable remote.
func deriveSlug(cwd string) string {
	out, err := exec.Command("git", "-C", cwd, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	m := remoteRe.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return ""
	}
	vcs, ok := ciVCS[m[1]]
	if !ok {
		return ""
	}
	return vcs + "/" + m[2]
}

// fetchCICmd resolves slugs and fetches branch statuses for the targets,
// a few at a time, returning everything as one ciMsg.
func fetchCICmd(token string, targets []ciTarget) tea.Cmd {
	return func() tea.Msg {
		msg := ciMsg{slugs: map[string]string{}, entries: map[string]ciEntry{}}
		for _, t := range targets {
			if t.Slug == "" {
				if s, ok := msg.slugs[t.CWD]; ok {
					t.Slug = s
				} else {
					t.Slug = deriveSlug(t.CWD)
					msg.slugs[t.CWD] = t.Slug
				}
			}
			if t.Slug == "" {
				continue
			}
			key := t.Slug + "@" + t.Branch
			if _, ok := msg.entries[key]; !ok {
				msg.entries[key] = ciEntry{At: time.Now()}
			}
		}

		client := &http.Client{Timeout: 8 * time.Second}
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for key := range msg.entries {
			slug, branch, _ := strings.Cut(key, "@")
			wg.Add(1)
			go func(key, slug, branch string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				status, buildURL := fetchBranchStatus(client, token, slug, branch)
				mu.Lock()
				msg.entries[key] = ciEntry{Status: status, URL: buildURL, At: time.Now()}
				mu.Unlock()
			}(key, slug, branch)
		}
		wg.Wait()
		return msg
	}
}

// fetchBranchStatus returns a short status word for the latest CircleCI
// pipeline on a branch — pass/fail/run/hold/cxl, "-" when the branch has no
// pipelines, or "" on any error (so it is retried after the TTL) — plus a
// deep link to the pipeline's latest workflow, when there is one.
func fetchBranchStatus(client *http.Client, token, slug, branch string) (string, string) {
	var pipelines struct {
		Items []struct {
			ID     string `json:"id"`
			Number int    `json:"number"`
		} `json:"items"`
	}
	u := fmt.Sprintf("https://circleci.com/api/v2/project/%s/pipeline?branch=%s",
		slug, url.QueryEscape(branch))
	if err := ciGet(client, token, u, &pipelines); err != nil {
		return "", ""
	}
	if len(pipelines.Items) == 0 {
		return "-", ""
	}
	var workflows struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	u = fmt.Sprintf("https://circleci.com/api/v2/pipeline/%s/workflow", pipelines.Items[0].ID)
	if err := ciGet(client, token, u, &workflows); err != nil {
		return "", ""
	}
	statuses := make([]string, 0, len(workflows.Items))
	buildURL := ""
	for _, w := range workflows.Items {
		statuses = append(statuses, w.Status)
	}
	if len(workflows.Items) > 0 {
		buildURL = fmt.Sprintf("https://app.circleci.com/pipelines/%s/%d/workflows/%s",
			slug, pipelines.Items[0].Number, workflows.Items[0].ID)
	}
	return summarizeWorkflows(statuses), buildURL
}

// ciBuildURL returns the browser URL for a session's latest CI build: the
// cached workflow deep link when available, else the branch's pipelines
// page, or "" when the session's project has no known CircleCI slug.
func (m model) ciBuildURL(s Session) string {
	slug := m.ciSlugs[s.CWD]
	if slug == "" || s.Branch == "" || s.Branch == "HEAD" {
		return ""
	}
	if e := m.ci[slug+"@"+s.Branch]; e.URL != "" {
		return e.URL
	}
	return fmt.Sprintf("https://app.circleci.com/pipelines/%s?branch=%s",
		slug, url.QueryEscape(s.Branch))
}

// summarizeWorkflows folds a pipeline's workflow statuses into one word.
func summarizeWorkflows(statuses []string) string {
	if len(statuses) == 0 {
		return "-"
	}
	seen := map[string]bool{}
	for _, s := range statuses {
		seen[s] = true
	}
	switch {
	case seen["failed"] || seen["failing"] || seen["error"]:
		return "fail"
	case seen["running"]:
		return "run"
	case seen["on_hold"]:
		return "hold"
	case seen["canceled"]:
		return "cxl"
	case seen["success"]:
		return "pass"
	}
	return trunc(statuses[0], 4)
}

func ciGet(client *http.Client, token, u string, into any) error {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Circle-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("circleci: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
