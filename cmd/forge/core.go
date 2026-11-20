package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ObjType string

const (
	objBlob   ObjType = "blob"
	objTree   ObjType = "tree"
	objCommit ObjType = "commit"
	objTag    ObjType = "tag"
)

type ForgeObject struct {
	Type ObjType
	Data []byte
}

type TreeEntry struct {
	Mode string
	Name string
	Sha  string
}

type CommitInfo struct {
	Sha     string
	Tree    string
	Parents []string
	Message string
	Raw     string
}

type FileEntry struct {
	Sha  string
	Mode string
}

type FileMap struct {
	m     map[string]FileEntry
	order []string
}

func newFileMap() *FileMap { return &FileMap{m: map[string]FileEntry{}} }

func (f *FileMap) Get(path string) (FileEntry, bool) {
	v, ok := f.m[path]
	return v, ok
}

func (f *FileMap) Set(path string, v FileEntry) {
	if _, ok := f.m[path]; !ok {
		f.order = append(f.order, path)
	}
	f.m[path] = v
}

func (f *FileMap) Delete(path string) bool {
	if _, ok := f.m[path]; !ok {
		return false
	}
	delete(f.m, path)
	for i, p := range f.order {
		if p == path {
			f.order = append(f.order[:i], f.order[i+1:]...)
			break
		}
	}
	return true
}

func (f *FileMap) Keys() []string { return f.order }
func (f *FileMap) Len() int       { return len(f.order) }

type Comment struct {
	ID        int
	Body      string
	Author    string
	CreatedAt string
}

type Issue struct {
	Number    int
	Title     string
	Body      string
	State     string
	Labels    []string
	ID        string
	Comments  []*Comment
	CreatedAt string
	UpdatedAt string
	Version   uint64
}

type PullFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
}

type Pull struct {
	Number         int
	Title          string
	Body           string
	State          string
	Head           string
	Base           string
	HeadSha        string
	BaseSha        string
	Merged         bool
	MergeCommitSha *string
	MergedAt       *string
	ID             string
	CreatedAt      string
	UpdatedAt      string
	Files          []*PullFile
}

type Repo struct {
	Owner         string
	Name          string
	Private       bool
	Description   string
	DefaultBranch string
	ID            int
	DatabaseID    int
	NodeID        string
	CreatedAt     string
	PushedAt      string
	UpdatedAt     string
	Refs          map[string]string
	RefOrder      []string
	Objects       map[string]*ForgeObject
	Issues        []*Issue
	Pulls         []*Pull
	Labels        []string
	NextIssue     int
	NextPull      int
	Parents       map[string][]string
	Version       uint64
	CacheMu       sync.Mutex
	AdCache       map[string][]byte
	FetchCache    map[string][]byte
	IssueCache    map[string][]byte
}

type State struct {
	mu            sync.RWMutex
	repos         map[string]*Repo
	repoOrder     []string
	nextRepoID    int
	nextCommentID int
	viewer        string
	viewerName    string
	viewerEmail   string
	token         string
	now           int64
	tick          int64
	req           uint64
}

var st = newState()

var tagTargetRE = regexp.MustCompile(`(?m)^object ([0-9a-f]{40})$`)

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func parseNow() int64 {
	raw := os.Getenv("MAKEFUN_TRIAL_NOW")
	if raw == "" {
		return 1700000000
	}
	if n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
		if n > 1e12 {
			return int64(n / 1000)
		}
		return int64(n)
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Unix()
	}
	return 1700000000
}

func newState() *State {
	viewer := envOr("MAKEFUN_FORGE_LOGIN", "adaiversen")
	return &State{
		repos:         map[string]*Repo{},
		nextRepoID:    100,
		nextCommentID: 1,
		viewer:        viewer,
		viewerName:    "ada iversen",
		viewerEmail:   viewer + "@example.invalid",
		token:         envOr("MAKEFUN_SHOP_TOKEN", "makefun-trial-token"),
		now:           parseNow(),
	}
}

func (s *State) NowSec() int64 {
	v := s.now + s.tick
	s.tick++
	return v
}

func (s *State) NowISO() string { return iso(s.now) }

func iso(ts int64) string {
	return time.Unix(ts, 0).UTC().Format("2006-01-02T15:04:05Z")
}

func sha1Hex(data []byte) string {
	h := sha1.Sum(data)
	return hex.EncodeToString(h[:])
}

func hashObject(t ObjType, data []byte) string {
	header := []byte(fmt.Sprintf("%s %d\x00", t, len(data)))
	return sha1Hex(append(header, data...))
}

func u32(n uint32) []byte {
	return []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
}

func encodePackHeader(code, size int) []byte {
	out := []byte{}
	s := uint64(size)
	first := byte((code << 4) | int(s&0x0f))
	s >>= 4
	for s > 0 {
		out = append(out, first|0x80)
		first = byte(s & 0x7f)
		s >>= 7
	}
	return append(out, first)
}

func deflateBytes(data []byte) []byte {
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	_, _ = zw.Write(data)
	_ = zw.Close()
	return b.Bytes()
}

func writePack(objects []*ForgeObject) []byte {
	parts := [][]byte{[]byte("PACK"), u32(2), u32(uint32(len(objects)))}
	for _, obj := range objects {
		code := 4
		switch obj.Type {
		case objCommit:
			code = 1
		case objTree:
			code = 2
		case objBlob:
			code = 3
		}
		parts = append(parts, encodePackHeader(code, len(obj.Data)), deflateBytes(obj.Data))
	}
	body := bytes.Join(parts, nil)
	return append(body, mustHexDecode(sha1Hex(body))...)
}

func mustHexDecode(s string) []byte {
	b, _ := hex.DecodeString(s)
	return b
}

type rawPackEntry struct {
	offset    int
	code      int
	size      int
	data      []byte
	baseOff   int
	baseOid   string
	hasBaseOf bool
	hasBaseOid bool
}

func readVarint(buf []byte, at int) (int, int, error) {
	var value uint64
	var shift uint
	pos := at
	for {
		if pos >= len(buf) {
			return 0, 0, fmt.Errorf("truncated varint")
		}
		b := buf[pos]
		pos++
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return int(value), pos, nil
		}
		shift += 7
		if shift > 63 {
			return 0, 0, fmt.Errorf("varint overflow")
		}
	}
}

func readOfsOffset(buf []byte, at int) (int, int, error) {
	if at >= len(buf) {
		return 0, 0, fmt.Errorf("truncated ofs")
	}
	c := buf[at]
	pos := at + 1
	value := int(c & 0x7f)
	for c&0x80 != 0 {
		if pos >= len(buf) {
			return 0, 0, fmt.Errorf("truncated ofs")
		}
		c = buf[pos]
		pos++
		value = ((value + 1) << 7) | int(c&0x7f)
	}
	return value, pos, nil
}

func applyDelta(base, delta []byte) ([]byte, error) {
	pos := 0
	_, pos, err := readVarint(delta, pos)
	if err != nil {
		return nil, err
	}
	resultSize, pos, err := readVarint(delta, pos)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, resultSize)
	for pos < len(delta) {
		op := delta[pos]
		pos++
		if op&0x80 != 0 {
			off := 0
			size := 0
			if op&0x01 != 0 {
				if pos >= len(delta) { return nil, fmt.Errorf("bad delta") }
				off = int(delta[pos]); pos++
			}
			if op&0x02 != 0 {
				if pos >= len(delta) { return nil, fmt.Errorf("bad delta") }
				off |= int(delta[pos]) << 8; pos++
			}
			if op&0x04 != 0 {
				if pos >= len(delta) { return nil, fmt.Errorf("bad delta") }
				off |= int(delta[pos]) << 16; pos++
			}
			if op&0x08 != 0 {
				if pos >= len(delta) { return nil, fmt.Errorf("bad delta") }
				off |= int(delta[pos]) << 24; pos++
			}
			if op&0x10 != 0 {
				if pos >= len(delta) { return nil, fmt.Errorf("bad delta") }
				size = int(delta[pos]); pos++
			}
			if op&0x20 != 0 {
				if pos >= len(delta) { return nil, fmt.Errorf("bad delta") }
				size |= int(delta[pos]) << 8; pos++
			}
			if op&0x40 != 0 {
				if pos >= len(delta) { return nil, fmt.Errorf("bad delta") }
				size |= int(delta[pos]) << 16; pos++
			}
			if size == 0 {
				size = 0x10000
			}
			if off < 0 || size < 0 || off+size > len(base) {
				return nil, fmt.Errorf("bad delta range")
			}
			out = append(out, base[off:off+size]...)
		} else if op != 0 {
			if pos+int(op) > len(delta) {
				return nil, fmt.Errorf("bad delta literal")
			}
			out = append(out, delta[pos:pos+int(op)]...)
			pos += int(op)
		} else {
			return nil, fmt.Errorf("invalid delta opcode 0")
		}
	}
	return out, nil
}

type countingReader struct {
	r *bytes.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func inflateOne(pack []byte, start, limit, size int) ([]byte, int, error) {
	cr := &countingReader{r: bytes.NewReader(pack[start:limit])}
	br := bufio.NewReader(cr)
	zr, err := zlib.NewReader(br)
	if err != nil {
		return nil, 0, err
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		_ = zr.Close()
		return nil, 0, err
	}
	if len(data) != size {
		_ = zr.Close()
		return nil, 0, fmt.Errorf("inflated size mismatch")
	}
	if err := zr.Close(); err != nil {
		return nil, 0, err
	}
	consumed := cr.n - br.Buffered()
	if consumed <= 0 {
		return nil, 0, fmt.Errorf("bad compressed stream")
	}
	return data, start + consumed, nil
}

func readPack(pack []byte, externals map[string]*ForgeObject) (map[string]*ForgeObject, error) {
	if len(pack) < 32 || string(pack[:4]) != "PACK" {
		return nil, fmt.Errorf("not a pack")
	}
	version := int(u32be(pack[4:8]))
	if version != 2 && version != 3 {
		return nil, fmt.Errorf("bad pack version")
	}
	count := int(u32be(pack[8:12]))
	trailer := len(pack) - 20
	if sha1Hex(pack[:trailer]) != hex.EncodeToString(pack[trailer:]) {
		return nil, fmt.Errorf("bad pack checksum")
	}
	raw := []*rawPackEntry{}
	pos := 12
	for i := 0; i < count; i++ {
		if pos >= trailer { return nil, fmt.Errorf("truncated pack") }
		offset := pos
		c := pack[pos]
		pos++
		code := int((c >> 4) & 0x07)
		size := int(c & 0x0f)
		shift := uint(4)
		for c&0x80 != 0 {
			if pos >= trailer { return nil, fmt.Errorf("truncated pack header") }
			c = pack[pos]
			pos++
			size |= int(c&0x7f) << shift
			shift += 7
		}
		e := &rawPackEntry{offset: offset, code: code, size: size}
		if code == 6 {
			v, next, err := readOfsOffset(pack, pos)
			if err != nil { return nil, err }
			e.baseOff = offset - v
			e.hasBaseOf = true
			pos = next
		}
		if code == 7 {
			if pos+20 > trailer { return nil, fmt.Errorf("truncated ref delta") }
			e.baseOid = hex.EncodeToString(pack[pos : pos+20])
			e.hasBaseOid = true
			pos += 20
		}
		data, next, err := inflateOne(pack, pos, trailer, size)
		if err != nil { return nil, err }
		e.data = data
		pos = next
		raw = append(raw, e)
	}
	if pos != trailer {
		return nil, fmt.Errorf("pack length mismatch")
	}
	byOffset := map[int]*rawPackEntry{}
	for _, e := range raw {
		byOffset[e.offset] = e
	}
	result := map[string]*ForgeObject{}
	resolved := map[int]*ForgeObject{}
	resolving := map[int]bool{}
	var rawType func(*rawPackEntry) ObjType
	var baseType func(*rawPackEntry) ObjType
	baseType = func(e *rawPackEntry) ObjType {
		switch e.code {
		case 1: return objCommit
		case 2: return objTree
		case 3: return objBlob
		case 4: return objTag
		case 6:
			if p := byOffset[e.baseOff]; p != nil { return baseType(p) }
		case 7:
			for _, r := range raw {
				if hashObject(rawType(r), r.data) == e.baseOid { return rawType(r) }
			}
		}
		return objBlob
	}
	rawType = func(e *rawPackEntry) ObjType {
		switch e.code {
		case 1: return objCommit
		case 2: return objTree
		case 3: return objBlob
		case 4: return objTag
		}
		return baseType(e)
	}
	var resolve func(*rawPackEntry) (*ForgeObject, error)
	resolve = func(e *rawPackEntry) (*ForgeObject, error) {
		if obj, ok := resolved[e.offset]; ok { return obj, nil }
		if resolving[e.offset] { return nil, fmt.Errorf("delta cycle") }
		resolving[e.offset] = true
		defer delete(resolving, e.offset)
		var typ ObjType
		var data []byte
		switch e.code {
		case 1, 2, 3, 4:
			typ = rawType(e)
			data = e.data
		default:
			var base *ForgeObject
			if e.hasBaseOf {
				parent := byOffset[e.baseOff]
				if parent == nil { return nil, fmt.Errorf("missing delta base offset") }
				var err error
				base, err = resolve(parent)
				if err != nil { return nil, err }
			} else {
				base = result[e.baseOid]
				if base == nil && externals != nil { base = externals[e.baseOid] }
				if base == nil {
					for _, r := range raw {
						if hashObject(rawType(r), r.data) == e.baseOid {
							var err error
							base, err = resolve(r)
							if err != nil { return nil, err }
							break
						}
					}
				}
			}
			if base == nil { return nil, fmt.Errorf("missing delta base") }
			var err error
			data, err = applyDelta(base.Data, e.data)
			if err != nil { return nil, err }
			typ = base.Type
		}
		obj := &ForgeObject{Type: typ, Data: data}
		resolved[e.offset] = obj
		result[hashObject(typ, data)] = obj
		return obj, nil
	}
	for _, e := range raw {
		if _, err := resolve(e); err != nil { return nil, err }
	}
	return result, nil
}

func u32be(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func parseTree(data []byte) ([]TreeEntry, error) {
	out := []TreeEntry{}
	i := 0
	for i < len(data) {
		sp := bytes.IndexByte(data[i:], 0x20)
		if sp < 0 { return nil, fmt.Errorf("bad tree") }
		sp += i
		nul := bytes.IndexByte(data[sp+1:], 0)
		if nul < 0 { return nil, fmt.Errorf("bad tree") }
		nul += sp + 1
		if nul+21 > len(data) { return nil, fmt.Errorf("bad tree") }
		out = append(out, TreeEntry{
			Mode: string(data[i:sp]),
			Name: string(data[sp+1 : nul]),
			Sha:  hex.EncodeToString(data[nul+1 : nul+21]),
		})
		i = nul + 21
	}
	return out, nil
}

func writeTreeFor(repo *Repo, entries []TreeEntry) string {
	sorted := append([]TreeEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare([]byte(sorted[i].Name), []byte(sorted[j].Name)) < 0
	})
	parts := [][]byte{}
	for _, e := range sorted {
		parts = append(parts, []byte(e.Mode+" "+e.Name+"\x00"), mustHexDecode(e.Sha))
	}
	return putObject(repo, objTree, bytes.Join(parts, nil))
}

type flatNode struct {
	files map[string]FileEntry
	dirs  map[string]*flatNode
}

func writeNode(repo *Repo, node *flatNode) string {
	entries := []TreeEntry{}
	for name, v := range node.files {
		entries = append(entries, TreeEntry{Mode: v.Mode, Name: name, Sha: v.Sha})
	}
	for name, dir := range node.dirs {
		entries = append(entries, TreeEntry{Mode: "40000", Name: name, Sha: writeNode(repo, dir)})
	}
	return writeTreeFor(repo, entries)
}

func treeFromFiles(repo *Repo, files *FileMap) string {
	root := &flatNode{files: map[string]FileEntry{}, dirs: map[string]*flatNode{}}
	for _, path := range files.Keys() {
		v, _ := files.Get(path)
		segments := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
		if len(segments) == 0 { continue }
		node := root
		for _, seg := range segments[:len(segments)-1] {
			child := node.dirs[seg]
			if child == nil {
				child = &flatNode{files: map[string]FileEntry{}, dirs: map[string]*flatNode{}}
				node.dirs[seg] = child
			}
			node = child
		}
		node.files[segments[len(segments)-1]] = v
	}
	return writeNode(repo, root)
}

func flattenTree(repo *Repo, treeSha, prefix string) *FileMap {
	out := newFileMap()
	obj := repo.Objects[treeSha]
	if obj == nil || obj.Type != objTree { return out }
	entries, err := parseTree(obj.Data)
	if err != nil { return out }
	for _, entry := range entries {
		path := entry.Name
		if prefix != "" { path = prefix + "/" + entry.Name }
		if entry.Mode == "160000" { continue }
		if entry.Mode == "40000" || entry.Mode == "040000" {
			sub := flattenTree(repo, entry.Sha, path)
			for _, k := range sub.Keys() {
				v, _ := sub.Get(k)
				out.Set(k, v)
			}
		} else {
			out.Set(path, FileEntry{Sha: entry.Sha, Mode: entry.Mode})
		}
	}
	return out
}

func parseCommitRaw(raw string) (string, []string, string) {
	tree := ""
	parents := []string{}
	split := strings.Index(raw, "\n\n")
	head := raw
	message := ""
	if split >= 0 {
		head = raw[:split]
		message = raw[split+2:]
	}
	for _, line := range strings.Split(head, "\n") {
		if strings.HasPrefix(line, "tree ") {
			tree = strings.TrimPrefix(line, "tree ")
		} else if strings.HasPrefix(line, "parent ") {
			parents = append(parents, strings.TrimPrefix(line, "parent "))
		}
	}
	return tree, parents, message
}

func commitInfo(repo *Repo, sha string) *CommitInfo {
	obj := repo.Objects[sha]
	if obj == nil || obj.Type != objCommit { return nil }
	raw := string(obj.Data)
	tree, parents, message := parseCommitRaw(raw)
	return &CommitInfo{Sha: sha, Tree: tree, Parents: parents, Message: message, Raw: raw}
}

func parentList(repo *Repo, sha string) []string {
	if p, ok := repo.Parents[sha]; ok { return p }
	info := commitInfo(repo, sha)
	p := []string{}
	if info != nil { p = info.Parents }
	repo.Parents[sha] = p
	return p
}

func ancestorsSet(repo *Repo, sha string, depth int, includeSelf bool) map[string]bool {
	out := map[string]bool{}
	type item struct{ sha string; level int }
	queue := []item{}
	if includeSelf { queue = append(queue, item{sha, 0}) }
	seen := map[string]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur.sha] { continue }
		seen[cur.sha] = true
		if repo.Objects[cur.sha] == nil { continue }
		out[cur.sha] = true
		if depth >= 0 && cur.level+1 > depth { continue }
		for _, p := range parentList(repo, cur.sha) {
			queue = append(queue, item{p, cur.level + 1})
		}
	}
	return out
}

func reachableObjects(repo *Repo, tips, excluded []string) map[string]*ForgeObject {
	blocked := map[string]bool{}
	for _, sha := range excluded {
		for k := range ancestorsSet(repo, sha, -1, true) { blocked[k] = true }
	}
	out := map[string]*ForgeObject{}
	queue := append([]string(nil), tips...)
	seen := map[string]bool{}
	for len(queue) > 0 {
		sha := queue[0]
		queue = queue[1:]
		if seen[sha] { continue }
		seen[sha] = true
		obj := repo.Objects[sha]
		if obj == nil { continue }
		out[sha] = obj
		switch obj.Type {
		case objCommit:
			info := commitInfo(repo, sha)
			if info != nil {
				for _, p := range info.Parents {
					if !blocked[p] { queue = append(queue, p) }
				}
				files := flattenTree(repo, info.Tree, "")
				for _, k := range files.Keys() {
					if v, ok := files.Get(k); ok && !blocked[v.Sha] {
						queue = append(queue, v.Sha)
					}
				}
				if !blocked[info.Tree] { queue = append(queue, info.Tree) }
			}
		case objTree:
			entries, _ := parseTree(obj.Data)
			for _, e := range entries {
				if e.Mode != "160000" { queue = append(queue, e.Sha) }
			}
		case objTag:
			m := tagTargetRE.FindStringSubmatch(string(obj.Data))
			if len(m) > 1 { queue = append(queue, m[1]) }
		}
	}
	for sha := range blocked { delete(out, sha) }
	return out
}

func lineStats(data []byte) (int, int) {
	if len(data) == 0 { return 0, 0 }
	return len(strings.Split(string(data), "\n")), 0
}

func compareTrees(repo *Repo, baseSha, headSha string) []*PullFile {
	baseTree := baseSha
	headTree := headSha
	if info := commitInfo(repo, baseSha); info != nil { baseTree = info.Tree }
	if info := commitInfo(repo, headSha); info != nil { headTree = info.Tree }
	base := flattenTree(repo, baseTree, "")
	head := flattenTree(repo, headTree, "")
	names := map[string]bool{}
	for _, k := range base.Keys() { names[k] = true }
	for _, k := range head.Keys() { names[k] = true }
	sorted := make([]string, 0, len(names))
	for k := range names { sorted = append(sorted, k) }
	sort.Strings(sorted)
	out := []*PullFile{}
	for _, name := range sorted {
		b, okB := base.Get(name)
		h, okH := head.Get(name)
		if okB && okH && b.Sha == h.Sha { continue }
		add, del := 0, 0
		if obj := repo.Objects[h.Sha]; obj != nil { add, del = lineStats(obj.Data) }
		status := "modified"
		if !okB { status = "added" } else if !okH { status = "removed" }
		out = append(out, &PullFile{Filename: name, Status: status, Additions: add, Deletions: del, Changes: add + del})
	}
	return out
}

func nodeID(kind string, parts ...any) string {
	all := []string{kind}
	for _, p := range parts {
		switch v := p.(type) {
		case string: all = append(all, v)
		case int: all = append(all, strconv.Itoa(v))
		default: all = append(all, fmt.Sprint(v))
		}
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(all, ":")))
}

func parseNodeID(id string) []string {
	b, err := base64.StdEncoding.DecodeString(id)
	if err != nil { return []string{} }
	return strings.Split(string(b), ":")
}

func repoKey(owner, name string) string { return owner + "/" + name }

func (s *State) GetRepo(owner, name string) *Repo { return s.repos[repoKey(owner, name)] }

func (s *State) SortedRepos() []*Repo {
	out := make([]*Repo, 0, len(s.repoOrder))
	for _, k := range s.repoOrder {
		if r := s.repos[k]; r != nil { out = append(out, r) }
	}
	return out
}

func (s *State) CreateRepo(owner, name string, private bool, description string) *Repo {
	id := s.nextRepoID
	s.nextRepoID++
	created := s.NowISO()
	r := &Repo{
		Owner: owner, Name: name, Private: private, Description: description, DefaultBranch: "main",
		ID: id, DatabaseID: id, NodeID: nodeID("Repository", id), CreatedAt: created, PushedAt: created, UpdatedAt: created,
		Refs: map[string]string{}, Objects: map[string]*ForgeObject{}, Issues: []*Issue{}, Pulls: []*Pull{},
		Labels: []string{"bug", "enhancement", "documentation", "question", "good first issue", "help wanted"},
		NextIssue: 1, NextPull: 1, Parents: map[string][]string{},
		AdCache: map[string][]byte{}, FetchCache: map[string][]byte{}, IssueCache: map[string][]byte{},
	}
	key := repoKey(owner, name)
	s.repos[key] = r
	s.repoOrder = append(s.repoOrder, key)
	return r
}

func (s *State) DeleteRepo(owner, name string) {
	key := repoKey(owner, name)
	delete(s.repos, key)
	for i, k := range s.repoOrder {
		if k == key {
			s.repoOrder = append(s.repoOrder[:i], s.repoOrder[i+1:]...)
			break
		}
	}
}

func setRef(repo *Repo, ref, oid string) {
	if _, ok := repo.Refs[ref]; !ok {
		repo.RefOrder = append(repo.RefOrder, ref)
	}
	repo.Refs[ref] = oid
	bump(repo)
}

func deleteRef(repo *Repo, ref string) {
	if _, ok := repo.Refs[ref]; !ok { return }
	delete(repo.Refs, ref)
	for i, k := range repo.RefOrder {
		if k == ref {
			repo.RefOrder = append(repo.RefOrder[:i], repo.RefOrder[i+1:]...)
			break
		}
	}
	bump(repo)
}

func bump(repo *Repo) { repo.Version++ }

func cacheGet(repo *Repo, cache map[string][]byte, key string) []byte {
	repo.CacheMu.Lock()
	v := cache[key]
	repo.CacheMu.Unlock()
	return v
}

func cacheSet(repo *Repo, cache map[string][]byte, key string, value []byte) {
	repo.CacheMu.Lock()
	cache[key] = value
	repo.CacheMu.Unlock()
}

type refPair struct{ Ref, OID string }

func repoRefs(repo *Repo) []refPair {
	out := make([]refPair, 0, len(repo.RefOrder)+1)
	for _, ref := range repo.RefOrder {
		if oid, ok := repo.Refs[ref]; ok { out = append(out, refPair{ref, oid}) }
	}
	head := repo.Refs["refs/heads/"+repo.DefaultBranch]
	if head == "" { head = zeroOID() }
	found := false
	for i := range out {
		if out[i].Ref == "HEAD" { out[i].OID = head; found = true; break }
	}
	if !found { out = append(out, refPair{"HEAD", head}) }
	return out
}

func zeroOID() string { return strings.Repeat("0", 40) }

func (s *State) NextCommentID() int {
	v := s.nextCommentID
	s.nextCommentID++
	return v
}

func putObject(repo *Repo, typ ObjType, data []byte) string {
	oid := hashObject(typ, data)
	if repo.Objects[oid] == nil {
		repo.Objects[oid] = &ForgeObject{Type: typ, Data: append([]byte(nil), data...)}
	}
	return oid
}

func blobFor(repo *Repo, data []byte) string { return putObject(repo, objBlob, data) }

func makeCommit(s *State, repo *Repo, tree string, parents []string, message string) string {
	ident := fmt.Sprintf("%s <%s> %d +0000", s.viewer, s.viewerEmail, s.NowSec())
	var b strings.Builder
	b.WriteString("tree " + tree + "\n")
	for _, p := range parents { b.WriteString("parent " + p + "\n") }
	b.WriteString("author " + ident + "\ncommitter " + ident + "\n\n" + message + "\n")
	return putObject(repo, objCommit, []byte(b.String()))
}

func setFile(s *State, repo *Repo, branch, path string, data []byte, remove bool, message string) (string, string, bool) {
	ref := "refs/heads/" + branch
	oldTip := repo.Refs[ref]
	files := newFileMap()
	if oldTip != "" {
		info := commitInfo(repo, oldTip)
		if info != nil { files = flattenTree(repo, info.Tree, "") }
	}
	if remove {
		if !files.Delete(path) { return "", "", false }
	} else {
		files.Set(path, FileEntry{Sha: blobFor(repo, data), Mode: "100644"})
	}
	tree := treeFromFiles(repo, files)
	parents := []string{}
	if oldTip != "" { parents = append(parents, oldTip) }
	commit := makeCommit(s, repo, tree, parents, message)
	setRef(repo, ref, commit)
	repo.PushedAt = s.NowISO()
	repo.UpdatedAt = repo.PushedAt
	sha := ""
	if !remove { sha = blobFor(repo, data) }
	return sha, commit, true
}
