package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	uploadCaps  = "side-band-64k multi_ack_detailed thin-pack no-progress ofs-delta symref=HEAD:refs/heads/main"
	receiveCaps = "report-status side-band-64k delete-refs ofs-delta agent=makefun-forge/1"
)

func pkt(payload []byte) []byte {
	head := []byte(fmt.Sprintf("%04x", len(payload)+4))
	return append(head, payload...)
}

func pktText(text string) []byte { return pkt([]byte(text)) }

var flushPkt = []byte("0000")
var delimPkt = []byte("0001")

type packet struct {
	payload []byte
	start   int
	control string
}

func decodePackets(buf []byte) []packet {
	out := []packet{}
	pos := 0
	for pos+4 <= len(buf) {
		raw := string(buf[pos : pos+4])
		if raw == "0000" {
			out = append(out, packet{payload: nil, start: pos, control: "flush"})
			break
		}
		if raw == "0001" {
			out = append(out, packet{payload: nil, start: pos, control: "delim"})
			pos += 4
			continue
		}
		n, err := strconv.ParseInt(raw, 16, 32)
		if err != nil || n < 4 || pos+int(n) > len(buf) {
			break
		}
		out = append(out, packet{payload: buf[pos+4 : pos+int(n)], start: pos})
		pos += int(n)
	}
	return out
}

func stripNul(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 { return s[:i] }
	return s
}

func tagTarget(repo *Repo, oid string) string {
	obj := repo.Objects[oid]
	if obj == nil || obj.Type != objTag { return "" }
	m := tagTargetRE.FindStringSubmatch(string(obj.Data))
	if len(m) > 1 { return m[1] }
	return ""
}

func advertisement(repo *Repo, service string, v2 bool) []byte {
	key := service + "|" + strconv.FormatBool(v2) + "|" + strconv.FormatUint(repo.Version, 10)
	if cached := cacheGet(repo, repo.AdCache, key); cached != nil { return cached }
	out := [][]byte{}
	name := "git-upload-pack"
	if service == "receive" { name = "git-receive-pack" }
	out = append(out, pktText("# service="+name+"\n"))
	if service == "upload" && v2 {
		out = append(out, pktText("version 2\n"), pktText("fetch=shallow filter\n"), pktText("ls-refs=unborn\n"), pktText("object-format=sha1\n"), pktText("symrefs\n"), pktText("agent=makefun-forge/1\n"), flushPkt)
		result := bytes.Join(out, nil)
		cacheSet(repo, repo.AdCache, key, result)
		return result
	}
	refs := repoRefs(repo)
	caps := uploadCaps
	if service == "receive" { caps = receiveCaps }
	if len(refs) == 0 || (len(refs) == 1 && refs[0].Ref == "HEAD" && refs[0].OID == zeroOID()) {
		out = append(out, pktText(zeroOID()+" capabilities^{}\x00"+caps+"\n"), flushPkt)
		result := bytes.Join(out, nil)
		cacheSet(repo, repo.AdCache, key, result)
		return result
	}
	for i, ref := range refs {
		line := ref.OID + " " + ref.Ref
		if i == 0 { line += "\x00" + caps }
		out = append(out, pktText(line+"\n"))
	}
	out = append(out, flushPkt)
	result := bytes.Join(out, nil)
	cacheSet(repo, repo.AdCache, key, result)
	return result
}

func lsRefs(repo *Repo, body []byte) []byte {
	packets := decodePackets(body)
	prefixes := []string{}
	afterDelim := false
	for _, p := range packets {
		if p.control == "delim" { afterDelim = true; continue }
		if p.control == "flush" { continue }
		line := strings.TrimSuffix(string(p.payload), "\n")
		if !afterDelim { continue }
		if strings.HasPrefix(line, "ref-prefix ") {
			prefixes = append(prefixes, strings.TrimPrefix(line, "ref-prefix "))
		}
	}
	matches := func(ref string) bool {
		if len(prefixes) == 0 { return true }
		for _, p := range prefixes {
			if ref == p || strings.HasPrefix(ref, p) { return true }
		}
		return false
	}
	refs := repoRefs(repo)
	out := [][]byte{}
	if len(refs) == 1 && refs[0].Ref == "HEAD" && refs[0].OID == zeroOID() && matches("HEAD") {
		out = append(out, pktText("unborn HEAD symref-target:refs/heads/"+repo.DefaultBranch+"\n"))
	}
	for _, ref := range refs {
		if !matches(ref.Ref) { continue }
		line := ref.OID + " " + ref.Ref
		if ref.Ref == "HEAD" { line += " symref-target:refs/heads/" + repo.DefaultBranch }
		if peeled := tagTarget(repo, ref.OID); peeled != "" { line += " peeled:" + peeled }
		out = append(out, pktText(line+"\n"))
	}
	out = append(out, flushPkt)
	return bytes.Join(out, nil)
}

type fetchRequest struct {
	wants  []string
	haves  []string
	depth  int
	shallow []string
}

func newFetchRequest() fetchRequest { return fetchRequest{depth: -1} }

func parseFetchCommands(packets []packet) fetchRequest {
	req := newFetchRequest()
	command := ""
	afterDelim := false
	for _, p := range packets {
		if p.control == "delim" { afterDelim = true; continue }
		if p.control == "flush" { continue }
		line := strings.TrimSuffix(string(p.payload), "\n")
		if strings.HasPrefix(line, "command=") {
			command = strings.TrimPrefix(line, "command=")
			continue
		}
		if !afterDelim || command != "fetch" { continue }
		switch {
		case strings.HasPrefix(line, "want "):
			fields := strings.Fields(strings.TrimPrefix(line, "want "))
			if len(fields) > 0 { req.wants = append(req.wants, fields[0]) }
		case strings.HasPrefix(line, "have "):
			req.haves = append(req.haves, strings.TrimSpace(strings.TrimPrefix(line, "have ")))
		case strings.HasPrefix(line, "shallow "):
			req.shallow = append(req.shallow, strings.TrimSpace(strings.TrimPrefix(line, "shallow ")))
		case strings.HasPrefix(line, "deepen "):
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "deepen "))); err == nil { req.depth = n }
		}
	}
	return req
}

func parseV0Fetch(packets []packet) fetchRequest {
	req := newFetchRequest()
	section := 0
	for _, p := range packets {
		if p.control == "flush" { section++; continue }
		if p.control != "" { continue }
		line := strings.TrimSuffix(string(p.payload), "\n")
		switch {
		case strings.HasPrefix(line, "want "):
			if section == 0 {
				fields := strings.Fields(strings.TrimPrefix(line, "want "))
				if len(fields) > 0 { req.wants = append(req.wants, fields[0]) }
			}
		case strings.HasPrefix(line, "have "):
			req.haves = append(req.haves, strings.TrimSpace(strings.TrimPrefix(line, "have ")))
		case strings.HasPrefix(line, "shallow "):
			req.shallow = append(req.shallow, strings.TrimSpace(strings.TrimPrefix(line, "shallow ")))
		case strings.HasPrefix(line, "deepen "):
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "deepen "))); err == nil { req.depth = n }
		}
	}
	return req
}

func flattenEntries(repo *Repo, treeSha string) *FileMap {
	out := newFileMap()
	var walk func(string, string)
	walk = func(sha, prefix string) {
		obj := repo.Objects[sha]
		if obj == nil || obj.Type != objTree { return }
		entries, err := parseTree(obj.Data)
		if err != nil { return }
		for _, e := range entries {
			path := e.Name
			if prefix != "" { path = prefix + "/" + e.Name }
			if e.Mode == "160000" { continue }
			if e.Mode == "40000" || e.Mode == "040000" { walk(e.Sha, path) } else { out.Set(path, FileEntry{Sha: e.Sha, Mode: e.Mode}) }
		}
	}
	walk(treeSha, "")
	return out
}

func treeObjectIds(repo *Repo, treeSha string) map[string]bool {
	out := map[string]bool{}
	var walk func(string)
	walk = func(sha string) {
		obj := repo.Objects[sha]
		if obj == nil || obj.Type != objTree || out[sha] { return }
		out[sha] = true
		entries, err := parseTree(obj.Data)
		if err != nil { return }
		for _, e := range entries {
			if e.Mode == "160000" { continue }
			if child := repo.Objects[e.Sha]; child != nil && child.Type == objTree { walk(e.Sha) }
		}
	}
	walk(treeSha)
	return out
}

func objectsForFetch(repo *Repo, req fetchRequest) (map[string]*ForgeObject, []string) {
	if len(req.wants) == 0 { return map[string]*ForgeObject{}, []string{} }
	boundaries := map[string]bool{}
	for _, sha := range req.shallow { boundaries[sha] = true }
	excluded := map[string]bool{}
	type item struct{ sha string; level int }
	haveQueue := []item{}
	for _, sha := range req.haves { haveQueue = append(haveQueue, item{sha, 0}) }
	haveSeen := map[string]bool{}
	for len(haveQueue) > 0 {
		cur := haveQueue[0]
		haveQueue = haveQueue[1:]
		if haveSeen[cur.sha] { continue }
		haveSeen[cur.sha] = true
		obj := repo.Objects[cur.sha]
		if obj == nil { continue }
		excluded[cur.sha] = true
		switch obj.Type {
		case objCommit:
			info := commitInfo(repo, cur.sha)
			if info != nil {
				for sha := range treeObjectIds(repo, info.Tree) { excluded[sha] = true }
				files := flattenEntries(repo, info.Tree)
				for _, k := range files.Keys() {
					if v, ok := files.Get(k); ok { excluded[v.Sha] = true }
				}
				if !boundaries[cur.sha] {
					for _, p := range info.Parents { haveQueue = append(haveQueue, item{p, cur.level + 1}) }
				}
			}
		case objTree:
			entries, _ := parseTree(obj.Data)
			for _, e := range entries {
				if e.Mode != "160000" { excluded[e.Sha] = true }
			}
		}
	}
	selected := map[string]*ForgeObject{}
	shallowOut := []string{}
	shallowSeen := map[string]bool{}
	queue := []item{}
	for _, oid := range req.wants {
		if boundaries[oid] {
			if info := commitInfo(repo, oid); info != nil {
				for _, p := range info.Parents { queue = append(queue, item{p, 1}) }
			}
		} else {
			queue = append(queue, item{oid, 0})
		}
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur.sha] || excluded[cur.sha] || boundaries[cur.sha] { continue }
		seen[cur.sha] = true
		obj := repo.Objects[cur.sha]
		if obj == nil { continue }
		selected[cur.sha] = obj
		switch obj.Type {
		case objCommit:
			info := commitInfo(repo, cur.sha)
			if info != nil {
				queue = append(queue, item{info.Tree, cur.level})
				if req.depth >= 0 && cur.level+1 >= req.depth {
					if !shallowSeen[cur.sha] { shallowSeen[cur.sha] = true; shallowOut = append(shallowOut, cur.sha) }
				} else {
					for _, p := range info.Parents { queue = append(queue, item{p, cur.level + 1}) }
				}
			}
		case objTree:
			entries, _ := parseTree(obj.Data)
			for _, e := range entries {
				if e.Mode == "160000" { continue }
				queue = append(queue, item{e.Sha, cur.level})
			}
		case objTag:
			m := tagTargetRE.FindStringSubmatch(string(obj.Data))
			if len(m) > 1 { queue = append(queue, item{m[1], cur.level}) }
		}
	}
	for sha := range excluded { delete(selected, sha) }
	return selected, shallowOut
}

func sidebandPack(pack []byte) [][]byte {
	out := [][]byte{}
	for i := 0; i < len(pack); i += 65515 {
		end := i + 65515
		if end > len(pack) { end = len(pack) }
		chunk := append([]byte{1}, pack[i:end]...)
		out = append(out, pkt(chunk))
	}
	if len(pack) == 0 { out = append(out, pkt([]byte{1})) }
	return out
}

func fetchResponse(repo *Repo, req fetchRequest, v2 bool) []byte {
	objects, shallow := objectsForFetch(repo, req)
	shas := make([]string, 0, len(objects))
	for sha := range objects { shas = append(shas, sha) }
	sort.Strings(shas)
	ordered := make([]*ForgeObject, 0, len(shas))
	for _, sha := range shas { ordered = append(ordered, objects[sha]) }
	pack := writePack(ordered)
	out := [][]byte{}
	if v2 {
		out = append(out, pktText("acknowledgments\n"), pktText("NAK\n"), flushPkt)
		if len(shallow) > 0 {
			out = append(out, pktText("shallow-info\n"))
			for _, sha := range shallow { out = append(out, pktText("shallow "+sha+"\n")) }
			out = append(out, flushPkt)
		}
		out = append(out, pktText("packfile\n"))
		out = append(out, sidebandPack(pack)...)
		out = append(out, flushPkt)
	} else {
		out = append(out, pktText("NAK\n"))
		for _, sha := range shallow { out = append(out, pktText("shallow "+sha+"\n")) }
		out = append(out, sidebandPack(pack)...)
		out = append(out, flushPkt)
	}
	return bytes.Join(out, nil)
}

func handleUploadPack(repo *Repo, body []byte, v2 bool) []byte {
	packets := decodePackets(body)
	for _, p := range packets {
		text := string(p.payload)
		if strings.HasPrefix(text, "command=ls-refs") { return lsRefs(repo, body) }
	}
	req := parseFetchCommands(packets)
	if !v2 { req = parseV0Fetch(packets) }
	parts := []string{strconv.FormatBool(v2), strconv.FormatUint(repo.Version, 10)}
	parts = append(parts, req.wants...)
	parts = append(parts, req.haves...)
	parts = append(parts, req.shallow...)
	parts = append(parts, "depth="+strconv.Itoa(req.depth))
	key := strings.Join(parts, "|")
	if cached := cacheGet(repo, repo.FetchCache, key); cached != nil { return cached }
	result := fetchResponse(repo, req, v2)
	cacheSet(repo, repo.FetchCache, key, result)
	return result
}

type pushCommand struct{ old, next, ref string }

func parseReceiveCommands(packets []packet) ([]pushCommand, int) {
	commands := []pushCommand{}
	end := 0
	for _, p := range packets {
		if p.control == "flush" {
			end = p.start + 4
			break
		}
		end = p.start + len(p.payload) + 4
		text := strings.TrimSuffix(stripNul(string(p.payload)), "\n")
		fields := strings.Fields(text)
		if len(fields) >= 3 { commands = append(commands, pushCommand{fields[0], fields[1], fields[2]}) }
	}
	return commands, end
}

func reportPacket(text string) []byte { return pktText(text + "\n") }

func handleReceivePack(s *State, repo *Repo, body []byte, nowISO string) []byte {
	packets := decodePackets(body)
	commands, bodyEnd := parseReceiveCommands(packets)
	unpackOK := true
	if bodyEnd <= len(body) {
		packBytes := body[bodyEnd:]
		if len(packBytes) >= 12 && string(packBytes[:4]) == "PACK" {
			objects, err := readPack(packBytes, nil)
			if err != nil {
				unpackOK = false
			} else {
				for oid, obj := range objects {
					if repo.Objects[oid] == nil { repo.Objects[oid] = obj }
				}
			}
		}
	}
	report := [][]byte{}
	if unpackOK { report = append(report, reportPacket("unpack ok")) } else { report = append(report, reportPacket("unpack error")) }
	for _, cmd := range commands {
		if !unpackOK {
			report = append(report, reportPacket("ng "+cmd.ref+" unpack-error"))
			continue
		}
		if cmd.next == zeroOID() {
			deleteRef(repo, cmd.ref)
			report = append(report, reportPacket("ok "+cmd.ref))
			continue
		}
		if cmd.old != zeroOID() && !ancestorsSet(repo, cmd.next, -1, true)[cmd.old] {
			report = append(report, reportPacket("ng "+cmd.ref+" non-fast-forward"))
			continue
		}
		if repo.Objects[cmd.next] == nil {
			report = append(report, reportPacket("ng "+cmd.ref+" missing-object"))
			continue
		}
		setRef(repo, cmd.ref, cmd.next)
		repo.PushedAt = nowISO
		bump(repo)
		report = append(report, reportPacket("ok "+cmd.ref))
	}
	report = append(report, flushPkt)
	out := [][]byte{}
	for _, line := range report {
		out = append(out, pkt(append([]byte{1}, line...)))
	}
	return bytes.Join(out, nil)
}
