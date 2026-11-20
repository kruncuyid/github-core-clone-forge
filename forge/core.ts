import { createHash } from 'node:crypto';
import { deflateSync, inflateSync } from 'node:zlib';

export type ObjType = 'blob' | 'tree' | 'commit' | 'tag';

export interface ForgeObject { type: ObjType; data: Buffer }
export interface TreeEntry { mode: string; name: string; sha: string }
export interface CommitInfo { sha: string; tree: string; parents: string[]; message: string; raw: string }

export interface IssueComment { id: number; body: string; author: string; createdAt: string }
export interface Issue {
  number: number; title: string; body: string; state: 'open' | 'closed'; labels: string[];
  id: string; comments: IssueComment[]; createdAt: string; updatedAt: string;
}
export interface PullFile { filename: string; status: string; additions: number; deletions: number; changes: number }
export interface Pull {
  number: number; title: string; body: string; state: 'open' | 'closed'; head: string; base: string;
  headSha: string; baseSha: string; merged: boolean; mergeCommitSha: string | null; mergedAt: string | null;
  id: string; createdAt: string; updatedAt: string; files: PullFile[];
}
export interface Repo {
  owner: string; name: string; private: boolean; description: string; defaultBranch: string;
  id: number; databaseId: number; nodeId: string; createdAt: string; pushedAt: string; updatedAt: string;
  refs: Map<string, string>; objects: Map<string, ForgeObject>; issues: Issue[]; pulls: Pull[];
  labels: string[]; nextIssue: number; nextPull: number;
}

export const VIEWER = process.env.MAKEFUN_FORGE_LOGIN || 'adaiversen';
export const VIEWER_NAME = 'ada iversen';
export const VIEWER_EMAIL = `${VIEWER}@example.invalid`;
export const TOKEN = process.env.MAKEFUN_SHOP_TOKEN || 'makefun-trial-token';
export const ZERO = '0'.repeat(40);

function parseNow(): number {
  const raw = process.env.MAKEFUN_TRIAL_NOW;
  if (!raw) return 1700000000;
  const n = Number(raw);
  if (raw.trim() !== '' && Number.isFinite(n)) return n > 1e12 ? Math.floor(n / 1000) : Math.floor(n);
  const t = Date.parse(raw);
  return Number.isFinite(t) ? Math.floor(t / 1000) : 1700000000;
}

const NOW_SEC = parseNow();
let tick = 0;

export function nowSec(): number { return NOW_SEC + tick++; }
export function nowIso(): string { return iso(NOW_SEC); }

export function iso(ts: number): string {
  const days = Math.floor(ts / 86400);
  let rem = ts - days * 86400;
  const hh = Math.floor(rem / 3600); rem -= hh * 3600;
  const mm = Math.floor(rem / 60); const ss = rem - mm * 60;
  const z = days + 719468;
  const era = Math.floor(z / 146097);
  const doe = z - era * 146097;
  const yoe = Math.floor((doe - Math.floor(doe / 1460) + Math.floor(doe / 36524) - Math.floor(doe / 146096)) / 365);
  let y = yoe + era * 400;
  const doy = doe - (365 * yoe + Math.floor(yoe / 4) - Math.floor(yoe / 100));
  const mp = Math.floor((5 * doy + 2) / 153);
  const d = doy - Math.floor((153 * mp + 2) / 5) + 1;
  const m = mp + (mp < 10 ? 3 : -9);
  y += m <= 2 ? 1 : 0;
  const p = (n: number) => String(n).padStart(2, '0');
  return `${y}-${p(m)}-${p(d)}T${p(hh)}:${p(mm)}:${p(ss)}Z`;
}

export function sha1(data: Buffer | string): string {
  return createHash('sha1').update(data).digest('hex');
}

export function hashObject(type: ObjType, data: Buffer): string {
  return sha1(Buffer.concat([Buffer.from(`${type} ${data.length}\0`), data]));
}

function u32(n: number): Buffer {
  const b = Buffer.alloc(4);
  b.writeUInt32BE(n >>> 0, 0);
  return b;
}

function encodeHeader(code: number, size: number): Buffer {
  const out: number[] = [];
  let s = size;
  let first = (code << 4) | (s & 0x0f);
  s >>>= 4;
  while (s > 0) { out.push(first | 0x80); first = s & 0x7f; s >>>= 7; }
  out.push(first);
  return Buffer.from(out);
}

export function writePack(objects: ForgeObject[]): Buffer {
  const chunks: Buffer[] = [Buffer.from('PACK'), u32(2), u32(objects.length)];
  for (const obj of objects) {
    const code = obj.type === 'commit' ? 1 : obj.type === 'tree' ? 2 : obj.type === 'blob' ? 3 : 4;
    chunks.push(encodeHeader(code, obj.data.length));
    chunks.push(deflateSync(obj.data));
  }
  const body = Buffer.concat(chunks);
  return Buffer.concat([body, Buffer.from(sha1(body), 'hex')]);
}

interface RawPackEntry {
  offset: number; code: number; size: number; data: Buffer; baseOffset?: number; baseOid?: string;
}

function readVarint(buf: Buffer, at: number): { value: number; next: number } {
  let value = 0; let shift = 0; let pos = at;
  for (;;) {
    const b = buf[pos++];
    if (b === undefined) throw new Error('truncated varint');
    value |= (b & 0x7f) * 2 ** shift;
    if ((b & 0x80) === 0) return { value, next: pos };
    shift += 7;
  }
}

function readOfsOffset(buf: Buffer, at: number): { value: number; next: number } {
  let c = buf[at]; let pos = at + 1;
  let value = c & 0x7f;
  while ((c & 0x80) !== 0) {
    c = buf[pos++];
    value = ((value + 1) << 7) | (c & 0x7f);
  }
  return { value, next: pos };
}

function applyDelta(base: Buffer, delta: Buffer): Buffer {
  let pos = 0;
  const baseSize = readVarint(delta, pos); pos = baseSize.next;
  const resultSize = readVarint(delta, pos); pos = resultSize.next;
  const out: Buffer[] = [];
  while (pos < delta.length) {
    const op = delta[pos++];
    if ((op & 0x80) !== 0) {
      let off = 0; let size = 0;
      if ((op & 0x01) !== 0) off = delta[pos++];
      if ((op & 0x02) !== 0) off |= delta[pos++] << 8;
      if ((op & 0x04) !== 0) off |= delta[pos++] << 16;
      if ((op & 0x08) !== 0) off |= delta[pos++] << 24;
      if ((op & 0x10) !== 0) size = delta[pos++];
      if ((op & 0x20) !== 0) size |= delta[pos++] << 8;
      if ((op & 0x40) !== 0) size |= delta[pos++] << 16;
      if (size === 0) size = 0x10000;
      out.push(base.subarray(off, off + size));
    } else if (op !== 0) {
      out.push(delta.subarray(pos, pos + op)); pos += op;
    } else {
      throw new Error('invalid delta opcode 0');
    }
  }
  return Buffer.concat(out);
}

function inflateOne(pack: Buffer, start: number, limit: number, size: number): { data: Buffer; next: number } {
  let lo = start + 1;
  let hi = limit;
  let best = -1;
  let data: Buffer | null = null;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    try {
      const out = inflateSync(pack.subarray(start, mid));
      if (out.length === size) {
        best = mid;
        data = out;
        hi = mid - 1;
      } else {
        lo = mid + 1;
      }
    } catch {
      lo = mid + 1;
    }
  }
  if (best < 0 || data === null) throw new Error('bad compressed stream');
  return { data, next: best };
}

export function readPack(pack: Buffer, externals?: Map<string, ForgeObject>): Map<string, ForgeObject> {
  if (pack.length < 32 || pack.subarray(0, 4).toString() !== 'PACK') throw new Error('not a pack');
  const version = pack.readUInt32BE(4);
  if (version !== 2 && version !== 3) throw new Error('bad pack version');
  const count = pack.readUInt32BE(8);
  const trailerStart = pack.length - 20;
  if (sha1(pack.subarray(0, trailerStart)) !== pack.subarray(trailerStart).toString('hex')) throw new Error('bad pack checksum');
  const raw: RawPackEntry[] = [];
  let pos = 12;
  for (let i = 0; i < count; i++) {
    const offset = pos;
    let c = pack[pos++];
    let code = (c >> 4) & 0x07;
    let size = c & 0x0f;
    let shift = 4;
    while ((c & 0x80) !== 0) {
      c = pack[pos++];
      size |= (c & 0x7f) << shift;
      shift += 7;
    }
    let baseOffset: number | undefined;
    let baseOid: string | undefined;
    if (code === 6) {
      const base = readOfsOffset(pack, pos);
      baseOffset = offset - base.value;
      pos = base.next;
    }
    if (code === 7) { baseOid = pack.subarray(pos, pos + 20).toString('hex'); pos += 20; }
    const inflated = inflateOne(pack, pos, trailerStart, size);
    pos = inflated.next;
    raw.push({ offset, code, size, data: inflated.data, baseOffset, baseOid });
  }
  if (pos !== trailerStart) throw new Error('pack length mismatch');
  const byOffset = new Map<number, RawPackEntry>();
  for (const e of raw) byOffset.set(e.offset, e);
  const result = new Map<string, ForgeObject>();
  const resolved = new Map<number, ForgeObject>();
  const resolving = new Set<number>();
  const resolve = (entry: RawPackEntry): ForgeObject => {
    if (resolved.has(entry.offset)) return resolved.get(entry.offset)!;
    if (resolving.has(entry.offset)) throw new Error('delta cycle');
    resolving.add(entry.offset);
    let type: ObjType; let data: Buffer;
    if (entry.code === 1 || entry.code === 2 || entry.code === 3 || entry.code === 4) {
      type = entry.code === 1 ? 'commit' : entry.code === 2 ? 'tree' : entry.code === 3 ? 'blob' : 'tag';
      data = entry.data;
    } else {
      let base: ForgeObject | undefined;
      if (entry.code === 6) {
        const parent = byOffset.get(entry.baseOffset!);
        if (!parent) throw new Error('missing delta base offset');
        base = resolve(parent);
      } else {
        base = result.get(entry.baseOid!) ?? externals?.get(entry.baseOid!);
        if (!base) {
          const candidate = raw.find((r) => hashObject(baseType(r), r.data) === entry.baseOid);
          if (candidate) base = resolve(candidate);
        }
      }
      if (!base) throw new Error('missing delta base');
      data = applyDelta(base.data, entry.data);
      type = base.type;
    }
    const obj: ForgeObject = { type, data };
    resolved.set(entry.offset, obj);
    result.set(hashObject(type, data), obj);
    resolving.delete(entry.offset);
    return obj;
  };
  const baseType = (entry: RawPackEntry): ObjType => {
    if (entry.code === 1) return 'commit';
    if (entry.code === 2) return 'tree';
    if (entry.code === 3) return 'blob';
    if (entry.code === 4) return 'tag';
    if (entry.code === 6) return baseType(byOffset.get(entry.baseOffset!)!);
    const found = raw.find((r) => hashObject(baseType(r), r.data) === entry.baseOid);
    return found ? baseType(found) : 'blob';
  };
  for (const entry of raw) resolve(entry);
  return result;
}

export function parseTree(data: Buffer): TreeEntry[] {
  const out: TreeEntry[] = [];
  let i = 0;
  while (i < data.length) {
    const sp = data.indexOf(0x20, i);
    if (sp < 0) throw new Error('bad tree');
    const mode = data.subarray(i, sp).toString();
    const nul = data.indexOf(0, sp + 1);
    if (nul < 0) throw new Error('bad tree');
    const name = data.subarray(sp + 1, nul).toString('utf8');
    const sha = data.subarray(nul + 1, nul + 21).toString('hex');
    i = nul + 21;
    out.push({ mode, name, sha });
  }
  return out;
}

export function writeTree(entries: TreeEntry[]): string {
  const sorted = [...entries].sort((a, b) => Buffer.compare(Buffer.from(a.name), Buffer.from(b.name)));
  const parts: Buffer[] = [];
  for (const e of sorted) {
    parts.push(Buffer.from(`${e.mode} ${e.name}\0`));
    parts.push(Buffer.from(e.sha, 'hex'));
  }
  return putCurrentObject('tree', Buffer.concat(parts));
}

let currentRepo: Repo | null = null;
export function withRepo(repo: Repo | null, fn: () => void): void {
  const old = currentRepo; currentRepo = repo;
  try { fn(); } finally { currentRepo = old; }
}

function putCurrentObject(type: ObjType, data: Buffer): string {
  const oid = hashObject(type, data);
  if (currentRepo && !currentRepo.objects.has(oid)) currentRepo.objects.set(oid, { type, data });
  return oid;
}

export function putObject(repo: Repo, type: ObjType, data: Buffer): string {
  const oid = hashObject(type, data);
  if (!repo.objects.has(oid)) repo.objects.set(oid, { type, data });
  return oid;
}

interface FlatNode { files: Map<string, { sha: string; mode: string }>; dirs: Map<string, FlatNode> }

function writeNode(node: FlatNode): string {
  const entries: TreeEntry[] = [];
  for (const [name, v] of node.files) entries.push({ name, sha: v.sha, mode: v.mode });
  for (const [name, dir] of node.dirs) entries.push({ name, sha: writeNode(dir), mode: '40000' });
  return writeTree(entries);
}

export function treeFromFiles(repo: Repo, files: Map<string, { sha: string; mode: string }>): string {
  const root: FlatNode = { files: new Map(), dirs: new Map() };
  for (const [path, value] of files) {
    const segments = path.split('/').filter(Boolean);
    let node = root;
    for (let i = 0; i < segments.length - 1; i++) {
      let child = node.dirs.get(segments[i]);
      if (!child) { child = { files: new Map(), dirs: new Map() }; node.dirs.set(segments[i], child); }
      node = child;
    }
    node.files.set(segments[segments.length - 1], value);
  }
  return withRepoResult(repo, () => writeNode(root));
}

function withRepoResult<T>(repo: Repo, fn: () => T): T {
  const old = currentRepo; currentRepo = repo;
  try { return fn(); } finally { currentRepo = old; }
}

export function flattenTree(repo: Repo, treeSha: string, prefix = ''): Map<string, { sha: string; mode: string }> {
  const out = new Map<string, { sha: string; mode: string }>();
  const obj = repo.objects.get(treeSha);
  if (!obj || obj.type !== 'tree') return out;
  for (const entry of parseTree(obj.data)) {
    const path = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.mode === '160000') continue;
    if (entry.mode === '40000' || entry.mode === '040000') {
      for (const [k, v] of flattenTree(repo, entry.sha, path)) out.set(k, v);
    } else {
      out.set(path, { sha: entry.sha, mode: entry.mode });
    }
  }
  return out;
}

export function parseCommit(raw: string): { tree: string; parents: string[]; message: string } {
  let tree = ''; const parents: string[] = [];
  const split = raw.indexOf('\n\n');
  const head = split < 0 ? raw : raw.slice(0, split);
  const message = split < 0 ? '' : raw.slice(split + 2);
  for (const line of head.split('\n')) {
    if (line.startsWith('tree ')) tree = line.slice(5);
    else if (line.startsWith('parent ')) parents.push(line.slice(7));
  }
  return { tree, parents, message };
}

export function makeCommit(repo: Repo, tree: string, parents: string[], message: string): string {
  const ident = `${VIEWER} <${VIEWER_EMAIL}> ${nowSec()} +0000`;
  const raw = `tree ${tree}\n${parents.map((p) => `parent ${p}\n`).join('')}author ${ident}\ncommitter ${ident}\n\n${message}\n`;
  return putObject(repo, 'commit', Buffer.from(raw));
}

export function commitInfo(repo: Repo, sha: string): CommitInfo | null {
  const obj = repo.objects.get(sha);
  if (!obj || obj.type !== 'commit') return null;
  const raw = obj.data.toString('utf8');
  const parsed = parseCommit(raw);
  return { sha, tree: parsed.tree, parents: parsed.parents, message: parsed.message, raw };
}

export function ancestors(repo: Repo, sha: string, depth = Infinity, includeSelf = true): Set<string> {
  const out = new Set<string>();
  const queue: Array<{ sha: string; level: number }> = includeSelf ? [{ sha, level: 0 }] : [];
  const seen = new Set<string>();
  while (queue.length) {
    const item = queue.shift()!;
    if (seen.has(item.sha)) continue;
    seen.add(item.sha);
    if (!repo.objects.has(item.sha)) continue;
    out.add(item.sha);
    if (item.level + 1 > depth) continue;
    const info = commitInfo(repo, item.sha);
    if (info) for (const p of info.parents) queue.push({ sha: p, level: item.level + 1 });
  }
  return out;
}

export function reachable(repo: Repo, tips: string[], excluded: string[] = []): Map<string, ForgeObject> {
  const blocked = new Set<string>();
  for (const e of excluded) for (const sha of ancestors(repo, e)) blocked.add(sha);
  const out = new Map<string, ForgeObject>();
  const queue = [...tips];
  const seen = new Set<string>();
  while (queue.length) {
    const sha = queue.shift()!;
    if (seen.has(sha)) continue;
    seen.add(sha);
    const obj = repo.objects.get(sha);
    if (!obj) continue;
    out.set(sha, obj);
    if (obj.type === 'commit') {
      const info = commitInfo(repo, sha)!;
      for (const p of info.parents) if (!blocked.has(p)) queue.push(p);
      for (const [, v] of flattenTree(repo, info.tree)) if (!blocked.has(v.sha)) queue.push(v.sha);
      queue.push(info.tree);
    } else if (obj.type === 'tree') {
      for (const entry of parseTree(obj.data)) if (entry.mode !== '160000') queue.push(entry.sha);
    } else if (obj.type === 'tag') {
      const m = /(?:^|\n)object ([0-9a-f]{40})/.exec(obj.data.toString('utf8'));
      if (m) queue.push(m[1]);
    }
  }
  for (const sha of blocked) out.delete(sha);
  return out;
}

export interface LineStats { additions: number; deletions: number }
export function lineStats(data: Buffer): LineStats {
  if (data.length === 0) return { additions: 0, deletions: 0 };
  const text = data.toString('utf8');
  return { additions: text.split('\n').length, deletions: 0 };
}

export function compareTrees(repo: Repo, baseSha: string, headSha: string): PullFile[] {
  const baseTree = commitInfo(repo, baseSha)?.tree ?? baseSha;
  const headTree = commitInfo(repo, headSha)?.tree ?? headSha;
  const base = flattenTree(repo, baseTree);
  const head = flattenTree(repo, headTree);
  const names = new Set([...base.keys(), ...head.keys()]);
  const out: PullFile[] = [];
  for (const name of [...names].sort()) {
    const b = base.get(name); const h = head.get(name);
    if (b && h && b.sha === h.sha) continue;
    const hObj = h ? repo.objects.get(h.sha) : undefined;
    const stats = hObj ? lineStats(hObj.data) : { additions: 0, deletions: 0 };
    out.push({
      filename: name,
      status: !b ? 'added' : !h ? 'removed' : 'modified',
      additions: stats.additions,
      deletions: stats.deletions,
      changes: stats.additions + stats.deletions,
    });
  }
  return out;
}

export function nodeId(kind: string, ...parts: Array<string | number>): string {
  return Buffer.from([kind, ...parts].join(':')).toString('base64');
}

export function parseNodeId(id: string): string[] {
  try { return Buffer.from(id, 'base64').toString('utf8').split(':'); } catch { return []; }
}

export const repos = new Map<string, Repo>();
let nextRepoId = 100;
let nextCommentId = 1;

export function repoKey(owner: string, name: string): string { return `${owner}/${name}`; }
export function getRepo(owner: string, name: string): Repo | undefined { return repos.get(repoKey(owner, name)); }

export function createRepo(owner: string, name: string, isPrivate: boolean, description: string): Repo {
  const id = nextRepoId++;
  const createdAt = nowIso();
  const repo: Repo = {
    owner, name, private: isPrivate, description, defaultBranch: 'main',
    id, databaseId: id, nodeId: nodeId('Repository', id), createdAt, pushedAt: createdAt, updatedAt: createdAt,
    refs: new Map(), objects: new Map(), issues: [], pulls: [],
    labels: ['bug', 'enhancement', 'documentation', 'question', 'good first issue', 'help wanted'],
    nextIssue: 1, nextPull: 1,
  };
  repos.set(repoKey(owner, name), repo);
  return repo;
}

export function nextComment(): number { return nextCommentId++; }

export function blobFor(repo: Repo, bytes: Buffer): string {
  return putObject(repo, 'blob', bytes);
}

export function setFile(repo: Repo, branch: string, path: string, bytes: Buffer | null, message: string): { sha: string; commitSha: string } | null {
  const ref = `refs/heads/${branch}`;
  const oldTip = repo.refs.get(ref);
  const files = oldTip ? flattenTree(repo, commitInfo(repo, oldTip)?.tree ?? '') : new Map<string, { sha: string; mode: string }>();
  if (bytes === null) {
    if (!files.delete(path)) return null;
  } else {
    files.set(path, { sha: blobFor(repo, bytes), mode: '100644' });
  }
  const tree = treeFromFiles(repo, files);
  const commitSha = makeCommit(repo, tree, oldTip ? [oldTip] : [], message);
  repo.refs.set(ref, commitSha);
  repo.pushedAt = nowIso(); repo.updatedAt = repo.pushedAt;
  return { sha: bytes === null ? '' : blobFor(repo, bytes), commitSha };
}
