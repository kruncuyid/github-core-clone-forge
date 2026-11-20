package main

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type userJSON struct {
	Login    string `json:"login"`
	ID       int    `json:"id"`
	NodeID   string `json:"node_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Type     string `json:"type"`
	HTMLURL  string `json:"html_url"`
	AvatarURL string `json:"avatar_url"`
}

type repoJSON struct {
	ID          int       `json:"id"`
	NodeID      string    `json:"node_id"`
	Name        string    `json:"name"`
	FullName    string    `json:"full_name"`
	Private     bool      `json:"private"`
	Owner       userJSON  `json:"owner"`
	Description string    `json:"description"`
	DefaultBranch string  `json:"default_branch"`
	HasIssues   bool      `json:"has_issues"`
	Archived    bool      `json:"archived"`
	Disabled    bool      `json:"disabled"`
	HTMLURL     string    `json:"html_url"`
	Size        int       `json:"size"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
	PushedAt    string    `json:"pushed_at"`
	CloneURL    string    `json:"clone_url"`
	URL         string    `json:"url"`
}

type labelJSON struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type issueJSON struct {
	ID          int         `json:"id"`
	NodeID      string      `json:"node_id"`
	Number      int         `json:"number"`
	Title       string      `json:"title"`
	Body        string      `json:"body"`
	State       string      `json:"state"`
	HTMLURL     string      `json:"html_url"`
	Labels      []labelJSON `json:"labels"`
	User        userJSON    `json:"user"`
	Comments    int         `json:"comments"`
	CreatedAt   string      `json:"created_at"`
	UpdatedAt   string      `json:"updated_at"`
	CommentsURL string      `json:"comments_url"`
}

type sideJSON struct {
	Ref   string   `json:"ref"`
	SHA   string   `json:"sha"`
	Label string   `json:"label"`
	Repo  repoJSON `json:"repo"`
}

type pullJSON struct {
	ID            int       `json:"id"`
	NodeID        string    `json:"node_id"`
	Number        int       `json:"number"`
	State         string    `json:"state"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	HTMLURL       string    `json:"html_url"`
	Head          sideJSON  `json:"head"`
	Base          sideJSON  `json:"base"`
	User          userJSON  `json:"user"`
	Merged        bool      `json:"merged"`
	Mergeable     bool      `json:"mergeable"`
	MergedAt      *string   `json:"merged_at"`
	MergeCommitSHA *string  `json:"merge_commit_sha"`
	Comments      int       `json:"comments"`
	CreatedAt     string    `json:"created_at"`
	UpdatedAt     string    `json:"updated_at"`
}

type personJSON struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type commitJSON struct {
	SHA     string `json:"sha"`
	NodeID  string `json:"node_id"`
	Commit  struct {
		Author    personJSON `json:"author"`
		Committer personJSON `json:"committer"`
		Message   string     `json:"message"`
		Tree      struct{ SHA string `json:"sha"` } `json:"tree"`
	} `json:"commit"`
	Parents []struct{ SHA string `json:"sha"` } `json:"parents"`
	HTMLURL string `json:"html_url"`
	URL     string `json:"url"`
}

type contentFileJSON struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Size     int    `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	Mode     string `json:"mode"`
	URL      string `json:"url"`
}

type contentDirJSON struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	SHA     string `json:"sha"`
	Size    int    `json:"size"`
	URL     string `json:"url"`
	GitURL  string `json:"git_url"`
	HTMLURL string `json:"html_url"`
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
var authorRE = regexp.MustCompile(`^author (.+) <(.+)> (\d+) [+-]\d{4}$`)

func errResp(status int, message string) Resp {
	return jresp(status, map[string]any{"message": message}, nil)
}

func makeUserShape() userJSON {
	return userJSON{
		Login: st.viewer, ID: 1000, NodeID: nodeID("User", 1000), Name: st.viewerName,
		Email: st.viewerEmail, Type: "User", HTMLURL: "https://github.com/" + st.viewer, AvatarURL: "",
	}
}

func makeRepoShape(repo *Repo) repoJSON {
	full := repo.Owner + "/" + repo.Name
	return repoJSON{
		ID: repo.DatabaseID, NodeID: repo.NodeID, Name: repo.Name, FullName: full,
		Private: repo.Private, Owner: makeUserShape(), Description: repo.Description,
		DefaultBranch: repo.DefaultBranch, HasIssues: true, Archived: false, Disabled: false,
		HTMLURL: "https://github.com/" + full, Size: 0,
		CreatedAt: repo.CreatedAt, UpdatedAt: repo.UpdatedAt, PushedAt: repo.PushedAt,
		CloneURL: "https://github.com/" + full + ".git", URL: "/api/v3/repos/" + full,
	}
}

func makeIssueShape(repo *Repo, issue *Issue) issueJSON {
	labels := make([]labelJSON, 0, len(issue.Labels))
	for _, name := range issue.Labels { labels = append(labels, labelJSON{Name: name, Color: "ededed"}) }
	full := repo.Owner + "/" + repo.Name
	return issueJSON{
		ID: issue.Number, NodeID: issue.ID, Number: issue.Number, Title: issue.Title, Body: issue.Body,
		State: issue.State, HTMLURL: "https://github.com/" + full + "/issues/" + strconv.Itoa(issue.Number),
		Labels: labels, User: makeUserShape(), Comments: len(issue.Comments),
		CreatedAt: issue.CreatedAt, UpdatedAt: issue.UpdatedAt,
		CommentsURL: "/api/v3/repos/" + full + "/issues/" + strconv.Itoa(issue.Number) + "/comments",
	}
}

func makePullShape(repo *Repo, pull *Pull) pullJSON {
	full := repo.Owner + "/" + repo.Name
	repoShape := makeRepoShape(repo)
	head := sideJSON{Ref: pull.Head, SHA: pull.HeadSha, Label: repo.Owner + ":" + pull.Head, Repo: repoShape}
	base := sideJSON{Ref: pull.Base, SHA: pull.BaseSha, Label: repo.Owner + ":" + pull.Base, Repo: repoShape}
	return pullJSON{
		ID: pull.Number + 50000, NodeID: pull.ID, Number: pull.Number, State: pull.State,
		Title: pull.Title, Body: pull.Body, HTMLURL: "https://github.com/" + full + "/pull/" + strconv.Itoa(pull.Number),
		Head: head, Base: base, User: makeUserShape(), Merged: pull.Merged, Mergeable: true,
		MergedAt: pull.MergedAt, MergeCommitSHA: pull.MergeCommitSha, Comments: 0,
		CreatedAt: pull.CreatedAt, UpdatedAt: pull.UpdatedAt,
	}
}

func makeCommitShape(repo *Repo, sha string) any {
	info := commitInfo(repo, sha)
	if info == nil { return nil }
	authorLine := ""
	for _, line := range strings.Split(info.Raw, "\n") {
		if strings.HasPrefix(line, "author ") { authorLine = line; break }
	}
	m := authorRE.FindStringSubmatch(authorLine)
	date := repo.CreatedAt
	name := st.viewerName
	email := st.viewerEmail
	if len(m) > 3 {
		name, email = m[1], m[2]
		if ts, err := strconv.ParseInt(m[3], 10, 64); err == nil { date = iso(ts) }
	}
	obj := commitJSON{
		SHA: sha, NodeID: nodeID("Commit", sha),
		HTMLURL: "https://github.com/" + repo.Owner + "/" + repo.Name + "/commit/" + sha,
		URL: "/api/v3/repos/" + repo.Owner + "/" + repo.Name + "/commits/" + sha,
	}
	obj.Commit.Author = personJSON{Name: name, Email: email, Date: date}
	obj.Commit.Committer = personJSON{Name: st.viewerName, Email: st.viewerEmail, Date: date}
	obj.Commit.Message = strings.TrimRightFunc(info.Message, unicode.IsSpace)
	obj.Commit.Tree.SHA = info.Tree
	for _, p := range info.Parents { obj.Parents = append(obj.Parents, struct{ SHA string `json:"sha"` }{p}) }
	return obj
}

func commitChain(repo *Repo, sha string) []string {
	out := []string{}
	seen := map[string]bool{}
	cur := sha
	for cur != "" && !seen[cur] {
		seen[cur] = true
		info := commitInfo(repo, cur)
		if info == nil { break }
		out = append(out, cur)
		if len(info.Parents) > 0 { cur = info.Parents[0] } else { cur = "" }
	}
	return out
}

func paginate[T any](items []T, u *url.URL, path string) ([]T, map[string]string) {
	perPage := 30
	if v, err := strconv.Atoi(u.Query().Get("per_page")); err == nil { perPage = v }
	if perPage < 1 || perPage > 100 { perPage = 30 }
	page := 1
	if v, err := strconv.Atoi(u.Query().Get("page")); err == nil { page = v }
	if page < 1 { page = 1 }
	start := (page - 1) * perPage
	if start > len(items) { start = len(items) }
	end := start + perPage
	if end > len(items) { end = len(items) }
	slice := items[start:end]
	pages := len(items) / perPage
	if len(items)%perPage != 0 { pages++ }
	if pages < 1 { pages = 1 }
	linkFor := func(p int, rel string) string {
		u2 := *u
		u2.Path = path
		u2.RawQuery = u.RawQuery
		q := u2.Query()
		q.Set("per_page", strconv.Itoa(perPage))
		q.Set("page", strconv.Itoa(p))
		u2.RawQuery = q.Encode()
		return "<" + u2.String() + ">; rel=\"" + rel + "\""
	}
	links := []string{linkFor(1, "first"), linkFor(pages, "last")}
	if page > 1 { links = append(links, linkFor(page-1, "prev")) }
	if page < pages { links = append(links, linkFor(page+1, "next")) }
	return slice, map[string]string{"link": strings.Join(links, ", ")}
}

func resolveRef(repo *Repo, ref string) string {
	if ref == "" { return repo.Refs["refs/heads/"+repo.DefaultBranch] }
	if shaRE.MatchString(ref) {
		if repo.Objects[ref] != nil { return ref }
		return ""
	}
	for _, key := range []string{"refs/heads/" + ref, "refs/tags/" + ref, ref} {
		if sha := repo.Refs[key]; sha != "" { return sha }
	}
	return ""
}

func contentShape(repo *Repo, path, sha, mode string) any {
	obj := repo.Objects[sha]
	if obj == nil { return nil }
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]
	modeOut := "100644"
	if mode == "100755" { modeOut = "100755" }
	return contentFileJSON{
		Type: "file", Name: name, Path: path, SHA: sha, Size: len(obj.Data),
		Encoding: "base64", Content: base64.StdEncoding.EncodeToString(obj.Data), Mode: modeOut,
		URL: "/api/v3/repos/" + repo.Owner + "/" + repo.Name + "/contents/" + path,
	}
}

func readBodyObject(raw string) map[string]any {
	if raw == "" { return nil }
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil { return nil }
	m, ok := v.(map[string]any)
	if !ok { return nil }
	return m
}

func stringField(m map[string]any, key string) string {
	if m == nil { return "" }
	if s, ok := m[key].(string); ok { return s }
	return ""
}

func boolField(m map[string]any, key string) bool {
	if m == nil { return false }
	if b, ok := m[key].(bool); ok { return b }
	return false
}

func issueNumberFromPath(parts []string) int {
	if len(parts) < 2 { return 0 }
	n, err := strconv.Atoi(parts[1])
	if err != nil { return 0 }
	return n
}

func cachedIssueResponse(repo *Repo, issue *Issue) Resp {
	key := strconv.FormatUint(repo.Version, 10) + ":" + strconv.Itoa(issue.Number)
	if b := cacheGet(repo, repo.IssueCache, key); b != nil {
		return rawResp(200, b, map[string]string{"content-type": "application/json; charset=utf-8"})
	}
	body, err := marshalJSON(makeIssueShape(repo, issue))
	if err != nil { return errResp(500, "Internal Server Error") }
	cacheSet(repo, repo.IssueCache, key, body)
	return rawResp(200, body, map[string]string{"content-type": "application/json; charset=utf-8"})
}

func handleRest(method, path string, u *url.URL, rawBody string) Resp {
	if rawBody != "" && path == "/api/v3/user/repos" && method == "POST" {
		var probe any
		if err := json.Unmarshal([]byte(rawBody), &probe); err != nil { return errResp(400, "Bad Request") }
	}
	p := strings.TrimRight(path, "/")
	if p == "" { p = "/" }

	if p == "/api/v3" || p == "/api/v3/" {
		return jresp(200, map[string]any{
			"current_user_url": "/api/v3/user",
			"current_user_authorizations_html_url": "https://github.com/settings/connections/applications",
			"authorizations_url": "/api/v3/authorizations",
			"code_search_url": "/api/v3/search/code?q={query}",
			"commit_search_url": "/api/v3/search/commits?q={query}",
			"emails_url": "/api/v3/user/emails",
			"rate_limit_url": "/api/v3/rate_limit",
			"repository_url": "/api/v3/repos/{owner}/{repo}",
			"user_url": "/api/v3/users/{user}",
		}, map[string]string{"x-oauth-scopes": "repo, read:org"})
	}
	if p == "/api/v3/meta" && method == "GET" {
		return jresp(200, map[string]any{"installed_version": "3.0.0", "verifiable_password_authentication": true}, nil)
	}
	if p == "/api/v3/rate_limit" && method == "GET" {
		return jresp(200, map[string]any{"resources": map[string]any{"core": map[string]any{"limit": 5000, "remaining": 4999, "reset": 0}}}, nil)
	}
	if p == "/api/v3/user" && method == "GET" { return jresp(200, makeUserShape(), nil) }
	if p == "/api/v3/no-such-route-content-type" { return errResp(404, "Not Found") }

	if p == "/api/v3/user/repos" {
		if method == "GET" {
			mine := []repoJSON{}
			for _, r := range st.SortedRepos() {
				if r.Owner == st.viewer { mine = append(mine, makeRepoShape(r)) }
			}
			page, headers := paginate(mine, u, "/api/v3/user/repos")
			return jresp(200, page, headers)
		}
		if method == "POST" {
			b := readBodyObject(rawBody)
			name := stringField(b, "name")
			if b == nil || name == "" { return errResp(422, "Validation Failed") }
			if st.GetRepo(st.viewer, name) != nil { return errResp(422, "Validation Failed: repository already exists") }
			desc := stringField(b, "description")
			repo := st.CreateRepo(st.viewer, name, boolField(b, "private"), desc)
			return jresp(201, makeRepoShape(repo), nil)
		}
	}

	prefix := "/api/v3/repos/"
	if !strings.HasPrefix(p, prefix) { return errResp(404, "Not Found") }
	segments := strings.Split(strings.TrimPrefix(p, prefix), "/")
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" { return errResp(404, "Not Found") }
	owner, name := segments[0], segments[1]
	parts := []string{}
	if len(segments) > 2 { parts = segments[2:] }
	repo := st.GetRepo(owner, name)
	if repo == nil { return errResp(404, "Not Found") }
	body := readBodyObject(rawBody)

	if len(parts) == 0 {
		switch method {
		case "GET":
			return jresp(200, makeRepoShape(repo), nil)
		case "PATCH":
			if body != nil {
				if v, ok := body["description"].(string); ok { repo.Description = v }
				if v, ok := body["default_branch"].(string); ok { repo.DefaultBranch = v }
			}
			bump(repo)
			return jresp(200, makeRepoShape(repo), nil)
		case "DELETE":
			st.DeleteRepo(owner, name)
			return emptyResp(204)
		default:
			return errResp(404, "Not Found")
		}
	}

	if parts[0] == "branches" && method == "GET" && len(parts) == 1 {
		items := []any{}
		for _, ref := range repo.RefOrder {
			if !strings.HasPrefix(ref, "refs/heads/") { continue }
			sha := repo.Refs[ref]
			items = append(items, map[string]any{
				"name": strings.TrimPrefix(ref, "refs/heads/"),
				"commit": map[string]any{"sha": sha, "url": ""},
				"protected": false,
			})
		}
		return jresp(200, items, nil)
	}

	if parts[0] == "branches" && method == "GET" && len(parts) == 2 {
		sha := resolveRef(repo, parts[1])
		if sha == "" { return errResp(404, "Not Found") }
		return jresp(200, map[string]any{"name": parts[1], "commit": map[string]any{"sha": sha}}, nil)
	}

	if parts[0] == "git" && len(parts) >= 2 && parts[1] == "refs" && method == "POST" {
		ref := stringField(body, "ref")
		sha := stringField(body, "sha")
		if body == nil || ref == "" || sha == "" || repo.Objects[sha] == nil { return errResp(422, "Validation Failed") }
		setRef(repo, ref, sha)
		return jresp(201, map[string]any{"ref": ref, "object": map[string]any{"sha": sha, "type": "commit"}}, nil)
	}

	if parts[0] == "git" && len(parts) >= 2 && parts[1] == "refs" && method == "GET" {
		ref := strings.Join(parts[2:], "/")
		sha := resolveRef(repo, ref)
		if sha == "" { return errResp(404, "Not Found") }
		return jresp(200, map[string]any{"ref": "refs/heads/" + ref, "object": map[string]any{"sha": sha, "type": "commit"}}, nil)
	}

	if parts[0] == "commits" && method == "GET" {
		sha := resolveRef(repo, u.Query().Get("sha"))
		if sha == "" { return jresp(200, []any{}, nil) }
		chain := commitChain(repo, sha)
		shapes := []any{}
		for _, s := range chain { if v := makeCommitShape(repo, s); v != nil { shapes = append(shapes, v) } }
		page, headers := paginate(shapes, u, p)
		return jresp(200, page, headers)
	}

	if parts[0] == "contents" && len(parts) >= 2 {
		filePath := strings.Join(parts[1:], "/")
		tip := resolveRef(repo, u.Query().Get("ref"))
		files := newFileMap()
		if info := commitInfo(repo, tip); info != nil { files = flattenTree(repo, info.Tree, "") }
		switch method {
		case "GET":
			if entry, ok := files.Get(filePath); ok {
				return jresp(200, contentShape(repo, filePath, entry.Sha, entry.Mode), nil)
			}
			prefix := ""
			if filePath != "" { prefix = filePath + "/" }
			type dirItem struct {
				Name string
				SHA  string
				IsFile bool
			}
			dirs := []dirItem{}
			seen := map[string]bool{}
			for _, path := range files.Keys() {
				if !strings.HasPrefix(path, prefix) { continue }
				tail := strings.TrimPrefix(path, prefix)
				if tail == "" { continue }
				slash := strings.IndexByte(tail, '/')
				var key string
				if slash < 0 { key = tail } else { key = tail[:slash] }
				if seen[key] { continue }
				seen[key] = true
				entry, _ := files.Get(path)
				_, isFile := files.Get(prefix + key)
				dirs = append(dirs, dirItem{Name: key, SHA: entry.Sha, IsFile: isFile})
			}
			if len(dirs) == 0 { return errResp(404, "Not Found") }
			items := []contentDirJSON{}
			for _, d := range dirs {
				typ := "dir"
				if d.IsFile { typ = "file" }
				items = append(items, contentDirJSON{
					Type: typ, Name: d.Name, Path: prefix + d.Name, SHA: d.SHA, Size: 0, URL: "", GitURL: "", HTMLURL: "",
				})
			}
			return jresp(200, items, nil)
		case "PUT":
			if body == nil || stringField(body, "message") == "" || stringField(body, "content") == "" { return errResp(422, "Validation Failed") }
			clean := strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) { return -1 }
				return r
			}, stringField(body, "content"))
			bytes, err := base64.StdEncoding.DecodeString(clean)
			if err != nil { return errResp(422, "Validation Failed") }
			branch := repo.DefaultBranch
			if v := stringField(body, "branch"); v != "" { branch = v }
			current := ""
			if e, ok := files.Get(filePath); ok { current = e.Sha }
			if provided := stringField(body, "sha"); provided != "" && current != "" && provided != current { return errResp(409, "Conflict") }
			_, commit, ok := setFile(st, repo, branch, filePath, bytes, false, stringField(body, "message"))
			if !ok { return errResp(409, "Conflict") }
			return jresp(201, map[string]any{
				"content": map[string]any{"sha": blobFor(repo, bytes), "encoding": "base64", "name": filePath, "path": filePath},
				"commit": map[string]any{"sha": commit, "message": stringField(body, "message")},
			}, nil)
		case "DELETE":
			if body == nil || stringField(body, "message") == "" { return errResp(422, "Validation Failed") }
			branch := repo.DefaultBranch
			if v := stringField(body, "branch"); v != "" { branch = v }
			_, commit, ok := setFile(st, repo, branch, filePath, nil, true, stringField(body, "message"))
			if !ok { return errResp(404, "Not Found") }
			return jresp(200, map[string]any{"commit": map[string]any{"sha": commit}}, nil)
		}
	}

	if parts[0] == "labels" && method == "POST" {
		name := stringField(body, "name")
		if body == nil || name == "" { return errResp(422, "Validation Failed") }
		found := -1
		for i, l := range repo.Labels { if l == name { found = i; break } }
		if found < 0 {
			repo.Labels = append(repo.Labels, name)
			found = len(repo.Labels) - 1
			bump(repo)
		}
		color := stringField(body, "color")
		if color == "" { color = "ededed" }
		return jresp(201, map[string]any{"id": found + 1, "name": name, "color": color}, nil)
	}

	if parts[0] == "issues" {
		if len(parts) == 1 && method == "GET" {
			state := u.Query().Get("state")
			if state == "" { state = "open" }
			items := []*Issue{}
			for _, i := range repo.Issues {
				if state == "all" || i.State == state { items = append(items, i) }
			}
			if u.Query().Get("sort") == "created" {
				asc := u.Query().Get("direction") != "desc"
				sort.SliceStable(items, func(i, j int) bool {
					if asc { return items[i].Number < items[j].Number }
					return items[i].Number > items[j].Number
				})
			}
			shapes := []issueJSON{}
			for _, i := range items { shapes = append(shapes, makeIssueShape(repo, i)) }
			page, headers := paginate(shapes, u, p)
			return jresp(200, page, headers)
		}
		if len(parts) == 1 && method == "POST" {
			if body == nil || stringField(body, "title") == "" { return errResp(422, "Validation Failed") }
			labels := []string{}
			if raw, ok := body["labels"].([]any); ok {
				for _, item := range raw {
					switch v := item.(type) {
					case string:
						labels = append(labels, v)
					case map[string]any:
						labels = append(labels, stringField(v, "name"))
					}
				}
			}
			for _, label := range labels {
				ok := false
				for _, existing := range repo.Labels { if existing == label { ok = true; break } }
				if !ok { return errResp(422, "Validation Failed: label does not exist") }
			}
			number := repo.NextIssue
			repo.NextIssue++
			created := iso(st.NowSec())
			issue := &Issue{
				Number: number, Title: stringField(body, "title"), Body: stringField(body, "body"),
				State: "open", Labels: labels, ID: nodeID("Issue", repo.DatabaseID, number),
				Comments: []*Comment{}, CreatedAt: created, UpdatedAt: created,
			}
			repo.Issues = append(repo.Issues, issue)
			bump(repo)
			return jresp(201, makeIssueShape(repo, issue), nil)
		}
		n := issueNumberFromPath(parts)
		var issue *Issue
		for _, i := range repo.Issues { if i.Number == n { issue = i; break } }
		if issue == nil { return errResp(404, "Not Found") }
		if len(parts) == 2 && method == "GET" { return cachedIssueResponse(repo, issue) }
		if len(parts) == 2 && method == "PATCH" {
			if body != nil && (stringField(body, "state") == "open" || stringField(body, "state") == "closed") {
				issue.State = stringField(body, "state")
				issue.UpdatedAt = iso(st.NowSec())
				bump(repo)
			}
			return jresp(200, map[string]any{"number": issue.Number, "state": issue.State, "id": issue.ID}, nil)
		}
		if len(parts) == 3 && parts[2] == "comments" && method == "GET" {
			items := []any{}
			for _, c := range issue.Comments {
				items = append(items, map[string]any{"id": c.ID, "body": c.Body, "user": makeUserShape(), "created_at": c.CreatedAt})
			}
			return jresp(200, items, nil)
		}
		if len(parts) == 3 && parts[2] == "comments" && method == "POST" {
			if body == nil || stringField(body, "body") == "" { return errResp(422, "Validation Failed") }
			comment := &Comment{ID: st.NextCommentID(), Body: stringField(body, "body"), Author: st.viewer, CreatedAt: iso(st.NowSec())}
			issue.Comments = append(issue.Comments, comment)
			bump(repo)
			return jresp(201, map[string]any{"id": comment.ID, "body": comment.Body, "user": makeUserShape(), "created_at": comment.CreatedAt}, nil)
		}
	}

	if parts[0] == "pulls" {
		if len(parts) == 1 && method == "GET" {
			state := u.Query().Get("state")
			if state == "" { state = "open" }
			items := []pullJSON{}
			for _, p := range repo.Pulls {
				if state == "all" || p.State == state { items = append(items, makePullShape(repo, p)) }
			}
			page, headers := paginate(items, u, p)
			return jresp(200, page, headers)
		}
		if len(parts) == 1 && method == "POST" {
			if body == nil || stringField(body, "head") == "" || stringField(body, "base") == "" || stringField(body, "title") == "" {
				return errResp(422, "Validation Failed")
			}
			headSha := resolveRef(repo, stringField(body, "head"))
			baseSha := resolveRef(repo, stringField(body, "base"))
			if headSha == "" || baseSha == "" { return errResp(422, "Validation Failed") }
			number := repo.NextPull
			repo.NextPull++
			created := iso(st.NowSec())
			pull := &Pull{
				Number: number, Title: stringField(body, "title"), Body: stringField(body, "body"),
				State: "open", Head: stringField(body, "head"), Base: stringField(body, "base"),
				HeadSha: headSha, BaseSha: baseSha, Merged: false, MergeCommitSha: nil, MergedAt: nil,
				ID: nodeID("PullRequest", repo.DatabaseID, number), CreatedAt: created, UpdatedAt: created,
				Files: compareTrees(repo, baseSha, headSha),
			}
			repo.Pulls = append(repo.Pulls, pull)
			bump(repo)
			return jresp(201, makePullShape(repo, pull), nil)
		}
		if len(parts) >= 2 {
			n, _ := strconv.Atoi(parts[1])
			var pull *Pull
			for _, p := range repo.Pulls { if p.Number == n { pull = p; break } }
			if pull == nil { return errResp(404, "Not Found") }
			if len(parts) == 2 && method == "GET" { return jresp(200, makePullShape(repo, pull), nil) }
			if len(parts) == 3 && parts[2] == "files" && method == "GET" { return jresp(200, pull.Files, nil) }
			if len(parts) == 3 && parts[2] == "merge" && method == "PUT" {
				if pull.State == "closed" && pull.Merged { return errResp(405, "Pull request already merged") }
				ref := "refs/heads/" + pull.Base
				currentBase := repo.Refs[ref]
				if currentBase == "" { currentBase = pull.BaseSha }
				if currentBase != pull.BaseSha {
					changed := map[string]bool{}
					for _, f := range compareTrees(repo, pull.BaseSha, currentBase) { changed[f.Filename] = true }
					for _, f := range pull.Files {
						if changed[f.Filename] { return errResp(405, "Merge conflict") }
					}
				}
				headInfo := commitInfo(repo, pull.HeadSha)
				if headInfo == nil { return errResp(405, "Merge conflict") }
				mergeSha := makeCommit(st, repo, headInfo.Tree, []string{currentBase}, "Merge pull request #"+strconv.Itoa(pull.Number)+" from "+pull.Head)
				setRef(repo, ref, mergeSha)
				pull.State = "closed"
				pull.Merged = true
				pull.MergeCommitSha = &mergeSha
				mergedAt := iso(st.NowSec())
				pull.MergedAt = &mergedAt
				pull.UpdatedAt = mergedAt
				return jresp(200, map[string]any{
					"merged": true, "sha": mergeSha, "message": "Pull request #" + strconv.Itoa(pull.Number) + " merged successfully", "node_id": pull.ID,
				}, nil)
			}
		}
	}

	return errResp(404, "Not Found")
}
