import crypto from 'node:crypto';
import {validateCodexCallback} from './openai_codex_diagnostics.mjs';

// Kilo quota authorization uses the Codex client directly. A separate ChatGPT
// Web login can select email OTP even when the source has password/TOTP material.
export async function loginCodexPassword(client, options, helpers) {
  const {authBase, email, password, totpSecret, rl, clientId, redirectUri} = options;
  if (authBase !== 'https://auth.openai.com' || clientId !== 'app_EMoamEEZ73f0CkXaXp7hrann' ||
      redirectUri !== 'http://localhost:1455/auth/callback') throw new Error('protocol_error');
  if (!email || !password) throw new Error('credentials_incomplete');
  const codeVerifier = crypto.randomBytes(48).toString('base64url');
  const state = crypto.randomBytes(24).toString('base64url');
  const authorization = new URL(authBase + '/oauth/authorize');
  authorization.search = new URLSearchParams({client_id: clientId, redirect_uri: redirectUri,
    response_type: 'code', scope: 'openid profile email offline_access', state,
    code_challenge_method: 'S256', code_challenge: crypto.createHash('sha256').update(codeVerifier).digest('base64url'),
    codex_cli_simplified_flow: 'true', id_token_add_organizations: 'true'}).toString();
  function officialPage(raw) {
    const url = new URL(raw, authBase);
    if (url.origin !== authBase || url.username || url.password || url.hash) throw new Error('protocol_error');
    if (url.pathname === '/email-verification') throw new Error('email_verification_required');
    return url.href;
  }
  let page = await client.follow(authorization.href);
  officialPage(page.finalUrl);
  const deviceId = client.jar.value('oai-did', authBase) || crypto.randomUUID();
  client.jar.merge([{name: 'oai-did', value: deviceId, domain: 'openai.com', path: '/', secure: true, hostOnly: false}]);
  if (!helpers.isPasswordLoginPage(page.finalUrl)) {
    if (!helpers.isAuthLoginPage(page.finalUrl)) throw new Error('interaction_required');
    const headers = await helpers.createSentinelHeaders(client, {authBase, deviceId, flow: 'authorize_continue'});
    const {data} = await helpers.authJsonStep(client, authBase, 'POST', '/api/accounts/authorize/continue',
      {username: {kind: 'email', value: email}}, {referer: page.finalUrl, headers});
    const next = helpers.getContinueUrl(data);
    if (!next) throw new Error('interaction_required');
    const nextURL = officialPage(next);
    if (!helpers.isPasswordLoginPage(nextURL)) throw new Error('interaction_required');
    page = await client.follow(nextURL, {referer: page.finalUrl});
    officialPage(page.finalUrl);
  }
  if (!helpers.isPasswordLoginPage(page.finalUrl)) throw new Error('interaction_required');
  let payload = await helpers.verifyPassword(client, {authBase, rl, deviceId, password, referer: page.finalUrl});
  const mfaRequired = helpers.isMfaChallengePayload(payload);
  payload = await helpers.completeTotpMfaIfNeeded(client, {authBase, rl, deviceId, payload, totpSecret, referer: page.finalUrl});
  const finished = await helpers.finishCodexOauth(client, {authBase}, payload, {codeVerifier, state});
  const callbackUrl = finished?.callbackUrl;
  if (callbackUrl && new URL(callbackUrl, authBase).origin === authBase) officialPage(callbackUrl);
  const oauth = {callbackUrl, codeVerifier, state};
  validateCodexCallback(oauth, redirectUri);
  client.codexPasswordOAuth = oauth;
  return {deviceId, emailVerified: true, loginMethod: 'password', mfaVerified: mfaRequired, callbackUrl};
}
