import { spawn } from 'node:child_process';
import { chmodSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ENV_URL = (process.env.MAKEFUN_ENV_URL ?? '').replace(/\/+$/, '');
if (ENV_URL === '') {
  process.stderr.write('[entry] MAKEFUN_ENV_URL is not set: there is no env to register with\n');
  process.exit(1);
}

const REQUEST_TIMEOUT_MS = 30_000;
const REGISTER_ROUTE = '/register';
const DONE_ROUTE = '/done';
const RETRY_INITIAL_MS = 250;
const RETRY_MAX_MS = 2_000;
const BIN_NAME = process.platform === 'win32' ? 'forge-server.exe' : 'forge-server';
const BIN_PATH = join(dirname(fileURLToPath(import.meta.url)), BIN_NAME);

const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

function backoffForAttempt(attempt: number): number {
  return Math.min(RETRY_MAX_MS, RETRY_INITIAL_MS * 2 ** Math.min(attempt, 8));
}

async function register(port: number, exited: () => Error | null): Promise<void> {
  for (let attempt = 0; ; attempt += 1) {
    const failure = exited();
    if (failure !== null) throw failure;
    try {
      const res = await fetch(`${ENV_URL}${REGISTER_ROUTE}`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ port }),
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
      });
      if (res.status === 200 || res.status === 409) return;
      if (res.status === 400 || res.status === 405) throw new Error(`register answered HTTP ${res.status}: ${await res.text()}`);
    } catch (err) {
      if (err instanceof Error && err.message.startsWith('register answered HTTP ')) throw err;
    }
    await sleep(backoffForAttempt(attempt));
  }
}

async function waitForDone(exited: () => Error | null): Promise<void> {
  for (let attempt = 0; ; attempt += 1) {
    const failure = exited();
    if (failure !== null) throw failure;
    try {
      const res = await fetch(`${ENV_URL}${DONE_ROUTE}`, { signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
      if (res.ok) {
        const body = (await res.json()) as { done?: unknown };
        if (body.done === true) return;
      }
    } catch {
    }
    await sleep(backoffForAttempt(attempt));
  }
}

function waitForPort(child: ReturnType<typeof spawn>): Promise<number> {
  return new Promise((resolve, reject) => {
    let output = '';
    let settled = false;
    const finish = (fn: () => void): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      fn();
    };
    const timer = setTimeout(() => finish(() => reject(new Error('[entry] timed out waiting for server port'))), 10_000);
    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (chunk: string) => {
      output += chunk;
      const match = /PORT\s+(\d+)/.exec(output);
      if (match !== null) finish(() => resolve(Number(match[1])));
    });
    child.once('error', (err) => finish(() => reject(err)));
    child.once('exit', (code, signal) => finish(() => reject(new Error(`[entry] forge-server exited before binding: ${code ?? signal}`))));
  });
}

async function stopChild(child: ReturnType<typeof spawn>): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      resolve();
    }, 2_000);
    child.once('exit', () => {
      clearTimeout(timer);
      resolve();
    });
    child.kill('SIGTERM');
  });
}

async function main(): Promise<void> {
  try {
    chmodSync(BIN_PATH, 0o755);
  } catch {
  }
  const child = spawn(BIN_PATH, [], { cwd: dirname(BIN_PATH), stdio: ['ignore', 'pipe', 'inherit'] });
  let exitFailure: Error | null = null;
  child.once('exit', (code, signal) => {
    exitFailure = new Error(`[entry] forge-server exited: ${code ?? signal}`);
  });
  const exited = (): Error | null => exitFailure;

  const port = await waitForPort(child);
  process.stdout.write(`[entry] listening on port ${port}\n`);

  await register(port, exited);
  process.stdout.write('[entry] registered; waiting for the env to say done\n');

  await waitForDone(exited);
  await stopChild(child);
  process.stdout.write('[entry] done\n');
}

try {
  await main();
  process.exit(0);
} catch (err) {
  process.stderr.write(`[entry] ${err instanceof Error ? err.message : String(err)}\n`);
  process.exit(1);
}
