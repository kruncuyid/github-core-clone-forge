import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { TOKEN, getRepo } from './forge/core.ts';
import { handleGraphQL } from './forge/graphql.ts';
import { handleUploadPack, handleReceivePack, advertisement } from './forge/git.ts';
import { handleMcp } from './forge/mcp.ts';
import { handleRest, type Resp as HandlerResp } from './forge/rest.ts';

const JSON_TYPE = 'application/json; charset=utf-8';

interface ResponseShape { status: number; body?: unknown; raw?: Buffer; headers?: Record<string, string> }

function authorized(req: IncomingMessage): boolean {
  const header = req.headers.authorization;
  if (typeof header !== 'string') return false;
  const match = /^Bearer\s+(.+)$/i.exec(header);
  const expected = process.env.MAKEFUN_SHOP_TOKEN || TOKEN;
  return match !== null && match[1] === expected;
}

function readBody(req: IncomingMessage): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer | string) => chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)));
    req.on('end', () => resolve(Buffer.concat(chunks)));
    req.on('error', reject);
  });
}

function pathOf(req: IncomingMessage): { path: string; url: URL } {
  try {
    const host = req.headers.host ?? 'entry';
    const url = new URL(req.url ?? '/', `http://${host}`);
    return { path: url.pathname, url };
  } catch {
    return { path: '/', url: new URL('http://entry/') };
  }
}

function send(res: ServerResponse, shape: ResponseShape, requestId: string): void {
  const headers: Record<string, string> = { ...(shape.headers ?? {}) };
  if (res.headersSent) return;
  if (shape.status === 204) {
    res.writeHead(204, headers);
    res.end();
    return;
  }
  if (shape.raw !== undefined) {
    headers['content-length'] = String(shape.raw.length);
    res.writeHead(shape.status, headers);
    res.end(shape.raw);
    return;
  }
  const text = shape.body === undefined ? '' : `${JSON.stringify(shape.body)}\n`;
  if (headers['content-type'] === undefined) headers['content-type'] = JSON_TYPE;
  headers['content-length'] = Buffer.byteLength(text);
  headers['x-github-request-id'] = headers['x-github-request-id'] ?? requestId;
  res.writeHead(shape.status, headers);
  res.end(text);
}

function jsonError(status: number, message: string, requestId: string): HandlerResp {
  return { status, body: { message }, headers: { 'x-github-request-id': requestId } };
}

function gitRoute(
  req: IncomingMessage,
  path: string,
  url: URL,
  body: Buffer,
  requestId: string,
): ResponseShape | null {
  const match = /^\/([^/]+)\/([^/]+)\.git\/(info\/refs|git-upload-pack|git-receive-pack)$/.exec(path);
  if (match === null) return null;
  let owner: string;
  let name: string;
  try {
    owner = decodeURIComponent(match[1]);
    name = decodeURIComponent(match[2]);
  } catch {
    return jsonError(404, 'Not Found', requestId);
  }
  const repo = getRepo(owner, name);
  if (!repo) return jsonError(404, 'Not Found', requestId);
  const endpoint = match[3];
  const serviceParam = url.searchParams.get('service');
  const v2 = String(req.headers['git-protocol'] ?? '').includes('version=2');
  if (req.method === 'GET' && endpoint === 'info/refs') {
    const service = serviceParam === 'git-upload-pack' ? 'upload' : serviceParam === 'git-receive-pack' ? 'receive' : null;
    if (service === null) return jsonError(404, 'Not Found', requestId);
    return {
      status: 200,
      raw: advertisement(repo, service, service === 'upload' && v2),
      headers: { 'content-type': `application/x-git-${service}-pack-advertisement`, 'cache-control': 'no-cache' },
    };
  }
  if (req.method === 'POST' && endpoint === 'git-upload-pack') {
    return {
      status: 200,
      raw: handleUploadPack(repo, body, v2),
      headers: { 'content-type': 'application/x-git-upload-pack-result' },
    };
  }
  if (req.method === 'POST' && endpoint === 'git-receive-pack') {
    return {
      status: 200,
      raw: handleReceivePack(repo, body),
      headers: { 'content-type': 'application/x-git-receive-pack-result' },
    };
  }
  return jsonError(404, 'Not Found', requestId);
}

function isGitPath(path: string): boolean {
  return /^\/([^/]+)\/([^/]+)\.git\/(info\/refs|git-upload-pack|git-receive-pack)$/.test(path);
}

async function route(req: IncomingMessage, res: ServerResponse, nextRequestId: () => string): Promise<void> {
  const { path, url } = pathOf(req);
  const method = req.method ?? 'GET';
  const requestId = nextRequestId();

  if (method === 'GET' && path === '/healthz') {
    send(res, { status: 200, body: { ok: true } }, requestId);
    return;
  }

  if (isGitPath(path) && method === 'GET') {
    const answer = gitRoute(req, path, url, Buffer.alloc(0), requestId);
    if (answer !== null) {
      send(res, answer, requestId);
      return;
    }
  }

  if (path.startsWith('/api/v3')) {
    if (!authorized(req)) {
      send(res, jsonError(401, 'Requires authentication', requestId), requestId);
      return;
    }
    const body = (await readBody(req)).toString('utf8');
    const answer = handleRest(method, path, url, body);
    send(res, answer as ResponseShape, requestId);
    return;
  }

  if (path === '/api/graphql') {
    if (!authorized(req)) {
      send(res, { status: 401, body: { errors: [{ message: 'Requires authentication' }] }, headers: { 'x-github-request-id': requestId } }, requestId);
      return;
    }
    const raw = (await readBody(req)).toString('utf8');
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      send(res, { status: 400, body: { errors: [{ message: 'Parse error' }] }, headers: { 'x-github-request-id': requestId } }, requestId);
      return;
    }
    send(res, handleGraphQL(parsed) as ResponseShape, requestId);
    return;
  }

  if (path === '/mcp') {
    if (method === 'DELETE') {
      send(res, { status: 204 }, requestId);
      return;
    }
    if (!authorized(req)) {
      send(res, { status: 401, body: { message: 'Requires authentication' }, headers: { 'x-github-request-id': requestId } }, requestId);
      return;
    }
    if (method !== 'POST') {
      send(res, { status: 405, body: { message: 'Method Not Allowed' }, headers: { 'x-github-request-id': requestId } }, requestId);
      return;
    }
    const raw = (await readBody(req)).toString('utf8');
    send(res, handleMcp(method, raw) as ResponseShape, requestId);
    return;
  }

  if (isGitPath(path) && method === 'POST') {
    const body = await readBody(req);
    const answer = gitRoute(req, path, url, body, requestId);
    if (answer !== null) {
      send(res, answer, requestId);
      return;
    }
  }

  send(res, jsonError(404, 'Not Found', requestId), requestId);
}

export function createApp(): Server {
  let counter = 0;
  return createServer((req, res) => {
    route(req, res, () => `req-${++counter}`).catch(() => {
      send(res, { status: 500, body: { message: 'Internal Server Error' } }, 'req-error');
    });
  });
}
