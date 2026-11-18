// Kilo's OAuth adapter. Password, TOTP, CookieJar and Sentinel header semantics
// come from the pinned toSub2 source; the original CLI is never executed.
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL, fileURLToPath} from 'node:url';
import {spawn} from 'node:child_process';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const KILO = 'https://app.kilo.ai';
const KILO_API = 'https://api.kilo.ai';
const AUTH = 'https://auth.openai.com';
const SOURCE_CLIENT_ID = 'app_EMoamEEZ73f0CkXaXp7hrann';
const SOURCE_REDIRECT = 'http://localhost:1455/auth/callback';
const MAX_IDENTITY_MATERIAL = 64 * 1024;
const KILO_COOKIE_DOMAINS = new Set(['kilo.ai', 'app.kilo.ai', 'api.kilo.ai']);
const OBO_SCOPE = 'openid profile email offline_access resource.invoke chatgpt.tokens.use.obo';
const SOURCE_BLOB = 'ff2db2507e401c29fa78705e44f5b40a3108a8c6';
const MAX_INPUT = 2 * 1024 * 1024;
const ALLOWED = new Set(['app.kilo.ai', 'kilo.ai', 'api.kilo.ai', 'auth.openai.com', 'sentinel.openai.com']);

export class FlowError extends Error {
  constructor(code, retryable = false, nextAction = '') {
    super(code); this.code = code; this.retryable = retryable; this.nextAction = nextAction;
  }
}
function fail(code, retryable = false, next = '') { throw new FlowError(code, retryable, next); }
function rebindFailure(value) {
  const message = typeof value === 'string' ? value : value?.message || value?.json?.message || '';
  if (/PROVIDER-ALREADY-LINKED|ACCOUNT-ALREADY-LINKED/i.test(message)) return new FlowError('BYOK_AUTHORIZATION_REJECTED', false, 'Kilo官方拒绝本次GPT订阅授权；原Kilo账号、登录方式和主Key保持，未将此回执认作换绑成功');
  return null;
}
function secureURL(raw) {
  let url; try { url = new URL(raw); } catch { fail('REMOTE_RESPONSE_INVALID'); }
  if (url.protocol !== 'https:' || url.username || url.password || url.port && url.port !== '443' || !ALLOWED.has(url.hostname)) fail('REMOTE_REDIRECT_REJECTED');
  return url;
}
function exactReplace(source, oldValue, newValue) {
  if (source.split(oldValue).length !== 2) fail('UNSUPPORTED_TOSUB2_LAYOUT');
  return source.replace(oldValue, newValue);
}
export async function loadHelpers(root) {
  let source = await fs.readFile(path.join(root, 'src', 'protocol-login.mjs'), 'utf8');
  const bytes = Buffer.from(source);
  const blob = crypto.createHash('sha1').update(`blob ${bytes.length}\0`).update(bytes).digest('hex');
  if (blob !== SOURCE_BLOB) fail('UNSUPPORTED_TOSUB2_LAYOUT');
  source = exactReplace(source, '} from "./tls-transport.mjs";', `} from ${JSON.stringify(pathToFileURL(path.join(root, 'src', 'tls-transport.mjs')).href)};`);
  const start = source.lastIndexOf('\nrun().catch((error) => {');
  const end = source.indexOf('\nfunction isProxyRiskControlError', start);
  if (start < 0 || end < 0) fail('UNSUPPORTED_TOSUB2_LAYOUT');
  source = source.slice(0, start) + source.slice(end);
  // Response-shape compatibility at the Kilo client boundary. The reusable
  // helpers still own password verification, MFA issuance/verification and TOTP.
  source = exactReplace(source, 'async function ask(rl, prompt) {', 'async function ask(rl, prompt) {\n  throw new Error("KILO_INTERACTION_REQUIRED");');
  source = exactReplace(source, '      if (!isRejectedPasswordError(error)) throw error;', '      if (!isRejectedPasswordError(error)) throw error;\n      throw new Error("KILO_PASSWORD_REJECTED");');
  source = exactReplace(source, '      if (!isRejectedTotpError(error)) throw error;', '      if (!isRejectedTotpError(error)) throw error;\n      throw new Error("KILO_MFA_REJECTED");');
  source = exactReplace(source, '  const session = payload?.["oai-client-auth-session"] || {};', '  const session = payload?.["oai-client-auth-session"] || {};\n  if (payload?.page?.payload?.factor_id) return {id: payload.page.payload.factor_id, factor_type: "totp"};');
  source = exactReplace(source, 'function pickWorkspaceId(payload) {\n  const workspaces = payload?.["oai-client-auth-session"]?.workspaces;', 'function pickWorkspaceId(payload) {\n  const workspaces = payload?.["oai-client-auth-session"]?.workspaces || payload?.page?.payload?.workspaces;');
  source = exactReplace(source, '  const url = payload?.continue_url || payload?.page?.payload?.url;', '  const url = payload?.continue_url || payload?.page?.payload?.external_url || payload?.page?.payload?.url;');
  source = exactReplace(source, '  return payload?.continue_url || payload?.page?.payload?.url || null;', '  return payload?.continue_url || payload?.page?.payload?.external_url || payload?.page?.payload?.url || null;');
  const mfaStart = source.indexOf('async function completeTotpMfaIfNeeded(');
  const mfaEnd = source.indexOf('\nfunction isRejectedPasswordError', mfaStart);
  let mfa = source.slice(mfaStart, mfaEnd);
  mfa = exactReplace(mfa, 'flow: "password_verify",', 'flow: "mfa_verify",');
  source = source.slice(0, mfaStart) + mfa + source.slice(mfaEnd);
  source += '\nexport {CookieJar, ProtocolClient, createSentinelHeaders, authJsonStep, verifyPassword, completeTotpMfaIfNeeded, selectChatgptLoginWorkspaceIfNeeded, continueFlow, getContinueUrl, isPasswordLoginPage, isAuthLoginPage, isMfaChallengePayload, extractFirstSessionId};\n';
  return import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);
}
// This is the consent handler already proven by kilo-protocol-20261113/probe.py
// and attempt-05: one exact POST form, a single recognized grant submitter,
// auth_session_id, no unresolved permission selection or mandatory checkbox.
export async function approveConsent(client, page, helpers, deviceId, root, jsdomClass) {
  const location = secureURL(page?.finalUrl);
  if (location.origin !== AUTH || location.pathname !== '/sign-in-with-chatgpt/consent') fail('ACTION_REQUIRED', false, 'OpenAI授权出现未识别的额外确认');
  let JSDOM = jsdomClass;
  if (!JSDOM) {
    try {
      const library = await import(pathToFileURL(path.join(root, 'node_modules/jsdom/lib/api.js')).href);
      JSDOM = (library.default || library).JSDOM;
    } catch { fail('EXECUTOR_UNAVAILABLE', true, '运行包缺少固定jsdom依赖'); }
  }
  const dom = new JSDOM(page.last?.text || '', {url: location.href});
  try {
    const forms = [...dom.window.document.querySelectorAll('form')].filter(form => {
      const action = new URL(form.action);
      return form.method.toLowerCase() === 'post' && action.origin === AUTH && action.pathname === '/sign-in-with-chatgpt/consent' && !action.username && !action.password && !action.hash;
    });
    if (forms.length !== 1) fail('ACTION_REQUIRED', false, 'OpenAI consent表单结构发生变化');
    const form = forms[0];
    const approved = [...form.querySelectorAll('button,input[type="submit"]')].filter(button =>
      button.type === 'submit' && button.name === 'resource_consent_decision' && button.value === 'grant');
    const button = approved.length === 1 ? approved[0] : null;
    if (!button || [...form.querySelectorAll('input[required][type="checkbox"]')].some(input => !input.checked)) fail('ACTION_REQUIRED', false, 'OpenAI consent要求额外选择');
    const data = new dom.window.FormData(form, button);
    const authSessionId = data.get('auth_session_id');
    if (typeof authSessionId !== 'string' || !authSessionId || authSessionId.length > 4096 || data.getAll('auth_session_id').length !== 1) fail('ACTION_REQUIRED', false, 'OpenAI consent授权字段发生变化');
    const permissionContext = data.get('permission_selection.context');
    const grant = {auth_session_id: authSessionId};
    const known = name => name === 'auth_session_id' || name === 'resource_consent_decision' || name === 'permission_selection.context' || name === 'permission_selection.availability_receipt' || /^permission_selection\.selected_permissions\[[0-6]\]$/.test(name);
    if ([...data.keys()].some(name => !known(name))) fail('ACTION_REQUIRED', false, 'OpenAI consent包含未识别的授权选择');
    if (permissionContext !== null) {
      if (typeof permissionContext !== 'string' || !permissionContext || permissionContext.length > 16384 || data.getAll('permission_selection.context').length !== 1) fail('AUTHORIZATION_CONTRACT_INVALID');
      const sessionDescriptor = async () => {
        const response = await helpers.authJsonStep(client, AUTH, 'GET', '/api/accounts/client_auth_session_dump', undefined, {referer: location.href});
        const session = response.data?.client_auth_session;
        const descriptor = session?.oauth_resource_consent;
        if (session?.session_id !== authSessionId || descriptor?.version !== 2 || descriptor?.selection?.context !== permissionContext) fail('AUTH_SESSION_EXPIRED');
        return descriptor;
      };
      const descriptor = await sessionDescriptor();
      if (!Array.isArray(descriptor.permissions) || descriptor.permissions.length > 7 || !Array.isArray(descriptor.selection.required_permissions)) fail('AUTHORIZATION_CONTRACT_INVALID');
      const allowed = new Set(['identity', 'resource_invoke', 'offline_access', 'chatgpt_allowance']);
      const selectedFromForm = [...data.entries()].filter(([name]) => /^permission_selection\.selected_permissions\[[0-6]\]$/.test(name)).map(([, value]) => value);
      const selected = new Set([...selectedFromForm, ...descriptor.selection.required_permissions]);
      if (descriptor.permissions.some(permission => permission.kind === 'chatgpt_allowance')) selected.add('chatgpt_allowance');
      if (!selected.size || [...selected].some(kind => !allowed.has(kind))) fail('ACTION_REQUIRED', false, 'OpenAI consent要求当前接入范围之外的权限');
      const selectedPermissions = descriptor.permissions.filter(permission => selected.has(permission.kind)).map(permission => permission.kind);
      if (selectedPermissions.length !== selected.size) fail('AUTHORIZATION_CONTRACT_INVALID');
      const availabilityHeaders = await helpers.createSentinelHeaders(client, {authBase: AUTH, deviceId, flow: 'authorize_continue'});
      const checked = await helpers.authJsonStep(client, AUTH, 'POST', '/api/accounts/consent/availability',
        {auth_session_id: authSessionId, context: permissionContext}, {referer: location.href,
          headers: {...availabilityHeaders, 'OpenAI-Consent-Display': 'workspace_name', 'OpenAI-Consent-Decision': 'v1'}});
      const availability = checked.data;
      if (availability?.context !== permissionContext || !Array.isArray(availability.permissions) || availability.permissions.length !== descriptor.permissions.length || availability.permissions.some((permission, index) => permission.kind !== descriptor.permissions[index].kind)) fail('AUTHORIZATION_CONTRACT_INVALID');
      if (availability.permissions.some(permission => selected.has(permission.kind) && permission.availability !== 'available')) fail('BYOK_NOT_CONNECTED', false, '来源工作区的ChatGPT额度共享权限未获官方允许');
      const current = await sessionDescriptor();
      if (current.permissions.map(permission => permission.kind).join() !== descriptor.permissions.map(permission => permission.kind).join()) fail('AUTH_SESSION_EXPIRED');
      grant.permission_selection = {context: permissionContext, selected_permissions: selectedPermissions};
      if (availability.availability_receipt !== undefined) {
        if (typeof availability.availability_receipt !== 'string' || !availability.availability_receipt || availability.availability_receipt.length > 16384) fail('AUTHORIZATION_CONTRACT_INVALID');
        grant.permission_selection.availability_receipt = availability.availability_receipt;
      }
      // SSR can disable Continue until its clientLoader checks availability.
      // The same official availability receipt above decides whether to grant.
    } else if (button.disabled) {
      fail('BYOK_NOT_CONNECTED', false, 'OpenAI当前工作区未允许订阅额度共享');
    }
    const headers = await helpers.createSentinelHeaders(client, {authBase: AUTH, deviceId, flow: 'authorize_continue'});
    let result;
    try { result = await client.request('POST', AUTH + '/api/accounts/consent/grant', {json: grant, headers, origin: AUTH, referer: location.href}); }
    catch (error) {
      if (error?.retryable) fail('REMOTE_OUTCOME_UNKNOWN', false, '恢复同一授权会话后核对consent结果');
      throw error;
    }
    if (result.res.status < 200 || result.res.status >= 400) fail('AUTHORIZATION_REJECTED');
    if (result.location) return client.follow(result.location, {referer: location.href});
    let payload;
    try { payload = JSON.parse(result.text); } catch { fail('REMOTE_RESPONSE_INVALID'); }
    const next = helpers.getContinueUrl(payload);
    if (!next) fail('AUTHORIZATION_INCOMPLETE');
    return client.follow(new URL(next, AUTH).href, {referer: location.href});
  } finally { dom.window.close(); }
}
// The source's confirmed OAuth workspace owns its allowance. Display names,
// plan labels and list order cannot substitute for this identity.
export function selectSourceWorkspace(workspaces, expectedIdentity) {
  const workspaceId = expectedIdentity?.source_workspace_id;
  if (typeof workspaceId !== 'string' || !workspaceId || workspaceId.length > 128 || !/^[A-Za-z0-9_-]+$/.test(workspaceId)) fail('IDENTITY_MISMATCH', false, '来源缺少已确认的OpenAI工作区身份');
  if (!Array.isArray(workspaces)) fail('IDENTITY_MISMATCH', false, 'OpenAI未返回来源工作区选择');
  const matched = workspaces.filter(workspace => workspace?.id === workspaceId);
  if (matched.length !== 1) fail('IDENTITY_MISMATCH', false, 'OpenAI工作区列表与来源OAuth身份不一致');
  return matched[0].id;
}
export async function selectWorkspacePage(client, page, helpers, root, jsdomClass, expectedIdentity) {
  const location = secureURL(page?.finalUrl);
  if (location.origin !== AUTH || location.pathname !== '/workspace') fail('ACTION_REQUIRED');
  let JSDOM = jsdomClass;
  if (!JSDOM) {
    try {
      const library = await import(pathToFileURL(path.join(root, 'node_modules/jsdom/lib/api.js')).href);
      JSDOM = (library.default || library).JSDOM;
    } catch { fail('EXECUTOR_UNAVAILABLE', true, '运行包缺少固定jsdom依赖'); }
  }
  const dom = new JSDOM(page.last?.text || '', {url: location.href});
  try {
    const forms = [...dom.window.document.querySelectorAll('form')].filter(form => {
      const action = new URL(form.action);
      return form.method.toLowerCase() === 'post' && action.origin === AUTH && action.pathname === '/workspace' && !action.username && !action.password && !action.hash;
    });
    if (forms.length !== 1) fail('ACTION_REQUIRED', false, 'OpenAI工作区选择表单结构发生变化');
    const form = forms[0];
    const buttons = [...form.querySelectorAll('button[name="workspace_id"]')].filter(button => button.type === 'submit' && !button.disabled);
    const workspaceId = selectSourceWorkspace(buttons.map(button => ({id: button.value})), expectedIdentity);
    const selectedButton = buttons.find(button => button.value === workspaceId);
    const data = new dom.window.FormData(form, selectedButton);
    if ([...data.keys()].some(name => name !== 'workspace_id') || data.getAll('workspace_id').length !== 1 || [...form.querySelectorAll('[required]')].some(element => !element.checkValidity())) fail('ACTION_REQUIRED', false, 'OpenAI工作区选择要求额外输入');
    const selected = await helpers.authJsonStep(client, AUTH, 'POST', '/api/accounts/workspace/select',
      {workspace_id: workspaceId}, {referer: location.href});
    return selected.data;
  } finally { dom.window.close(); }
}
function privateChild(command, args, payload, deadline) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {stdio: ['pipe', 'pipe', 'ignore'], windowsHide: true});
    const chunks = []; let size = 0;
    const timer = setTimeout(() => { child.kill(); reject(new FlowError('DEADLINE_EXCEEDED')); }, Math.max(1, deadline - Date.now()));
    child.stdout.on('data', chunk => { size += chunk.length; if (size > MAX_INPUT) { child.kill(); reject(new FlowError('REMOTE_RESPONSE_INVALID')); } else chunks.push(chunk); });
    child.on('error', () => { clearTimeout(timer); reject(new FlowError('EXECUTOR_UNAVAILABLE', true)); });
    child.on('close', code => {
      clearTimeout(timer);
      if (code !== 0) return reject(new FlowError('REMOTE_UNAVAILABLE', true));
      try { resolve(JSON.parse(Buffer.concat(chunks).toString('utf8'))); } catch { reject(new FlowError('REMOTE_RESPONSE_INVALID')); }
    });
    child.stdin.on('error', () => {});
    child.stdin.end(JSON.stringify(payload));
  });
}
// A saved Kilo web session is independent of its replaceable ChatGPT connection.
// Go encrypts this envelope; it never contains an OpenAI jar, a key or a password.
function identityProxy(proxy) {
  return {id: proxy.id, fingerprint: crypto.createHash('sha256').update(proxy.url).digest('hex')};
}
function kiloCookies(cookies, strict = false) {
  if (!Array.isArray(cookies) || cookies.length > 128) fail('IDENTITY_SESSION_INVALID');
  const result = [];
  for (const cookie of cookies) {
    const domain = typeof cookie?.domain === 'string' ? cookie.domain.replace(/^\./, '').toLowerCase() : '';
    if (!KILO_COOKIE_DOMAINS.has(domain)) {
      if (strict) fail('IDENTITY_SESSION_INVALID');
      continue;
    }
    if (typeof cookie.name !== 'string' || !/^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,256}$/.test(cookie.name) ||
        typeof cookie.value !== 'string' || cookie.value.length > 32768 || /[\x00-\x1f\x7f;]/.test(cookie.value) ||
        typeof cookie.path !== 'string' || !cookie.path.startsWith('/') || cookie.path.length > 1024 || /[\x00-\x1f\x7f]/.test(cookie.path) ||
        cookie.expires !== null && cookie.expires !== undefined && (typeof cookie.expires !== 'number' || !Number.isFinite(cookie.expires))) fail('IDENTITY_SESSION_INVALID');
    if (cookie.expires && cookie.expires <= Date.now()) continue;
    result.push({name: cookie.name, value: cookie.value, domain, path: cookie.path, secure: cookie.secure === true,
      expires: cookie.expires ?? null});
  }
  return result;
}
function restoreIdentityMaterial(input) {
  const raw = input.kilo_identity?.session_material;
  if (!raw) return [];
  if (typeof raw !== 'string' || Buffer.byteLength(raw) > MAX_IDENTITY_MATERIAL) fail('IDENTITY_SESSION_INVALID');
  let material; try { material = JSON.parse(raw); } catch { fail('IDENTITY_SESSION_INVALID'); }
  if (material?.version !== 1 || typeof material.kilo_user_id !== 'string' || typeof material.kilo_email !== 'string' ||
      !Number.isFinite(Date.parse(material.expires_at))) fail('IDENTITY_SESSION_INVALID');
  if (material.kilo_user_id !== input.kilo_identity.user_id || material.kilo_email.toLowerCase() !== input.kilo_identity.email.toLowerCase()) fail('IDENTITY_MISMATCH');
  const proxy = identityProxy(input.login_proxy);
  if (material.login_proxy?.id !== proxy.id || material.login_proxy?.fingerprint !== proxy.fingerprint) fail('LOGIN_PROXY_CONFLICT');
  const cookies = kiloCookies(material.cookies, true);
  return Date.parse(material.expires_at) > Date.now() ? cookies : [];
}
function exportIdentityMaterial(input, current, jar) {
  const material = JSON.stringify({version: 1, kilo_user_id: current.id, kilo_email: current.email,
    login_proxy: identityProxy(input.login_proxy), expires_at: current.expires, cookies: kiloCookies(jar.toJSON())});
  if (Buffer.byteLength(material) > MAX_IDENTITY_MATERIAL) fail('IDENTITY_SESSION_INVALID');
  return material;
}
function sourceToken(raw, keys, {nonce, startedAt, snapshot = false} = {}) {
  if (typeof raw !== 'string' || raw.length > 65536) fail('IDENTITY_MISMATCH');
  const parts = raw.split('.');
  if (parts.length !== 3 || parts.some(part => !/^[A-Za-z0-9_-]+$/.test(part) || Buffer.from(part, 'base64url').toString('base64url') !== part)) fail('IDENTITY_MISMATCH');
  let header, claims;
  try {
    header = JSON.parse(Buffer.from(parts[0], 'base64url'));
    claims = JSON.parse(Buffer.from(parts[1], 'base64url'));
  } catch { fail('IDENTITY_MISMATCH'); }
  if (!header || !claims || header.alg !== 'RS256' || typeof header.kid !== 'string' || !header.kid || header.kid.length > 256 || header.crit !== undefined) fail('IDENTITY_MISMATCH');
  const candidates = Array.isArray(keys?.keys) && keys.keys.length <= 128 ? keys.keys.filter(key => key?.kty === 'RSA' && key.kid === header.kid && (!key.alg || key.alg === 'RS256') && (!key.use || key.use === 'sig')) : [];
  if (candidates.length !== 1) fail('IDENTITY_MISMATCH');
  try {
    const key = crypto.createPublicKey({key: candidates[0], format: 'jwk'});
    if ((key.asymmetricKeyDetails?.modulusLength || 0) < 2048 || !crypto.verify('RSA-SHA256', Buffer.from(parts[0] + '.' + parts[1]), key, Buffer.from(parts[2], 'base64url'))) fail('IDENTITY_MISMATCH');
  } catch { fail('IDENTITY_MISMATCH'); }
  const audiences = typeof claims.aud === 'string' ? [claims.aud] : claims.aud;
  const now = Date.now() / 1000;
  if (claims.iss !== AUTH || !Array.isArray(audiences) || !audiences.includes(SOURCE_CLIENT_ID) ||
      audiences.length > 1 && claims.azp !== SOURCE_CLIENT_ID || typeof claims.sub !== 'string' || !/^[\x21-\x7e]{1,256}$/.test(claims.sub) ||
      !Number.isFinite(claims.exp) || !Number.isFinite(claims.iat) || claims.exp <= claims.iat || claims.iat > now + 5 ||
      claims.nbf !== undefined && (!Number.isFinite(claims.nbf) || claims.nbf > now + 5) ||
      !snapshot && (claims.exp <= now - 5 || claims.iat < startedAt / 1000 - 5 || claims.nonce !== nonce)) fail('IDENTITY_MISMATCH');
  return claims;
}
function credentialMAC(input, owner, clientId, credential, key) {
  return crypto.createHmac('sha256', key).update(JSON.stringify({credential: [credential.email.toLowerCase(), credential.password, credential.totp_secret],
    source: input.expected_identity, owner, clientId, proxy: identityProxy(input.login_proxy)})).digest('hex');
}
export async function execute(input, dependencies = {}) {
  // Neither helper callbacks nor a later phase may change the credential pair
  // whose two independent authentications establish this association.
  input = {...input, expected_identity: Object.freeze({...input.expected_identity}), credential: Object.freeze({...input.credential})};
  const h = dependencies.helpers || await loadHelpers(input.config.tosub2_root);
  const deadline = Date.parse(input.deadline_at);
  const check = () => { if (!Number.isFinite(deadline) || Date.now() >= deadline) fail('DEADLINE_EXCEEDED'); };
  const proxy = input.login_proxy.url;
  const python = input.config.python;
  const expectedWorkspace = input.expected_identity?.source_workspace_id;
  if (['connect_subscription', 'reconcile_subscription', 'reauthorize_subscription'].includes(input.phase)) selectSourceWorkspace([{id: expectedWorkspace}], input.expected_identity);
  const loginCredential = input.phase === 'resume_identity' ? input.kilo_identity?.credential && Object.freeze({...input.kilo_identity.credential}) : input.credential;
  let credentialControl = input.private_state?.credential_control || null;
  let authenticatedKiloClientId = '';
  let associationClientId = '';
  let sourceAuthenticated = false;
  const fetchHTTP = dependencies.fetch || (async (url, options) => {
    check(); secureURL(url);
    const result = await privateChild(python, [path.join(HERE, 'requests_fetch.py')], {
      url, proxy, method: options.method || 'GET', headers: Object.fromEntries(options.headers || []),
      body: options.body ?? null, timeout: Math.max(1, Math.min(45, (deadline - Date.now()) / 1000)),
    }, deadline);
    if (result.error_code) fail(result.error_code, result.retryable === true);
    const headers = new Headers(result.headers || []);
    Object.defineProperty(headers, 'rawSetCookie', {value: result.set_cookie || []});
    return {status: result.status, ok: result.status >= 200 && result.status < 300, headers, text: async () => result.body};
  });
  const transportFor = getJar => ({
    enabled: true, profile: 'chrome131', identityProfile: 'chrome131', fetch: fetchHTTP,
    generateSentinelTokens: dependencies.sentinel || (async ({flow, deviceID, pageUrl}) => {
      check();
      const raw = await privateChild(input.config.node, [path.join(input.config.sentinel_assets, 'sentinel_bridge.js')], {
        ua: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36',
        cores: 16, deviceId: deviceID, flow, proxy, preProxy: 'requests', version: '20260810913b', pageUrl,
        language: 'en-US', timezone: 'Asia/Shanghai', platform: 'Win32', platformLabel: 'Windows', screenW: 1920, screenH: 1080, availH: 1032,
        cookieHeader: getJar().headerFor(AUTH), sentinelOrigin: 'https://sentinel.openai.com',
      }, deadline);
      if (!raw.main || raw.error) fail('SENTINEL_UNAVAILABLE', true);
      let soToken = null;
      if (raw.so) {
        const main = JSON.parse(raw.main);
        soToken = JSON.stringify({so: raw.so, c: main.c || '', id: deviceID, flow});
      }
      return {token: raw.main, soToken};
    }),
  });
  const initialCookies = ['resume_identity', 'reconcile_subscription', 'reauthorize_subscription'].includes(input.phase) ? restoreIdentityMaterial(input) : input.phase === 'login' ? [] : input.private_state?.cookies || [];
  const client = new h.ProtocolClient({jar: new h.CookieJar(initialCookies), transport: transportFor(() => client.jar)});
  let oauth = null;
  let callbackSeen = false;
  let callbackCompleted = false;
  let consentCompleted = false;
  const personalSource = ['plus', 'pro'].includes(input.expected_identity?.source_plan_type) &&
    input.expected_identity?.source_issuer === AUTH && typeof input.expected_identity?.source_subject === 'string' &&
    input.expected_identity.source_subject.length > 0;
  const requireSelectedWorkspace = () => {
    if (oauth?.connect && oauth.workspace === expectedWorkspace) return oauth.workspace;
    if (oauth?.connect && oauth.personalSessionVerified === true) return '';
    fail('IDENTITY_MISMATCH', false, '本次授权缺少来源工作区或个人主体的确认依据');
  };
  const selectWorkspaceForConsent = async page => {
    if (oauth?.workspace === expectedWorkspace) return page;
    // Without a selected workspace, only the independently verified source
    // and same-credential Kilo identity may complete a personal grant.
    if (!personalSource || !sourceAuthenticated || !associationClientId) fail('IDENTITY_MISMATCH', false, '本次授权没有已确认的工作区选择或同凭据个人身份关联');
    let JSDOM = dependencies.jsdomClass;
    if (!JSDOM) {
      const library = await import(pathToFileURL(path.join(input.config.tosub2_root, 'node_modules/jsdom/lib/api.js')).href);
      JSDOM = (library.default || library).JSDOM;
    }
    const dom = new JSDOM(page.last?.text || '', {url: page.finalUrl});
    try {
      const ids = [...dom.window.document.querySelectorAll('input[name="auth_session_id"]')];
      const context = dom.window.document.querySelector('input[name="permission_selection.context"]')?.value;
      if (ids.length !== 1 || !ids[0].value) fail('AUTHORIZATION_CONTRACT_INVALID');
      const dump = await h.authJsonStep(client, AUTH, 'GET', '/api/accounts/client_auth_session_dump', undefined, {referer: page.finalUrl});
      const session = dump.data?.client_auth_session;
      const email = input.expected_identity.source_email.toLowerCase();
      if (session?.session_id !== ids[0].value || session?.email?.toLowerCase() !== email ||
          session?.username?.kind !== 'email' || session.username.value?.toLowerCase() !== email ||
          context && session?.oauth_resource_consent?.selection?.context !== context) fail('IDENTITY_MISMATCH', false, '个人订阅同意页与本次来源会话不一致');
      oauth.personalSessionVerified = true;
    } finally { dom.window.close(); }
    return page;
  };
  const request = client.request.bind(client);
  client.request = async (method, raw, options = {}) => {
    check(); const url = secureURL(raw);
    // Existing-connection recovery may authenticate the original identity,
    // but cannot acquire new consent or mutate a subscription to prove it.
    if (input.phase === 'reconcile_subscription' && method !== 'GET') {
      const paths = url.origin === KILO ? ['/api/auth/verify-turnstile', '/api/auth/signin/openai'] : url.origin === AUTH ?
        ['/api/accounts/authorize/continue', '/api/accounts/password/verify', '/api/accounts/mfa/issue_challenge', '/api/accounts/mfa/verify', '/api/accounts/workspace/select'] : [];
      if (method !== 'POST' || !paths.includes(url.pathname)) fail('ACTION_REQUIRED', false, '既存订阅恢复只允许原身份认证，不会重新同意或修改订阅');
    }
    if (options.headers && new Headers(options.headers).has('authorization') &&
        (url.origin !== KILO_API || url.pathname !== '/api/profile' || url.search || method !== 'GET')) fail('REMOTE_REQUEST_INVALID');
    if (oauth?.connect && url.origin === AUTH && url.pathname === '/api/accounts/consent/grant') requireSelectedWorkspace();
    const selectingWorkspace = oauth?.connect && url.origin === AUTH && url.pathname === '/api/accounts/workspace/select';
    if (selectingWorkspace && (method !== 'POST' || options.json?.workspace_id !== expectedWorkspace)) fail('IDENTITY_MISMATCH');
    if (url.origin === KILO && ['/auth/openai/callback', '/api/auth/callback/openai'].includes(url.pathname) && url.searchParams.has('code')) {
      if (!oauth || url.searchParams.getAll('code').length !== 1 || url.searchParams.getAll('state').length !== 1 || url.searchParams.get('state') !== oauth.state || url.searchParams.has('error') || url.hash) fail('CALLBACK_STATE_MISMATCH');
      if (oauth.connect) requireSelectedWorkspace();
      callbackSeen = true;
    }
    const authenticationStep = oauth && !oauth.connect && url.origin === AUTH && method === 'POST' &&
      ['/api/accounts/password/verify', '/api/accounts/mfa/issue_challenge', '/api/accounts/mfa/verify'].includes(url.pathname);
    if (authenticationStep) {
      if (oauth.authenticationCalls.has(url.pathname)) oauth.authenticationValid = false;
      oauth.authenticationCalls.add(url.pathname);
      if (url.pathname === '/api/accounts/mfa/issue_challenge') {
        if (!oauth.passwordVerified || options.json?.type !== 'totp' || !options.json.id) oauth.authenticationValid = false;
        oauth.factor = options.json?.id;
      }
      if (url.pathname === '/api/accounts/mfa/verify' && (!oauth.challengeVerified || options.json?.type !== 'totp' || options.json.id !== oauth.factor)) oauth.authenticationValid = false;
    }
    const response = await request(method, url.href, {...options, timeoutMs: Math.min(45000, deadline - Date.now())});
    if (authenticationStep && response.res.status >= 200 && response.res.status < 300) {
      let value; try { value = JSON.parse(response.text); } catch { fail('REMOTE_RESPONSE_INVALID'); }
      if (!value?.error) {
        if (url.pathname === '/api/accounts/password/verify') oauth.passwordVerified = true;
        if (url.pathname === '/api/accounts/mfa/issue_challenge') oauth.challengeVerified = true;
        if (url.pathname === '/api/accounts/mfa/verify') oauth.totpVerified = true;
      }
    }
    if (selectingWorkspace) {
      let selected; try { selected = JSON.parse(response.text); } catch { fail('REMOTE_RESPONSE_INVALID'); }
      const next = h.getContinueUrl(selected);
      if (response.res.status < 200 || response.res.status >= 300 || selected?.error || !next || selected?.page?.type === 'workspace' || new URL(next, AUTH).pathname === '/workspace') fail('IDENTITY_MISMATCH', false, '官方未确认来源工作区选择');
      // Only an accepted selection in this OAuth attempt proves workspace
      // ownership. An earlier login/session or an expected ID is not evidence.
      oauth.workspace = options.json.workspace_id;
    }
    if (url.origin === AUTH && response.res.status === 429) fail('MFA_VERIFY_TRANSIENT', true, '在当前授权会话内遵守限流预算后重试');
    return response;
  };
  client.follow = async (raw, options = {}) => {
    let current = new URL(raw, AUTH).href;
    for (let hop = 0; hop < 14; hop++) {
      const target = secureURL(current);
      const refusal = target.origin === KILO ? rebindFailure(target.searchParams.get('openai_error') || target.searchParams.get('error')) : null;
      if (refusal) throw refusal;
      const result = await client.request('GET', current, {referer: options.referer, accept: 'text/html,application/xhtml+xml'});
      if (result.res.status >= 400) fail(result.res.status === 429 ? 'REMOTE_RATE_LIMITED' : 'REMOTE_HTTP_ERROR', result.res.status >= 500 || result.res.status === 429);
      if (![301, 302, 303, 307, 308].includes(result.res.status) || !result.location) {
        const final = secureURL(current);
        if (callbackSeen && final.origin === KILO) callbackCompleted = true;
        if (final.searchParams.has('openai_error') || final.pathname.includes('/auth/error')) fail('AUTHORIZATION_REJECTED');
        return {finalUrl: current, last: result};
      }
      current = result.location;
    }
    fail('REMOTE_REDIRECT_REJECTED');
  };
  async function json(method, suffix, options = {}) {
    const result = await client.request(method, KILO + suffix, {origin: method === 'POST' ? KILO : undefined, referer: KILO + '/', ...options});
    let value; try { value = JSON.parse(result.text); } catch { fail('REMOTE_RESPONSE_INVALID'); }
    if (result.res.status < 200 || result.res.status >= 300 || value?.error) {
      const refusal = rebindFailure(value?.error);
      if (refusal) throw refusal;
      const status = result.res.status;
      fail(status === 401 ? 'AUTH_SESSION_EXPIRED' : status === 403 ? 'REMOTE_ACCESS_DENIED' : status === 429 ? 'REMOTE_RATE_LIMITED' : 'REMOTE_HTTP_ERROR', status >= 500 || status === 429);
    }
    return value;
  }
  async function identity() {
    const session = await json('GET', '/api/auth/session');
    const id = session?.kiloUserId || session?.user?.id;
    if (typeof id !== 'string' || !id || typeof session.user?.email !== 'string' || !session.user.email ||
        !Number.isFinite(Date.parse(session.expires)) || Date.parse(session.expires) <= Date.now()) fail('AUTH_SESSION_EXPIRED');
    const expected = input.kilo_identity?.user_id || input.private_state?.verified_identity?.kilo_user_id;
    if (expected && id !== expected) fail('IDENTITY_MISMATCH');
    return {id, email: session.user.email, expires: session.expires};
  }
  async function verifyIdentityKey() {
    const expected = input.kilo_identity;
    if (!expected?.api_key || !expected.user_id || !expected.email) fail('LOGIN_MATERIAL_MISSING');
    const result = await client.request('GET', KILO_API + '/api/profile', {headers: {authorization: 'Bearer ' + expected.api_key}});
    if (result.res.status !== 200) fail(result.res.status === 401 ? 'AUTH_SESSION_EXPIRED' : result.res.status === 403 ? 'REMOTE_ACCESS_DENIED' : 'REMOTE_HTTP_ERROR', result.res.status >= 500 || result.res.status === 429);
    let value; try { value = JSON.parse(result.text); } catch { fail('REMOTE_RESPONSE_INVALID'); }
    if (value?.user?.id !== expected.user_id || typeof value.user.email !== 'string' || value.user.email.toLowerCase() !== expected.email.toLowerCase()) fail('IDENTITY_MISMATCH');
  }
  async function loginKilo() {
    if (!loginCredential?.email || !loginCredential?.password) fail('AUTH_SESSION_EXPIRED');
    // Rebuild only with the original Kilo login snapshot. The new source's
    // credential belongs exclusively to the delegated connect authorization.
    let token = input.turnstile_token;
    if (!token) {
      const solved = await privateChild(input.config.cfsolve_bin, [], {proxy, profile: input.config.cfsolve_profile}, deadline);
      if (typeof solved.token !== 'string' || !solved.token) fail('TURNSTILE_UNAVAILABLE', true);
      token = solved.token;
    }
    const emptyJar = client.jar.toJSON().length === 0;
    authenticatedKiloClientId = '';
    await client.follow(KILO + '/users/sign_in');
    const turnstile = await json('POST', '/api/auth/verify-turnstile', {json: {token}});
    if (!turnstile) fail('TURNSTILE_REJECTED');
    await authorize(false);
    const current = await identity();
    if (emptyJar && oauth.authenticationValid && oauth.passwordVerified && oauth.challengeVerified && oauth.totpVerified && loginCredential.totp_secret) {
      authenticatedKiloClientId = oauth.clientId;
      const key = crypto.randomBytes(32);
      // The verifier measures the lifetime from verified_at, so both bounds share one clock reading.
      const verifiedAt = Date.now();
      credentialControl = {version: 1, key: key.toString('base64url'), kilo_user_id: current.id, kilo_client_id: authenticatedKiloClientId,
        verified_at: new Date(verifiedAt).toISOString(), expires_at: new Date(Math.min(Date.parse(current.expires), verifiedAt + 900000)).toISOString(),
        fingerprint: credentialMAC(input, current.id, authenticatedKiloClientId, loginCredential, key)};
    }
    return current;
  }
  async function authorize(connect) {
    const credential = connect ? input.credential : loginCredential;
    const authorizationEmail = connect ? input.expected_identity.source_email : credential?.email;
    if (!authorizationEmail) fail('LOGIN_MATERIAL_MISSING');
    // A recovery performs identity login and delegated consent in one execution;
    // callback evidence belongs only to the OAuth round trip that produced it.
    callbackSeen = false;
    callbackCompleted = false;
    consentCompleted = false;
    const csrf = await json('GET', '/api/auth/csrf');
    if (!csrf.csrfToken) fail('REMOTE_RESPONSE_INVALID');
    const query = connect ? '?' + new URLSearchParams({scope: OBO_SCOPE, force_reconsent: 'true', login_hint: authorizationEmail}) : '?' + new URLSearchParams({login_hint: authorizationEmail});
    const callbackPath = input.organization_id ? `/organizations/${input.organization_id}/byok` : '/byok';
    const signin = await json('POST', '/api/auth/signin/openai' + query, {form: {csrfToken: csrf.csrfToken, callbackUrl: KILO + (connect ? callbackPath : '/'), json: 'true'}});
    const url = secureURL(signin.url);
    const redirect = new URL(url.searchParams.get('redirect_uri'));
    if (url.origin !== AUTH || !['/api/accounts/authorize', '/oauth/authorize'].includes(url.pathname) || redirect.origin !== KILO || redirect.pathname !== '/auth/openai/callback' || !url.searchParams.get('state') || !url.searchParams.get('nonce') || url.searchParams.get('code_challenge_method') !== 'S256' || !url.searchParams.get('code_challenge')) fail('AUTHORIZATION_CONTRACT_INVALID');
    if (connect && !['resource.invoke', 'chatgpt.tokens.use.obo', 'offline_access'].every(scope => url.searchParams.get('scope')?.split(' ').includes(scope))) fail('BYOK_SCOPE_MISSING');
    const scopes = url.searchParams.get('scope')?.split(/\s+/).filter(Boolean);
    const clientId = url.searchParams.get('client_id');
    if (typeof clientId !== 'string' || !/^[\x21-\x7e]{1,256}$/.test(clientId) || clientId === SOURCE_CLIENT_ID ||
        [...url.searchParams.keys()].some(key => url.searchParams.getAll(key).length !== 1) ||
        url.searchParams.get('response_type') !== 'code' || !connect && (!scopes?.includes('openid') || scopes.some(scope => !['openid', 'profile', 'email'].includes(scope)))) fail('AUTHORIZATION_CONTRACT_INVALID');
    if (connect && associationClientId && clientId !== associationClientId) fail('IDENTITY_MISMATCH');
    oauth = {state: url.searchParams.get('state'), connect, workspace: '', clientId, authenticationCalls: new Set(), authenticationValid: true};
    let page = await client.follow(url.href);
    let usedPassword = false;
    let payload;
    const did = client.jar.value('oai-did', AUTH) || crypto.randomUUID();
    client.jar.merge([{name: 'oai-did', value: did, domain: 'openai.com', path: '/', secure: true, hostOnly: false}]);
    if (callbackCompleted) {
      if (!connect) fail('IDENTITY_UNVERIFIED');
      return;
    }
    if (new URL(page.finalUrl).origin === AUTH && new URL(page.finalUrl).pathname === '/sign-in-with-chatgpt/consent') {
      if (!connect) fail('IDENTITY_UNVERIFIED');
      page = await selectWorkspaceForConsent(page);
      page = await approveConsent(client, page, h, did, input.config.tosub2_root, dependencies.jsdomClass);
      consentCompleted = true;
      if (!callbackCompleted) fail('AUTHORIZATION_INCOMPLETE');
      return;
    }
    const workspacePage = connect && new URL(page.finalUrl).origin === AUTH && new URL(page.finalUrl).pathname === '/workspace';
    if (workspacePage) {
      payload = await selectWorkspacePage(client, page, h, input.config.tosub2_root, dependencies.jsdomClass, input.expected_identity);
    }
    if (!workspacePage && h.isAuthLoginPage(page.finalUrl) && !h.isPasswordLoginPage(page.finalUrl)) {
      if (!credential?.email || !credential?.password) fail('LOGIN_MATERIAL_MISSING', false, '后端需重新交付当前登录材料');
      const headers = await h.createSentinelHeaders(client, {authBase: AUTH, deviceId: did, flow: 'authorize_continue'});
      ({data: payload} = await h.authJsonStep(client, AUTH, 'POST', '/api/accounts/authorize/continue', {username: {kind: 'email', value: credential.email}}, {referer: page.finalUrl, headers}));
      const next = h.getContinueUrl(payload);
      if (!next) fail('ACTION_REQUIRED', false, 'OpenAI登录需要额外验证');
      page = await client.follow(new URL(next, AUTH).href, {referer: page.finalUrl});
    }
    if (workspacePage) {
      // Continue the selected workspace through the same callback checks below.
    } else if (h.isPasswordLoginPage(page.finalUrl)) {
      if (!credential?.password) fail('LOGIN_MATERIAL_MISSING', false, '后端需重新交付当前登录材料');
      payload = await h.verifyPassword(client, {authBase: AUTH, deviceId: did, password: credential.password, referer: page.finalUrl});
      usedPassword = true;
      payload = await h.completeTotpMfaIfNeeded(client, {authBase: AUTH, deviceId: did, payload, totpSecret: credential.totp_secret, referer: page.finalUrl});
    } else {
      const selected = h.extractFirstSessionId(page.last?.text || '');
      if (selected && connect) ({data: payload} = await h.authJsonStep(client, AUTH, 'POST', '/api/accounts/session/select', {session_id: selected}, {referer: page.finalUrl}));
      else fail('ACTION_REQUIRED', false, 'OpenAI授权出现未适配的确认、邮箱或电话步骤');
    }
    if (!connect && !usedPassword) fail('IDENTITY_UNVERIFIED');
    for (let step = 0; step < 4 && !callbackCompleted; step++) {
      const workspaces = payload?.['oai-client-auth-session']?.workspaces || payload?.page?.payload?.workspaces;
      const continueURL = h.getContinueUrl(payload);
      const workspacePending = payload?.page?.type === 'workspace' || (continueURL && new URL(continueURL, AUTH).pathname === '/workspace');
      if (workspacePending) {
        if (!connect && input.phase === 'resume_identity') {
          payload = await h.selectChatgptLoginWorkspaceIfNeeded(client, {authBase: AUTH, payload});
        } else {
          const workspaceId = selectSourceWorkspace(workspaces, input.expected_identity);
          const selected = await h.authJsonStep(client, AUTH, 'POST', '/api/accounts/workspace/select',
            {workspace_id: workspaceId}, {referer: h.getContinueUrl(payload) || AUTH + '/workspace'});
          payload = selected.data;
        }
      }
      let continued = await h.continueFlow(client, payload);
      if (callbackCompleted) break;
      if (!continued || !continued.finalUrl || new URL(continued.finalUrl).origin !== AUTH) fail('ACTION_REQUIRED', false, 'OpenAI授权未返回Kilo回调');
      if (new URL(continued.finalUrl).pathname === '/sign-in-with-chatgpt/consent') {
        if (connect) continued = await selectWorkspaceForConsent(continued);
        continued = await approveConsent(client, continued, h, did, input.config.tosub2_root, dependencies.jsdomClass);
        consentCompleted = true;
        if (callbackCompleted) break;
      }
      fail('ACTION_REQUIRED', false, 'OpenAI授权需要未识别的额外步骤');
    }
    if (!callbackCompleted) fail('AUTHORIZATION_INCOMPLETE');
  }
  async function verifyCredentialSource(current, requireAssociation = true) {
    if (sourceAuthenticated) {
      if (requireAssociation && !associationClientId) fail('IDENTITY_MISMATCH');
      return;
    }
    const control = credentialControl;
    const credential = input.credential;
    if (!personalSource || input.organization_id || !input.source_id_token || credential.email?.toLowerCase() !== input.expected_identity.source_email.toLowerCase() ||
        typeof credential.password !== 'string' || !credential.password || typeof credential.totp_secret !== 'string' || !credential.totp_secret) fail('IDENTITY_MISMATCH');
    // A restored independent identity can carry control for its ORIGINAL
    // credentials. Only a full match permits bridging; otherwise an actual
    // workspace selection is still required after source authentication.
    let controlledClientId = '';
    if (control?.version === 1 && control.kilo_user_id === current.id && /^[\x21-\x7e]{1,256}$/.test(control.kilo_client_id || '') && control.kilo_client_id !== SOURCE_CLIENT_ID &&
        typeof control.key === 'string' && /^[A-Za-z0-9_-]{43}$/.test(control.key) && /^[a-f0-9]{64}$/.test(control.fingerprint || '') &&
        Number.isFinite(Date.parse(control.verified_at)) && Date.parse(control.verified_at) <= Date.now() &&
        Number.isFinite(Date.parse(control.expires_at)) && Date.parse(control.expires_at) > Date.now() && Date.parse(control.expires_at) <= Date.parse(control.verified_at) + 900000) {
      const calculated = credentialMAC(input, current.id, control.kilo_client_id, credential, Buffer.from(control.key, 'base64url'));
      if (crypto.timingSafeEqual(Buffer.from(calculated, 'hex'), Buffer.from(control.fingerprint, 'hex')) && (!oauth?.connect || oauth.clientId === control.kilo_client_id)) controlledClientId = control.kilo_client_id;
    }
    if (requireAssociation && !controlledClientId) fail('IDENTITY_MISMATCH');
    const nonce = crypto.randomBytes(32).toString('base64url');
    const state = crypto.randomBytes(32).toString('base64url');
    const verifier = crypto.randomBytes(48).toString('base64url');
    const startedAt = Date.now();
    const authorization = new URL(AUTH + '/oauth/authorize?' + new URLSearchParams({client_id: SOURCE_CLIENT_ID, redirect_uri: SOURCE_REDIRECT,
      response_type: 'code', scope: 'openid profile email offline_access', state, nonce, code_challenge: crypto.createHash('sha256').update(verifier).digest('base64url'),
      code_challenge_method: 'S256', codex_cli_simplified_flow: 'true', id_token_add_organizations: 'true'}));
    // This is an independent credential authentication, not a shared session or
    // a signed mapping between the two clients' subject namespaces.
    const bridge = new h.ProtocolClient({jar: new h.CookieJar(), transport: transportFor(() => bridge.jar)});
    const bridgeRequest = bridge.request.bind(bridge);
    const called = new Set(), completed = new Set();
    let factor = '';
    bridge.request = async (method, raw, options = {}) => {
      check();
      const url = secureURL(raw);
      const allowedGet = ['/oauth/authorize', '/api/accounts/authorize', '/log-in', '/log-in/password', '/mfa-challenge', '/workspace', '/.well-known/jwks.json'];
      const allowedPost = ['/api/accounts/authorize/continue', '/api/accounts/password/verify', '/api/accounts/mfa/issue_challenge', '/api/accounts/mfa/verify', '/api/accounts/workspace/select', '/oauth/token'];
      if (url.origin !== AUTH || url.hash || new Headers(options.headers || {}).has('authorization') ||
          (method === 'GET' ? !allowedGet.includes(url.pathname) && !/^\/mfa-challenge\/[A-Za-z0-9_-]+$/.test(url.pathname) : method !== 'POST' || !allowedPost.includes(url.pathname))) fail('ACTION_REQUIRED', false, '来源身份认证出现额外步骤，已停止且未修改订阅');
      if (['/oauth/authorize', '/api/accounts/authorize'].includes(url.pathname)) {
        for (const key of ['client_id', 'redirect_uri', 'state', 'nonce', 'code_challenge', 'code_challenge_method', 'response_type', 'scope']) {
          if (url.searchParams.getAll(key).length !== 1 || url.searchParams.get(key) !== authorization.searchParams.get(key)) fail('AUTHORIZATION_CONTRACT_INVALID');
        }
      }
      if (method === 'POST') {
        if (called.has(url.pathname)) fail('ACTION_REQUIRED', false, '同次来源身份认证不重复提交密码或二次验证');
        called.add(url.pathname);
      }
      if (url.pathname === '/api/accounts/authorize/continue' && (options.json?.username?.kind !== 'email' || options.json.username.value?.toLowerCase() !== credential.email.toLowerCase())) fail('IDENTITY_MISMATCH');
      if (url.pathname === '/api/accounts/mfa/issue_challenge') {
        if (!completed.has('/api/accounts/password/verify') || options.json?.type !== 'totp' || !options.json.id) fail('IDENTITY_MISMATCH');
        factor = options.json.id;
      }
      if (url.pathname === '/api/accounts/mfa/verify' && (!completed.has('/api/accounts/mfa/issue_challenge') || options.json?.type !== 'totp' || options.json.id !== factor)) fail('IDENTITY_MISMATCH');
      if (url.pathname === '/api/accounts/workspace/select' && (!completed.has('/api/accounts/mfa/verify') || options.json?.workspace_id !== expectedWorkspace)) fail('IDENTITY_MISMATCH');
      if (url.pathname === '/oauth/token' && (!completed.has('/api/accounts/mfa/verify') || options.form?.grant_type !== 'authorization_code' || options.form?.client_id !== SOURCE_CLIENT_ID || options.form?.redirect_uri !== SOURCE_REDIRECT)) fail('IDENTITY_MISMATCH');
      const response = await bridgeRequest(method, url.href, {...options, timeoutMs: Math.min(45000, deadline - Date.now())});
      if (method === 'POST' && response.res.status >= 200 && response.res.status < 300) {
        let value; try { value = JSON.parse(response.text); } catch { fail('REMOTE_RESPONSE_INVALID'); }
        if (!value?.error) completed.add(url.pathname);
      }
      return response;
    };
    bridge.follow = async (raw, options = {}) => {
      let current = new URL(raw, AUTH).href;
      for (let hop = 0; hop < 12; hop++) {
        const target = new URL(current);
        if (target.origin === new URL(SOURCE_REDIRECT).origin && target.pathname === new URL(SOURCE_REDIRECT).pathname) return {finalUrl: current};
        const response = await bridge.request('GET', current, {accept: 'text/html,application/xhtml+xml', referer: options.referer});
        if (response.res.status >= 400) fail(response.res.status === 429 ? 'REMOTE_RATE_LIMITED' : 'REMOTE_HTTP_ERROR', response.res.status >= 500 || response.res.status === 429);
        if (![301, 302, 303, 307, 308].includes(response.res.status) || !response.location) return {finalUrl: current, last: response};
        current = new URL(response.location, current).href;
      }
      fail('REMOTE_REDIRECT_REJECTED');
    };
    let page = await bridge.follow(authorization.href);
    const did = bridge.jar.value('oai-did', AUTH) || crypto.randomUUID();
    bridge.jar.merge([{name: 'oai-did', value: did, domain: 'openai.com', path: '/', secure: true, hostOnly: false}]);
    if (h.isAuthLoginPage(page.finalUrl) && !h.isPasswordLoginPage(page.finalUrl)) {
      const headers = await h.createSentinelHeaders(bridge, {authBase: AUTH, deviceId: did, flow: 'authorize_continue'});
      const started = await h.authJsonStep(bridge, AUTH, 'POST', '/api/accounts/authorize/continue', {username: {kind: 'email', value: credential.email}}, {referer: page.finalUrl, headers});
      const next = h.getContinueUrl(started.data);
      if (!next) fail('ACTION_REQUIRED');
      page = await bridge.follow(new URL(next, AUTH).href);
    }
    if (!h.isPasswordLoginPage(page.finalUrl)) fail('IDENTITY_MISMATCH');
    let payload = await h.verifyPassword(bridge, {authBase: AUTH, deviceId: did, password: credential.password, referer: page.finalUrl});
    payload = await h.completeTotpMfaIfNeeded(bridge, {authBase: AUTH, deviceId: did, payload, totpSecret: credential.totp_secret, referer: page.finalUrl});
    if (!completed.has('/api/accounts/password/verify') || !completed.has('/api/accounts/mfa/verify')) fail('IDENTITY_MISMATCH');
    const workspaces = payload?.['oai-client-auth-session']?.workspaces || payload?.page?.payload?.workspaces;
    if (Array.isArray(workspaces)) {
      const workspaceId = selectSourceWorkspace(workspaces, input.expected_identity);
      const selected = await h.authJsonStep(bridge, AUTH, 'POST', '/api/accounts/workspace/select', {workspace_id: workspaceId}, {referer: h.getContinueUrl(payload) || AUTH + '/workspace'});
      payload = selected.data;
    }
    let continued = await h.continueFlow(bridge, payload);
    if (continued?.finalUrl && new URL(continued.finalUrl).origin === AUTH && new URL(continued.finalUrl).pathname === '/workspace') {
      payload = await selectWorkspacePage(bridge, continued, h, input.config.tosub2_root, dependencies.jsdomClass, input.expected_identity);
      continued = await h.continueFlow(bridge, payload);
    }
    const callback = continued?.finalUrl && new URL(continued.finalUrl);
    if (!callback || callback.origin !== new URL(SOURCE_REDIRECT).origin || callback.pathname !== new URL(SOURCE_REDIRECT).pathname || callback.username || callback.password || callback.hash ||
        callback.searchParams.getAll('state').length !== 1 || callback.searchParams.get('state') !== state || callback.searchParams.has('error') ||
        callback.searchParams.getAll('code').length !== 1 || !callback.searchParams.get('code') || callback.searchParams.has('iss') && callback.searchParams.get('iss') !== AUTH) fail('CALLBACK_STATE_MISMATCH');
    const exchanged = await bridge.request('POST', AUTH + '/oauth/token', {form: {grant_type: 'authorization_code', client_id: SOURCE_CLIENT_ID, code: callback.searchParams.get('code'), code_verifier: verifier, redirect_uri: SOURCE_REDIRECT}});
    const jwks = await bridge.request('GET', AUTH + '/.well-known/jwks.json');
    if (exchanged.res.status !== 200 || jwks.res.status !== 200) fail('IDENTITY_MISMATCH');
    let tokens, keys;
    try { tokens = JSON.parse(exchanged.text); keys = JSON.parse(jwks.text); } catch { fail('REMOTE_RESPONSE_INVALID'); }
    const snapshot = sourceToken(input.source_id_token, keys, {snapshot: true});
    const fresh = sourceToken(tokens.id_token, keys, {nonce, startedAt});
    tokens = null;
    const auth = fresh['https://api.openai.com/auth'];
    const snapshotAuth = snapshot['https://api.openai.com/auth'];
    if (snapshot.sub !== input.expected_identity.source_subject || snapshot.iss !== input.expected_identity.source_issuer || fresh.sub !== snapshot.sub ||
        snapshot.email?.toLowerCase() !== input.expected_identity.source_email.toLowerCase() || snapshotAuth?.chatgpt_account_id !== expectedWorkspace || snapshotAuth?.chatgpt_plan_type !== input.expected_identity.source_plan_type ||
        fresh.email?.toLowerCase() !== input.expected_identity.source_email.toLowerCase() || auth?.chatgpt_account_id !== expectedWorkspace || auth?.chatgpt_plan_type !== input.expected_identity.source_plan_type) fail('IDENTITY_MISMATCH');
    associationClientId = controlledClientId;
    sourceAuthenticated = true;
    credentialControl = null;
  }
  function association(connection) {
    if (!sourceAuthenticated || !associationClientId) fail('IDENTITY_MISMATCH');
    return {version: 1, method: 'credential_snapshot', source_issuer: AUTH, source_subject: input.expected_identity.source_subject,
      source_workspace_id: expectedWorkspace, source_plan_type: input.expected_identity.source_plan_type, kilo_user_id: current.id,
      kilo_client_id: associationClientId, kilo_subject: connection.subject, verified_at: new Date().toISOString()};
  }
  function personalConnection(connection) {
    if (!personalSource || input.organization_id || connection?.state !== 'connected' || typeof connection.email !== 'string' || connection.email.toLowerCase() !== source ||
        typeof connection.subject !== 'string' || !/^[\x21-\x7e]{1,256}$/.test(connection.subject) || !Number.isFinite(Date.parse(connection.connectedAt)) || Date.parse(connection.connectedAt) > Date.now()) fail('IDENTITY_MISMATCH');
    return connection;
  }
  async function providerIdentity() {
    const value = await json('GET', '/api/trpc/user.getAuthProviders');
    const data = value?.result?.data;
    const matches = Array.isArray(data?.providers) ? data.providers.filter(provider => provider?.provider === 'openai') : [];
    if (data?.success !== true || matches.length !== 1 || !Number.isFinite(Date.parse(matches[0].created_at))) fail('IDENTITY_MISMATCH');
    const text = JSON.stringify(matches[0], Object.keys(matches[0]).sort());
    if (text.length > 16384) fail('REMOTE_RESPONSE_INVALID');
    return {createdAt: matches[0].created_at, fingerprint: crypto.createHash('sha256').update(text).digest('hex')};
  }
  const sameConnection = (before, after) => before.state === after?.state && before.subject === after.subject && before.email === after.email && before.connectedAt === after.connectedAt;
  let current;
  const source = input.expected_identity.source_email.toLowerCase();
  let openaiEmail = input.private_state?.verified_identity?.openai_email || '';
  let extra = {};
  if (input.phase === 'login') {
    current = await loginKilo();
    openaiEmail = loginCredential.email;
  } else if (input.phase === 'resume_identity') {
    await verifyIdentityKey();
    try { current = await identity(); }
    catch (error) {
      if (!(error instanceof FlowError) || error.code !== 'AUTH_SESSION_EXPIRED') throw error;
      if (!loginCredential) fail('AUTH_SESSION_EXPIRED', false, 'Kilo网页会话已过期，且没有原Kilo身份的独立登录材料');
      client.jar = new h.CookieJar();
      try { current = await loginKilo(); }
      catch (error) {
        if (error instanceof FlowError && error.code === 'AUTHORIZATION_REJECTED') fail('LOGIN_REJECTED', false, '原Kilo登录身份被官方拒绝，保留槽位并更新该身份的有效登录授权');
        throw error;
      }
    }
    openaiEmail = '';
  } else if (input.phase === 'reconcile_subscription') {
    current = await identity();
    const originalOwner = current.id;
    const statusPath = '/api/trpc/openAiChatGpt.status?input=%7B%7D';
    const before = personalConnection((await json('GET', statusPath))?.result?.data);
    const providerBefore = await providerIdentity();
    if (Date.parse(providerBefore.createdAt) > Date.parse(before.connectedAt)) fail('IDENTITY_MISMATCH');
    client.jar = new h.CookieJar();
    credentialControl = null;
    current = await loginKilo();
    if (current.id !== originalOwner || !authenticatedKiloClientId) fail('IDENTITY_MISMATCH');
    await verifyCredentialSource(current);
    current = await identity();
    const after = personalConnection((await json('GET', statusPath))?.result?.data);
    const providerAfter = await providerIdentity();
    if (!sameConnection(before, after) || providerBefore.createdAt !== providerAfter.createdAt || providerBefore.fingerprint !== providerAfter.fingerprint) fail('IDENTITY_MISMATCH');
    const identityAssociation = association(after);
    const observedAt = identityAssociation.verified_at;
    extra = {byok_enabled: false, byok_enable_evidence: '', byok_evidence: '', organization_id: '', connection_email: after.email, connection_subject: after.subject,
      connection_at: after.connectedAt, connection_workspace_id: '', connection_identity_evidence: 'credential_snapshot', identity_association: identityAssociation,
      credential_identity_evidence: {version: 1, method: 'credential_snapshot_password_totp', kilo_user_id: current.id, kilo_client_id: associationClientId,
        source_client_id: SOURCE_CLIENT_ID, source_issuer: AUTH, source_subject: input.expected_identity.source_subject, source_workspace_id: expectedWorkspace,
        source_plan_type: input.expected_identity.source_plan_type, connection_subject: after.subject, provider_created_at: providerBefore.createdAt,
        provider_fingerprint: providerBefore.fingerprint, observed_at: observedAt, kilo_password_totp_verified: true, source_password_totp_verified: true,
        source_snapshot_signature_verified: true, source_signature_verified: true, nonce_verified: true, provider_unchanged: true},
      connection_observation: {version: 1, method: 'existing_delegated_connection', kilo_user_id: current.id, organization_id: '', openai_email: after.email.toLowerCase(),
        openai_subject: after.subject, connected_at: after.connectedAt, observed_at: observedAt, provider_created_at: providerBefore.createdAt,
        provider_fingerprint: providerBefore.fingerprint, connection_unchanged: true, provider_unchanged: true, enabled_evidence: 'unverified'}};
    openaiEmail = after.email;
  } else {
    current = await identity();
    const reauthorizing = input.phase === 'reauthorize_subscription';
    if (reauthorizing) {
      if (!personalSource || input.organization_id || !input.kilo_identity?.user_id || current.id !== input.kilo_identity.user_id) fail('IDENTITY_MISMATCH');
      const initial = (await json('GET', '/api/trpc/openAiChatGpt.status?input=%7B%7D'))?.result?.data;
      if (initial?.state !== 'disconnected') fail('BYOK_ENABLE_UNVERIFIED', false, '原订阅并非明确断开，未重复授权');
      const originalOwner = current.id;
      client.jar = new h.CookieJar();
      credentialControl = null;
      current = await loginKilo();
      if (current.id !== originalOwner) fail('IDENTITY_MISMATCH');
      openaiEmail = current.email;
    }
    if (input.phase === 'connect_subscription' || reauthorizing) {
      const owner = input.organization_id ? {organizationId: input.organization_id} : {};
      const statusPath = '/api/trpc/openAiChatGpt.status?' + new URLSearchParams({input: JSON.stringify(owner)});
      const before = await json('GET', statusPath);
      const existing = before?.result?.data;
      const previousConnectedAt = existing?.connectedAt || null;
      // An explicit recovery cannot turn an intervening connection into another grant.
      if (reauthorizing && existing?.state !== 'disconnected') fail('BYOK_ENABLE_UNVERIFIED', false, '原订阅状态已改变，未重复授权');
      if (existing?.state === 'connected') {
        personalConnection(existing);
        await verifyCredentialSource(current);
        current = await identity();
        const observed = personalConnection((await json('GET', statusPath))?.result?.data);
        if (!sameConnection(existing, observed)) fail('IDENTITY_MISMATCH');
        // Read-only continuity is not authority for the remote enabled bit.
        extra = {byok_enabled: false, byok_enable_evidence: 'existing_connection_enablement_unknown', byok_evidence: '', organization_id: '',
          connection_email: observed.email, connection_subject: observed.subject, connection_at: observed.connectedAt, connection_workspace_id: '',
          connection_identity_evidence: 'credential_snapshot', identity_association: association(observed)};
        openaiEmail = observed.email;
      } else {
      // Reject invalid source proofs before creating a link or granting access.
      // An independent identity still needs its actual workspace selection.
      if (personalSource) await verifyCredentialSource(current, false);
      if (reauthorizing && (await json('GET', statusPath))?.result?.data?.state !== 'disconnected') {
        fail('BYOK_ENABLE_UNVERIFIED', false, '来源认证期间订阅状态已改变，未重复授权');
      }
      const authorizationStarted = Date.now();
      // A previous OpenAI session must never select the old subscriber after
      // this Kilo identity has been assigned to a different source.
      if (!openaiEmail || openaiEmail.toLowerCase() !== source) client.jar = new h.CookieJar(kiloCookies(client.jar.toJSON()));
      // There is no token-import/connect method in openAiChatGpt's router.
      // Creating this linking cookie is mandatory even for an existing OpenAI login.
      const linked = await json('POST', '/api/trpc/user.linkAuthProvider', {json: {provider: 'openai', ...owner}});
      if (linked.error || !linked.result) fail('BYOK_LINK_REJECTED');
      await authorize(true);
      const connectionWorkspace = requireSelectedWorkspace();
      current = await identity();
      const status = await json('GET', statusPath);
      const connection = status?.result?.data;
      if (connection?.state !== 'connected') fail('BYOK_NOT_CONNECTED', false, '检查Kilo返回的订阅资格或授权错误');
      if (typeof connection.email !== 'string' || connection.email.toLowerCase() !== source || !connection.subject) fail('IDENTITY_MISMATCH');
      let identityEvidence = connectionWorkspace ? 'accepted_workspace_selection' : 'personal_subject';
      let identityAssociation;
      if (!connectionWorkspace && (!personalSource || connection.subject !== input.expected_identity.source_subject)) {
        identityEvidence = 'credential_snapshot';
        identityAssociation = association(connection);
      }
      const connectedTime = Date.parse(connection.connectedAt);
      if (!Number.isFinite(connectedTime) || connectedTime < authorizationStarted - 5000 || connectedTime > Date.now() + 30000 || previousConnectedAt === connection.connectedAt) fail('BYOK_ENABLE_UNVERIFIED');
      // This fact is scoped to a NEW completed delegated authorization. A status
      // read on its own never proves enabled. Pinned upstream save inserts and
      // upserts is_enabled:true; eligibility refusal changes state to error.
      extra = {byok_enabled: true, byok_enable_evidence: 'fresh_delegated_authorization', connection_email: connection.email, connection_subject: connection.subject, connection_at: connection.connectedAt, connection_workspace_id: connectionWorkspace, connection_identity_evidence: identityEvidence, organization_id: input.organization_id || '',
        byok_evidence: {version: 1, method: 'fresh_delegated_authorization', contract_commit: '4f7acd6e99ebc66870c07324341eaf53d8b1fa1f', kilo_user_id: current.id, openai_email: connection.email, openai_workspace_id: connectionWorkspace, workspace_evidence: identityEvidence, openai_subject: connection.subject, openai_plan_type: input.expected_identity.source_plan_type || '', organization_id: input.organization_id || '', requested_scope: OBO_SCOPE, oauth_callback_verified: callbackCompleted, consent_granted: consentCompleted, connected_at: connection.connectedAt, previous_connected_at: previousConnectedAt, observed_at: new Date().toISOString()}};
      if (identityAssociation) extra.identity_association = identityAssociation;
      openaiEmail = connection.email;
      }
    } else if (input.phase === 'approve_device') {
      const verification = secureURL(input.device.verification_url);
      if (verification.origin !== KILO || verification.pathname !== '/device-auth' || verification.searchParams.getAll('code').length !== 1 || verification.searchParams.get('code') !== input.device.user_code) fail('DEVICE_INPUT_INVALID');
      // Plain requests from Go do not carry a requesting_client or pending link.
      // Those optional UI choices require an explicit future contract.
      let approved;
      try { approved = await json('POST', '/api/device-auth/tokens', {json: {code: input.device.user_code}}); }
      catch (error) {
        if (error?.retryable) fail('REMOTE_OUTCOME_UNKNOWN', false, '由Go查询同一设备会话；确认结果前不要重复批准');
        throw error;
      }
      if (approved.success !== true) fail('DEVICE_APPROVAL_FAILED');
      extra = {device_approved: true};
    } else fail('PHASE_UNSUPPORTED');
  }
  if (input.phase !== 'resume_identity' && openaiEmail.toLowerCase() !== source) fail('IDENTITY_MISMATCH');
  const verified = {kilo_user_id: current.id, openai_email: openaiEmail};
  const identitySessionMaterial = exportIdentityMaterial(input, current, client.jar);
  const privateState = {cookies: client.jar.toJSON(), verified_identity: verified};
  if (credentialControl && ['login', 'resume_identity'].includes(input.phase)) privateState.credential_control = credentialControl;
  return {...extra, verified_identity: verified, identity_session_material: identitySessionMaterial, expires_at: current.expires,
    private_state: privateState};
}
export function classified(error) {
  const refusal = rebindFailure(error);
  if (refusal) return {error_code: refusal.code, retryable: false, next_action: refusal.nextAction};
  if (error instanceof FlowError) return {error_code: error.code, retryable: error.retryable, next_action: error.nextAction};
  const message = String(error?.message || '');
  if (/KILO_PASSWORD_REJECTED|invalid.password|incorrect.password|wrong.password/i.test(message)) return {error_code: 'LOGIN_REJECTED', retryable: false};
  if (/KILO_MFA_REJECTED|invalid.(totp|otp|code)|(totp|otp|code).(invalid|incorrect|wrong)/i.test(message)) return {error_code: 'MFA_REJECTED', retryable: false};
  if (/KILO_INTERACTION_REQUIRED|TOTP factor|2FA key/i.test(message)) return {error_code: 'ACTION_REQUIRED', retryable: false, next_action: '登录需要额外验证或不支持的MFA因子'};
  if (/HTTP 429|returned 429|timed out|ECONN|fetch failed/i.test(message)) return {error_code: 'REMOTE_UNAVAILABLE', retryable: true};
  return {error_code: 'AUTH_PROTOCOL_ERROR', retryable: false, next_action: '检查官方授权流程兼容性'};
}
async function main() {
  let text = '';
  for await (const chunk of process.stdin) { text += chunk; if (Buffer.byteLength(text) > MAX_INPUT) fail('REQUEST_TOO_LARGE'); }
  // Helper output can include URLs or errors containing private server data.
  // Only the fixed JSON result below is ever allowed onto this pipe.
  console.log = () => {}; console.error = () => {}; console.warn = () => {};
  try { process.stdout.write(JSON.stringify({ok: true, ...await execute(JSON.parse(text))})); }
  catch (error) { process.stdout.write(JSON.stringify({ok: false, ...classified(error)})); }
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main().catch(() => { process.stdout.write(JSON.stringify({ok: false, error_code: 'REQUEST_INVALID', retryable: false})); process.exitCode = 1; });
