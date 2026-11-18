// Runs real pinned toSub2 helpers against an in-memory HTTP fixture.
// Set TOSUB2_ROOT to an already available checkout/package; no real requests.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {execute, loadHelpers, approveConsent, selectWorkspacePage, classified} from './tosub2_adapter.mjs';
import path from 'node:path';
import {pathToFileURL} from 'node:url';

const root = process.env.TOSUB2_ROOT;
if (!root) throw new Error('TOSUB2_ROOT is required for the offline helper fixture');
const helpers = await loadHelpers(root);
const library = await import(pathToFileURL(process.argv[2] || path.join(root, 'node_modules/jsdom/lib/api.js')).href);
const JSDOM = (library.default || library).JSDOM;
const KILO = 'https://app.kilo.ai';
const AUTH = 'https://auth.openai.com';
const email = 'owner@example.test';
const requests = [];
const sessionExpires = new Date(Date.now() + 7 * 86400000).toISOString();
let sourceEmail = email;
let webIdentity = 'kilo-user-1';
let profileIdentity = 'kilo-user-1';
let requireWebSession = false;
let rejectOriginalLogin = false;
let expectedPassword = 'fixture-only';
let callbackError = '';
let connected = false;
let mfa = false;
let consentOnly = false;
let workspaceOnly = false;
let directCallback = false;
let selectionResult = 'accepted';
let selectedWorkspace = 'personal-workspace';
let permissionConsent = false;
let permissionAvailable = true;
let connectionTime = null;
let directPersonalConsent = false;
let callbackCounter = 0;
const SOURCE_CLIENT = 'app_EMoamEEZ73f0CkXaXp7hrann';
const SOURCE_CALLBACK = 'http://localhost:1455/auth/callback';
const signing = crypto.generateKeyPairSync('rsa', {modulusLength: 2048});
const jwk = {...signing.publicKey.export({format: 'jwk'}), kid: 'offline-key', alg: 'RS256', use: 'sig'};
const providerCreated = new Date(Date.now() - 86400000).toISOString();
let sourceAuthorization = null;
let sourceFlow = false;
let identityOnlyCallback = false;
let freshClaims = {};
let invalidFreshSignature = false;
let providerReads = 0;
let changeProvider = false;
function sourceJWT(claims = {}) {
  const now = Math.floor(Date.now() / 1000);
  const head = Buffer.from(JSON.stringify({alg: 'RS256', kid: jwk.kid})).toString('base64url');
  const body = Buffer.from(JSON.stringify({iss: AUTH, aud: SOURCE_CLIENT, sub: 'codex-original-sub', email,
    iat: now, exp: now + 300, 'https://api.openai.com/auth': {chatgpt_account_id: 'personal-workspace', chatgpt_plan_type: 'plus'}, ...claims})).toString('base64url');
  const signed = head + '.' + body;
  return signed + '.' + crypto.sign('RSA-SHA256', Buffer.from(signed), signing.privateKey).toString('base64url');
}
const consentHTML = '<form method="post" action="/sign-in-with-chatgpt/consent"><input type="hidden" name="auth_session_id" value="private-auth-session"><button type="submit" name="resource_consent_decision" value="deny">Deny</button><button type="submit" name="resource_consent_decision" value="grant">Continue</button></form>';
const workspaceHTML = '<form method="post" action="/workspace"><button name="workspace_id" value="team-workspace">Business team</button><button name="workspace_id" value="personal-workspace">Personal account</button></form>';
const consentV2HTML = '<form method="post" action="/sign-in-with-chatgpt/consent"><input type="hidden" name="auth_session_id" value="private-auth-session"><input type="hidden" name="permission_selection.context" value="fixture-context"><input type="hidden" name="permission_selection.selected_permissions[0]" value="identity"><input type="hidden" name="permission_selection.selected_permissions[1]" value="resource_invoke"><input type="hidden" name="permission_selection.selected_permissions[2]" value="offline_access"><button type="submit" name="resource_consent_decision" value="deny">Cancel</button><button disabled type="submit" name="resource_consent_decision" value="grant">Continue</button></form>';
const permissionKinds = ['identity', 'resource_invoke', 'offline_access', 'chatgpt_allowance'];
function response(status, body, headers = {}, cookies = []) {
  const h = new Headers(headers);
  Object.defineProperty(h, 'rawSetCookie', {value: cookies});
  return {status, headers: h, text: async () => typeof body === 'string' ? body : JSON.stringify(body)};
}
function authorizeURL(connect) {
  return AUTH + '/api/accounts/authorize?' + new URLSearchParams({client_id: 'official-kilo-client', redirect_uri: KILO + '/auth/openai/callback', response_type: 'code', state: connect ? 'byok-state' : 'login-state', nonce: 'official-nonce', code_challenge_method: 'S256', code_challenge: 'official-pkce', scope: connect ? 'openid profile email offline_access resource.invoke chatgpt.tokens.use.obo' : 'openid profile email'});
}
const fetch = async (raw, opts) => {
  const url = new URL(raw);
  const body = opts.body ? JSON.parse(opts.headers.get('content-type') === 'application/json' ? opts.body : '{}') : {};
  requests.push({url: url.pathname, method: opts.method, body});
  if (url.pathname === '/api/profile') {
    assert.equal(url.origin, 'https://api.kilo.ai');
    assert.equal(opts.headers.get('authorization'), 'Bearer fixture-kilo-key');
    return response(200, {user: {id: profileIdentity, email}});
  }
  assert.equal(opts.headers.get('authorization'), null);
  if (url.pathname === '/users/sign_in') return response(200, '<html/>');
  if (url.pathname === '/api/auth/verify-turnstile') return response(200, {success: true});
  if (url.pathname === '/api/auth/csrf') return response(200, {csrfToken: 'csrf-owned'});
  if (url.pathname === '/api/auth/signin/openai') {
    const connect = url.searchParams.has('force_reconsent');
    assert.equal(url.searchParams.get('login_hint'), connect ? sourceEmail : email);
    return response(200, {url: authorizeURL(connect)});
  }
  if (url.pathname === '/oauth/authorize') {
    assert.equal(url.origin, AUTH);
    assert.equal(url.searchParams.get('client_id'), SOURCE_CLIENT);
    assert.equal(url.searchParams.get('redirect_uri'), SOURCE_CALLBACK);
    assert.equal(opts.headers.get('cookie'), null, 'source authentication must start with an empty jar');
    sourceAuthorization = url;
    sourceFlow = true;
    return response(302, '', {location: AUTH + '/log-in/password'});
  }
  if (url.pathname === '/oauth/token') {
    const form = new URLSearchParams(opts.body);
    assert.equal(form.get('client_id'), SOURCE_CLIENT);
    assert.equal(form.get('grant_type'), 'authorization_code');
    assert.equal(form.get('code'), 'source-code');
    assert.equal(crypto.createHash('sha256').update(form.get('code_verifier')).digest('base64url'), sourceAuthorization.searchParams.get('code_challenge'));
    let token = sourceJWT({nonce: sourceAuthorization.searchParams.get('nonce'), ...freshClaims});
    if (invalidFreshSignature) token = token.slice(0, -8) + 'AAAAAAAA';
    sourceFlow = false;
    return response(200, {id_token: token, access_token: 'discarded-offline-access', refresh_token: 'discarded-offline-refresh'});
  }
  if (url.pathname === '/.well-known/jwks.json') return response(200, {keys: [jwk]});
  if (url.pathname === '/api/accounts/authorize') {
    connected = url.searchParams.get('state') === 'byok-state';
    if (directCallback && connected) return response(302, '', {location: KILO + '/auth/openai/callback?code=unselected-code&state=byok-state'});
    return response(302, '', {location: AUTH + (workspaceOnly ? '/workspace' : consentOnly ? '/sign-in-with-chatgpt/consent' : '/log-in/password')});
  }
  if (url.pathname === '/log-in/password') return response(200, '<html>password</html>');
  if (url.pathname === '/api/accounts/password/verify') {
    assert.equal(body.password, expectedPassword);
    if (rejectOriginalLogin) return response(401, {error: {code: 'invalid_password', message: 'invalid password'}});
    return response(200, {page: {type: 'mfa_challenge', payload: {factor_id: 'factor'}}, continue_url: AUTH + '/mfa-challenge/factor'}, {}, ['openai-session=fixture-openai; Path=/; Secure; Max-Age=3600']);
  }
  if (url.pathname === '/api/accounts/mfa/issue_challenge') return response(200, {});
  if (url.pathname === '/api/accounts/mfa/verify') {
    mfa = true;
    if (sourceFlow) return response(200, {continue_url: SOURCE_CALLBACK + '?' + new URLSearchParams({code: 'source-code', state: sourceAuthorization.searchParams.get('state')})});
    if (directPersonalConsent && connected) return response(200, {
      page: {type: 'sign_in_with_chatgpt_consent'}, continue_url: AUTH + '/sign-in-with-chatgpt/consent',
      'oai-client-auth-session': {session_id: 'private-auth-session', email: sourceEmail, username: {kind: 'email', value: sourceEmail}},
    });
    return response(200, {page: {type: 'workspace', payload: {workspaces: [{kind: 'organization', id: 'team-workspace'}, {kind: 'personal', id: 'personal-workspace'}]}}, continue_url: AUTH + '/workspace'});
  }
  if (url.pathname === '/workspace') return response(200, workspaceHTML);
  if (url.pathname === '/api/accounts/workspace/select') {
    assert.deepEqual(body, {workspace_id: selectedWorkspace});
    if (selectionResult === 'pending') return response(200, {page: {type: 'workspace'}, continue_url: AUTH + '/workspace'});
    if (selectionResult === 'error') return response(200, {error: {code: 'invalid_workspace'}, continue_url: AUTH + '/sign-in-with-chatgpt/consent'});
    if (identityOnlyCallback && !connected) return response(200, {continue_url: KILO + '/auth/openai/callback?code=identity-code&state=login-state'});
    return response(200, {page: {type: 'sign_in_with_chatgpt_consent', payload: {workspaces: [{kind: 'organization', id: 'team-workspace'}, {kind: 'personal', id: 'personal-workspace'}]}}, continue_url: AUTH + '/sign-in-with-chatgpt/consent'});
  }
  if (url.pathname === '/sign-in-with-chatgpt/consent') return response(200, permissionConsent ? consentV2HTML : consentHTML);
  if (url.pathname === '/api/accounts/client_auth_session_dump') return response(200, {client_auth_session: {session_id: 'private-auth-session', email: sourceEmail, username: {kind:'email',value:sourceEmail}, oauth_resource_consent: {version: 2, resource: 'https://api.openai.com/v1', permissions: permissionKinds.map(kind => ({kind})), selection: {context: 'fixture-context', required_permissions: ['identity', 'resource_invoke', 'offline_access']}}}});
  if (url.pathname === '/api/accounts/consent/availability') {
    assert.deepEqual(body, {auth_session_id: 'private-auth-session', context: 'fixture-context'});
    assert.equal(opts.headers.get('OpenAI-Consent-Decision'), 'v1');
    return response(200, {context: 'fixture-context', availability_receipt: 'fixture-receipt', permissions: permissionKinds.map(kind => ({kind, availability: kind === 'chatgpt_allowance' && !permissionAvailable ? 'upgrade_required' : 'available'}))});
  }
  if (url.pathname === '/api/accounts/consent/grant') {
    assert.deepEqual(body, permissionConsent ? {auth_session_id: 'private-auth-session', permission_selection: {context: 'fixture-context', selected_permissions: permissionKinds, availability_receipt: 'fixture-receipt'}} : {auth_session_id: 'private-auth-session'});
    assert.equal(opts.headers.get('origin'), AUTH);
    assert.equal(opts.headers.get('referer'), AUTH + '/sign-in-with-chatgpt/consent');
    return response(200, {continue_url: KILO + '/auth/openai/callback?' + new URLSearchParams({code: 'one-use-code', state: connected ? 'byok-state' : 'login-state'})});
  }
  if (url.pathname === '/auth/openai/callback') {
    if (connected) {
      if (requireWebSession) assert.match(opts.headers.get('cookie') || '', /kilo-web-session=fixture-kilo/);
      if (callbackError) return response(302, '', {location: KILO + '/byok?' + new URLSearchParams({openai_error: callbackError})});
      callbackCounter++;
      connectionTime = new Date(Date.now()).toISOString();
    }
    return response(302, '', {location: KILO + (connected ? '/byok' : '/')}, ['kilo-web-session=fixture-kilo; Path=/; Secure; Max-Age=604800']);
  }
  if (url.pathname === '/' || url.pathname === '/byok') return response(200, '<html/>');
  if (url.pathname === '/api/auth/session') {
    if (requireWebSession && !/kilo-web-session=fixture-kilo/.test(opts.headers.get('cookie') || '')) return response(200, {});
    return response(200, {user: {id: webIdentity, email}, kiloUserId: webIdentity, expires: sessionExpires});
  }
  if (url.pathname === '/api/trpc/user.linkAuthProvider') { assert.deepEqual(body, {provider: 'openai'}); return response(200, {result: {data: {success: true}}}); }
  if (url.pathname === '/api/trpc/user.getAuthProviders') {
    providerReads++;
    return response(200, {result: {data: {success: true, providers: [{provider: 'openai', created_at: providerCreated,
      id: changeProvider && providerReads > 1 ? 'changed-provider' : 'original-provider'}]}}});
  }
  if (url.pathname === '/api/trpc/openAiChatGpt.status') return response(200, {result: {data: connectionTime ? {state: 'connected', email: sourceEmail, subject: 'oidc-sub', connectedAt: connectionTime} : {state: 'disconnected'}}});
  if (url.pathname === '/api/device-auth/tokens') { assert.deepEqual(body, {code: 'ABCD-EFGH'}); return response(200, {success: true}); }
  throw new Error('unexpected fixture URL ' + raw);
};
const input = {config: {tosub2_root: root}, phase: 'login', expected_identity: {source_email: email, source_workspace_id: 'personal-workspace', source_plan_type: 'plus'}, login_proxy: {id: 1, url: 'http://127.0.0.1:1'}, credential: {email, password: 'fixture-only', totp_secret: 'JBSWY3DPEHPK3PXP'}, deadline_at: new Date(Date.now() + 60000).toISOString(), turnstile_token: 'fixture'};
const dependencies = {helpers, fetch, jsdomClass: JSDOM, sentinel: async () => ({token: 'fixture-sentinel'})};
const login = await execute(input, dependencies);
assert.equal(login.verified_identity.kilo_user_id, 'kilo-user-1');
assert(mfa);
const byok = await execute({...input, phase: 'connect_subscription', private_state: login.private_state}, dependencies);
assert.equal(byok.byok_enabled, true);
assert.equal(byok.connection_email, email);
assert.equal(byok.byok_evidence.oauth_callback_verified, true);
assert.equal(byok.byok_evidence.consent_granted, true);
assert.equal(byok.byok_evidence.kilo_user_id, 'kilo-user-1');
assert.equal(byok.byok_evidence.organization_id, '');
assert.equal(byok.byok_evidence.connected_at, connectionTime);
assert.equal(byok.byok_evidence.workspace_evidence, 'accepted_workspace_selection');
// These legacy cases exercise NEW grants, not observation of an existing connection.
// Explicit disconnection keeps the fresh-authorization precondition visible.
connectionTime = null;
// A direct consent page still needs a successful selection in this attempt.
consentOnly = true;
await assert.rejects(execute({...input, phase: 'connect_subscription', private_state: byok.private_state}, dependencies), error => error.code === 'IDENTITY_MISMATCH');
consentOnly = false;
// An unselected direct callback cannot prove this attempt's workspace.
for (const route of ['callback']) {
  consentOnly = route === 'consent';
  directCallback = route === 'callback';
  const mark = requests.length;
  await assert.rejects(execute({...input, phase: 'connect_subscription', private_state: byok.private_state}, dependencies), error => error.code === 'IDENTITY_MISMATCH');
  assert(!requests.slice(mark).some(request => ['/api/accounts/consent/grant', '/auth/openai/callback'].includes(request.url)));
}
consentOnly = false;
directCallback = false;
workspaceOnly = true;
const beforeWorkspace = requests.length;
const workspaceReconnected = await execute({...input, phase: 'connect_subscription', private_state: byok.private_state}, dependencies);
assert.equal(workspaceReconnected.byok_evidence.consent_granted, true);
assert.equal(workspaceReconnected.connection_email, email);
assert(!requests.slice(beforeWorkspace).some(request => request.url.includes('/password/') || request.url.includes('/mfa/')));
assert.equal(requests.slice(beforeWorkspace).filter(request => request.url === '/api/accounts/workspace/select').length, 1);
workspaceOnly = false;
for (const unsupported of [workspaceHTML.replace('value="personal-workspace"', 'value="not-the-source"'), workspaceHTML.replace('value="personal-workspace"', 'type="button" value="personal-workspace"'), workspaceHTML.replace('</form>', '<button name="workspace_id" value="personal-workspace">Another</button></form>'), workspaceHTML.replace('</form>', '<input required name="additional_verification"></form>')]) {
  await assert.rejects(selectWorkspacePage({}, {finalUrl: AUTH + '/workspace', last: {text: unsupported}}, helpers, root, JSDOM, input.expected_identity), error => ['IDENTITY_MISMATCH', 'ACTION_REQUIRED'].includes(error.code));
}
connectionTime = null;
selectedWorkspace = 'team-workspace';
workspaceOnly = true;
const businessInput = {...input, expected_identity: {source_email: email, source_workspace_id: 'team-workspace', source_plan_type: 'self_serve_business_prolite'}};
const business = await execute({...businessInput, phase: 'connect_subscription', private_state: login.private_state}, dependencies);
assert.equal(business.connection_workspace_id, 'team-workspace');
assert.equal(business.byok_evidence.openai_workspace_id, 'team-workspace');
permissionConsent = true;
connectionTime = null;
const permissionConnected = await execute({...businessInput, phase: 'connect_subscription', private_state: business.private_state}, dependencies);
assert.equal(permissionConnected.byok_enabled, true);
const beforeDenied = requests.filter(request => request.url === '/api/accounts/consent/grant').length;
permissionAvailable = false;
connectionTime = null;
await assert.rejects(execute({...businessInput, phase: 'connect_subscription', private_state: permissionConnected.private_state}, dependencies), error => error.code === 'BYOK_NOT_CONNECTED');
assert.equal(requests.filter(request => request.url === '/api/accounts/consent/grant').length, beforeDenied);
permissionAvailable = true;
for (selectionResult of ['pending', 'error']) {
  const mark = requests.length;
  await assert.rejects(execute({...businessInput, phase: 'connect_subscription', private_state: business.private_state}, dependencies), error => error.code === 'IDENTITY_MISMATCH');
  assert(!requests.slice(mark).some(request => ['/api/accounts/consent/grant', '/auth/openai/callback'].includes(request.url)));
}
selectionResult = 'accepted';
permissionConsent = false;
permissionAvailable = true;
workspaceOnly = false;
selectedWorkspace = 'personal-workspace';
const device = await execute({...input, phase: 'approve_device', private_state: byok.private_state, device: {verification_url: KILO + '/device-auth?code=ABCD-EFGH', user_code: 'ABCD-EFGH'}}, dependencies);
assert.equal(device.device_approved, true);
assert(!requests.some(x => /device-auth\/(codes|token$)/.test(x.url)));
await assert.rejects(execute({...input, phase: 'approve_device', private_state: byok.private_state, device: {verification_url: KILO + '/device-auth?code=wrong', user_code: 'ABCD-EFGH'}}, dependencies), error => error.code === 'DEVICE_INPUT_INVALID');
const grantCount = requests.filter(x => x.url === '/api/accounts/consent/grant').length;
assert.equal(grantCount, 5);
for (const unsupported of [consentHTML.replace('type="hidden"', 'required type="checkbox"'), consentHTML.replace('auth_session_id', 'permission_selection_models'), consentHTML.replace('</form>', '<button type="submit" name="resource_consent_decision" value="grant">Allow</button></form>')]) {
  await assert.rejects(approveConsent({}, {finalUrl: AUTH + '/sign-in-with-chatgpt/consent', last: {text: unsupported}}, helpers, 'fixture-device', root, JSDOM), error => error.code === 'ACTION_REQUIRED');
}
// Durable identity material survives closing the runtime handle and excludes
// the replaceable OpenAI login, passwords, keys and source identity.
requireWebSession = true;
const material = JSON.parse(device.identity_session_material);
assert.equal(material.version, 1);
assert.equal(material.kilo_user_id, 'kilo-user-1');
assert.equal(material.kilo_email, email);
assert.equal(material.expires_at, sessionExpires);
assert(Date.parse(material.expires_at) > Date.now() + 900000);
assert(material.cookies.some(cookie => cookie.name === 'kilo-web-session'));
assert(material.cookies.every(cookie => ['app.kilo.ai', 'kilo.ai', 'api.kilo.ai'].includes(cookie.domain)));
assert(!device.identity_session_material.includes('fixture-openai'));
assert(!device.identity_session_material.includes('fixture-only'));
const identity = {api_key: 'fixture-kilo-key', user_id: 'kilo-user-1', email, session_material: device.identity_session_material};
const nextSource = {source_email: 'new-source@example.test', source_workspace_id: 'personal-workspace', source_plan_type: 'plus'};
const resumeInput = {...input, phase: 'resume_identity', expected_identity: nextSource, kilo_identity: identity,
  credential: {email: nextSource.source_email, password: 'new-source-password', totp_secret: 'JBSWY3DPEHPK3PXP'}};
const restoreMark = requests.length;
const resumed = await execute(resumeInput, dependencies);
assert.equal(resumed.verified_identity.kilo_user_id, identity.user_id);
assert.equal(resumed.verified_identity.openai_email, '');
assert(!requests.slice(restoreMark).some(request => request.url.includes('/password/') || request.url.includes('/mfa/') || request.url === '/users/sign_in'));
sourceEmail = nextSource.source_email;
selectedWorkspace = nextSource.source_workspace_id;
expectedPassword = 'new-source-password';
const connectInput = {...resumeInput, phase: 'connect_subscription', private_state: resumed.private_state};
// This is an allowed-subscription fixture, not proof that the live upstream
// permits a different subscriber. The immutable Kilo owner must stay the same.
const connectMark = requests.length;
connectionTime = null;
const switched = await execute(connectInput, dependencies);
assert.equal(switched.connection_email, nextSource.source_email);
assert.equal(switched.connection_workspace_id, nextSource.source_workspace_id);
assert.equal(switched.byok_evidence.kilo_user_id, identity.user_id);
assert.equal(JSON.parse(switched.identity_session_material).kilo_email, email);
assert(!requests.slice(connectMark).some(request => request.url === '/users/sign_in'));
assert(!requests.some(request => ['/api/trpc/user.unlinkAuthProvider', '/api/trpc/user.getAuthProviders'].includes(request.url)));
// An actual callback refusal is kept as a subscription authorization failure;
// it does not cause login-provider mutation or replacement of the Kilo owner.
callbackError = 'PROVIDER-ALREADY-LINKED';
connectionTime = null;
await assert.rejects(execute(connectInput, dependencies), error => error.code === 'BYOK_AUTHORIZATION_REJECTED' && !error.retryable);
callbackError = '';
assert.equal(classified(new Error('PROVIDER-ALREADY-LINKED')).error_code, 'BYOK_AUTHORIZATION_REJECTED');
// A missing jar cannot be repaired with the NEW source's password.
await assert.rejects(execute({...resumeInput, kilo_identity: {...identity, session_material: ''}}, dependencies), error => error.code === 'AUTH_SESSION_EXPIRED');
const originalLogin = {email, password: 'original-kilo-password', totp_secret: 'JBSWY3DPEHPK3PXP'};
expectedPassword = originalLogin.password;
sourceEmail = email;
// Identity-only Kilo sign-in selects its own workspace rather than the NEW
// subscriber's expected workspace. The pinned helper picks the first workspace
// returned for the original login, which is distinct from nextSource here.
selectedWorkspace = 'team-workspace';
const recovered = await execute({...resumeInput, turnstile_token: 'fixture', kilo_identity: {...identity, session_material: '', credential: originalLogin}}, dependencies);
assert.equal(recovered.verified_identity.kilo_user_id, identity.user_id);
assert.equal(recovered.verified_identity.openai_email, '');
rejectOriginalLogin = true;
await assert.rejects(execute({...resumeInput, turnstile_token: 'fixture', kilo_identity: {...identity, session_material: '', credential: originalLogin}}, dependencies), /KILO_PASSWORD_REJECTED/);
rejectOriginalLogin = false;
webIdentity = 'wrong-kilo';
await assert.rejects(execute(resumeInput, dependencies), error => error.code === 'IDENTITY_MISMATCH');
webIdentity = identity.user_id;
profileIdentity = 'wrong-kilo';
await assert.rejects(execute(resumeInput, dependencies), error => error.code === 'IDENTITY_MISMATCH');
profileIdentity = identity.user_id;
await assert.rejects(execute({...resumeInput, login_proxy: {...input.login_proxy, id: 99}}, dependencies), error => error.code === 'LOGIN_PROXY_CONFLICT');
await assert.rejects(execute({...resumeInput, kilo_identity: {...identity, session_material: '{invalid'}}, dependencies), error => error.code === 'IDENTITY_SESSION_INVALID');
// Direct personal consent proves the subject, never a fabricated workspace ID.
sourceEmail = email;
expectedPassword = input.credential.password;
selectedWorkspace = 'personal-workspace';
const personalIdentity = {...input.expected_identity, source_subject: 'oidc-sub', source_issuer: AUTH};
const personalLogin = await execute({...input, expected_identity: personalIdentity}, dependencies);
directPersonalConsent = true;
selectionResult = 'error';
connectionTime = null;
freshClaims = {sub: 'oidc-sub'};
const personalInput = {...input, phase: 'connect_subscription', private_state: personalLogin.private_state,
  expected_identity: personalIdentity, source_id_token: sourceJWT({sub: 'oidc-sub'})};
const personalMark = requests.length;
const confirmedPersonal = await execute(personalInput, dependencies);
assert.equal(confirmedPersonal.connection_workspace_id, '');
assert.equal(confirmedPersonal.connection_identity_evidence, 'personal_subject');
assert.equal(confirmedPersonal.byok_evidence.openai_workspace_id, '');
assert(!requests.slice(personalMark).some(request => request.url === '/api/accounts/workspace/select'));
connectionTime = null;
await assert.rejects(execute({...personalInput, expected_identity: {...personalInput.expected_identity, source_subject:'other-subject'}}, dependencies), error => error.code === 'IDENTITY_MISMATCH');
const businessMark = requests.length;
connectionTime = null;
await assert.rejects(execute({...personalInput, expected_identity: {...personalInput.expected_identity, source_plan_type:'business'}}, dependencies), error => error.code === 'IDENTITY_MISMATCH');
assert(!requests.slice(businessMark).some(request => request.url === '/api/accounts/consent/grant'));
// Different client subjects are associated only by two independent credential
// authentications and signed original/fresh source tickets, never by email alone.
selectionResult = 'accepted';
freshClaims = {};
const bridgeIdentity = {...personalInput.expected_identity, source_subject: 'codex-original-sub'};
const bridgeBase = {...input, expected_identity: bridgeIdentity};
// Force separate implicit and explicit clock reads one millisecond apart.
// A newly issued proof must still satisfy the verifier's 15-minute lifetime cap.
const RealDate = Date;
const controlTime = RealDate.now();
class AdvancingDate extends RealDate {
  constructor(...args) { super(...(args.length ? args : [controlTime])); }
  static now() { return controlTime + 1; }
}
let bridgeLogin;
globalThis.Date = AdvancingDate;
try { bridgeLogin = await execute(bridgeBase, dependencies); }
finally { globalThis.Date = RealDate; }
assert(bridgeLogin.private_state.credential_control);
assert.equal(Date.parse(bridgeLogin.private_state.credential_control.expires_at) -
  Date.parse(bridgeLogin.private_state.credential_control.verified_at), 900000);
const snapshot = sourceJWT({iat: Math.floor(Date.now() / 1000) - 7200, exp: Math.floor(Date.now() / 1000) - 3600});
const bridgeConnect = {...bridgeBase, phase: 'connect_subscription', private_state: bridgeLogin.private_state, source_id_token: snapshot};
connectionTime = null;
const associatedMark = requests.length;
const associated = await execute(bridgeConnect, dependencies);
const associatedRequests = requests.slice(associatedMark);
const sourceVerifiedAt = associatedRequests.findIndex(request => request.url === '/.well-known/jwks.json');
assert(sourceVerifiedAt >= 0);
assert(associatedRequests.findIndex(request => request.url === '/api/trpc/user.linkAuthProvider') > sourceVerifiedAt);
assert(associatedRequests.findIndex(request => request.url === '/api/accounts/consent/grant') > sourceVerifiedAt);
assert(associatedRequests.findIndex(request => request.url === '/auth/openai/callback') > sourceVerifiedAt);
assert.equal(associated.connection_subject, 'oidc-sub');
assert.equal(associated.identity_association.source_subject, 'codex-original-sub');
assert.equal(associated.identity_association.kilo_subject, 'oidc-sub');
assert.equal(associated.identity_association.method, 'credential_snapshot');
assert.equal(associated.connection_workspace_id, '');
assert.equal(associated.byok_enabled, true);
assert.equal(associated.byok_enable_evidence, 'fresh_delegated_authorization');
assert.equal(associated.byok_evidence.workspace_evidence, 'credential_snapshot');
assert.equal(Object.keys(associated.identity_association).length, 10);
assert(!associated.private_state.credential_control);
assert(!JSON.stringify(associated).includes('discarded-offline'));
// Explicit recovery checks the saved owner and a disconnected subscription
// before authenticating, then uses the same guarded personal grant path.
const reauthorizeInput = {...bridgeBase, phase: 'reauthorize_subscription', source_id_token: snapshot,
  kilo_identity: {user_id: identity.user_id, email, session_material: bridgeLogin.identity_session_material}};
const connectedMark = requests.length;
await assert.rejects(execute(reauthorizeInput, dependencies), error => error.code === 'BYOK_ENABLE_UNVERIFIED');
assert(requests.slice(connectedMark).every(request => request.method === 'GET'));
const unknownMark = requests.length;
await assert.rejects(execute(reauthorizeInput, {...dependencies, fetch: async (url, options) =>
  new URL(url).pathname === '/api/trpc/openAiChatGpt.status' ? response(200, {result: {data: {state: 'error'}}}) : fetch(url, options)}),
  error => error.code === 'BYOK_ENABLE_UNVERIFIED');
assert(requests.slice(unknownMark).every(request => request.method === 'GET'));
await assert.rejects(execute({...reauthorizeInput, kilo_identity: {...reauthorizeInput.kilo_identity, user_id: 'other-owner'}}, dependencies),
  error => error.code === 'IDENTITY_MISMATCH');
connectionTime = null;
identityOnlyCallback = true;
let recoveryStatusReads = 0;
const changedMark = requests.length;
await assert.rejects(execute(reauthorizeInput, {...dependencies, fetch: async (url, options) => {
  if (new URL(url).pathname === '/api/trpc/openAiChatGpt.status' && ++recoveryStatusReads === 2)
    return response(200, {result: {data: {state: 'connected'}}});
  return fetch(url, options);
}}), error => error.code === 'BYOK_ENABLE_UNVERIFIED');
assert(!requests.slice(changedMark).some(request => request.url === '/api/trpc/user.linkAuthProvider'));
connectionTime = null;
const recoveredSubscription = await execute(reauthorizeInput, dependencies);
assert.equal(recoveredSubscription.verified_identity.kilo_user_id, identity.user_id);
assert.equal(recoveredSubscription.byok_enable_evidence, 'fresh_delegated_authorization');
assert.equal(recoveredSubscription.byok_enabled, true);
assert.equal(recoveredSubscription.identity_association.source_subject, bridgeIdentity.source_subject);
assert.equal(recoveredSubscription.connection_workspace_id, '');
identityOnlyCallback = false;
async function rejectedBeforeGrant(request) {
  const mark = requests.length;
  const callbacks = callbackCounter;
  connectionTime = null;
  await assert.rejects(execute(request, dependencies), error => error.code === 'IDENTITY_MISMATCH');
  assert(!requests.slice(mark).some(item => ['/api/trpc/user.linkAuthProvider', '/api/accounts/consent/grant', '/auth/openai/callback', '/api/auth/callback/openai', '/api/device-auth/tokens'].includes(item.url)));
  assert.equal(callbackCounter, callbacks);
  assert.equal(connectionTime, null);
}
for (const mutation of [{nonce: 'wrong-nonce'}, {sub: 'other-source'}, {aud: 'other-client'}, {iss: 'https://other.example.test'},
  {'https://api.openai.com/auth': {chatgpt_account_id: 'other-workspace', chatgpt_plan_type: 'plus'}},
  {'https://api.openai.com/auth': {chatgpt_account_id: 'personal-workspace', chatgpt_plan_type: 'pro'}}]) {
  freshClaims = mutation;
  await rejectedBeforeGrant(bridgeConnect);
}
freshClaims = {};
invalidFreshSignature = true;
await rejectedBeforeGrant(bridgeConnect);
invalidFreshSignature = false;
await rejectedBeforeGrant({...bridgeConnect, source_id_token: snapshot.slice(0, -8) + 'AAAAAAAA'});
for (const mutation of [{sub: 'other-source'}, {aud: 'other-client'}, {iss: 'https://other.example.test'}, {email: 'other@example.test'},
  {'https://api.openai.com/auth': {chatgpt_account_id: 'other-workspace', chatgpt_plan_type: 'plus'}},
  {'https://api.openai.com/auth': {chatgpt_account_id: 'personal-workspace', chatgpt_plan_type: 'pro'}}]) {
  await rejectedBeforeGrant({...bridgeConnect, source_id_token: sourceJWT(mutation)});
}
// A reusable independent identity has no same-credential control. Source
// authentication must not grant it a subject association or bypass selection.
assert(recovered.private_state.credential_control, 'original-credential reauthentication must exercise its retained control');
for (const privateState of [resumed.private_state, recovered.private_state]) {
  const independentInput = {...bridgeConnect, private_state: privateState};
  const independentMark = requests.length;
  directPersonalConsent = true;
  connectionTime = null;
  await assert.rejects(execute(independentInput, dependencies), error => error.code === 'IDENTITY_MISMATCH');
  assert(!requests.slice(independentMark).some(request => ['/api/accounts/consent/grant', '/auth/openai/callback'].includes(request.url)));
  directPersonalConsent = false;
  connectionTime = null;
  const selectedIndependent = await execute(independentInput, dependencies);
  assert.equal(selectedIndependent.connection_workspace_id, 'personal-workspace');
  assert.equal(selectedIndependent.connection_identity_evidence, 'accepted_workspace_selection');
  assert.equal(selectedIndependent.byok_enabled, true);
  assert.equal(selectedIndependent.identity_association, undefined);
}
directPersonalConsent = true;
// A matching existing connection must never re-grant just to obtain enabled.
connectionTime = new Date(Date.now() - 3600000).toISOString();
const existingTime = connectionTime;
const existingMark = requests.length;
const existing = await execute(bridgeConnect, dependencies);
assert.equal(existing.byok_enabled, false);
assert.equal(existing.byok_enable_evidence, 'existing_connection_enablement_unknown');
assert.equal(existing.byok_evidence, '');
assert.equal(existing.connection_at, existingTime);
assert(!requests.slice(existingMark).some(request => ['/api/trpc/user.linkAuthProvider', '/api/accounts/consent/grant', '/api/device-auth/tokens'].includes(request.url)));
const reconcileInput = {...bridgeBase, phase: 'reconcile_subscription', source_id_token: snapshot,
  kilo_identity: {user_id: identity.user_id, email, session_material: bridgeLogin.identity_session_material}};
providerReads = 0;
const blockedMark = requests.length;
await assert.rejects(execute(reconcileInput, dependencies), error => error.code === 'ACTION_REQUIRED');
assert(!requests.slice(blockedMark).some(request => request.url === '/api/accounts/consent/grant'));
// A previously linked login returns its identity-only callback without another
// consent. It must not be confused with the subscription's OBO callback.
identityOnlyCallback = true;
providerReads = 0;
const observeMark = requests.length;
const observed = await execute(reconcileInput, dependencies);
assert.equal(observed.byok_enabled, false);
assert.equal(observed.byok_enable_evidence, '');
assert.equal(observed.connection_at, existingTime);
assert.equal(observed.credential_identity_evidence.method, 'credential_snapshot_password_totp');
assert.equal(observed.credential_identity_evidence.source_subject, 'codex-original-sub');
assert.equal(observed.connection_observation.enabled_evidence, 'unverified');
assert.equal(observed.connection_observation.provider_fingerprint, observed.credential_identity_evidence.provider_fingerprint);
assert(!requests.slice(observeMark).some(request => ['/api/trpc/user.linkAuthProvider', '/api/accounts/consent/grant', '/api/device-auth/tokens', '/api/profile'].includes(request.url)));
changeProvider = true;
providerReads = 0;
await assert.rejects(execute(reconcileInput, dependencies), error => error.code === 'IDENTITY_MISMATCH');
changeProvider = false;
console.log('real pinned helpers: exact workspaces, credential association, signed-source tamper rejection and read-only existing observation passed');
