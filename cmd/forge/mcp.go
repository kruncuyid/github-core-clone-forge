package main

import (
	"encoding/json"
	"net/url"
	"strconv"
)

var tools = []any{
	map[string]any{
		"name": "create_repo", "title": "Create repository",
		"description": "Create a new repository for the authenticated user.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string", "description": "Repository owner login."},
				"name": map[string]any{"type": "string", "description": "Repository name."},
				"description": map[string]any{"type": "string", "description": "Repository description."},
				"private": map[string]any{"type": "boolean", "description": "Whether the repository is private."},
			},
			"required": []any{"name"},
		},
	},
	map[string]any{
		"name": "get_repo", "title": "Get repository",
		"description": "Read one repository by owner and name.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string", "description": "Repository owner login."},
				"repo": map[string]any{"type": "string", "description": "Repository name."},
			},
			"required": []any{"owner", "repo"},
		},
	},
	map[string]any{
		"name": "create_issue", "title": "Create issue",
		"description": "Open an issue in a repository.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string", "description": "Repository owner login."},
				"repo": map[string]any{"type": "string", "description": "Repository name."},
				"title": map[string]any{"type": "string", "description": "Issue title."},
				"body": map[string]any{"type": "string", "description": "Issue body."},
			},
			"required": []any{"owner", "repo", "title"},
		},
	},
	map[string]any{
		"name": "list_issues", "title": "List issues",
		"description": "List issues in a repository, optionally filtered by state.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string", "description": "Repository owner login."},
				"repo": map[string]any{"type": "string", "description": "Repository name."},
				"state": map[string]any{"type": "string", "enum": []any{"open", "closed", "all"}, "description": "Issue state."},
			},
			"required": []any{"owner", "repo"},
		},
	},
	map[string]any{
		"name": "create_pull_request", "title": "Create pull request",
		"description": "Open a pull request from a head branch into a base branch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string", "description": "Repository owner login."},
				"repo": map[string]any{"type": "string", "description": "Repository name."},
				"title": map[string]any{"type": "string", "description": "Pull request title."},
				"body": map[string]any{"type": "string", "description": "Pull request body."},
				"head": map[string]any{"type": "string", "description": "Head branch name."},
				"base": map[string]any{"type": "string", "description": "Base branch name."},
			},
			"required": []any{"owner", "repo", "title", "head", "base"},
		},
	},
	map[string]any{
		"name": "merge_pull_request", "title": "Merge pull request",
		"description": "Merge a pull request by number.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string", "description": "Repository owner login."},
				"repo": map[string]any{"type": "string", "description": "Repository name."},
				"pull_number": map[string]any{"type": "integer", "description": "Pull request number."},
			},
			"required": []any{"owner", "repo", "pull_number"},
		},
	},
}

var serverInfo = map[string]any{"name": "makefun-forge", "version": "1.0.0"}
var supportedVersions = []any{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
var legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

func rpcID(message map[string]any) any {
	switch v := message["id"].(type) {
	case string, float64:
		return v
	default:
		return nil
	}
}

func resultResp(id any, value any) *Resp {
	r := jresp(200, map[string]any{"jsonrpc": "2.0", "id": id, "result": value}, nil)
	return &r
}

func rpcError(id any, code int, message string, status int) *Resp {
	r := jresp(status, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}, nil)
	return &r
}

func toolResult(id any, value any) *Resp {
	return resultResp(id, map[string]any{
		"content": []any{map[string]any{"type": "text", "text": compactJSON(value)}},
		"structuredContent": value,
		"isError": false,
	})
}

func toolFailure(id any, message string) *Resp {
	return resultResp(id, map[string]any{
		"content": []any{map[string]any{"type": "text", "text": message}},
		"structuredContent": map[string]any{"message": message},
		"isError": true,
	})
}

func textArg(args map[string]any, names []string) string {
	for _, name := range names {
		switch v := args[name].(type) {
		case string:
			if v != "" { return v }
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		}
	}
	return ""
}

func boolArg(args map[string]any, names []string) (bool, bool) {
	for _, name := range names {
		switch v := args[name].(type) {
		case bool:
			return v, true
		case string:
			if v == "true" { return true, true }
			if v == "false" { return false, true }
		}
	}
	return false, false
}

func restCall(method, target string, body any) Resp {
	u, err := url.Parse(target)
	if err != nil {
		return errResp(500, "Internal Server Error")
	}
	raw := ""
	if body != nil { raw = compactJSON(body) }
	return handleRest(method, u.EscapedPath(), u, raw)
}

func repoPath(owner, repo string) string {
	return "/api/v3/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

func callTool(id any, name string, rawArgs any) *Resp {
	args := recordAny(rawArgs)
	if name == "create_repo" {
		repoName := textArg(args, []string{"name", "repo", "repository", "repository_name", "repo_name"})
		if repoName == "" { return rpcError(id, -32602, "Missing required argument: name", 200) }
		description := textArg(args, []string{"description", "body", "about", "summary"})
		privateFlag, ok := boolArg(args, []string{"private", "is_private"})
		if !ok { privateFlag = false }
		answer := restCall("POST", "/api/v3/user/repos", map[string]any{"name": repoName, "description": description, "private": privateFlag})
		if answer.Status >= 400 { return toolFailure(id, "create_repo failed with HTTP "+strconv.Itoa(answer.Status)) }
		return toolResult(id, answer.Value)
	}
	if name == "get_repo" {
		owner := textArg(args, []string{"owner", "owner_login", "org", "organization", "user", "login"})
		repoName := textArg(args, []string{"repo", "repository", "repository_name", "repo_name"})
		if owner == "" || repoName == "" { return rpcError(id, -32602, "Missing required arguments: owner, repo", 200) }
		answer := restCall("GET", repoPath(owner, repoName), nil)
		if answer.Status >= 400 { return toolFailure(id, "get_repo failed with HTTP "+strconv.Itoa(answer.Status)) }
		return toolResult(id, answer.Value)
	}
	if name == "create_issue" {
		owner := textArg(args, []string{"owner", "owner_login", "org", "organization", "user", "login"})
		repoName := textArg(args, []string{"repo", "repository", "repository_name", "repo_name"})
		title := textArg(args, []string{"title", "issue_title", "subject"})
		if owner == "" || repoName == "" || title == "" { return rpcError(id, -32602, "Missing required arguments: owner, repo, title", 200) }
		body := textArg(args, []string{"body", "issue_body", "description", "content", "text"})
		answer := restCall("POST", repoPath(owner, repoName)+"/issues", map[string]any{"title": title, "body": body})
		if answer.Status >= 400 { return toolFailure(id, "create_issue failed with HTTP "+strconv.Itoa(answer.Status)) }
		return toolResult(id, answer.Value)
	}
	if name == "list_issues" {
		owner := textArg(args, []string{"owner", "owner_login", "org", "organization", "user", "login"})
		repoName := textArg(args, []string{"repo", "repository", "repository_name", "repo_name"})
		if owner == "" || repoName == "" { return rpcError(id, -32602, "Missing required arguments: owner, repo", 200) }
		state := textArg(args, []string{"state", "status"})
		if state == "" { state = "open" }
		target := repoPath(owner, repoName) + "/issues?state=" + url.QueryEscape(state) + "&per_page=100"
		answer := restCall("GET", target, nil)
		if answer.Status >= 400 { return toolFailure(id, "list_issues failed with HTTP "+strconv.Itoa(answer.Status)) }
		return toolResult(id, answer.Value)
	}
	if name == "create_pull_request" {
		owner := textArg(args, []string{"owner", "owner_login", "org", "organization", "user", "login"})
		repoName := textArg(args, []string{"repo", "repository", "repository_name", "repo_name"})
		title := textArg(args, []string{"title", "pull_request_title", "pr_title", "subject"})
		head := textArg(args, []string{"head", "head_ref", "head_branch", "source", "source_branch", "branch"})
		base := textArg(args, []string{"base", "base_ref", "base_branch", "target", "target_branch"})
		if owner == "" || repoName == "" || title == "" || head == "" || base == "" {
			return rpcError(id, -32602, "Missing required arguments: owner, repo, title, head, base", 200)
		}
		body := textArg(args, []string{"body", "description", "content", "text"})
		answer := restCall("POST", repoPath(owner, repoName)+"/pulls", map[string]any{"title": title, "body": body, "head": head, "base": base})
		if answer.Status >= 400 { return toolFailure(id, "create_pull_request failed with HTTP "+strconv.Itoa(answer.Status)) }
		return toolResult(id, answer.Value)
	}
	if name == "merge_pull_request" {
		owner := textArg(args, []string{"owner", "owner_login", "org", "organization", "user", "login"})
		repoName := textArg(args, []string{"repo", "repository", "repository_name", "repo_name"})
		number := textArg(args, []string{"pull_number", "pull_request_number", "pr_number", "number", "issue_number"})
		if owner == "" || repoName == "" || number == "" { return rpcError(id, -32602, "Missing required arguments: owner, repo, pull_number", 200) }
		answer := restCall("PUT", repoPath(owner, repoName)+"/pulls/"+url.PathEscape(number)+"/merge", map[string]any{"merge_method": "merge"})
		if answer.Status >= 400 { return toolFailure(id, "merge_pull_request failed with HTTP "+strconv.Itoa(answer.Status)) }
		return toolResult(id, answer.Value)
	}
	return rpcError(id, -32602, "Unknown tool: "+name, 200)
}

func dispatch(message map[string]any) *Resp {
	id := rpcID(message)
	method := ""
	if v, ok := message["method"].(string); ok { method = v }
	params := recordAny(message["params"])
	switch method {
	case "server/discover":
		return resultResp(id, map[string]any{
			"resultType": "discover",
			"protocolVersion": "2026-07-28",
			"supportedVersions": supportedVersions,
			"capabilities": map[string]any{"tools": map[string]any{}},
			"instructions": "Use tools/list and tools/call to operate the forge.",
			"serverInfo": serverInfo,
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion": "2026-07-28",
				"io.modelcontextprotocol/serverInfo": serverInfo,
			},
		})
	case "initialize":
		offered := ""
		if v, ok := params["protocolVersion"].(string); ok { offered = v }
		chosen := "2026-07-28"
		for _, v := range legacyVersions { if v == offered { chosen = offered; break } }
		return resultResp(id, map[string]any{
			"protocolVersion": chosen,
			"capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": serverInfo,
			"instructions": "Use tools/list and tools/call to operate the forge.",
		})
	case "notifications/initialized":
		return nil
	case "tools/list":
		return resultResp(id, map[string]any{"tools": tools})
	case "tools/call":
		name := textArg(params, []string{"name", "tool", "tool_name"})
		if name == "" { return rpcError(id, -32602, "Missing required argument: name", 200) }
		return callTool(id, name, params["arguments"])
	case "ping":
		return resultResp(id, map[string]any{})
	default:
		return rpcError(id, -32601, "Method not found: "+method, 200)
	}
}

func handleMcp(method, rawBody string) Resp {
	if method != "POST" {
		return jresp(405, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32600, "message": "Method not allowed"}}, nil)
	}
	var parsed any
	if err := json.Unmarshal([]byte(rawBody), &parsed); err != nil {
		return jresp(400, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "Parse error"}}, nil)
	}
	if items, ok := parsed.([]any); ok {
		answers := []any{}
		for _, item := range items {
			if answer := dispatch(recordAny(item)); answer != nil { answers = append(answers, answer.Value) }
		}
		if len(answers) == 0 { return emptyResp(202) }
		return jresp(200, answers, nil)
	}
	answer := dispatch(recordAny(parsed))
	if answer == nil { return emptyResp(202) }
	return *answer
}
