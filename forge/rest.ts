import type { Repo, Issue, Pull, ForgeObject } from './core.ts';
import {
  VIEWER, VIEWER_NAME, VIEWER_EMAIL, ZERO, getRepo, createRepo, repos, repoKey,
  commitInfo, ancestors, flattenTree, parseTree, treeFromFiles, makeCommit, putObject,
  blobFor, setFile, compareTrees, nodeId, iso, nowSec, nextComment,
} from './core.ts';

export interface Resp { status: number; body?: unknown; raw?: Buffer; headers?: Record<string, string> }

const ok = (body: unknown, status = 200, headers: Record<string, string> = {}): Resp => ({ status, body, headers });
const err = (status: number, message: string): Resp => ({ status, body: { message } });

function userShape() {
  return { login: VIEWER, id: 1000, node_id: nodeId('User', 1000), name: VIEWER_NAME, email: VIEWER_EMAIL, type: 'User', html_url: `https://github.com/${VIEWER}`, avatar_url: '' };
}

export function repoShape(repo: Repo) {
  return {
    id: repo.databaseId, node_id: repo.nodeId, name: repo.name, full_name: `${repo.owner}/${repo.name}`,
    private: repo.private, owner: userShape(), description: repo.description,
    default_branch: repo.defaultBranch, has_issues: true, archived: false, disabled: false,
    html_url: `https://github.com/${repo.owner}/${repo.name}`, size: 0,
    created_at: repo.createdAt, updated_at: repo.updatedAt, pushed_at: repo.pushedAt,
    clone_url: `https://github.com/${repo.owner}/${repo.name}.git`,
    url: `/api/v3/repos/${repo.owner}/${repo.name}`,
  };
}

function issueShape(repo: Repo, issue: Issue) {
  return {
    id: issue.number,
    node_id: issue.id, number: issue.number, title: issue.title, body: issue.body,
    state: issue.state, html_url: `https://github.com/${repo.owner}/${repo.name}/issues/${issue.number}`,
    labels: issue.labels.map((name) => ({ name, color: 'ededed' })),
    user: userShape(), comments: issue.comments.length,
    created_at: issue.createdAt, updated_at: issue.updatedAt, comments_url: `/api/v3/repos/${repo.owner}/${repo.name}/issues/${issue.number}/comments`,
  };
}

function pullShape(repo: Repo, pull: Pull) {
  return {
    id: pull.number + 50000, node_id: pull.id, number: pull.number, state: pull.state,
    title: pull.title, body: pull.body, html_url: `https://github.com/${repo.owner}/${repo.name}/pull/${pull.number}`,
    head: { ref: pull.head, sha: pull.headSha, label: `${repo.owner}:${pull.head}`, repo: repoShape(repo) },
    base: { ref: pull.base, sha: pull.baseSha, label: `${repo.owner}:${pull.base}`, repo: repoShape(repo) },
    user: userShape(), merged: pull.merged, mergeable: true, merged_at: pull.mergedAt,
    merge_commit_sha: pull.mergeCommitSha, comments: 0,
    created_at: pull.createdAt, updated_at: pull.updatedAt,
  };
}

function commitShape(repo: Repo, sha: string) {
  const info = commitInfo(repo, sha);
  if (!info) return null;
  const author = /^author (.+) <(.+)> (\d+) [+-]\d{4}$/.exec(info.raw.split('\n').find((l) => l.startsWith('author ')) ?? '');
  const date = author ? iso(Number(author[3])) : repo.createdAt;
  return {
    sha, node_id: nodeId('Commit', sha), commit: {
      author: { name: author?.[1] ?? VIEWER_NAME, email: author?.[2] ?? VIEWER_EMAIL, date },
      committer: { name: VIEWER_NAME, email: VIEWER_EMAIL, date },
      message: info.message.trimEnd(), tree: { sha: info.tree },
    },
    parents: info.parents.map((p) => ({ sha: p })),
    html_url: `https://github.com/${repo.owner}/${repo.name}/commit/${sha}`,
    url: `/api/v3/repos/${repo.owner}/${repo.name}/commits/${sha}`,
  };
}

function commitChain(repo: Repo, sha: string): string[] {
  const out: string[] = [];
  let current: string | undefined = sha;
  const seen = new Set<string>();
  while (current && !seen.has(current)) {
    seen.add(current);
    const info = commitInfo(repo, current);
    if (!info) break;
    out.push(current);
    current = info.parents[0];
  }
  return out;
}

function paginate<T>(items: T[], url: URL, path: string): { items: T[]; headers: Record<string, string> } {
  const perPage = Math.max(1, Math.min(100, Number.parseInt(url.searchParams.get('per_page') ?? '30', 10) || 30));
  const page = Math.max(1, Number.parseInt(url.searchParams.get('page') ?? '1', 10) || 1);
  const start = (page - 1) * perPage;
  const slice = items.slice(start, start + perPage);
  const pages = Math.max(1, Math.ceil(items.length / perPage));
  const linkFor = (p: number, rel: string) => {
    const u = new URL(path, url.origin);
    u.search = url.search;
    u.searchParams.set('per_page', String(perPage));
    u.searchParams.set('page', String(p));
    return `<${u.toString()}>; rel="${rel}"`;
  };
  const links: string[] = [linkFor(1, 'first'), linkFor(pages, 'last')];
  if (page > 1) links.push(linkFor(page - 1, 'prev'));
  if (page < pages) links.push(linkFor(page + 1, 'next'));
  return { items: slice, headers: { link: links.join(', ') } };
}

function resolveRef(repo: Repo, ref: string | null): string | null {
  if (!ref) return repo.refs.get(`refs/heads/${repo.defaultBranch}`) ?? null;
  if (/^[0-9a-f]{40}$/.test(ref)) return repo.objects.has(ref) ? ref : null;
  return repo.refs.get(`refs/heads/${ref}`) ?? repo.refs.get(`refs/tags/${ref}`) ?? repo.refs.get(ref) ?? null;
}

function contentShape(repo: Repo, path: string, sha: string, mode: string) {
  const obj = repo.objects.get(sha);
  if (!obj) return null;
  return {
    type: 'file', name: path.split('/').pop(), path, sha, size: obj.data.length,
    encoding: 'base64', content: obj.data.toString('base64').replace(/\n/g, ''),
    mode: mode === '100755' ? '100755' : '100644',
    url: `/api/v3/repos/${repo.owner}/${repo.name}/contents/${path}`,
  };
}

function readBodyObject(raw: string): Record<string, any> | null {
  try {
    const value = JSON.parse(raw);
    return value && typeof value === 'object' && !Array.isArray(value) ? value : null;
  } catch { return null; }
}

function issueNumberFromPath(parts: string[]): number | null {
  const n = Number.parseInt(parts[1] ?? '', 10);
  return Number.isFinite(n) ? n : null;
}

export function handleRest(method: string, path: string, url: URL, rawBody: string): Resp {
  if (rawBody !== '' && path === '/api/v3/user/repos' && method === 'POST') {
    try { JSON.parse(rawBody); } catch { return err(400, 'Bad Request'); }
  }
  const p = path.replace(/\/+$/, '') || '/';
  if (p === '/api/v3' || p === '/api/v3/') {
    return ok({
      current_user_url: '/api/v3/user',
      current_user_authorizations_html_url: 'https://github.com/settings/connections/applications',
      authorizations_url: '/api/v3/authorizations',
      code_search_url: '/api/v3/search/code?q={query}',
      commit_search_url: '/api/v3/search/commits?q={query}',
      emails_url: '/api/v3/user/emails',
      rate_limit_url: '/api/v3/rate_limit',
      repository_url: '/api/v3/repos/{owner}/{repo}',
      user_url: '/api/v3/users/{user}',
    }, 200, { 'x-oauth-scopes': 'repo, read:org' });
  }
  if (p === '/api/v3/meta' && method === 'GET') return ok({ installed_version: '3.0.0', verifiable_password_authentication: true });
  if (p === '/api/v3/rate_limit' && method === 'GET') return ok({ resources: { core: { limit: 5000, remaining: 4999, reset: 0 } } });
  if (p === '/api/v3/user' && method === 'GET') return ok(userShape());
  if (p === '/api/v3/no-such-route-content-type') return err(404, 'Not Found');

  if (p === '/api/v3/user/repos') {
    if (method === 'GET') {
      const mine = [...repos.values()].filter((r) => r.owner === VIEWER).map(repoShape);
      const page = paginate(mine, url, '/api/v3/user/repos');
      return ok(page.items, 200, page.headers);
    }
    if (method === 'POST') {
      const b = readBodyObject(rawBody);
      if (!b || typeof b.name !== 'string') return err(422, 'Validation Failed');
      const existing = getRepo(VIEWER, b.name);
      if (existing) return err(422, 'Validation Failed: repository already exists');
      const repo = createRepo(VIEWER, b.name, b.private === true, typeof b.description === 'string' ? b.description : '');
      return ok(repoShape(repo), 201);
    }
  }

  const m = /^\/api\/v3\/repos\/([^/]+)\/([^/]+)(?:\/(.*))?$/.exec(p);
  if (!m) return err(404, 'Not Found');
  const [, owner, name, rest = ''] = m;
  const repo = getRepo(owner, name);
  if (!repo) return err(404, 'Not Found');
  const parts = rest ? rest.split('/') : [];
  const body = readBodyObject(rawBody);

  if (parts.length === 0) {
    if (method === 'GET') return ok(repoShape(repo));
    if (method === 'PATCH') {
      if (body && typeof body.description === 'string') repo.description = body.description;
      if (body && typeof body.default_branch === 'string') repo.defaultBranch = body.default_branch;
      return ok(repoShape(repo));
    }
    if (method === 'DELETE') { repos.delete(repoKey(owner, name)); return { status: 204 }; }
    return err(404, 'Not Found');
  }

  if (parts[0] === 'branches' && method === 'GET' && parts.length === 1) {
    const branches = [...repo.refs.entries()].filter(([r]) => r.startsWith('refs/heads/')).map(([ref, sha]) => ({
      name: ref.slice('refs/heads/'.length), commit: { sha, url: '' }, protected: false,
    }));
    return ok(branches);
  }

  if (parts[0] === 'branches' && method === 'GET' && parts.length === 2) {
    const sha = resolveRef(repo, parts[1]);
    if (!sha) return err(404, 'Not Found');
    return ok({ name: parts[1], commit: { sha } });
  }

  if (parts[0] === 'git' && parts[1] === 'refs' && method === 'POST') {
    if (!body || typeof body.ref !== 'string' || typeof body.sha !== 'string' || !repo.objects.has(body.sha)) return err(422, 'Validation Failed');
    repo.refs.set(body.ref, body.sha);
    return ok({ ref: body.ref, object: { sha: body.sha, type: 'commit' } }, 201);
  }

  if (parts[0] === 'git' && parts[1] === 'refs' && method === 'GET') {
    const ref = parts.slice(2).join('/');
    const sha = resolveRef(repo, ref);
    if (!sha) return err(404, 'Not Found');
    return ok({ ref: `refs/heads/${ref}`, object: { sha, type: 'commit' } });
  }

  if (parts[0] === 'commits' && method === 'GET') {
    const ref = url.searchParams.get('sha');
    const sha = resolveRef(repo, ref);
    if (!sha) return ok([]);
    const chain = commitChain(repo, sha);
    const page = paginate(chain.map((s) => commitShape(repo, s)), url, p);
    return ok(page.items, 200, page.headers);
  }

  if (parts[0] === 'contents') {
    const filePath = parts.slice(1).join('/');
    const ref = url.searchParams.get('ref');
    const tip = resolveRef(repo, ref);
    const info = tip ? commitInfo(repo, tip) : null;
    const files = info ? flattenTree(repo, info.tree) : new Map<string, { sha: string; mode: string }>();
    if (method === 'GET') {
      const direct = files.get(filePath);
      if (direct) return ok(contentShape(repo, filePath, direct.sha, direct.mode));
      const prefix = filePath ? `${filePath}/` : '';
      const dirs = new Map<string, string>();
      for (const [path, value] of files) {
        if (!path.startsWith(prefix)) continue;
        const tail = path.slice(prefix.length);
        if (!tail) continue;
        const slash = tail.indexOf('/');
        if (slash < 0) dirs.set(tail, value.sha);
        else if (!dirs.has(tail.slice(0, slash))) dirs.set(tail.slice(0, slash), value.sha);
      }
      if (dirs.size === 0) return err(404, 'Not Found');
      return ok([...dirs.entries()].map(([entryName, sha]) => ({
        type: files.get(`${prefix}${entryName}`) ? 'file' : 'dir',
        name: entryName, path: `${prefix}${entryName}`, sha, size: 0,
        url: '', git_url: '', html_url: '',
      })));
    }
    if (method === 'PUT') {
      if (!body || typeof body.message !== 'string' || typeof body.content !== 'string') return err(422, 'Validation Failed');
      const bytes = Buffer.from(body.content.replace(/\s/g, ''), 'base64');
      const branch = typeof body.branch === 'string' ? body.branch : repo.defaultBranch;
      const current = files.get(filePath)?.sha;
      if (typeof body.sha === 'string' && current && body.sha !== current) return err(409, 'Conflict');
      const result = setFile(repo, branch, filePath, bytes, body.message);
      if (!result) return err(409, 'Conflict');
      return ok({ content: { sha: blobFor(repo, bytes), encoding: 'base64', name: filePath, path: filePath }, commit: { sha: result.commitSha, message: body.message } }, 201);
    }
    if (method === 'DELETE') {
      if (!body || typeof body.message !== 'string') return err(422, 'Validation Failed');
      const branch = typeof body.branch === 'string' ? body.branch : repo.defaultBranch;
      const result = setFile(repo, branch, filePath, null, body.message);
      if (!result) return err(404, 'Not Found');
      return ok({ commit: { sha: result.commitSha } });
    }
  }

  if (parts[0] === 'labels' && method === 'POST') {
    if (!body || typeof body.name !== 'string') return err(422, 'Validation Failed');
    if (!repo.labels.includes(body.name)) repo.labels.push(body.name);
    return ok({ id: repo.labels.indexOf(body.name) + 1, name: body.name, color: typeof body.color === 'string' ? body.color : 'ededed' }, 201);
  }

  if (parts[0] === 'issues') {
    if (parts.length === 1 && method === 'GET') {
      const state = url.searchParams.get('state') ?? 'open';
      let items = repo.issues.filter((i) => (state === 'all' ? true : i.state === state));
      if (url.searchParams.get('sort') === 'created') {
        const asc = url.searchParams.get('direction') !== 'desc';
        items = [...items].sort((a, b) => asc ? a.number - b.number : b.number - a.number);
      }
      const page = paginate(items.map((i) => issueShape(repo, i)), url, p);
      return ok(page.items, 200, page.headers);
    }
    if (parts.length === 1 && method === 'POST') {
      if (!body || typeof body.title !== 'string') return err(422, 'Validation Failed');
      const labels: string[] = Array.isArray(body.labels) ? body.labels.map((l: any) => typeof l === 'string' ? l : l?.name) : [];
      const unknown = labels.find((l) => typeof l !== 'string' || !repo.labels.includes(l));
      if (unknown !== undefined) return err(422, 'Validation Failed: label does not exist');
      const number = repo.nextIssue++;
      const createdAt = iso(nowSec());
      const issue: Issue = {
        number, title: body.title, body: typeof body.body === 'string' ? body.body : '', state: 'open',
        labels: labels as string[], id: nodeId('Issue', repo.databaseId, number), comments: [], createdAt, updatedAt: createdAt,
      };
      repo.issues.push(issue);
      return ok(issueShape(repo, issue), 201);
    }
    const n = issueNumberFromPath(parts);
    const issue = n === null ? undefined : repo.issues.find((i) => i.number === n);
    if (!issue) return err(404, 'Not Found');
    if (parts.length === 2 && method === 'GET') return ok(issueShape(repo, issue));
    if (parts.length === 2 && method === 'PATCH') {
      if (body && (body.state === 'open' || body.state === 'closed')) {
        issue.state = body.state;
        issue.updatedAt = iso(nowSec());
      }
      return ok({ number: issue.number, state: issue.state, id: issue.id });
    }
    if (parts[2] === 'comments' && parts.length === 3 && method === 'GET') {
      return ok(issue.comments.map((c) => ({ id: c.id, body: c.body, user: userShape(), created_at: c.createdAt })));
    }
    if (parts[2] === 'comments' && parts.length === 3 && method === 'POST') {
      if (!body || typeof body.body !== 'string') return err(422, 'Validation Failed');
      const comment = { id: nextComment(), body: body.body, author: VIEWER, createdAt: iso(nowSec()) };
      issue.comments.push(comment);
      return ok({ id: comment.id, body: comment.body, user: userShape(), created_at: comment.createdAt }, 201);
    }
  }

  if (parts[0] === 'pulls') {
    if (parts.length === 1 && method === 'GET') {
      const state = url.searchParams.get('state') ?? 'open';
      const items = repo.pulls.filter((p) => (state === 'all' ? true : p.state === state)).map((p) => pullShape(repo, p));
      const page = paginate(items, url, p);
      return ok(page.items, 200, page.headers);
    }
    if (parts.length === 1 && method === 'POST') {
      if (!body || typeof body.head !== 'string' || typeof body.base !== 'string' || typeof body.title !== 'string') return err(422, 'Validation Failed');
      const headSha = resolveRef(repo, body.head);
      const baseSha = resolveRef(repo, body.base);
      if (!headSha || !baseSha) return err(422, 'Validation Failed');
      const number = repo.nextPull++;
      const createdAt = iso(nowSec());
      const pull: Pull = {
        number, title: body.title, body: typeof body.body === 'string' ? body.body : '', state: 'open',
        head: body.head, base: body.base, headSha, baseSha, merged: false, mergeCommitSha: null, mergedAt: null,
        id: nodeId('PullRequest', repo.databaseId, number), createdAt, updatedAt: createdAt,
        files: compareTrees(repo, baseSha, headSha),
      };
      repo.pulls.push(pull);
      return ok(pullShape(repo, pull), 201);
    }
    const n = Number.parseInt(parts[1] ?? '', 10);
    const pull = Number.isFinite(n) ? repo.pulls.find((p) => p.number === n) : undefined;
    if (!pull) return err(404, 'Not Found');
    if (parts.length === 2 && method === 'GET') return ok(pullShape(repo, pull));
    if (parts[2] === 'files' && method === 'GET') return ok(pull.files);
    if (parts[2] === 'merge' && method === 'PUT') {
      if (pull.state === 'closed' && pull.merged) return err(405, 'Pull request already merged');
      const ref = `refs/heads/${pull.base}`;
      const currentBase = repo.refs.get(ref) ?? pull.baseSha;
      if (currentBase !== pull.baseSha) {
        const changedSince = compareTrees(repo, pull.baseSha, currentBase).map((f) => f.filename);
        if (changedSince.some((path) => pull.files.some((f) => f.filename === path))) return err(405, 'Merge conflict');
      }
      const headInfo = commitInfo(repo, pull.headSha);
      if (!headInfo) return err(405, 'Merge conflict');
      const mergedTree = headInfo.tree;
      const mergeSha = makeCommit(repo, mergedTree, [currentBase], `Merge pull request #${pull.number} from ${pull.head}`);
      repo.refs.set(ref, mergeSha);
      pull.state = 'closed'; pull.merged = true; pull.mergeCommitSha = mergeSha;
      pull.mergedAt = iso(nowSec()); pull.updatedAt = pull.mergedAt;
      return ok({ merged: true, sha: mergeSha, message: `Pull request #${pull.number} merged successfully`, node_id: pull.id });
    }
  }

  return err(404, 'Not Found');
}
