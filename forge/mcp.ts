import type { Repo } from './core.ts';
import { VIEWER, getRepo } from './core.ts';
import { handleRest, type Resp as RestResp } from './rest.ts';

export interface Resp { status: number; body?: unknown; raw?: Buffer; headers?: Record<string, string> }

type RpcId = string | number | null;

const JSONRPC = '2.0';
const SERVER_INFO = { name: 'makefun-forge', version: '1.0.0' };
const SUPPORTED_VERSIONS = ['2026-07-28', '2025-11-25', '2025-06-18', '2025-03-26', '2024-11-05'];
const LEGACY_VERSIONS = ['2025-11-25', '2025-06-18', '2025-03-26', '2024-11-05'];

const TOOLS = Object.freeze([
  {
    name: 'create_repo',
    title: 'Create repository',
    description: 'Create a new repository for the authenticated user.',
    inputSchema: {
      type: 'object',
      properties: {
        owner: { type: 'string', description: 'Repository owner login.' },
        name: { type: 'string', description: 'Repository name.' },
        description: { type: 'string', description: 'Repository description.' },
        private: { type: 'boolean', description: 'Whether the repository is private.' },
      },
      required: ['name'],
    },
  },
  {
    name: 'get_repo',
    title: 'Get repository',
    description: 'Read one repository by owner and name.',
    inputSchema: {
      type: 'object',
      properties: {
        owner: { type: 'string', description: 'Repository owner login.' },
        repo: { type: 'string', description: 'Repository name.' },
      },
      required: ['owner', 'repo'],
    },
  },
  {
    name: 'create_issue',
    title: 'Create issue',
    description: 'Open an issue in a repository.',
    inputSchema: {
      type: 'object',
      properties: {
        owner: { type: 'string', description: 'Repository owner login.' },
        repo: { type: 'string', description: 'Repository name.' },
        title: { type: 'string', description: 'Issue title.' },
        body: { type: 'string', description: 'Issue body.' },
      },
      required: ['owner', 'repo', 'title'],
    },
  },
  {
    name: 'list_issues',
    title: 'List issues',
    description: 'List issues in a repository, optionally filtered by state.',
    inputSchema: {
      type: 'object',
      properties: {
        owner: { type: 'string', description: 'Repository owner login.' },
        repo: { type: 'string', description: 'Repository name.' },
        state: { type: 'string', enum: ['open', 'closed', 'all'], description: 'Issue state.' },
      },
      required: ['owner', 'repo'],
    },
  },
  {
    name: 'create_pull_request',
    title: 'Create pull request',
    description: 'Open a pull request from a head branch into a base branch.',
    inputSchema: {
      type: 'object',
      properties: {
        owner: { type: 'string', description: 'Repository owner login.' },
        repo: { type: 'string', description: 'Repository name.' },
        title: { type: 'string', description: 'Pull request title.' },
        body: { type: 'string', description: 'Pull request body.' },
        head: { type: 'string', description: 'Head branch name.' },
        base: { type: 'string', description: 'Base branch name.' },
      },
      required: ['owner', 'repo', 'title', 'head', 'base'],
    },
  },
  {
    name: 'merge_pull_request',
    title: 'Merge pull request',
    description: 'Merge a pull request by number.',
    inputSchema: {
      type: 'object',
      properties: {
        owner: { type: 'string', description: 'Repository owner login.' },
        repo: { type: 'string', description: 'Repository name.' },
        pull_number: { type: 'integer', description: 'Pull request number.' },
      },
      required: ['owner', 'repo', 'pull_number'],
    },
  },
]);

function result(id: RpcId, value: unknown): Resp {
  return { status: 200, body: { jsonrpc: JSONRPC, id, result: value } };
}

function error(id: RpcId, code: number, message: string, status = 200): Resp {
  return { status, body: { jsonrpc: JSONRPC, id, error: { code, message } } };
}

function toolResult(id: RpcId, value: unknown): Resp {
  return result(id, {
    content: [{ type: 'text', text: JSON.stringify(value) }],
    structuredContent: value,
    isError: false,
  });
}

function toolFailure(id: RpcId, message: string): Resp {
  return result(id, {
    content: [{ type: 'text', text: message }],
    structuredContent: { message },
    isError: true,
  });
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function textArg(args: Record<string, unknown>, names: readonly string[]): string {
  for (const name of names) {
    const value = args[name];
    if (typeof value === 'string' && value !== '') return value;
    if (typeof value === 'number' && Number.isFinite(value)) return String(value);
  }
  return '';
}

function boolArg(args: Record<string, unknown>, names: readonly string[]): boolean | undefined {
  for (const name of names) {
    const value = args[name];
    if (typeof value === 'boolean') return value;
    if (value === 'true') return true;
    if (value === 'false') return false;
  }
  return undefined;
}

function rest(method: string, target: string, body?: unknown): RestResp {
  const raw = body === undefined ? '' : JSON.stringify(body);
  const url = new URL(target, 'http://entry');
  return handleRest(method, url.pathname, url, raw);
}

function repoPath(owner: string, repo: string): string {
  return `/api/v3/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}`;
}

function callTool(id: RpcId, name: string, rawArgs: unknown): Resp {
  const args = record(rawArgs);
  if (name === 'create_repo') {
    const repoName = textArg(args, ['name', 'repo', 'repository', 'repository_name', 'repo_name']);
    if (repoName === '') return error(id, -32602, 'Missing required argument: name');
    const description = textArg(args, ['description', 'body', 'about', 'summary']);
    const privateFlag = boolArg(args, ['private', 'is_private']) ?? false;
    const answer = rest('POST', '/api/v3/user/repos', { name: repoName, description, private: privateFlag });
    if (answer.status >= 400) return toolFailure(id, `create_repo failed with HTTP ${answer.status}`);
    return toolResult(id, answer.body);
  }
  if (name === 'get_repo') {
    const owner = textArg(args, ['owner', 'owner_login', 'org', 'organization', 'user', 'login']);
    const repoName = textArg(args, ['repo', 'repository', 'repository_name', 'repo_name']);
    if (owner === '' || repoName === '') return error(id, -32602, 'Missing required arguments: owner, repo');
    const answer = rest('GET', repoPath(owner, repoName));
    if (answer.status >= 400) return toolFailure(id, `get_repo failed with HTTP ${answer.status}`);
    return toolResult(id, answer.body);
  }
  if (name === 'create_issue') {
    const owner = textArg(args, ['owner', 'owner_login', 'org', 'organization', 'user', 'login']);
    const repoName = textArg(args, ['repo', 'repository', 'repository_name', 'repo_name']);
    const title = textArg(args, ['title', 'issue_title', 'subject']);
    if (owner === '' || repoName === '' || title === '') return error(id, -32602, 'Missing required arguments: owner, repo, title');
    const body = textArg(args, ['body', 'issue_body', 'description', 'content', 'text']);
    const answer = rest('POST', `${repoPath(owner, repoName)}/issues`, { title, body });
    if (answer.status >= 400) return toolFailure(id, `create_issue failed with HTTP ${answer.status}`);
    return toolResult(id, answer.body);
  }
  if (name === 'list_issues') {
    const owner = textArg(args, ['owner', 'owner_login', 'org', 'organization', 'user', 'login']);
    const repoName = textArg(args, ['repo', 'repository', 'repository_name', 'repo_name']);
    if (owner === '' || repoName === '') return error(id, -32602, 'Missing required arguments: owner, repo');
    const state = textArg(args, ['state', 'status']) || 'open';
    const answer = rest('GET', `${repoPath(owner, repoName)}/issues?state=${encodeURIComponent(state)}&per_page=100`);
    if (answer.status >= 400) return toolFailure(id, `list_issues failed with HTTP ${answer.status}`);
    return toolResult(id, answer.body);
  }
  if (name === 'create_pull_request') {
    const owner = textArg(args, ['owner', 'owner_login', 'org', 'organization', 'user', 'login']);
    const repoName = textArg(args, ['repo', 'repository', 'repository_name', 'repo_name']);
    const title = textArg(args, ['title', 'pull_request_title', 'pr_title', 'subject']);
    const head = textArg(args, ['head', 'head_ref', 'head_branch', 'source', 'source_branch', 'branch']);
    const base = textArg(args, ['base', 'base_ref', 'base_branch', 'target', 'target_branch']);
    if (owner === '' || repoName === '' || title === '' || head === '' || base === '') return error(id, -32602, 'Missing required arguments: owner, repo, title, head, base');
    const body = textArg(args, ['body', 'description', 'content', 'text']);
    const answer = rest('POST', `${repoPath(owner, repoName)}/pulls`, { title, body, head, base });
    if (answer.status >= 400) return toolFailure(id, `create_pull_request failed with HTTP ${answer.status}`);
    return toolResult(id, answer.body);
  }
  if (name === 'merge_pull_request') {
    const owner = textArg(args, ['owner', 'owner_login', 'org', 'organization', 'user', 'login']);
    const repoName = textArg(args, ['repo', 'repository', 'repository_name', 'repo_name']);
    const numberText = textArg(args, ['pull_number', 'pull_request_number', 'pr_number', 'number', 'issue_number']);
    if (owner === '' || repoName === '' || numberText === '') return error(id, -32602, 'Missing required arguments: owner, repo, pull_number');
    const answer = rest('PUT', `${repoPath(owner, repoName)}/pulls/${encodeURIComponent(numberText)}/merge`, { merge_method: 'merge' });
    if (answer.status >= 400) return toolFailure(id, `merge_pull_request failed with HTTP ${answer.status}`);
    return toolResult(id, answer.body);
  }
  return error(id, -32602, `Unknown tool: ${name}`);
}

function dispatch(message: Record<string, unknown>): Resp | null {
  const id = (typeof message.id === 'string' || typeof message.id === 'number' ? message.id : null);
  const method = typeof message.method === 'string' ? message.method : '';
  const params = record(message.params);
  if (method === 'server/discover') {
    return result(id, {
      resultType: 'discover',
      protocolVersion: '2026-07-28',
      supportedVersions: SUPPORTED_VERSIONS,
      capabilities: { tools: {} },
      instructions: 'Use tools/list and tools/call to operate the forge.',
      serverInfo: SERVER_INFO,
      _meta: {
        'io.modelcontextprotocol/protocolVersion': '2026-07-28',
        'io.modelcontextprotocol/serverInfo': SERVER_INFO,
      },
    });
  }
  if (method === 'initialize') {
    const offered = typeof params.protocolVersion === 'string' ? params.protocolVersion : '';
    const chosen = LEGACY_VERSIONS.includes(offered) ? offered : '2026-07-28';
    return result(id, {
      protocolVersion: chosen,
      capabilities: { tools: {} },
      serverInfo: SERVER_INFO,
      instructions: 'Use tools/list and tools/call to operate the forge.',
    });
  }
  if (method === 'notifications/initialized') return null;
  if (method === 'tools/list') return result(id, { tools: TOOLS });
  if (method === 'tools/call') {
    const name = textArg(params, ['name', 'tool', 'tool_name']);
    if (name === '') return error(id, -32602, 'Missing required argument: name');
    return callTool(id, name, params.arguments);
  }
  if (method === 'ping') return result(id, {});
  return error(id, -32601, `Method not found: ${method}`);
}

export function handleMcp(method: string, rawBody: string): Resp {
  if (method !== 'POST') return { status: 405, body: { jsonrpc: JSONRPC, id: null, error: { code: -32600, message: 'Method not allowed' } } };
  let parsed: unknown;
  try {
    parsed = JSON.parse(rawBody);
  } catch {
    return { status: 400, body: { jsonrpc: JSONRPC, id: null, error: { code: -32700, message: 'Parse error' } } };
  }
  if (Array.isArray(parsed)) {
    const answers = parsed.map((item) => dispatch(record(item))).filter((answer): answer is Resp => answer !== null);
    if (answers.length === 0) return { status: 202 };
    return { status: 200, body: answers.map((answer) => answer.body) };
  }
  const answer = dispatch(record(parsed));
  if (answer === null) return { status: 202 };
  return answer;
}

export function mcpTools(): readonly unknown[] {
  return TOOLS;
}

export function mcpViewer(): string {
  return VIEWER;
}

export function mcpRepo(owner: string, name: string): Repo | undefined {
  return getRepo(owner, name);
}
