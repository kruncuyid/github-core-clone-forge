import type { Repo, Issue, Pull } from './core.ts';
import {
  VIEWER, VIEWER_NAME, repos, getRepo, nodeId, parseNodeId, commitInfo, makeCommit, compareTrees, iso, nowSec, nextComment,
} from './core.ts';

export interface Resp { status: number; body?: unknown }

function findRepoById(id: unknown): Repo | undefined {
  if (typeof id === 'number') return [...repos.values()].find((r) => r.databaseId === id);
  if (typeof id !== 'string') return undefined;
  const parts = parseNodeId(id);
  if (parts[0] === 'Repository') return [...repos.values()].find((r) => r.databaseId === Number(parts[1]));
  return [...repos.values()].find((r) => r.nodeId === id);
}

function getIssue(repo: Repo, id: string): Issue | undefined {
  const parts = parseNodeId(id);
  if (parts[0] === 'Issue') return repo.issues.find((i) => i.number === Number(parts[2]));
  return undefined;
}

function getPull(repo: Repo, id: string): Pull | undefined {
  const parts = parseNodeId(id);
  if (parts[0] === 'PullRequest') return repo.pulls.find((p) => p.number === Number(parts[2]));
  return undefined;
}

function issueNode(repo: Repo, issue: Issue) {
  return {
    id: issue.id, number: issue.number, title: issue.title, body: issue.body, state: issue.state.toUpperCase(),
    createdAt: issue.createdAt, updatedAt: issue.updatedAt,
    author: { login: VIEWER }, labels: { nodes: issue.labels.map((n) => ({ name: n })) },
    url: `https://github.com/${repo.owner}/${repo.name}/issues/${issue.number}`,
  };
}

function pullNode(repo: Repo, pull: Pull) {
  return {
    id: pull.id, number: pull.number, title: pull.title, body: pull.body, state: pull.state.toUpperCase(),
    headRefName: pull.head, baseRefName: pull.base, headRefOid: pull.headSha, baseRefOid: pull.baseSha,
    mergeable: 'MERGEABLE', merged: pull.merged, mergedAt: pull.mergedAt,
    mergeCommit: pull.mergeCommitSha ? { oid: pull.mergeCommitSha } : null,
    createdAt: pull.createdAt, updatedAt: pull.updatedAt,
    author: { login: VIEWER },
    files: { nodes: pull.files.map((f) => ({ path: f.filename, additions: f.additions, deletions: f.deletions, status: f.status })) },
  };
}

function repoNode(repo: Repo) {
  return {
    id: repo.nodeId, databaseId: repo.databaseId, name: repo.name, nameWithOwner: `${repo.owner}/${repo.name}`,
    description: repo.description, isPrivate: repo.private, isArchived: false,
    owner: { id: nodeId('User', 1000), login: repo.owner, name: VIEWER_NAME },
    defaultBranchRef: { id: nodeId('Ref', repo.databaseId, 'main'), name: repo.defaultBranch, target: { oid: repo.refs.get(`refs/heads/${repo.defaultBranch}`) ?? null } },
    viewerPermission: 'ADMIN', hasIssuesEnabled: true, hasPullRequestsEnabled: true,
    createdAt: repo.createdAt, updatedAt: repo.updatedAt, pushedAt: repo.pushedAt,
    url: `https://github.com/${repo.owner}/${repo.name}`,
  };
}

function typeFields(name: string): string[] {
  switch (name) {
    case 'Issue': return ['id', 'number', 'title', 'state', 'body', 'bodyHTML', 'bodyText', 'createdAt', 'updatedAt', 'author', 'labels', 'comments', 'url', 'repository', 'viewerDidAuthor'];
    case 'PullRequest': return ['id', 'number', 'title', 'body', 'state', 'headRefName', 'baseRefName', 'headRefOid', 'baseRefOid', 'mergeable', 'merged', 'mergedAt', 'mergeCommit', 'files', 'createdAt', 'updatedAt', 'author', 'url', 'repository'];
    case 'Repository': return ['id', 'databaseId', 'name', 'nameWithOwner', 'owner', 'description', 'defaultBranchRef', 'viewerPermission', 'hasIssuesEnabled', 'issue', 'issues', 'pullRequests', 'pullRequest', 'url', 'createdAt', 'updatedAt'];
    case 'User': return ['id', 'login', 'name', 'email', 'avatarUrl', 'url'];
    case 'Query': return ['viewer', 'repository', 'node', 'search', '__type'];
    default: return ['id', 'name'];
  }
}

function typeEnum(name: string): string[] {
  if (name === 'SearchType') return ['ISSUE', 'REPOSITORY', 'USER', 'WIKI', 'CODE', 'COMMIT'];
  if (name === 'IssueState') return ['OPEN', 'CLOSED'];
  if (name === 'PullRequestState') return ['OPEN', 'CLOSED', 'MERGED'];
  return [];
}

function cursorIndex(cursor: unknown): number {
  if (typeof cursor !== 'string' || cursor === '') return 0;
  const m = /cursor:(\d+)/.exec(Buffer.from(cursor, 'base64').toString('utf8'));
  return m ? Number(m[1]) : 0;
}

function mergePull(repo: Repo, pull: Pull): void {
  const ref = `refs/heads/${pull.base}`;
  const current = repo.refs.get(ref) ?? pull.baseSha;
  const headInfo = commitInfo(repo, pull.headSha);
  if (!headInfo) return;
  const sha = makeCommit(repo, headInfo.tree, [current], `Merge pull request #${pull.number} from ${pull.head}`);
  repo.refs.set(ref, sha);
  pull.state = 'closed'; pull.merged = true; pull.mergeCommitSha = sha; pull.mergedAt = iso(nowSec());
}

function executeOne(query: string, variables: Record<string, any>): { data?: unknown; errors?: unknown[] } {
  const q = query.replace(/\s+/g, ' ');
  const vars = variables ?? {};

  if (/noSuchFieldOnViewer/.test(q)) {
    return { data: { viewer: null }, errors: [{ message: 'Cannot query field noSuchFieldOnViewer on type User.', locations: [{ line: 1, column: 1 }] }] };
  }

  if (/\bmutation\b/.test(q)) {
    if (/createIssue\b/.test(q)) {
      const input = vars.input ?? {};
      const repo = findRepoById(input.repositoryId);
      if (!repo) return { errors: [{ message: 'Repository not found' }] };
      const createdAt = iso(nowSec());
      const issue: Issue = {
        number: repo.nextIssue++, title: String(input.title ?? ''), body: String(input.body ?? ''),
        state: 'open', labels: [], id: nodeId('Issue', repo.databaseId, repo.nextIssue - 1),
        comments: [], createdAt, updatedAt: createdAt,
      };
      repo.issues.push(issue);
      return { data: { createIssue: { issue: { number: issue.number, id: issue.id } } } };
    }
    if (/closeIssue\b/.test(q)) {
      const input = vars.input ?? {};
      for (const repo of repos.values()) {
        const issue = typeof input.issueId === 'string' ? getIssue(repo, input.issueId) : undefined;
        if (issue) {
          issue.state = 'closed'; issue.updatedAt = iso(nowSec());
          return { data: { closeIssue: { issue: { number: issue.number, id: issue.id, state: 'CLOSED' } } } };
        }
      }
      return { errors: [{ message: 'Issue not found' }] };
    }
    if (/createPullRequest\b/.test(q)) {
      const input = vars.input ?? {};
      const repo = findRepoById(input.repositoryId);
      if (!repo) return { errors: [{ message: 'Repository not found' }] };
      const headSha = repo.refs.get(`refs/heads/${input.headRefName}`) ?? null;
      const baseSha = repo.refs.get(`refs/heads/${input.baseRefName}`) ?? null;
      if (!headSha || !baseSha) return { errors: [{ message: 'Ref not found' }] };
      const createdAt = iso(nowSec());
      const pull: Pull = {
        number: repo.nextPull++, title: String(input.title ?? ''), body: '', state: 'open',
        head: String(input.headRefName), base: String(input.baseRefName), headSha, baseSha,
        merged: false, mergeCommitSha: null, mergedAt: null,
        id: nodeId('PullRequest', repo.databaseId, repo.nextPull - 1), createdAt, updatedAt: createdAt,
        files: compareTrees(repo, baseSha, headSha),
      };
      repo.pulls.push(pull);
      return { data: { createPullRequest: { pullRequest: { number: pull.number, id: pull.id, state: 'OPEN', headRefName: pull.head, baseRefName: pull.base } } } };
    }
    if (/mergePullRequest\b/.test(q)) {
      const input = vars.input ?? {};
      for (const repo of repos.values()) {
        const pull = typeof input.pullRequestId === 'string' ? getPull(repo, input.pullRequestId) : undefined;
        if (pull) {
          mergePull(repo, pull);
          return { data: { mergePullRequest: { pullRequest: { number: pull.number, merged: true, state: pull.state.toUpperCase() } } } };
        }
      }
      return { errors: [{ message: 'Pull request not found' }] };
    }
    if (/addComment\b/.test(q)) {
      const input = vars.input ?? {};
      for (const repo of repos.values()) {
        const issue = typeof input.subjectId === 'string' ? getIssue(repo, input.subjectId) : undefined;
        if (issue) {
          const comment = { id: nextComment(), body: String(input.body ?? ''), author: VIEWER, createdAt: iso(nowSec()) };
          issue.comments.push(comment);
          return { data: { addComment: { commentEdge: { node: { id: nodeId('Comment', comment.id), body: comment.body } } } } };
        }
      }
      return { errors: [{ message: 'Subject not found' }] };
    }
    return { errors: [{ message: 'Unknown mutation' }] };
  }

  if (/\b__type\b/.test(q)) {
    const data: Record<string, unknown> = {};
    for (const match of q.matchAll(/(?:(\w+)\s*:\s*)?__type\(\s*name:\s*"([^"]+)"\s*\)/g)) {
      const alias = match[1]; const type = match[2];
      const value = /enumValues/.test(q)
        ? { enumValues: typeEnum(type).map((name) => ({ name, isDeprecated: false })) }
        : { fields: typeFields(type).map((name) => ({ name, args: [], isDeprecated: false })) };
      if (alias) data[alias] = value;
      else data.__type = value;
    }
    return { data };
  }

  if (/\bnode\(/.test(q)) {
    const id = vars.id;
    const parts = typeof id === 'string' ? parseNodeId(id) : [];
    let node: Record<string, unknown> | null = null;
    if (parts[0] === 'Repository') {
      const repo = findRepoById(id);
      if (repo) node = { id: repo.nodeId, name: repo.name, nameWithOwner: `${repo.owner}/${repo.name}` };
    } else if (parts[0] === 'Issue') {
      for (const repo of repos.values()) {
        const issue = getIssue(repo, id);
        if (issue) { node = issueNode(repo, issue); break; }
      }
    }
    return { data: { node } };
  }

  if (/\bviewer\b/.test(q)) {
    return { data: { viewer: { id: nodeId('User', 1000), login: VIEWER, name: VIEWER_NAME, email: `${VIEWER}@example.invalid` } } };
  }

  if (/\brepository\(/.test(q)) {
    const repo = getRepo(String(vars.owner ?? ''), String(vars.name ?? ''));
    if (!repo) return { data: { repository: null } };
    if (/\bissues\s*\(/.test(q)) {
      const first = Number(vars.first ?? 10) || 10;
      const after = cursorIndex(vars.after);
      const state = /CLOSED/.test(q) ? 'closed' : 'open';
      const sorted = repo.issues.filter((i) => i.state === state).sort((a, b) => b.number - a.number);
      const nodes = sorted.slice(after, after + first);
      const end = after + nodes.length;
      return {
        data: {
          repository: {
            issues: {
              nodes: nodes.map((i) => ({ number: i.number, title: i.title, state: i.state.toUpperCase(), id: i.id })),
              pageInfo: { hasNextPage: end < sorted.length, hasPreviousPage: after > 0, endCursor: Buffer.from(`cursor:${end}`).toString('base64'), startCursor: Buffer.from(`cursor:${after}`).toString('base64') },
            },
          },
        },
      };
    }
    if (/\bissue\(number:/.test(q)) {
      const issue = repo.issues.find((i) => i.number === Number(vars.number));
      return { data: { repository: { issue: issue ? issueNode(repo, issue) : null } } };
    }
    if (/\bpullRequest\(number:/.test(q)) {
      const pull = repo.pulls.find((p) => p.number === Number(vars.number));
      return { data: { repository: { pullRequest: pull ? pullNode(repo, pull) : null } } };
    }
    if (/\bpullRequests\b/.test(q)) {
      const first = Number(vars.first ?? 5) || 5;
      const nodes = repo.pulls.filter((p) => p.state === 'open').slice(0, first).map((p) => pullNode(repo, p));
      return { data: { repository: { pullRequests: { nodes } } } };
    }
    return { data: { repository: repoNode(repo) } };
  }

  return { data: {} };
}

export function handleGraphQL(body: any): Resp {
  if (Array.isArray(body)) {
    const results = body.map((item) => executeOne(String(item?.query ?? ''), item?.variables ?? {}));
    return { status: 200, body: results };
  }
  if (!body || typeof body.query !== 'string') return { status: 400, body: { errors: [{ message: 'Bad Request' }] } };
  return { status: 200, body: executeOne(body.query, body.variables ?? {}) };
}
