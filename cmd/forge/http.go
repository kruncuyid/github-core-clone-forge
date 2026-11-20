package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
)

type Resp struct {
	Status  int
	Body    []byte
	Headers map[string]string
	Value   any
}

func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil { return nil, err }
	return b.Bytes(), nil
}

func compactJSON(v any) string {
	b, err := marshalJSON(v)
	if err != nil { return "{}" }
	return strings.TrimSuffix(string(b), "\n")
}

func jresp(status int, v any, headers map[string]string) Resp {
	if headers == nil { headers = map[string]string{} }
	body, err := marshalJSON(v)
	if err != nil {
		return Resp{Status: 500, Body: []byte("{\"message\":\"Internal Server Error\"}\n"), Headers: map[string]string{"content-type": "application/json; charset=utf-8"}}
	}
	if _, ok := headers["content-type"]; !ok { headers["content-type"] = "application/json; charset=utf-8" }
	return Resp{Status: status, Body: body, Headers: headers, Value: v}
}

func rawResp(status int, body []byte, headers map[string]string) Resp {
	return Resp{Status: status, Body: body, Headers: headers}
}

func emptyResp(status int) Resp { return Resp{Status: status} }

func jsonError(status int, message string) Resp {
	return jresp(status, map[string]any{"message": message}, nil)
}

type gitPathInfo struct {
	owner    string
	name     string
	endpoint string
}

func parseGitPath(path string) (gitPathInfo, bool) {
	endpoint := ""
	for _, candidate := range []string{"/info/refs", "/git-upload-pack", "/git-receive-pack"} {
		if strings.HasSuffix(path, candidate) {
			endpoint = strings.TrimPrefix(candidate, "/")
			path = strings.TrimSuffix(path, candidate)
			break
		}
	}
	if endpoint == "" || !strings.HasPrefix(path, "/") { return gitPathInfo{}, false }
	rest := path[1:]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 { return gitPathInfo{}, false }
	owner := rest[:slash]
	rest = rest[slash+1:]
	if strings.IndexByte(rest, '/') >= 0 || !strings.HasSuffix(rest, ".git") { return gitPathInfo{}, false }
	name := rest[:len(rest)-4]
	if owner == "" || name == "" { return gitPathInfo{}, false }
	return gitPathInfo{owner: owner, name: name, endpoint: endpoint}, true
}

func isGitPath(path string) bool { _, ok := parseGitPath(path); return ok }

func authorized(r *http.Request) bool {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") { return false }
	return fields[1] == st.token
}

func lockFor(method, path string) func() {
	switch {
	case isGitPath(path):
		git, _ := parseGitPath(path)
		if method == "POST" && git.endpoint == "git-receive-pack" {
			st.mu.Lock()
			return st.mu.Unlock
		}
		st.mu.RLock()
		return st.mu.RUnlock
	case strings.HasPrefix(path, "/api/v3"):
		if method == "GET" {
			st.mu.RLock()
			return st.mu.RUnlock
		}
		st.mu.Lock()
		return st.mu.Unlock
	case path == "/api/graphql" || path == "/mcp":
		if path == "/mcp" && method == "DELETE" {
			st.mu.RLock()
			return st.mu.RUnlock
		}
		st.mu.Lock()
		return st.mu.Unlock
	default:
		st.mu.RLock()
		return st.mu.RUnlock
	}
}

func serveHTTP(w http.ResponseWriter, r *http.Request) {
	var idbuf [32]byte
	reqID := "req-" + string(strconv.AppendUint(idbuf[:0], atomic.AddUint64(&st.req, 1), 10))
	path := r.URL.EscapedPath()
	if path == "" { path = "/" }
	var body []byte
	if (r.Method != "GET" && r.Method != "HEAD") || r.ContentLength > 0 {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			writeResp(w, jsonError(500, "Internal Server Error"), reqID)
			return
		}
	}
	unlock := lockFor(r.Method, path)
	resp := routeLocked(r, path, body, reqID)
	unlock()
	writeResp(w, resp, reqID)
}

func routeLocked(r *http.Request, path string, body []byte, reqID string) Resp {
	method := r.Method
	if method == "GET" && path == "/healthz" {
		return jresp(200, map[string]any{"ok": true}, nil)
	}

	if isGitPath(path) && method == "GET" {
		if answer := gitRouteLocked(r, path, nil, reqID); answer != nil { return *answer }
	}

	if strings.HasPrefix(path, "/api/v3") {
		if !authorized(r) { return jsonError(401, "Requires authentication") }
		if method == "GET" && r.URL.RawQuery == "" && strings.HasPrefix(path, "/api/v3/repos/") {
			if fast := cachedIssueGET(path); fast != nil { return *fast }
		}
		apiURL := *r.URL
		if apiURL.Scheme == "" { apiURL.Scheme = "http" }
		if apiURL.Host == "" { apiURL.Host = r.Host }
		return handleRest(method, path, &apiURL, string(body))
	}

	if path == "/api/graphql" {
		if !authorized(r) {
			return jresp(401, map[string]any{"errors": []any{map[string]any{"message": "Requires authentication"}}}, nil)
		}
		var parsed any
		if len(body) == 0 || json.Unmarshal(body, &parsed) != nil {
			return jresp(400, map[string]any{"errors": []any{map[string]any{"message": "Parse error"}}}, nil)
		}
		return handleGraphQL(parsed)
	}

	if path == "/mcp" {
		if method == "DELETE" { return emptyResp(204) }
		if !authorized(r) { return jsonError(401, "Requires authentication") }
		if method != "POST" { return jsonError(405, "Method Not Allowed") }
		return handleMcp(method, string(body))
	}

	if isGitPath(path) && method == "POST" {
		if answer := gitRouteLocked(r, path, body, reqID); answer != nil { return *answer }
	}

	return jsonError(404, "Not Found")
}

func gitRouteLocked(r *http.Request, path string, body []byte, reqID string) *Resp {
	git, ok := parseGitPath(path)
	if !ok { return nil }
	owner, err1 := url.PathUnescape(git.owner)
	name, err2 := url.PathUnescape(git.name)
	if err1 != nil || err2 != nil { a := jsonError(404, "Not Found"); return &a }
	repo := st.GetRepo(owner, name)
	if repo == nil { a := jsonError(404, "Not Found"); return &a }
	endpoint := git.endpoint
	serviceParam := r.URL.Query().Get("service")
	v2 := strings.Contains(r.Header.Get("Git-Protocol"), "version=2")
	if r.Method == "GET" && endpoint == "info/refs" {
		service := ""
		if serviceParam == "git-upload-pack" { service = "upload" } else if serviceParam == "git-receive-pack" { service = "receive" }
		if service == "" { a := jsonError(404, "Not Found"); return &a }
		a := rawResp(200, advertisement(repo, service, service == "upload" && v2), map[string]string{
			"content-type": "application/x-git-" + service + "-pack-advertisement",
			"cache-control": "no-cache",
		})
		return &a
	}
	if r.Method == "POST" && endpoint == "git-upload-pack" {
		a := rawResp(200, handleUploadPack(repo, body, v2), map[string]string{"content-type": "application/x-git-upload-pack-result"})
		return &a
	}
	if r.Method == "POST" && endpoint == "git-receive-pack" {
		a := rawResp(200, handleReceivePack(st, repo, body, st.NowISO()), map[string]string{"content-type": "application/x-git-receive-pack-result"})
		return &a
	}
	a := jsonError(404, "Not Found")
	return &a
}

func writeResp(w http.ResponseWriter, resp Resp, reqID string) {
	headers := resp.Headers
	if headers == nil { headers = map[string]string{} }
	for k, v := range headers { w.Header().Set(k, v) }
	if w.Header().Get("x-github-request-id") == "" { w.Header().Set("x-github-request-id", reqID) }
	if resp.Status != 204 && resp.Body != nil {
		if w.Header().Get("content-type") == "" { w.Header().Set("content-type", "application/json; charset=utf-8") }
		w.Header().Set("content-length", strconv.Itoa(len(resp.Body)))
	}
	w.WriteHeader(resp.Status)
	if resp.Status != 204 && resp.Body != nil { _, _ = w.Write(resp.Body) }
}
