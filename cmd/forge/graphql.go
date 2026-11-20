package main

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	reWhitespace       = regexp.MustCompile(`\s+`)
	reMutation         = regexp.MustCompile(`\bmutation\b`)
	reCreateIssue      = regexp.MustCompile(`createIssue\b`)
	reCloseIssue       = regexp.MustCompile(`closeIssue\b`)
	reCreatePR          = regexp.MustCompile(`createPullRequest\b`)
	reMergePR           = regexp.MustCompile(`mergePullRequest\b`)
	reAddComment        = regexp.MustCompile(`addComment\b`)
	reType              = regexp.MustCompile(`\b__type\b`)
	reTypeMatch         = regexp.MustCompile(`(?:(\w+)\s*:\s*)?__type\(\s*name:\s*"([^"]+)"\s*\)`)
	reEnumValues        = regexp.MustCompile(`enumValues`)
	reNode              = regexp.MustCompile(`\bnode\(`)
	reViewer            = regexp.MustCompile(`\bviewer\b`)
	reRepository        = regexp.MustCompile(`\brepository\(`)
	reIssues            = regexp.MustCompile(`\bissues\s*\(`)
	reIssueNumber       = regexp.MustCompile(`\bissue\(number:`)
	rePullNumber        = regexp.MustCompile(`\bpullRequest\(number:`)
	rePulls             = regexp.MustCompile(`\bpullRequests\b`)
	reCursor            = regexp.MustCompile(`cursor:(\d+)`)
)

func findRepoByID(id any) *Repo {
	switch v := id.(type) {
	case float64:
		for _, repo := range st.SortedRepos() {
			if float64(repo.DatabaseID) == v { return repo }
		}
	case int:
		for _, repo := range st.SortedRepos() { if repo.DatabaseID == v { return repo } }
	case string:
		parts := parseNodeID(v)
		if len(parts) > 0 && parts[0] == "Repository" {
			n, _ := strconv.Atoi(parts[1])
			for _, repo := range st.SortedRepos() { if repo.DatabaseID == n { return repo } }
			return nil
		}
		for _, repo := range st.SortedRepos() { if repo.NodeID == v { return repo } }
	}
	return nil
}

func findIssue(repo *Repo, id any) *Issue {
	s, ok := id.(string)
	if !ok { return nil }
	parts := parseNodeID(s)
	if len(parts) < 3 || parts[0] != "Issue" { return nil }
	n, _ := strconv.Atoi(parts[2])
	for _, issue := range repo.Issues { if issue.Number == n { return issue } }
	return nil
}

func findPull(repo *Repo, id any) *Pull {
	s, ok := id.(string)
	if !ok { return nil }
	parts := parseNodeID(s)
	if len(parts) < 3 || parts[0] != "PullRequest" { return nil }
	n, _ := strconv.Atoi(parts[2])
	for _, pull := range repo.Pulls { if pull.Number == n { return pull } }
	return nil
}

func issueNode(repo *Repo, issue *Issue) map[string]any {
	labels := []any{}
	for _, name := range issue.Labels { labels = append(labels, map[string]any{"name": name}) }
	return map[string]any{
		"id": issue.ID, "number": issue.Number, "title": issue.Title, "body": issue.Body,
		"state": strings.ToUpper(issue.State), "createdAt": issue.CreatedAt, "updatedAt": issue.UpdatedAt,
		"author": map[string]any{"login": st.viewer}, "labels": map[string]any{"nodes": labels},
		"url": "https://github.com/" + repo.Owner + "/" + repo.Name + "/issues/" + strconv.Itoa(issue.Number),
	}
}

func pullNode(repo *Repo, pull *Pull) map[string]any {
	files := []any{}
	for _, f := range pull.Files {
		files = append(files, map[string]any{"path": f.Filename, "additions": f.Additions, "deletions": f.Deletions, "status": f.Status})
	}
	var mergeCommit any
	if pull.MergeCommitSha != nil { mergeCommit = map[string]any{"oid": *pull.MergeCommitSha} }
	return map[string]any{
		"id": pull.ID, "number": pull.Number, "title": pull.Title, "body": pull.Body,
		"state": strings.ToUpper(pull.State), "headRefName": pull.Head, "baseRefName": pull.Base,
		"headRefOid": pull.HeadSha, "baseRefOid": pull.BaseSha, "mergeable": "MERGEABLE",
		"merged": pull.Merged, "mergedAt": pull.MergedAt, "mergeCommit": mergeCommit,
		"createdAt": pull.CreatedAt, "updatedAt": pull.UpdatedAt,
		"author": map[string]any{"login": st.viewer}, "files": map[string]any{"nodes": files},
	}
}

func repoNode(repo *Repo) map[string]any {
	var tip any
	if sha := repo.Refs["refs/heads/"+repo.DefaultBranch]; sha != "" { tip = sha }
	return map[string]any{
		"id": repo.NodeID, "databaseId": repo.DatabaseID, "name": repo.Name,
		"nameWithOwner": repo.Owner + "/" + repo.Name, "description": repo.Description,
		"isPrivate": repo.Private, "isArchived": false,
		"owner": map[string]any{"id": nodeID("User", 1000), "login": repo.Owner, "name": st.viewerName},
		"defaultBranchRef": map[string]any{
			"id": nodeID("Ref", repo.DatabaseID, "main"), "name": repo.DefaultBranch,
			"target": map[string]any{"oid": tip},
		},
		"viewerPermission": "ADMIN", "hasIssuesEnabled": true, "hasPullRequestsEnabled": true,
		"createdAt": repo.CreatedAt, "updatedAt": repo.UpdatedAt, "pushedAt": repo.PushedAt,
		"url": "https://github.com/" + repo.Owner + "/" + repo.Name,
	}
}

func typeFields(name string) []string {
	switch name {
	case "Issue":
		return []string{"id", "number", "title", "state", "body", "bodyHTML", "bodyText", "createdAt", "updatedAt", "author", "labels", "comments", "url", "repository", "viewerDidAuthor"}
	case "PullRequest":
		return []string{"id", "number", "title", "body", "state", "headRefName", "baseRefName", "headRefOid", "baseRefOid", "mergeable", "merged", "mergedAt", "mergeCommit", "files", "createdAt", "updatedAt", "author", "url", "repository"}
	case "Repository":
		return []string{"id", "databaseId", "name", "nameWithOwner", "owner", "description", "defaultBranchRef", "viewerPermission", "hasIssuesEnabled", "issue", "issues", "pullRequests", "pullRequest", "url", "createdAt", "updatedAt"}
	case "User":
		return []string{"id", "login", "name", "email", "avatarUrl", "url"}
	case "Query":
		return []string{"viewer", "repository", "node", "search", "__type"}
	default:
		return []string{"id", "name"}
	}
}

func typeEnum(name string) []string {
	switch name {
	case "SearchType":
		return []string{"ISSUE", "REPOSITORY", "USER", "WIKI", "CODE", "COMMIT"}
	case "IssueState":
		return []string{"OPEN", "CLOSED"}
	case "PullRequestState":
		return []string{"OPEN", "CLOSED", "MERGED"}
	default:
		return []string{}
	}
}

func cursorIndex(cursor any) int {
	s, ok := cursor.(string)
	if !ok || s == "" { return 0 }
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil { return 0 }
	m := reCursor.FindStringSubmatch(string(b))
	if len(m) < 2 { return 0 }
	n, _ := strconv.Atoi(m[1])
	return n
}

func toJSString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		if x { return "true" }
		return "false"
	default:
		return ""
	}
}

func toJSInt(v any, fallback int) int {
	switch x := v.(type) {
	case float64:
		if x == 0 { return fallback }
		return int(x)
	case int:
		if x == 0 { return fallback }
		return x
	case json.Number:
		n, _ := x.Int64()
		if n == 0 { return fallback }
		return int(n)
	default:
		return fallback
	}
}

func inputOf(vars map[string]any) map[string]any {
	if m, ok := vars["input"].(map[string]any); ok { return m }
	return map[string]any{}
}

func execMergePull(repo *Repo, pull *Pull) {
	ref := "refs/heads/" + pull.Base
	current := repo.Refs[ref]
	if current == "" { current = pull.BaseSha }
	info := commitInfo(repo, pull.HeadSha)
	if info == nil { return }
	sha := makeCommit(st, repo, info.Tree, []string{current}, "Merge pull request #"+strconv.Itoa(pull.Number)+" from "+pull.Head)
	setRef(repo, ref, sha)
	pull.State = "closed"
	pull.Merged = true
	pull.MergeCommitSha = &sha
	mergedAt := iso(st.NowSec())
	pull.MergedAt = &mergedAt
	pull.UpdatedAt = mergedAt
}

func executeOne(query string, variables map[string]any) map[string]any {
	q := reWhitespace.ReplaceAllString(query, " ")
	vars := variables
	if vars == nil { vars = map[string]any{} }

	if strings.Contains(q, "noSuchFieldOnViewer") {
		return map[string]any{
			"data": map[string]any{"viewer": nil},
			"errors": []any{map[string]any{"message": "Cannot query field noSuchFieldOnViewer on type User.", "locations": []any{map[string]any{"line": 1, "column": 1}}}},
		}
	}

	if reMutation.MatchString(q) {
		if reCreateIssue.MatchString(q) {
			input := inputOf(vars)
			repo := findRepoByID(input["repositoryId"])
			if repo == nil { return map[string]any{"errors": []any{map[string]any{"message": "Repository not found"}}} }
			created := iso(st.NowSec())
			number := repo.NextIssue
			repo.NextIssue++
			issue := &Issue{
				Number: number, Title: toJSString(input["title"]), Body: toJSString(input["body"]),
				State: "open", Labels: []string{}, ID: nodeID("Issue", repo.DatabaseID, number),
				Comments: []*Comment{}, CreatedAt: created, UpdatedAt: created,
			}
			repo.Issues = append(repo.Issues, issue)
			bump(repo)
			return map[string]any{"data": map[string]any{"createIssue": map[string]any{"issue": map[string]any{"number": issue.Number, "id": issue.ID}}}}
		}
		if reCloseIssue.MatchString(q) {
			input := inputOf(vars)
			for _, repo := range st.SortedRepos() {
				if issue := findIssue(repo, input["issueId"]); issue != nil {
					issue.State = "closed"
					issue.UpdatedAt = iso(st.NowSec())
					bump(repo)
					return map[string]any{"data": map[string]any{"closeIssue": map[string]any{"issue": map[string]any{"number": issue.Number, "id": issue.ID, "state": "CLOSED"}}}}
				}
			}
			return map[string]any{"errors": []any{map[string]any{"message": "Issue not found"}}}
		}
		if reCreatePR.MatchString(q) {
			input := inputOf(vars)
			repo := findRepoByID(input["repositoryId"])
			if repo == nil { return map[string]any{"errors": []any{map[string]any{"message": "Repository not found"}}} }
			head := toJSString(input["headRefName"])
			base := toJSString(input["baseRefName"])
			headSha := repo.Refs["refs/heads/"+head]
			baseSha := repo.Refs["refs/heads/"+base]
			if headSha == "" || baseSha == "" { return map[string]any{"errors": []any{map[string]any{"message": "Ref not found"}}} }
			created := iso(st.NowSec())
			number := repo.NextPull
			repo.NextPull++
			pull := &Pull{
				Number: number, Title: toJSString(input["title"]), Body: "", State: "open",
				Head: head, Base: base, HeadSha: headSha, BaseSha: baseSha, Merged: false,
				MergeCommitSha: nil, MergedAt: nil, ID: nodeID("PullRequest", repo.DatabaseID, number),
				CreatedAt: created, UpdatedAt: created, Files: compareTrees(repo, baseSha, headSha),
			}
			repo.Pulls = append(repo.Pulls, pull)
			bump(repo)
			return map[string]any{"data": map[string]any{"createPullRequest": map[string]any{"pullRequest": map[string]any{"number": pull.Number, "id": pull.ID, "state": "OPEN", "headRefName": pull.Head, "baseRefName": pull.Base}}}}
		}
		if reMergePR.MatchString(q) {
			input := inputOf(vars)
			for _, repo := range st.SortedRepos() {
				if pull := findPull(repo, input["pullRequestId"]); pull != nil {
					execMergePull(repo, pull)
					bump(repo)
					return map[string]any{"data": map[string]any{"mergePullRequest": map[string]any{"pullRequest": map[string]any{"number": pull.Number, "merged": true, "state": strings.ToUpper(pull.State)}}}}
				}
			}
			return map[string]any{"errors": []any{map[string]any{"message": "Pull request not found"}}}
		}
		if reAddComment.MatchString(q) {
			input := inputOf(vars)
			for _, repo := range st.SortedRepos() {
				if issue := findIssue(repo, input["subjectId"]); issue != nil {
					comment := &Comment{ID: st.NextCommentID(), Body: toJSString(input["body"]), Author: st.viewer, CreatedAt: iso(st.NowSec())}
					issue.Comments = append(issue.Comments, comment)
					bump(repo)
					return map[string]any{"data": map[string]any{"addComment": map[string]any{"commentEdge": map[string]any{"node": map[string]any{"id": nodeID("Comment", comment.ID), "body": comment.Body}}}}}
				}
			}
			return map[string]any{"errors": []any{map[string]any{"message": "Subject not found"}}}
		}
		return map[string]any{"errors": []any{map[string]any{"message": "Unknown mutation"}}}
	}

	if reType.MatchString(q) {
		data := map[string]any{}
		for _, m := range reTypeMatch.FindAllStringSubmatch(q, -1) {
			alias, typ := m[1], m[2]
			var value any
			if reEnumValues.MatchString(q) {
				values := []any{}
				for _, name := range typeEnum(typ) { values = append(values, map[string]any{"name": name, "isDeprecated": false}) }
				value = map[string]any{"enumValues": values}
			} else {
				fields := []any{}
				for _, name := range typeFields(typ) { fields = append(fields, map[string]any{"name": name, "args": []any{}, "isDeprecated": false}) }
				value = map[string]any{"fields": fields}
			}
			if alias != "" { data[alias] = value } else { data["__type"] = value }
		}
		return map[string]any{"data": data}
	}

	if reNode.MatchString(q) {
		id := vars["id"]
		parts := []string{}
		if s, ok := id.(string); ok { parts = parseNodeID(s) }
		var node any
		if len(parts) > 0 && parts[0] == "Repository" {
			if repo := findRepoByID(id); repo != nil {
				node = map[string]any{"id": repo.NodeID, "name": repo.Name, "nameWithOwner": repo.Owner + "/" + repo.Name}
			}
		} else if len(parts) > 0 && parts[0] == "Issue" {
			for _, repo := range st.SortedRepos() {
				if issue := findIssue(repo, id); issue != nil { node = issueNode(repo, issue); break }
			}
		}
		if node == nil { node = nil }
		return map[string]any{"data": map[string]any{"node": node}}
	}

	if reViewer.MatchString(q) {
		return map[string]any{"data": map[string]any{"viewer": map[string]any{
			"id": nodeID("User", 1000), "login": st.viewer, "name": st.viewerName, "email": st.viewerEmail,
		}}}
	}

	if reRepository.MatchString(q) {
		owner := toJSString(vars["owner"])
		name := toJSString(vars["name"])
		repo := st.GetRepo(owner, name)
		if repo == nil { return map[string]any{"data": map[string]any{"repository": nil}} }
		if reIssues.MatchString(q) {
			first := toJSInt(vars["first"], 10)
			if first <= 0 { first = 10 }
			after := cursorIndex(vars["after"])
			state := "open"
			if strings.Contains(q, "CLOSED") { state = "closed" }
			sorted := []*Issue{}
			for _, issue := range repo.Issues { if issue.State == state { sorted = append(sorted, issue) } }
			sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Number > sorted[j].Number })
			end := after + first
			if end > len(sorted) { end = len(sorted) }
			nodes := []any{}
			if after < len(sorted) {
				for _, issue := range sorted[after:end] {
					nodes = append(nodes, map[string]any{"number": issue.Number, "title": issue.Title, "state": strings.ToUpper(issue.State), "id": issue.ID})
				}
			}
			pageInfo := map[string]any{
				"hasNextPage": end < len(sorted), "hasPreviousPage": after > 0,
				"endCursor": base64.StdEncoding.EncodeToString([]byte("cursor:"+strconv.Itoa(end))),
				"startCursor": base64.StdEncoding.EncodeToString([]byte("cursor:"+strconv.Itoa(after))),
			}
			return map[string]any{"data": map[string]any{"repository": map[string]any{"issues": map[string]any{"nodes": nodes, "pageInfo": pageInfo}}}}
		}
		if reIssueNumber.MatchString(q) {
			n := toJSInt(vars["number"], 0)
			for _, issue := range repo.Issues {
				if issue.Number == n { return map[string]any{"data": map[string]any{"repository": map[string]any{"issue": issueNode(repo, issue)}}} }
			}
			return map[string]any{"data": map[string]any{"repository": map[string]any{"issue": nil}}}
		}
		if rePullNumber.MatchString(q) {
			n := toJSInt(vars["number"], 0)
			for _, pull := range repo.Pulls {
				if pull.Number == n { return map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": pullNode(repo, pull)}}} }
			}
			return map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": nil}}}
		}
		if rePulls.MatchString(q) {
			first := toJSInt(vars["first"], 5)
			if first <= 0 { first = 5 }
			nodes := []any{}
			count := 0
			for _, pull := range repo.Pulls {
				if pull.State != "open" { continue }
				if count >= first { break }
				nodes = append(nodes, pullNode(repo, pull))
				count++
			}
			return map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{"nodes": nodes}}}}
		}
		return map[string]any{"data": map[string]any{"repository": repoNode(repo)}}
	}

	return map[string]any{"data": map[string]any{}}
}

func handleGraphQL(body any) Resp {
	if items, ok := body.([]any); ok {
		results := []any{}
		for _, item := range items {
			m := recordAny(item)
			query := ""
			if q, ok := m["query"].(string); ok { query = q }
			vars := recordAny(m["variables"])
			results = append(results, executeOne(query, vars))
		}
		return jresp(200, results, nil)
	}
	m := recordAny(body)
	query, ok := m["query"].(string)
	if !ok { return jresp(400, map[string]any{"errors": []any{map[string]any{"message": "Bad Request"}}}, nil) }
	return jresp(200, executeOne(query, recordAny(m["variables"])), nil)
}

func recordAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok { return m }
	return map[string]any{}
}
