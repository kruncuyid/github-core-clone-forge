import type { ForgeObject, Repo } from './core.ts';
import { deflateSync } from 'node:zlib';
import { ZERO, parseTree, commitInfo, ancestors, readPack, sha1, iso, nowSec } from './core.ts';

export function pkt(data: string | Buffer): Buffer {
  const payload = typeof data === 'string' ? Buffer.from(data) : data;
  const head = Buffer.from((payload.length + 4).toString(16).padStart(4, '0'));
  return Buffer.concat([head, payload]);
}

export const FLUSH = Buffer.from('0000');
export const DELIM = Buffer.from('0001');

interface Packet { payload: Buffer; raw: Buffer; start: number; control: 'flush' | 'delim' | null }

function decodePackets(buf: Buffer): Packet[] {
  const out: Packet[] = [];
  let pos = 0;
  while (pos + 4 <= buf.length) {
    const raw = buf.subarray(pos, pos + 4).toString();
    if (raw === '0000') {
      out.push({ payload: Buffer.alloc(0), raw: buf.subarray(pos, pos + 4), start: pos, control: 'flush' });
      break;
    }
    if (raw === '0001') {
      out.push({ payload: Buffer.alloc(0), raw: buf.subarray(pos, pos + 4), start: pos, control: 'delim' });
      pos += 4;
      continue;
    }
    const len = Number.parseInt(raw, 16);
    if (!Number.isFinite(len) || len < 4) break;
    if (pos + len > buf.length) break;
    const payload = buf.subarray(pos + 4, pos + len);
    out.push({ payload, raw: buf.subarray(pos, pos + len), start: pos, control: null });
    pos += len;
  }
  return out;
}

function stripNul(text: string): string { return text.split('\0')[0]; }

function tagTarget(repo: Repo, oid: string): string | null {
  const obj = repo.objects.get(oid);
  if (!obj || obj.type !== 'tag') return null;
  const m = /(?:^|\n)object ([0-9a-f]{40})/.exec(obj.data.toString('utf8'));
  return m ? m[1] : null;
}

function repoRefs(repo: Repo): Map<string, string> {
  const out = new Map<string, string>();
  for (const [ref, oid] of repo.refs) out.set(ref, oid);
  const head = repo.refs.get(`refs/heads/${repo.defaultBranch}`) ?? ZERO;
  out.set('HEAD', head);
  return out;
}

const UPLOAD_CAPS = 'side-band-64k multi_ack_detailed thin-pack no-progress ofs-delta symref=HEAD:refs/heads/main';
const RECEIVE_CAPS = 'report-status side-band-64k delete-refs ofs-delta agent=makefun-forge/1';

export function advertisement(repo: Repo, service: 'upload' | 'receive', v2: boolean): Buffer {
  const out: Buffer[] = [];
  const name = service === 'upload' ? 'git-upload-pack' : 'git-receive-pack';
  out.push(pkt(`# service=${name}\n`));
  if (service === 'upload' && v2) {
    out.push(pkt('version 2\n'));
    out.push(pkt('fetch=shallow filter\n'));
    out.push(pkt('ls-refs=unborn\n'));
    out.push(pkt('object-format=sha1\n'));
    out.push(pkt('symrefs\n'));
    out.push(pkt('agent=makefun-forge/1\n'));
    out.push(FLUSH);
    return Buffer.concat(out);
  }
  const refs = repoRefs(repo);
  let first = true;
  if (refs.size === 0 || (refs.size === 1 && refs.get('HEAD') === ZERO)) {
    out.push(pkt(`${ZERO} capabilities^{}\0${service === 'receive' ? RECEIVE_CAPS : UPLOAD_CAPS}\n`));
    out.push(FLUSH);
    return Buffer.concat(out);
  }
  for (const [ref, oid] of refs) {
    const caps = first ? `\0${service === 'receive' ? RECEIVE_CAPS : UPLOAD_CAPS}` : '';
    out.push(pkt(`${oid} ${ref}${caps}\n`));
    first = false;
  }
  out.push(FLUSH);
  return Buffer.concat(out);
}

function lsRefs(repo: Repo, body: Buffer): Buffer {
  const packets = decodePackets(body);
  const prefixes: string[] = [];
  let afterDelim = false;
  for (const p of packets) {
    if (p.control === 'delim') { afterDelim = true; continue; }
    if (p.control === 'flush') continue;
    const line = p.payload.toString('utf8').replace(/\n$/, '');
    if (!afterDelim) continue;
    if (line.startsWith('ref-prefix ')) prefixes.push(line.slice('ref-prefix '.length));
  }
  const refs = repoRefs(repo);
  const out: Buffer[] = [];
  const matches = (ref: string) => prefixes.length === 0 || prefixes.some((p) => ref === p || ref.startsWith(p));
  if (refs.size === 1 && refs.get('HEAD') === ZERO) {
    if (matches('HEAD')) out.push(pkt(`unborn HEAD symref-target:refs/heads/${repo.defaultBranch}\n`));
  }
  for (const [ref, oid] of refs) {
    if (!matches(ref)) continue;
    let line = `${oid} ${ref}`;
    if (ref === 'HEAD') line += ` symref-target:refs/heads/${repo.defaultBranch}`;
    const peeled = tagTarget(repo, oid);
    if (peeled) line += ` peeled:${peeled}`;
    out.push(pkt(`${line}\n`));
  }
  out.push(FLUSH);
  return Buffer.concat(out);
}

interface FetchRequest {
  wants: string[]; haves: string[]; depth: number | null; shallow: string[];
}

function parseFetchCommands(packets: Packet[]): FetchRequest {
  const req: FetchRequest = { wants: [], haves: [], depth: null, shallow: [] };
  let command = '';
  let afterDelim = false;
  for (const p of packets) {
    if (p.control === 'delim') { afterDelim = true; continue; }
    if (p.control === 'flush') continue;
    const line = p.payload.toString('utf8').replace(/\n$/, '');
    if (line.startsWith('command=')) { command = line.slice(8); continue; }
    if (!afterDelim || command !== 'fetch') continue;
    if (line.startsWith('want ')) req.wants.push(line.slice(5).trim().split(' ')[0]);
    else if (line.startsWith('have ')) req.haves.push(line.slice(5).trim());
    else if (line.startsWith('shallow ')) req.shallow.push(line.slice(8).trim());
    else if (line.startsWith('deepen ')) req.depth = Number.parseInt(line.slice(7).trim(), 10);
  }
  return req;
}

function parseV0Fetch(packets: Packet[]): FetchRequest {
  const req: FetchRequest = { wants: [], haves: [], depth: null, shallow: [] };
  let section = 0;
  for (const p of packets) {
    if (p.control === 'flush') { section += 1; continue; }
    if (p.control) continue;
    const line = p.payload.toString('utf8').replace(/\n$/, '');
    if (line.startsWith('want ')) {
      if (section === 0) req.wants.push(line.slice(5).trim().split(' ')[0]);
    } else if (line.startsWith('have ')) req.haves.push(line.slice(5).trim());
    else if (line.startsWith('shallow ')) req.shallow.push(line.slice(8).trim());
    else if (line.startsWith('deepen ')) req.depth = Number.parseInt(line.slice(7).trim(), 10);
  }
  return req;
}

function objectsForFetch(repo: Repo, req: FetchRequest): { objects: Map<string, ForgeObject>; shallow: string[] } {
  if (req.wants.length === 0) return { objects: new Map(), shallow: [] };
  const boundaries = new Set(req.shallow);
  const excluded = new Set<string>();
  const haveQueue = [...req.haves];
  const haveSeen = new Set<string>();
  while (haveQueue.length) {
    const oid = haveQueue.shift()!;
    if (haveSeen.has(oid)) continue;
    haveSeen.add(oid);
    const obj = repo.objects.get(oid);
    if (!obj) continue;
    excluded.add(oid);
    if (obj.type === 'commit') {
      const info = commitInfo(repo, oid)!;
      for (const sha of treeObjectIds(repo, info.tree)) excluded.add(sha);
      for (const [, v] of flattenEntries(repo, info.tree)) excluded.add(v.sha);
      if (!boundaries.has(oid)) for (const p of info.parents) haveQueue.push(p);
    } else if (obj.type === 'tree') {
      for (const entry of parseTree(obj.data)) if (entry.mode !== '160000') excluded.add(entry.sha);
    }
  }
  const selected = new Map<string, ForgeObject>();
  const shallowOut = new Set<string>();
  const queue: Array<{ oid: string; level: number }> = [];
  for (const oid of req.wants) {
    if (boundaries.has(oid)) {
      const info = commitInfo(repo, oid);
      if (info) for (const p of info.parents) queue.push({ oid: p, level: 1 });
    } else {
      queue.push({ oid, level: 0 });
    }
  }
  const seen = new Set<string>();
  while (queue.length) {
    const { oid, level } = queue.shift()!;
    if (seen.has(oid) || excluded.has(oid) || boundaries.has(oid)) continue;
    seen.add(oid);
    const obj = repo.objects.get(oid);
    if (!obj) continue;
    selected.set(oid, obj);
    if (obj.type === 'commit') {
      const info = commitInfo(repo, oid)!;
      queue.push({ oid: info.tree, level });
      if (req.depth !== null && level + 1 >= req.depth) {
        shallowOut.add(oid);
      } else {
        for (const p of info.parents) queue.push({ oid: p, level: level + 1 });
      }
    } else if (obj.type === 'tree') {
      for (const entry of parseTree(obj.data)) {
        if (entry.mode === '160000') continue;
        queue.push({ oid: entry.sha, level });
      }
    } else if (obj.type === 'tag') {
      const m = /(?:^|\n)object ([0-9a-f]{40})/.exec(obj.data.toString('utf8'));
      if (m) queue.push({ oid: m[1], level });
    }
  }
  for (const sha of excluded) selected.delete(sha);
  return { objects: selected, shallow: [...shallowOut] };
}

function flattenEntries(repo: Repo, treeSha: string): Map<string, { sha: string; mode: string }> {
  const out = new Map<string, { sha: string; mode: string }>();
  const walk = (sha: string, prefix: string) => {
    const obj = repo.objects.get(sha);
    if (!obj || obj.type !== 'tree') return;
    for (const entry of parseTree(obj.data)) {
      const path = prefix ? `${prefix}/${entry.name}` : entry.name;
      if (entry.mode === '160000') continue;
      if (entry.mode === '40000' || entry.mode === '040000') walk(entry.sha, path);
      else out.set(path, { sha: entry.sha, mode: entry.mode });
    }
  };
  walk(treeSha, '');
  return out;
}

function treeObjectIds(repo: Repo, treeSha: string): Set<string> {
  const out = new Set<string>();
  const walk = (sha: string): void => {
    const obj = repo.objects.get(sha);
    if (!obj || obj.type !== 'tree' || out.has(sha)) return;
    out.add(sha);
    for (const entry of parseTree(obj.data)) {
      if (entry.mode === '160000') continue;
      if (repo.objects.get(entry.sha)?.type === 'tree') walk(entry.sha);
    }
  };
  walk(treeSha);
  return out;
}

function sidebandPack(pack: Buffer): Buffer[] {
  const out: Buffer[] = [];
  for (let i = 0; i < pack.length; i += 65515) {
    const chunk = pack.subarray(i, i + 65515);
    out.push(pkt(Buffer.concat([Buffer.from([1]), chunk])));
  }
  if (pack.length === 0) out.push(pkt(Buffer.from([1])));
  return out;
}

function fetchResponse(repo: Repo, req: FetchRequest, v2: boolean): Buffer {
  const { objects, shallow } = objectsForFetch(repo, req);
  const sorted = [...objects.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  const pack = writePackSorted(sorted.map(([, o]) => o));
  const out: Buffer[] = [];
  if (v2) {
    out.push(pkt('acknowledgments\n'));
    out.push(pkt('NAK\n'));
    out.push(FLUSH);
    if (shallow.length) {
      out.push(pkt('shallow-info\n'));
      for (const sha of shallow) out.push(pkt(`shallow ${sha}\n`));
      out.push(FLUSH);
    }
    out.push(pkt('packfile\n'));
    out.push(...sidebandPack(pack));
    out.push(FLUSH);
  } else {
    out.push(pkt('NAK\n'));
    for (const sha of shallow) out.push(pkt(`shallow ${sha}\n`));
    out.push(...sidebandPack(pack));
    out.push(FLUSH);
  }
  return Buffer.concat(out);
}

function writePackSorted(objects: ForgeObject[]): Buffer {
  const header = Buffer.concat([Buffer.from('PACK'), Buffer.from([0, 0, 0, 2])]);
  const count = Buffer.alloc(4); count.writeUInt32BE(objects.length, 0);
  const parts: Buffer[] = [header, count];
  for (const obj of objects) {
    const code = obj.type === 'commit' ? 1 : obj.type === 'tree' ? 2 : obj.type === 'blob' ? 3 : 4;
    parts.push(encodeHeaderLocal(code, obj.data.length));
    parts.push(deflateSync(obj.data));
  }
  const body = Buffer.concat(parts);
  return Buffer.concat([body, Buffer.from(sha1(body), 'hex')]);
}

function encodeHeaderLocal(code: number, size: number): Buffer {
  const out: number[] = [];
  let s = size;
  let first = (code << 4) | (s & 0x0f);
  s >>>= 4;
  while (s > 0) { out.push(first | 0x80); first = s & 0x7f; s >>>= 7; }
  out.push(first);
  return Buffer.from(out);
}

export function handleUploadPack(repo: Repo, body: Buffer, v2: boolean): Buffer {
  const packets = decodePackets(body);
  const command = packets.find((p) => p.payload.toString('utf8').startsWith('command='))?.payload.toString('utf8');
  if (command && command.startsWith('command=ls-refs')) return lsRefs(repo, body);
  const req = v2 ? parseFetchCommands(packets) : parseV0Fetch(packets);
  return fetchResponse(repo, req, v2);
}

interface PushCommand { old: string; next: string; ref: string }

function parseReceiveCommands(packets: Packet[]): { commands: PushCommand[]; bodyEnd: number } {
  const commands: PushCommand[] = [];
  let end = 0;
  for (const p of packets) {
    if (p.control === 'flush') { end = p.start + p.raw.length; break; }
    end = p.start + p.raw.length;
    const text = stripNul(p.payload.toString('utf8')).replace(/\n$/, '');
    const parts = text.split(' ');
    if (parts.length >= 3) commands.push({ old: parts[0], next: parts[1], ref: parts[2] });
  }
  return { commands, bodyEnd: end };
}

function reportPacket(text: string): Buffer { return pkt(`${text}\n`); }

export function handleReceivePack(repo: Repo, body: Buffer): Buffer {
  const packets = decodePackets(body);
  const { commands, bodyEnd } = parseReceiveCommands(packets);
  let unpackOk = true;
  try {
    const packBytes = body.subarray(bodyEnd);
    if (packBytes.length >= 12 && packBytes.subarray(0, 4).toString() === 'PACK') {
      const objects = readPack(packBytes);
      for (const [oid, obj] of objects) if (!repo.objects.has(oid)) repo.objects.set(oid, obj);
    }
  } catch {
    unpackOk = false;
  }
  const report: Buffer[] = [reportPacket(unpackOk ? 'unpack ok' : 'unpack error')];
  for (const command of commands) {
    if (!unpackOk) {
      report.push(reportPacket(`ng ${command.ref} unpack-error`));
      continue;
    }
    if (command.next === ZERO) {
      repo.refs.delete(command.ref);
      report.push(reportPacket(`ok ${command.ref}`));
      continue;
    }
    if (command.old !== ZERO && !ancestors(repo, command.next).has(command.old)) {
      report.push(reportPacket(`ng ${command.ref} non-fast-forward`));
      continue;
    }
    if (!repo.objects.has(command.next)) {
      report.push(reportPacket(`ng ${command.ref} missing-object`));
      continue;
    }
    repo.refs.set(command.ref, command.next);
    repo.pushedAt = iso(nowSec());
    report.push(reportPacket(`ok ${command.ref}`));
  }
  report.push(FLUSH);
  const out: Buffer[] = [];
  for (const line of report) out.push(pkt(Buffer.concat([Buffer.from([1]), line])));
  return Buffer.concat(out);
}
