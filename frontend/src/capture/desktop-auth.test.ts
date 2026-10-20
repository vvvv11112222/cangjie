// @vitest-environment jsdom
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const html = readFileSync(resolve('public/capture-demo.html'), 'utf8');
const script = html.match(/<script type="module">([\s\S]*?)<\/script>/)?.[1];
if (!script) throw Error('Missing desktop capture script');
const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const json = (data: unknown, status = 200) => new Response(JSON.stringify({ data }), { status });

beforeEach(() => { document.body.innerHTML = html; });
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it('blocks capture creation until the current origin is authenticated', async () => {
  const fetch = vi.fn(async () => json(null, 401));
  vi.stubGlobal('fetch', fetch);
  window.eval(script);
  await vi.waitFor(() => expect(element('login-form').hidden).toBe(false));
  expect(element<HTMLButtonElement>('create').disabled).toBe(true);
  element('create').click();
  expect(fetch.mock.calls.length).toBe(1);
  expect(element('account-status').textContent).toContain('尚未登录');
});

it('uses the existing CSRF/login session, clears the password, and enables capture', async () => {
  let loggedIn = false;
  const fetch = vi.fn(async (path: string, options?: RequestInit) => {
    if (path.endsWith('/auth/me')) return json(loggedIn ? { display_name: '测试管理员' } : null, loggedIn ? 200 : 401);
    if (path.endsWith('/auth/csrf')) return json({ csrf_token: 'login-csrf' });
    expect(path).toBe('/api/v1/auth/login');
    expect(options?.headers).toMatchObject({ 'X-CSRF-Token': 'login-csrf' });
    expect(options?.credentials).toBe('same-origin');
    loggedIn = true;
    return json({ csrf_token: 'rotated-csrf' });
  });
  vi.stubGlobal('fetch', fetch);
  window.eval(script);
  await vi.waitFor(() => expect(element('login-form').hidden).toBe(false));
  element<HTMLInputElement>('login-username').value = 'existing-admin';
  element<HTMLInputElement>('login-password').value = 'synthetic-password';
  element('login-form').dispatchEvent(new Event('submit', { cancelable: true }));
  await vi.waitFor(() => expect(element('login-form').hidden).toBe(true));
  expect(element<HTMLInputElement>('login-password').value).toBe('');
  expect(element<HTMLButtonElement>('create').disabled).toBe(false);
  expect(element('account-status').textContent).toContain('测试管理员');
});

it('returns to login when the session expires during capture creation', async () => {
  const fetch = vi.fn(async (path: string) => {
    if (path.endsWith('/auth/me')) return json({ display_name: '测试管理员' });
    if (path.endsWith('/auth/csrf')) return json({ csrf_token: 'csrf' });
    return new Response(JSON.stringify({ error: { code: 'UNAUTHENTICATED' } }), { status: 401 });
  });
  vi.stubGlobal('fetch', fetch);
  window.eval(script);
  await vi.waitFor(() => expect(element<HTMLButtonElement>('create').disabled).toBe(false));
  element<HTMLInputElement>('session').value = 'synthetic-session';
  element<HTMLInputElement>('source').value = 'synthetic-source';
  element('create').click();
  await vi.waitFor(() => expect(element('notice').textContent).toContain('请先在本页登录'));
  expect(element('login-form').hidden).toBe(false);
  expect(element<HTMLButtonElement>('create').disabled).toBe(true);
  element('create').click();
  expect(fetch.mock.calls.filter(([path]) => path.includes('/live-captures'))).toHaveLength(1);
});
