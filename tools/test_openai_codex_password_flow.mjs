import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {test} from 'node:test';
import {loginCodexPassword} from './openai_codex_password_flow.mjs';

function fixture(override = {}) {
  const options = {authBase: 'https://auth.openai.com', clientId: 'app_EMoamEEZ73f0CkXaXp7hrann',
    redirectUri: 'http://localhost:1455/auth/callback', email: 'fixture@example.test',
    password: 'synthetic-password', totpSecret: 'synthetic-totp'};
  const calls = [];
  let authorization;
  const client = {jar: {value: () => 'device', merge: () => {}}, follow: async url => {
    calls.push('follow');
    if (url.includes('/oauth/authorize?')) { authorization = new URL(url); return {finalUrl: options.authBase + '/log-in'}; }
    return {finalUrl: url};
  }};
  const helpers = {
    isPasswordLoginPage: raw => new URL(raw).pathname === '/log-in/password',
    isAuthLoginPage: raw => new URL(raw).pathname === '/log-in',
    getContinueUrl: payload => payload?.continue_url,
    createSentinelHeaders: async () => ({}),
    authJsonStep: async (value, authBase, method, path, body) => {
      assert.equal(method, 'POST'); assert.equal(path, '/api/accounts/authorize/continue');
      assert.deepEqual(body, {username: {kind: 'email', value: options.email}});
      return {data: {continue_url: authBase + '/log-in/password'}};
    },
    verifyPassword: async (value, input) => { assert.equal(input.password, options.password); calls.push('password'); return {mfa: true}; },
    isMfaChallengePayload: payload => payload.mfa === true,
    completeTotpMfaIfNeeded: async (value, input) => { assert.equal(input.totpSecret, options.totpSecret); calls.push('totp'); return {}; },
    finishCodexOauth: async (value, input, payload, oauth) => {
      assert.equal(input.authBase, options.authBase);
      assert.equal(oauth.state, authorization.searchParams.get('state'));
      calls.push('workspace');
      return {callbackUrl: options.redirectUri + '?code=fixture&state=' + oauth.state, ...oauth};
    },
    ...override,
  };
  return {options, client, helpers, calls, authorization: () => authorization};
}

test('direct Codex login binds password, TOTP, callback and PKCE to the same request', async () => {
  const f = fixture();
  const result = await loginCodexPassword(f.client, f.options, f.helpers);
  assert.equal(result.mfaVerified, true);
  assert.deepEqual(f.calls, ['follow', 'follow', 'password', 'totp', 'workspace']);
  const q = f.authorization().searchParams;
  assert.equal(q.get('client_id'), f.options.clientId);
  assert.equal(q.get('redirect_uri'), f.options.redirectUri);
  assert.equal(q.get('code_challenge'), crypto.createHash('sha256').update(f.client.codexPasswordOAuth.codeVerifier).digest('base64url'));
});

test('an official additional verification stops before password or TOTP submission', async () => {
  const f = fixture({authJsonStep: async () => ({data: {continue_url: 'https://auth.openai.com/email-verification'}})});
  await assert.rejects(loginCodexPassword(f.client, f.options, f.helpers), {message: 'email_verification_required'});
  assert.deepEqual(f.calls, ['follow']);
  assert.equal(f.client.codexPasswordOAuth, undefined);
});

test('foreign password continuation is rejected before credentials are sent', async () => {
  const f = fixture({authJsonStep: async () => ({data: {continue_url: 'https://foreign.example/log-in/password'}})});
  await assert.rejects(loginCodexPassword(f.client, f.options, f.helpers), {message: 'protocol_error'});
  assert.deepEqual(f.calls, ['follow']);
  assert.equal(f.client.codexPasswordOAuth, undefined);
});

test('invalid callback cannot become a usable OAuth result', async () => {
  for (const callback of ['http://localhost:1455/auth/callback?code=x&state=wrong',
    'http://localhost:1455/auth/callback?code=x&state=x&state=y', 'https://foreign.example/callback?code=x']) {
    const f = fixture({finishCodexOauth: async () => ({callbackUrl: callback})});
    await assert.rejects(loginCodexPassword(f.client, f.options, f.helpers));
    assert.equal(f.client.codexPasswordOAuth, undefined);
  }
});

test('other OAuth clients cannot select the Codex password helper', async () => {
  const f = fixture();
  await assert.rejects(loginCodexPassword(f.client, {...f.options, clientId: 'other-client'}, f.helpers), {message: 'protocol_error'});
  assert.deepEqual(f.calls, []);
});
