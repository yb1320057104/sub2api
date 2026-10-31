package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gorilla/websocket"
	"github.com/pquerna/otp/totp"
	xproxy "golang.org/x/net/proxy"
)

type authorizationBrowserError struct {
	stage, message string
	needsInput     bool
	// noReplay marks a failure that happened after the password or a completed
	// challenge was submitted. Such a task is never replayed in another browser
	// identity or against another exit.
	noReplay bool
}

func (e *authorizationBrowserError) Error() string { return e.message }
func authorizationBrowserFailure(stage, message string) error {
	return &authorizationBrowserError{stage: stage, message: message}
}

func authorizationBannedFailure() error {
	return authorizationBrowserFailure("account_banned", "账号已封禁：官方提示账户已被删除、停用或封禁；已停止授权，未写入新凭据")
}

// Reference account API: run-account.cjs detects the official deleted/deactivated
// page; credits-protocol.cjs also recognizes error.code=account_deactivated.
// A bare 401/403, Cloudflare challenge or proxy error is not evidence of a ban.
var authorizationBanPattern = regexp.MustCompile(`(?i)\b(?:account|user)[_ -](?:deactivated|deleted|disabled|banned|suspended)\b|\bdeleted or deactivated\b|\b(?:this |your )?account (?:has been |was |is )(?:deleted|deactivated|disabled|banned|suspended)\b|(?:账号|账户)(?:已被|已经|已)(?:删除|停用|封禁|封号|禁用)|账户已被删除或停用`)

func authorizationBanText(text string) bool {
	if len(text) > 65536 {
		text = text[:65536]
	}
	return authorizationBanPattern.MatchString(text)
}

func authorizationBanJSON(raw string) bool {
	if len(raw) > 65536 {
		return false
	}
	var envelope struct {
		Code      string          `json:"code"`
		ErrorCode string          `json:"errorCode"`
		Message   string          `json:"message"`
		Detail    string          `json:"detail"`
		Error     json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		return false
	}
	if authorizationBanText(envelope.Code) || authorizationBanText(envelope.ErrorCode) || authorizationBanText(envelope.Message) || authorizationBanText(envelope.Detail) {
		return true
	}
	if len(envelope.Error) > 0 {
		var nested struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &nested) == nil {
			return authorizationBanText(nested.Code) || authorizationBanText(nested.Type) || authorizationBanText(nested.Message)
		}
		var code string
		if json.Unmarshal(envelope.Error, &code) == nil {
			return authorizationBanText(code)
		}
	}
	return false
}

// Authentication errors are semantic: HTTP 403 alone never proves a ban.
// Only fixed messages, a bounded endpoint name, an upstream error identifier and
// one short upstream sentence may reach the task history.
func authorizationUpstreamDetail(status int, endpoint, code, message string) string {
	parts := []string{"HTTP " + strconv.Itoa(status)}
	if endpoint != "" {
		parts = append(parts, "接口："+endpoint)
	}
	if regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`).MatchString(code) {
		parts = append(parts, "错误标识："+code)
	}
	message = strings.Join(strings.Fields(message), " ")
	if message != "" {
		if len(message) > 120 {
			message = strings.TrimSpace(message[:120]) + "…"
		}
		parts = append(parts, "上游说明："+message)
	}
	return "（" + strings.Join(parts, "；") + "）"
}

// The endpoint decides the step, so a shared upstream code such as
// incorrect_code is never reported as the wrong kind of failure.
func authorizationLoginResponse(status int, raw string, challenge bool, endpoint string) error {
	if len(raw) > 65536 {
		raw = raw[:65536]
	}
	suffix := "（HTTP " + strconv.Itoa(status) + "）"
	lower := strings.ToLower(raw)
	if challenge || (strings.Contains(lower, "<html") && (strings.Contains(lower, "cloudflare") || strings.Contains(lower, "challenge-platform") || strings.Contains(lower, "verify you are human") || strings.Contains(lower, "<title>just a moment"))) {
		return &authorizationBrowserError{stage: "login_security", message: "登录接口返回安全验证页面，尚未完成账号验证；这不表示账号封禁" + suffix, needsInput: true}
	}
	if authorizationBanJSON(raw) {
		return authorizationBannedFailure()
	}
	var envelope struct {
		Code      string
		ErrorCode string
		Message   string
		Error     json.RawMessage
	}
	_ = json.Unmarshal([]byte(raw), &envelope)
	var detail struct {
		Code    string
		Type    string
		Message string
	}
	_ = json.Unmarshal(envelope.Error, &detail)
	var errorText string
	_ = json.Unmarshal(envelope.Error, &errorText)
	code := detail.Code
	if code == "" {
		code = envelope.Code
	}
	if code == "" {
		code = envelope.ErrorCode
	}
	message := detail.Message
	if message == "" {
		message = envelope.Message
	}
	text := strings.ToLower(strings.Join([]string{envelope.Code, envelope.ErrorCode, envelope.Message, detail.Code, detail.Type, detail.Message, errorText}, " "))
	full := authorizationUpstreamDetail(status, endpoint, code, message)
	step := strings.ToLower(endpoint)
	switch {
	case strings.Contains(step, "mfa/") || strings.Contains(step, "/verify_mfa"):
		if status >= 400 {
			return &authorizationBrowserError{stage: "invalid_twofa", message: "2FA验证失败，官方拒绝了这一步的验证码；请检查密钥与系统时间后重试" + full, needsInput: true}
		}
	case strings.Contains(step, "password/"):
		if status >= 400 {
			if regexp.MustCompile(`invalid_username_or_password|invalid_credentials|invalid_email_or_password`).MatchString(text) {
				return &authorizationBrowserError{stage: "invalid_credentials", message: "邮箱或密码未被接受，请检查并补全凭据后重试；未确认账号封禁" + full, needsInput: true}
			}
			return &authorizationBrowserError{stage: "invalid_password", message: "账号密码未被接受，请补全正确密码后重试" + full, needsInput: true}
		}
	}
	if regexp.MustCompile(`\b(?:invalid_credentials|invalid_username_or_password|invalid_email_or_password)\b`).MatchString(text) {
		return &authorizationBrowserError{stage: "invalid_credentials", message: "邮箱或密码未被接受，请检查并补全凭据后重试；未确认账号封禁" + full, needsInput: true}
	}
	if regexp.MustCompile(`(?:invalid|incorrect|wrong)[_ -]*(?:password)|password[_ -]*(?:invalid|incorrect|wrong)`).MatchString(text) {
		return &authorizationBrowserError{stage: "invalid_password", message: "账号密码未被接受，请补全正确密码后重试" + full, needsInput: true}
	}
	if regexp.MustCompile(`(?:invalid|incorrect|wrong|expired)[_ -]*code|(?:invalid|incorrect|wrong|expired)[_ -]*(?:totp|otp|verification[_ -]*code|mfa[_ -]*code)|(?:totp|otp|mfa[_ -]*code|verification[_ -]*code)[_ -]*(?:invalid|incorrect|wrong|expired)`).MatchString(text) {
		return &authorizationBrowserError{stage: "invalid_twofa", message: "2FA验证码未被接受，请检查密钥及系统时间后重试" + full, needsInput: true}
	}
	if status == http.StatusTooManyRequests || strings.Contains(text, "rate_limit_exceeded") || strings.Contains(text, "auth_rate_limited") {
		return &authorizationBrowserError{stage: "login_rate_limited", message: "官方登录请求被限流，请稍后重试；未确认账号封禁" + full, needsInput: true}
	}
	if status >= 400 {
		return &authorizationBrowserError{stage: "login_http_failed", message: "官方登录接口拒绝请求，未确认账号封禁" + full, needsInput: true}
	}
	return nil
}

func authorizationBrowserPath() string {
	if path := os.Getenv("PK_AUTH_BROWSER_PATH"); path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
		return ""
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func authorizationBrowserHost(host string) bool {
	host = strings.ToLower(host)
	for _, base := range []string{"openai.com", "auth0.openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com", "challenges.cloudflare.com", "fonts.googleapis.com", "fonts.gstatic.com"} {
		if host == base || strings.HasSuffix(host, "."+base) {
			return true
		}
	}
	return false
}

func authorizationProxyDial(ctx context.Context, proxyURL, target string) (net.Conn, error) {
	u, err := url.Parse(proxyURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid proxy")
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	if u.Scheme == "socks5" || u.Scheme == "socks5h" {
		copyURL := *u
		copyURL.Scheme = "socks5"
		d, err := xproxy.FromURL(&copyURL, dialer)
		if err != nil {
			return nil, err
		}
		cd, ok := d.(xproxy.ContextDialer)
		if !ok {
			return nil, errors.New("proxy lacks cancellation")
		}
		return cd.DialContext(ctx, "tcp", target)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("unsupported proxy")
	}
	conn, err := dialer.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(25 * time.Second))
	if u.Scheme == "https" {
		secured := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if err = secured.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secured
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if u.User != nil {
		password, _ := u.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+password)))
	}
	if err = req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("proxy CONNECT rejected")
	}
	_ = conn.SetDeadline(time.Time{})
	success = true
	return &authorizationBufferedConn{Conn: conn, reader: reader}, nil
}

type authorizationBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *authorizationBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// Chromium cannot authenticate directly to a SOCKS5 proxy. This per-account,
// loopback-only CONNECT bridge owns the upstream credentials and never falls back.
func authorizationBrowserProxy(ctx context.Context, proxyURL string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	bridgeCtx, cancel := context.WithCancel(ctx)
	var sockets sync.Map
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.Host)
		if err != nil || port != "443" || !authorizationBrowserHost(host) || r.Method != http.MethodConnect {
			http.Error(w, "blocked", http.StatusForbidden)
			return
		}
		upstream, err := authorizationProxyDial(bridgeCtx, proxyURL, r.Host)
		if err != nil {
			http.Error(w, "proxy unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = upstream.Close() }()
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		client, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		sockets.Store(client, upstream)
		defer sockets.Delete(client)
		if bridgeCtx.Err() != nil {
			return
		}
		if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err = rw.Flush(); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, rw); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	})}
	go func() { _ = server.Serve(listener) }()
	closeBridge := func() {
		cancel()
		_ = server.Close()
		sockets.Range(func(k, v any) bool { _ = k.(net.Conn).Close(); _ = v.(net.Conn).Close(); return true })
	}
	return "http://" + listener.Addr().String(), closeBridge, nil
}

type authorizationCDPMessage struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type authorizationCDP struct {
	conn    *websocket.Conn
	seq     atomic.Int64
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan authorizationCDPMessage
	events  chan authorizationCDPMessage
	done    chan struct{}
}

func newAuthorizationCDP(ctx context.Context, address string) (*authorizationCDP, error) {
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, response, err := d.DialContext(ctx, address, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	c := &authorizationCDP{conn: conn, pending: make(map[int64]chan authorizationCDPMessage), events: make(chan authorizationCDPMessage, 256), done: make(chan struct{})}
	conn.SetReadLimit(2 << 20)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-c.done:
		}
	}()
	go func() {
		defer close(c.done)
		for {
			var m authorizationCDPMessage
			if conn.ReadJSON(&m) != nil {
				return
			}
			if m.ID != 0 {
				c.mu.Lock()
				ch := c.pending[m.ID]
				c.mu.Unlock()
				if ch != nil {
					select {
					case ch <- m:
					default:
					}
				}
			} else if m.Method == "Network.responseReceived" || m.Method == "Network.requestWillBeSent" || m.Method == "Network.loadingFinished" || m.Method == "Network.loadingFailed" || m.Method == "Fetch.requestPaused" {
				select {
				case c.events <- m:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return c, nil
}
func (c *authorizationCDP) call(ctx context.Context, method string, params any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	id := c.seq.Add(1)
	ch := make(chan authorizationCDPMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	c.writeMu.Lock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := c.conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	c.writeMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case response := <-ch:
		if len(response.Error) > 0 {
			return errors.New("browser command failed")
		}
		if out != nil {
			return json.Unmarshal(response.Result, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("browser closed")
	}
}

func authorizationPause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Keep browser identity aligned with the installed Chromium TLS implementation.
// Do not advertise the reference client's Chrome 146 while running Chromium 136.
func authorizationBrowserIdentity(ctx context.Context, cdp *authorizationCDP) error {
	var version struct {
		Product   string `json:"product"`
		UserAgent string `json:"userAgent"`
	}
	if err := cdp.call(ctx, "Browser.getVersion", map[string]any{}, &version); err != nil {
		return err
	}
	full := strings.TrimPrefix(strings.TrimPrefix(version.Product, "Chrome/"), "HeadlessChrome/")
	major, _, _ := strings.Cut(full, ".")
	if _, err := strconv.Atoi(major); err != nil || version.UserAgent == "" {
		return authorizationBrowserFailure("browser_identity", "无法读取授权浏览器版本，未发送预检请求")
	}
	// The OpenAI account page records the device from this metadata, so the
	// session always presents a macOS Chrome identity regardless of the host
	// that runs Chromium. PK_AUTH_BROWSER_DEVICE=host keeps the real platform
	// for local debugging.
	hostIdentity := strings.EqualFold(strings.TrimSpace(os.Getenv("PK_AUTH_BROWSER_DEVICE")), "host")
	platform, navigatorPlatform, platformVersion := "macOS", "MacIntel", "15.0.0"
	ua := strings.ReplaceAll(version.UserAgent, "HeadlessChrome/", "Chrome/")
	// Replace only the leading platform token; the WebKit token must stay intact.
	if start := strings.Index(ua, "("); start >= 0 {
		if end := strings.Index(ua[start:], ")"); end > 0 {
			ua = ua[:start+1] + "Macintosh; Intel Mac OS X 10_15_7" + ua[start+end:]
		}
	}
	if hostIdentity {
		platform, navigatorPlatform, platformVersion = "Linux", "Linux x86_64", "6.5.0"
		ua = strings.ReplaceAll(version.UserAgent, "HeadlessChrome/", "Chrome/")
		if strings.Contains(version.UserAgent, "Windows") {
			platform, navigatorPlatform, platformVersion = "Windows", "Win32", "15.0.0"
		} else if strings.Contains(version.UserAgent, "Macintosh") {
			platform, navigatorPlatform, platformVersion = "macOS", "MacIntel", "14.6.1"
		}
	}
	// The brand list must agree with the user agent: a Chrome UA without the
	// Google Chrome brand is a mismatch Cloudflare scores against.
	brands := []map[string]string{{"brand": "Not.A/Brand", "version": "99"}, {"brand": "Chromium", "version": major}}
	fullBrands := []map[string]string{{"brand": "Not.A/Brand", "version": "99.0.0.0"}, {"brand": "Chromium", "version": full}}
	if hostIdentity {
		if strings.HasPrefix(version.Product, "Chrome/") {
			brands = append(brands, map[string]string{"brand": "Google Chrome", "version": major})
			fullBrands = append(fullBrands, map[string]string{"brand": "Google Chrome", "version": full})
		}
	} else {
		brands = append(brands, map[string]string{"brand": "Google Chrome", "version": major})
		fullBrands = append(fullBrands, map[string]string{"brand": "Google Chrome", "version": full})
	}
	params := map[string]any{
		"userAgent":      ua,
		"acceptLanguage": "en-US,en;q=0.9",
		"platform":       navigatorPlatform,
		"userAgentMetadata": map[string]any{
			"brands":          brands,
			"fullVersionList": fullBrands,
			"fullVersion":     full,
			"platform":        platform,
			"platformVersion": platformVersion,
			"architecture":    "x86", "model": "", "mobile": false, "bitness": "64",
		},
	}
	if err := cdp.call(ctx, "Network.setUserAgentOverride", params, nil); err != nil {
		return authorizationBrowserFailure("browser_identity", "授权浏览器身份配置失败，未发送预检请求")
	}
	authorizationTrace(nil, "identity platform=%s platformVersion=%s brands=%d ua=%s", platform, platformVersion, len(brands), ua)
	// Keep the rendered environment consistent with the claimed device; both
	// overrides are best effort and never block the flow.
	_ = cdp.call(ctx, "Emulation.setTimezoneOverride", map[string]any{"timezoneId": "America/Los_Angeles"}, nil)
	_ = cdp.call(ctx, "Emulation.setLocaleOverride", map[string]any{"locale": "en-US"}, nil)
	return nil
}

// Cloudflare detects automation through navigator.webdriver and a missing
// window.chrome far more often than through the CDP session itself. This runs
// before any document exists and never touches the page's own state. It also
// keeps the renderer and font answers consistent with the macOS identity the
// user agent announces, because a Linux SwiftShader renderer is the loudest
// remaining headless tell.
const authorizationBrowserStealthScript = `(()=>{
const def=(o,k,v)=>{try{Object.defineProperty(o,k,{get:()=>v,configurable:true});}catch(e){}};
try{def(Navigator.prototype,'webdriver',undefined);}catch(e){}
try{if(!navigator.languages||!navigator.languages.length)def(Navigator.prototype,'languages',['en-US','en']);}catch(e){}
try{def(Navigator.prototype,'hardwareConcurrency',10);}catch(e){}
try{def(Navigator.prototype,'deviceMemory',8);}catch(e){}
try{def(Navigator.prototype,'maxTouchPoints',0);}catch(e){}
try{def(Navigator.prototype,'vendor','Google Inc.');}catch(e){}
try{def(Navigator.prototype,'platform','MacIntel');}catch(e){}
try{def(Navigator.prototype,'pdfViewerEnabled',true);}catch(e){}
try{if(!window.chrome)window.chrome={runtime:{}};}catch(e){}
try{
const V='Google Inc. (Apple)',R='ANGLE (Apple, ANGLE Metal Renderer: Apple M2, Unspecified Version)';
for(const P of [window.WebGLRenderingContext&&WebGLRenderingContext.prototype,window.WebGL2RenderingContext&&WebGL2RenderingContext.prototype]){
if(!P||!P.getParameter)continue;
P.getParameter=new Proxy(P.getParameter,{apply(t,s,a){const p=a[0];if(p===37445)return V;if(p===37446)return R;return Reflect.apply(t,s,a);}});
}
}catch(e){}
try{
const MAC=['sf pro text','sf pro display','.sf ns','.sf ns text','helvetica neue','helvetica','lucida grande','menlo','monaco','avenir','avenir next','sf mono','san francisco','geneva','optima','futura','baskerville','copperplate','pingfang sc','hiragino sans gb','apple color emoji','apple symbols','songti sc','stheiti'];
if(window.FontFaceSet&&FontFaceSet.prototype&&FontFaceSet.prototype.check){
FontFaceSet.prototype.check=new Proxy(FontFaceSet.prototype.check,{apply(t,s,a){try{const spec=String(a[0]||'');const fam=spec.replace(/^\s*\d+(?:\.\d+)?(?:px|em|pt)\s+/,'').replace(/^["']|["']$/g,'').trim().toLowerCase();if(fam&&MAC.indexOf(fam)>=0)return true;}catch(e){}return Reflect.apply(t,s,a);}});
}
}catch(e){}
})()`

// A private Xvfb-backed display for the background Chrome identity.
func authorizationBrowserDisplayEnv(mode string) ([]string, func(), error) {
	if mode != authorizationModeBackground || runtime.GOOS != "linux" || os.Getenv("DISPLAY") != "" {
		return nil, func() {}, nil
	}
	binary, err := exec.LookPath("Xvfb")
	if err != nil {
		return nil, func() {}, errors.New("no display")
	}
	for number := 90; number < 100; number++ {
		display := ":" + strconv.Itoa(number)
		if _, statErr := os.Stat("/tmp/.X" + strconv.Itoa(number) + "-lock"); statErr == nil {
			continue
		}
		cmd := exec.Command(binary, display, "-screen", "0", "1366x900x24", "-nolisten", "tcp")
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err = cmd.Start(); err != nil {
			return nil, func() {}, err
		}
		if authorizationPause(context.Background(), 700*time.Millisecond) != nil {
			_ = cmd.Process.Kill()
			return nil, func() {}, errors.New("display start interrupted")
		}
		return []string{"DISPLAY=" + display}, func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}, nil
	}
	return nil, func() {}, errors.New("no display")
}

// Chromium errors are reduced to known categories; never expose proxy URLs or
// arbitrary upstream error text in administrator-visible results.
func authorizationPrecheckNetworkFailure(reason string) error {
	message := "代理连接或页面响应失败"
	switch {
	case strings.Contains(reason, "PROXY_AUTH"), strings.Contains(reason, "INVALID_AUTH_CREDENTIALS"):
		message = "代理认证失败，请检查代理用户名和密码"
	case strings.Contains(reason, "TUNNEL_CONNECTION_FAILED"), strings.Contains(reason, "PROXY_CONNECTION_FAILED"):
		message = "代理隧道建立失败，请检查协议、端口、认证及来源 IP 白名单"
	case strings.Contains(reason, "NAME_NOT_RESOLVED"):
		message = "代理或目标域名解析失败"
	case strings.Contains(reason, "CERT_"), strings.Contains(reason, "SSL_"):
		message = "代理链路的 HTTPS/TLS 连接失败"
	case strings.Contains(reason, "TIMED_OUT"):
		message = "代理连接或首页响应超时"
	case strings.Contains(reason, "CONNECTION_RESET"), strings.Contains(reason, "CONNECTION_CLOSED"):
		message = "代理或目标站点提前断开连接"
	}
	return authorizationBrowserFailure("proxy_network_failed", message+"，未提交账号密码")
}

func authorizationPrecheckHTTPState(code int64) string {
	if code >= 100 && code <= 599 {
		return "HTTP " + strconv.FormatInt(code, 10)
	}
	return "尚未收到 HTTP 响应"
}

// The authorization trace is ON by default so every failed login leaves the
// upstream status and body in the application log. Setting
// PK_AUTH_BROWSER_TRACE=false (or 0/off/no) silences it. Every line is
// redacted, flattened to one line and truncated.
func authorizationTraceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PK_AUTH_BROWSER_TRACE"))) {
	case "false", "0", "off", "no":
		return false
	}
	return true
}

// Proxy URLs carry credentials; only host:port may reach the log.
func authorizationProxyEndpoint(proxyURL string) string {
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Host == "" {
		return "invalid"
	}
	return parsed.Scheme + "://" + parsed.Host
}

// The OAuth client formats failures as "token exchange failed: status %d,
// body: %s" and "request failed: %v". The task message keeps one bounded,
// single-line copy so a failed exchange is diagnosable from the UI.
func authorizationExchangeDetail(err error) string {
	if err == nil {
		return ""
	}
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > 200 {
		text = strings.TrimSpace(text[:200]) + "…"
	}
	return "（" + text + "）"
}

// A retry cannot recover a 4xx: the code is either consumed or invalid. Only a
// request that produced no HTTP answer is worth one more attempt.
func authorizationExchangeRetryable(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "status 4") {
		return false
	}
	for _, marker := range []string{"request failed", "status 5", "timeout", "deadline exceeded", "connection", "eof", "no such host", "tls"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func authorizationTrace(secret *AuthorizationSecret, format string, args ...any) {
	if !authorizationTraceEnabled() {
		return
	}
	line := fmt.Sprintf(format, args...)
	if secret != nil {
		for _, value := range []string{secret.Password, secret.TwoFA, secret.Email} {
			if len(value) >= 4 {
				line = strings.ReplaceAll(line, value, "<redacted>")
			}
		}
	}
	line = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, line)
	if len(line) > 600 {
		line = line[:600] + "..."
	}
	logger.LegacyPrintf("service.authorization", "trace: %s", line)
}

// Cloudflare renders a managed challenge in a cross-origin iframe. The probe
// reports the checkbox position of the widget that is really on screen so the
// click can be dispatched as a mouse event through CDP instead of a synthetic
// .click() from page script, which the widget rejects.
const authorizationChallengeScript = `(()=>{const frames=Array.from(document.querySelectorAll('iframe[src*="challenges.cloudflare.com"],iframe[src*="challenge-platform"]'));for(const frame of frames){const box=frame.getBoundingClientRect();if(box.width>80&&box.height>30)return {state:'widget',x:box.left+28,y:box.top+Math.min(30,box.height/2)};}const text=((document.title||'')+' '+(document.body?document.body.innerText:'')).toLowerCase();if(/just a moment|checking your browser|verify (?:you are|that you are) (?:a )?human|needs to review the security|security verification|enable javascript and cookies|验证您是真人|人机验证/.test(text))return {state:'challenge'};return {state:'clear'};})()`

const authorizationPrecheckScript = `(()=>{if(!document.body||document.readyState==='loading')return 'loading';const text=(document.title+' '+document.body.innerText).toLowerCase();if(/just a moment|checking your browser|verify.{0,20}human|security verification|enable javascript and cookies|验证您是真人|人机验证/.test(text))return 'challenge';return document.contentType==='text/html'&&document.documentElement?'html':'invalid'})()`

func authorizationChallengeProbe(ctx context.Context, cdp *authorizationCDP) (string, float64, float64) {
	var result struct {
		Result struct {
			Value struct {
				State string  `json:"state"`
				X     float64 `json:"x"`
				Y     float64 `json:"y"`
			} `json:"value"`
		} `json:"result"`
	}
	if err := cdp.call(ctx, "Runtime.evaluate", map[string]any{"expression": authorizationChallengeScript, "returnByValue": true}, &result); err != nil {
		return "clear", 0, 0
	}
	if result.Result.Value.State == "" {
		return "clear", 0, 0
	}
	return result.Result.Value.State, result.Result.Value.X, result.Result.Value.Y
}

func authorizationChallengeClick(ctx context.Context, cdp *authorizationCDP, x, y float64) {
	x += float64(time.Now().UnixNano()%7) - 3
	y += float64(time.Now().UnixNano()%5) - 2
	for _, event := range []struct {
		kind    string
		buttons int
		clicks  int
	}{{"mouseMoved", 0, 0}, {"mousePressed", 1, 1}, {"mouseReleased", 0, 1}} {
		_ = cdp.call(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type": event.kind, "x": x, "y": y, "button": "left",
			"buttons": event.buttons, "clickCount": event.clicks, "pointerType": "mouse",
		}, nil)
		if event.kind == "mousePressed" {
			_ = authorizationPause(ctx, 70*time.Millisecond)
		}
	}
}

// Never report a challenge as a failure while the browser can still clear it:
// the widget is clicked (at most once every 8s) until the document is real again.
func authorizationPassChallenge(ctx context.Context, cdp *authorizationCDP, mode string) bool {
	deadline := time.Now().Add(authorizationChallengeWindow(mode))
	clicked := time.Time{}
	for time.Now().Before(deadline) {
		state, x, y := authorizationChallengeProbe(ctx, cdp)
		if state == "clear" {
			return true
		}
		if state == "widget" && (clicked.IsZero() || time.Since(clicked) > 8*time.Second) {
			authorizationChallengeClick(ctx, cdp, x, y)
			clicked = time.Now()
		}
		if authorizationPause(ctx, 700*time.Millisecond) != nil {
			return false
		}
	}
	return false
}

const (
	authorizationModeHeadless       = "headless"
	authorizationModeBackground     = "background"
	authorizationProxyAttempts      = 3
	authorizationPrecheckHeadless   = 70 * time.Second
	authorizationPrecheckBackground = 180 * time.Second
	authorizationChallengeHeadless  = 30 * time.Second
	authorizationChallengeHeadful   = 180 * time.Second
	// Each proxy's browser phase has an 8-minute budget even when both
	// identities have to wait for a Cloudflare challenge twice.
	authorizationBrowserBudget = 8 * time.Minute
)

// PK_AUTH_BROWSER_MODE pins one identity; the default keeps the reference order
// (headless first, then a real offscreen Chrome window).
func authorizationBrowserModes() []string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PK_AUTH_BROWSER_MODE"))) {
	case "headless":
		return []string{authorizationModeHeadless}
	case "background", "headful", "visible":
		return []string{authorizationModeBackground}
	}
	return []string{authorizationModeHeadless, authorizationModeBackground}
}

func authorizationChallengeStage(stage string) bool {
	switch stage {
	case "proxy_challenge", "login_security", "security", "challenge":
		return true
	}
	return false
}

// Chrome 132 dropped the old headless shell: --headless=new is required only on
// older builds, and newer builds already default to the new implementation.
func authorizationBrowserHeadlessArg(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err == nil {
		// The banner carries extra words ("Chromium 136.0.7103.113 Alpine Linux"),
		// so take the first numeric token instead of the last one.
		for _, field := range strings.Fields(string(out)) {
			major, _, _ := strings.Cut(field, ".")
			if number, convertErr := strconv.Atoi(major); convertErr == nil && number > 0 {
				if number >= 132 {
					return "--headless"
				}
				return "--headless=new"
			}
		}
	}
	return "--headless=new"
}

func authorizationBrowserLaunchArgs(mode, profile, bridge string) []string {
	args := []string{
		// Software WebGL is not enabled on purpose: on Alpine's Chromium the
		// SwiftShader flags stall startup, and a stalled browser costs the whole
		// authorization. Keep the pixel path off and let the request succeed.
		"--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check",
		"--disable-background-networking", "--disable-sync", "--disable-quic",
		"--disable-blink-features=AutomationControlled",
		"--disable-component-update", "--disable-domain-reliability", "--metrics-recording-only",
		"--mute-audio", "--hide-crash-restore-bubble", "--lang=en-US",
		"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0",
		"--user-data-dir=" + profile, "--proxy-server=" + bridge, "--proxy-bypass-list=<-loopback>",
	}
	if mode == authorizationModeHeadless {
		args = append([]string{authorizationBrowserHeadlessArg(authorizationBrowserPath())}, args...)
	} else {
		args = append(args, "--window-position=-32000,-32000", "--window-size=1366,900")
	}
	if os.Getenv("PK_AUTH_BROWSER_NO_SANDBOX") == "true" {
		args = append(args, "--no-sandbox")
	}
	return append(args, "about:blank")
}

func authorizationPrecheckWindow(mode string) time.Duration {
	if mode == authorizationModeBackground {
		return authorizationPrecheckBackground
	}
	return authorizationPrecheckHeadless
}

func authorizationChallengeWindow(mode string) time.Duration {
	if mode == authorizationModeBackground {
		return authorizationChallengeHeadful
	}
	return authorizationChallengeHeadless
}

// Cloudflare scores the whole browser, not one request. A headless Chromium is
// challenged and cannot clear an interactive Turnstile, so the flow keeps the
// reference Kilo login order: try the headless identity first, then retry the
// same account and the same proxy in a real Chrome window parked offscreen.
// Profiles, cookie jars and exits are never shared between identities.
func runAuthorizationBrowser(ctx context.Context, proxyURL, authURL string, secret *AuthorizationSecret, progress func(string, string) error) (string, error) {
	budgetCtx, stopBudget := context.WithTimeout(ctx, authorizationBrowserBudget)
	defer stopBudget()
	modes := authorizationBrowserModes()
	var last *authorizationBrowserError
	for index, mode := range modes {
		if index > 0 {
			if err := progress("browser_retry", "无头会话未通过 Cloudflare 验证，正在改用独立后台 Chrome 会话（同一代理）"); err != nil {
				return "", err
			}
			if err := authorizationPause(budgetCtx, time.Second); err != nil {
				return "", err
			}
		}
		value, err, escalate := runAuthorizationBrowserMode(budgetCtx, mode, proxyURL, authURL, secret, progress)
		if err == nil {
			return value, nil
		}
		if ctx.Err() != nil {
			return value, err
		}
		if budgetCtx.Err() != nil {
			return "", authorizationBrowserFailure("login_timeout", "授权流程超过总时限（含 Cloudflare 验证等待），已停止；请检查授权代理出口或稍后重试")
		}
		var failure *authorizationBrowserError
		if !errors.As(err, &failure) {
			return value, err
		}
		last = failure
		if !escalate {
			return value, err
		}
	}
	if last == nil {
		return "", authorizationBrowserFailure("browser_failed", "授权浏览器未启动，未提交账号密码")
	}
	final := *last
	if authorizationChallengeStage(final.stage) {
		final.message += "（无头与后台 Chrome 会话均未通过 Cloudflare 验证；可用 PK_AUTH_BROWSER_MODE=background 固定后台会话）"
		final.needsInput = true
	}
	return "", &final
}

// One browser identity: proxy-level failures are retried within the identity,
// while a Cloudflare block escalates to the next identity instead of resubmitting.
func runAuthorizationBrowserMode(ctx context.Context, mode, proxyURL, authURL string, secret *AuthorizationSecret, progress func(string, string) error) (string, error, bool) {
	for attempt := 1; attempt <= authorizationProxyAttempts; attempt++ {
		value, err := runAuthorizationBrowserAttempt(ctx, mode, proxyURL, authURL, secret, progress)
		if err == nil || ctx.Err() != nil {
			return value, err, false
		}
		var failure *authorizationBrowserError
		if !errors.As(err, &failure) {
			return value, err, false
		}
		// Never replay a submitted password or a cleared challenge, and never
		// switch the exit while a credential is in flight.
		if failure.noReplay {
			return value, err, false
		}
		if authorizationChallengeStage(failure.stage) {
			return value, err, true
		}
		switch failure.stage {
		case "proxy_navigation_timeout", "proxy_network_failed", "proxy_http_failed", "proxy_precheck_timeout":
		default:
			return value, err, false
		}
		if attempt == authorizationProxyAttempts {
			final := *failure
			final.message += "（已尝试3次独立连接）"
			return value, &final, false
		}
		if err = progress("proxy_retry", "代理预检未通过，正在使用同一代理重试（"+strconv.Itoa(attempt+1)+"/3）"); err != nil {
			return "", err, false
		}
		if err = authorizationPause(ctx, time.Second); err != nil {
			return "", err, false
		}
	}
	return "", authorizationBrowserFailure("proxy_precheck_failed", "代理预检未通过，未提交账号密码"), false
}

func runAuthorizationBrowserAttempt(ctx context.Context, mode, proxyURL, authURL string, secret *AuthorizationSecret, progress func(string, string) error) (value string, err error) {
	path := authorizationBrowserPath()
	if path == "" {
		return "", authorizationBrowserFailure("browser_missing", "未找到外置授权浏览器，请在应用运行环境安装 Chromium，或设置 PK_AUTH_BROWSER_PATH；Docker 部署可执行 deploy/install-browser.sh")
	}
	if proxyURL == "" {
		return "", authorizationBrowserFailure("proxy_missing", "尚未配置授权代理")
	}
	authorizationTrace(secret, "attempt start mode=%s browser=%s proxy=%s", mode, path, authorizationProxyEndpoint(proxyURL))
	displayEnv, stopDisplay, displayErr := authorizationBrowserDisplayEnv(mode)
	if displayErr != nil {
		return "", authorizationBrowserFailure("browser_display_missing", "后台 Chrome 会话需要图形环境（Xvfb 或 DISPLAY），未提交账号密码")
	}
	defer stopDisplay()
	bridge, closeBridge, err := authorizationBrowserProxy(ctx, proxyURL)
	if err != nil {
		return "", authorizationBrowserFailure("proxy_failed", "无法创建授权代理连接")
	}
	defer closeBridge()
	profile, err := os.MkdirTemp("", "sub2api-auth-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(profile) }()
	browserCtx, stop := context.WithCancel(ctx)
	defer stop()
	args := authorizationBrowserLaunchArgs(mode, profile, bridge)
	cmd := exec.CommandContext(browserCtx, path, args...)
	cmd.Env = append(os.Environ(), displayEnv...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return "", authorizationBrowserFailure("browser_failed", "授权浏览器启动失败，请检查运行依赖")
	}
	defer func() { stop(); _ = cmd.Wait() }()
	var port string
	for i := 0; i < 300; i++ {
		data, e := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if e == nil {
			port = strings.Split(string(data), "\n")[0]
			break
		}
		if authorizationPause(ctx, 100*time.Millisecond) != nil {
			return "", ctx.Err()
		}
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", authorizationBrowserFailure("browser_failed", "浏览器未就绪，请检查 Chromium 沙箱与系统依赖")
	}
	local := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer local.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://127.0.0.1:"+port+"/json/new?about:blank", nil)
	if err != nil {
		return "", err
	}
	response, err := local.Do(req)
	if err != nil {
		return "", authorizationBrowserFailure("browser_failed", "浏览器控制连接失败")
	}
	var target struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&target)
	_ = response.Body.Close()
	if err != nil {
		return "", err
	}
	socketURL, err := url.Parse(target.WebSocketDebuggerURL)
	if err != nil || socketURL.Scheme != "ws" || socketURL.Host != "127.0.0.1:"+port {
		return "", authorizationBrowserFailure("browser_failed", "浏览器控制地址无效")
	}
	cdp, err := newAuthorizationCDP(browserCtx, target.WebSocketDebuggerURL)
	if err != nil {
		return "", err
	}
	defer func() { _ = cdp.conn.Close() }()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = cdp.call(closeCtx, "Browser.close", map[string]any{}, nil)
	}()
	callback := make(chan string, 1)
	accountBanned := make(chan struct{}, 1)
	loginFailure := make(chan error, 1)
	var precheckStatus atomic.Int64
	var precheckChallenge atomic.Bool
	var precheckNetworkError atomic.Value
	var loginChallenge atomic.Bool
	var secretSubmitted atomic.Bool
	// A failure that happens after the password was submitted is never replayed
	// in another browser identity, against another exit or in a second round.
	defer func() {
		if err == nil {
			return
		}
		var failure *authorizationBrowserError
		if errors.As(err, &failure) {
			if secretSubmitted.Load() {
				failure.noReplay = true
			}
			authorizationTrace(secret, "attempt failed mode=%s stage=%s noReplay=%v message=%s", mode, failure.stage, failure.noReplay, failure.message)
			return
		}
		authorizationTrace(secret, "attempt failed mode=%s error=%v", mode, err)
	}()
	var frameTree struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err = cdp.call(ctx, "Page.getFrameTree", map[string]any{}, &frameTree); err != nil {
		return "", err
	}
	rootFrame := frameTree.FrameTree.Frame.ID
	var callbackURL *url.URL
	var expectedState string
	if authURL != "" {
		u, e := url.Parse(authURL)
		if e != nil || u.Scheme != "https" || u.Host != "auth.openai.com" {
			return "", errors.New("invalid authorization URL")
		}
		callbackURL, err = url.Parse(u.Query().Get("redirect_uri"))
		if err != nil {
			return "", err
		}
		expectedState = u.Query().Get("state")
	}
	go func() {
		var documentRequest string
		type loginResponse struct {
			status    int
			challenge bool
			url       string
		}
		loginResponses := make(map[string]loginResponse)
		for {
			select {
			case <-browserCtx.Done():
				return
			case <-cdp.done:
				return
			case event := <-cdp.events:
				if event.Method == "Network.loadingFinished" {
					var v struct {
						RequestID string `json:"requestId"`
					}
					if json.Unmarshal(event.Params, &v) == nil {
						if response, ok := loginResponses[v.RequestID]; ok {
							delete(loginResponses, v.RequestID)
							go func(id string, response loginResponse) {
								readCtx, done := context.WithTimeout(browserCtx, 5*time.Second)
								defer done()
								var body struct {
									Body          string `json:"body"`
									Base64Encoded bool   `json:"base64Encoded"`
								}
								_ = cdp.call(readCtx, "Network.getResponseBody", map[string]any{"requestId": id}, &body)
								if body.Base64Encoded {
									decoded, e := base64.StdEncoding.DecodeString(body.Body)
									if e == nil {
										body.Body = string(decoded)
									} else {
										body.Body = ""
									}
								}
								authorizationTrace(secret, "login response status=%d url=%s body=%s", response.status, response.url, body.Body)
								endpoint := ""
								if parsed, parseErr := url.Parse(response.url); parseErr == nil {
									endpoint = strings.TrimPrefix(parsed.Path, "/api/accounts/")
								}
								if failure := authorizationLoginResponse(response.status, body.Body, response.challenge, endpoint); failure != nil && !response.challenge {
									select {
									case loginFailure <- failure:
									default:
									}
								}
							}(v.RequestID, response)
						}
					}
				}
				if event.Method == "Network.requestWillBeSent" {
					var v struct {
						RequestID string `json:"requestId"`
						FrameID   string `json:"frameId"`
						Type      string `json:"type"`
					}
					if json.Unmarshal(event.Params, &v) == nil && v.Type == "Document" && v.FrameID == rootFrame {
						documentRequest = v.RequestID
					}
				}
				if event.Method == "Network.loadingFailed" {
					var v struct {
						RequestID string `json:"requestId"`
						ErrorText string `json:"errorText"`
					}
					if json.Unmarshal(event.Params, &v) == nil {
						delete(loginResponses, v.RequestID)
						if documentRequest != "" && v.RequestID == documentRequest && v.ErrorText != "" {
							precheckNetworkError.Store(v.ErrorText)
						}
					}
				}
				if event.Method == "Network.responseReceived" {
					var v struct {
						RequestID string `json:"requestId"`
						FrameID   string `json:"frameId"`
						Type      string `json:"type"`
						Response  struct {
							URL     string            `json:"url"`
							Status  int               `json:"status"`
							Headers map[string]string `json:"headers"`
						} `json:"response"`
					}
					if json.Unmarshal(event.Params, &v) == nil {
						u, e := url.Parse(v.Response.URL)
						if e == nil && u.Host == "auth.openai.com" && strings.HasPrefix(u.Path, "/api/accounts/") && v.Response.Status >= 400 {
							challenged := false
							for key, value := range v.Response.Headers {
								if strings.EqualFold(key, "cf-mitigated") && strings.EqualFold(value, "challenge") {
									challenged = true
								}
							}
							loginResponses[v.RequestID] = loginResponse{status: v.Response.Status, challenge: challenged, url: v.Response.URL}
							if challenged {
								// Cloudflare answered the account API with a managed
								// challenge. A real browser renders and clears it, so this
								// is neither a credential failure nor a ban: record it and
								// let the login loop drive the challenge itself.
								loginChallenge.Store(true)
							}
						}
						if e == nil && v.Type == "Document" && v.FrameID == rootFrame && (u.Host == "chatgpt.com" || u.Host == "www.chatgpt.com") {
							precheckStatus.Store(int64(v.Response.Status))
							precheckChallenge.Store(false)
							for key, value := range v.Response.Headers {
								if strings.EqualFold(key, "cf-mitigated") && strings.EqualFold(strings.TrimSpace(value), "challenge") {
									precheckChallenge.Store(true)
								}
							}
						}
					}
				}
				if event.Method != "Fetch.requestPaused" {
					continue
				}
				var v struct {
					RequestID          string `json:"requestId"`
					ResponseStatusCode int    `json:"responseStatusCode"`
					Request            struct {
						URL string `json:"url"`
					} `json:"request"`
				}
				if json.Unmarshal(event.Params, &v) != nil {
					continue
				}
				u, e := url.Parse(v.Request.URL)

				method := "Fetch.failRequest"
				params := map[string]any{"requestId": v.RequestID, "errorReason": "BlockedByClient"}
				validCallback := ""
				if e == nil && callbackURL != nil && u.Scheme == callbackURL.Scheme && u.Host == callbackURL.Host && u.Path == callbackURL.Path {
					q := u.Query()
					if len(q["state"]) == 1 && q.Get("state") == expectedState && (authorizationBanText(q.Get("error")) || authorizationBanText(q.Get("error_description"))) {
						select {
						case accountBanned <- struct{}{}:
						default:
						}
					}
					if u.User == nil && u.Fragment == "" && len(q["state"]) == 1 && q.Get("state") == expectedState && len(q["code"]) == 1 && q.Get("code") != "" && !q.Has("error") {
						validCallback = u.String()
					}
					method = "Fetch.fulfillRequest"
					params = map[string]any{"requestId": v.RequestID, "responseCode": 200, "body": base64.StdEncoding.EncodeToString([]byte("Authorization received. You may close this page."))}
				} else if e == nil && u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") && authorizationBrowserHost(u.Hostname()) {
					method = "Fetch.continueRequest"
					params = map[string]any{"requestId": v.RequestID}
				}
				// Continue requests asynchronously so an asset burst cannot fill the
				// event channel while the reader is trying to deliver a command reply.
				go func(method string, params map[string]any) {
					commandCtx, cancel := context.WithTimeout(browserCtx, 10*time.Second)
					defer cancel()
					_ = cdp.call(commandCtx, method, params, nil)
				}(method, params)
				if validCallback != "" {
					select {
					case callback <- validCallback:
					default:
					}
				}
			}
		}
	}()
	for _, method := range []string{"Page.enable", "Network.enable"} {
		if err = cdp.call(ctx, method, map[string]any{}, nil); err != nil {
			return "", err
		}
	}
	if err = cdp.call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": authorizationBrowserStealthScript}, nil); err != nil {
		return "", err
	}
	if err = authorizationBrowserIdentity(ctx, cdp); err != nil {
		return "", err
	}
	if err = cdp.call(ctx, "Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}}}, nil); err != nil {
		return "", err
	}
	if err = progress("proxy_check", "正在通过授权代理 GET 检查 chatgpt.com"); err != nil {
		return "", err
	}
	var navigation struct {
		ErrorText string `json:"errorText"`
	}
	if err = cdp.call(ctx, "Page.navigate", map[string]any{"url": "https://chatgpt.com/"}, &navigation); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", authorizationBrowserFailure("proxy_navigation_timeout", "代理首页导航等待超时，未提交账号密码")
		}
		return "", authorizationBrowserFailure("proxy_failed", "授权代理无法访问 chatgpt.com")
	}
	if navigation.ErrorText != "" {
		return "", authorizationPrecheckNetworkFailure(navigation.ErrorText)
	}
	precheckDeadline := time.Now().Add(authorizationPrecheckWindow(mode))
	passed := false
	for time.Now().Before(precheckDeadline) {
		if err = progress("", ""); err != nil {
			return "", err
		}
		var result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		_ = cdp.call(ctx, "Runtime.evaluate", map[string]any{"expression": authorizationPrecheckScript, "returnByValue": true}, &result)
		code := precheckStatus.Load()
		// A challenge page is not an exit failure: the browser is given the same
		// chance a human visitor gets, then the precheck reads the real document.
		if precheckChallenge.Load() || result.Result.Value == "challenge" {
			if !authorizationPassChallenge(ctx, cdp, mode) {
				return "", authorizationBrowserFailure("proxy_challenge", "chatgpt.com 返回验证页（"+authorizationPrecheckHTTPState(code)+"）；当前浏览器身份未通过 Cloudflare 验证，未提交账号密码")
			}
			precheckChallenge.Store(false)
			if err = cdp.call(ctx, "Page.navigate", map[string]any{"url": "https://chatgpt.com/"}, nil); err != nil {
				return "", authorizationBrowserFailure("proxy_failed", "授权代理无法访问 chatgpt.com")
			}
			continue
		}
		if code >= 400 {
			return "", authorizationBrowserFailure("proxy_http_failed", "chatgpt.com 首页返回 HTTP "+strconv.FormatInt(code, 10)+"，未提交账号密码")
		}
		if reason, ok := precheckNetworkError.Load().(string); ok && reason != "" {
			return "", authorizationPrecheckNetworkFailure(reason)
		}
		if result.Result.Value == "html" && code >= 200 && code < 300 {
			passed = true
			break
		}
		if err = authorizationPause(ctx, time.Second); err != nil {
			return "", err
		}
	}
	if !passed {
		return "", authorizationBrowserFailure("proxy_precheck_timeout", "chatgpt.com 首页预检超时，未收到可用的正常页面（"+authorizationPrecheckHTTPState(precheckStatus.Load())+"），未提交账号密码")
	}
	if authURL == "" {
		return "", nil
	}
	if err = progress("login", "代理预检通过，正在打开本项目生成的授权链接"); err != nil {
		return "", err
	}
	if err = cdp.call(ctx, "Page.navigate", map[string]any{"url": authURL}, nil); err != nil {
		return "", authorizationBrowserFailure("login_failed", "无法打开授权页面")
	}
	loginDeadline := time.Now().Add(420 * time.Second)
	challengeRecoveries := 0
	for time.Now().Before(loginDeadline) {
		if err = progress("", ""); err != nil {
			return "", err
		}
		select {
		case failure := <-loginFailure:
			return "", failure
		case <-accountBanned:
			return "", authorizationBannedFailure()
		case value := <-callback:
			return value, nil
		default:
		}
		if err = ctx.Err(); err != nil {
			return "", err
		}
		otp := ""
		if secret.TwoFA != "" {
			otp, _ = totp.GenerateCode(secret.TwoFA, time.Now())
		}
		data, _ := json.Marshal(map[string]string{"email": secret.Email, "password": secret.Password, "otp": otp})
		var result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		expression := "(" + authorizationLoginScript + ")(" + string(data) + ")"
		if err = cdp.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, &result); err == nil {
			stage := result.Result.Value
			if stage != "" && stage != "waiting" {
				authorizationTrace(secret, "login stage=%s", stage)
			}
			if stage == "login_error" && loginChallenge.Load() {
				// Cloudflare rejected the account API call and the app rendered its
				// own error page. That is a security step, not a credential error.
				stage = "challenge"
			}
			switch stage {
			case "account_banned":
				return "", authorizationBannedFailure()
			case "login_error":
				// Give a pending response-body read a bounded chance to identify the
				// actual failure before falling back to the generic error page.
				wait := time.NewTimer(2 * time.Second)
				select {
				case failure := <-loginFailure:
					wait.Stop()
					return "", failure
				case <-ctx.Done():
					wait.Stop()
					return "", ctx.Err()
				case <-wait.C:
				}
				return "", &authorizationBrowserError{stage: stage, message: "官方登录页面已进入错误终态，未取得授权回调；请查看登录接口错误", needsInput: true}
			case "invalid_password":
				return "", &authorizationBrowserError{stage: stage, message: "登录页面提示密码未被接受，请检查并补全密码后重试", needsInput: true}
			case "invalid_twofa":
				return "", &authorizationBrowserError{stage: stage, message: "登录页面提示2FA验证码未被接受，请检查密钥及系统时间", needsInput: true}
			case "needs_input":
				return "", &authorizationBrowserError{stage: stage, message: "官方登录要求邮箱或短信验证码，本流程不读取邮箱；请先在官方页面完成该验证后再重试", needsInput: true}
			case "stalled":
				return "", &authorizationBrowserError{stage: stage, message: "登录页重复提交后仍未前进，可能被官方安全策略拦截或页面结构已变化；请人工确认该账号当前的登录状态", needsInput: true}
			case "invalid_credentials":
				return "", &authorizationBrowserError{stage: stage, message: "登录页提示邮箱或密码不被接受，请检查并补全凭据后重试；未确认账号封禁", needsInput: true}
			case "security", "challenge":
				// The challenge document replaced the login page. Clear it in place
				// with a real mouse click on the Turnstile checkbox, then restart the
				// same authorization URL on the cleared exit.
				if challengeRecoveries >= 2 {
					return "", &authorizationBrowserError{stage: "login_security", message: "登录页仍停留在 Cloudflare 验证，未进入密码或2FA步骤；请检查授权代理出口", needsInput: true}
				}
				challengeRecoveries++
				if err = progress("challenge", "正在完成 Cloudflare 验证"); err != nil {
					return "", err
				}
				if !authorizationPassChallenge(ctx, cdp, mode) {
					return "", &authorizationBrowserError{stage: "login_security", message: "登录页 Cloudflare 验证未在限定时间内完成，未取得授权回调", needsInput: true}
				}
				loginChallenge.Store(false)
				if err = cdp.call(ctx, "Page.navigate", map[string]any{"url": authURL}, nil); err != nil {
					return "", authorizationBrowserFailure("login_failed", "无法打开授权页面")
				}
				continue
			case "email", "password", "mfa", "consent":
				if stage == "password" || stage == "mfa" || stage == "consent" {
					secretSubmitted.Store(true)
				}
				messages := map[string]string{"email": "正在提交账号邮箱", "password": "正在提交账号密码", "mfa": "正在提交2FA验证码", "consent": "正在确认官方授权"}
				if err = progress(stage, messages[stage]); err != nil {
					return "", err
				}
			}
		}
		if err = authorizationPause(ctx, time.Second); err != nil {
			return "", err
		}
	}
	return "", authorizationBrowserFailure("login_timeout", "授权超时，未获得完整回调；请检查账号或官方验证要求")
}

const authorizationLoginScript = `(data)=>{
 if(location.origin!=='https://auth.openai.com'&&location.origin!=='https://auth0.openai.com')return 'waiting';
 const body=(document.body?.innerText||'').toLowerCase();
 if(/oops,? an error occurred|something went wrong/.test(document.title.toLowerCase())&&!document.querySelector('input[type=email],input[type=password]'))return 'login_error';
  if(/deleted or deactivated|\b(?:account|user)[_ -](?:deactivated|deleted|disabled|banned|suspended)\b|\b(?:this |your )?account (?:has been |was |is )(?:deleted|deactivated|disabled|banned|suspended)\b|(?:账号|账户)(?:已被|已经|已)(?:删除|停用|封禁|封号|禁用)/i.test(body))return 'account_banned';
 if(location.pathname==='/error'){try{const encoded=new URL(location.href).searchParams.get('payload')||'';if(encoded.length<16384){const payload=JSON.parse(atob(encoded.replace(/-/g,'+').replace(/_/g,'/')));if(/^(?:account|user)_(?:deactivated|deleted|disabled|banned|suspended)$/.test(payload.errorCode||payload.error?.code||''))return 'account_banned';}}catch{}}
 if(/incorrect password|wrong password|invalid password|密码错误/.test(body))return 'invalid_password';
  if(/invalid code|incorrect code|expired code|验证码错误/.test(body))return 'invalid_twofa';
 if(/invalid credentials|invalid username or password|invalid email or password|email is not valid/.test(body))return 'invalid_credentials';
 if(Array.from(document.querySelectorAll('iframe[src*="challenges.cloudflare.com"]')).some(f=>{const b=f.getBoundingClientRect();return b.width>80&&b.height>30;}))return 'challenge';
 if(/just a moment|checking your browser|verify.{0,20}human|complete.{0,20}captcha|security verification|人机验证/.test(body))return 'challenge';
 const visible=e=>e&&!e.disabled&&e.getClientRects().length>0;
 const find=s=>Array.from(document.querySelectorAll(s)).find(visible);
 const fill=(e,v)=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,v);e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));};
 const submit=e=>{const form=e.closest('form');const b=Array.from((form||document).querySelectorAll('button')).find(x=>visible(x)&&(x.type==='submit'||/^(continue|next|sign in|log in|verify|继续|下一步|登录|验证)$/i.test(x.innerText.trim())));if(b)b.click();else if(form)form.requestSubmit();else return false;return true;};
 const email=find('input[type=email],input[name=username],input[autocomplete=username]');
 const password=find('input[type=password]');
 const otp=find('input[autocomplete=one-time-code],input[name=code],input[name=otp],input[name=totp],input[name=verification_code],input[maxlength="1"][inputmode=numeric]');
 const state=window.__pkAuthorizationActions||(window.__pkAuthorizationActions={});
 const act=(key,fn)=>{const old=state[key];if(old&&Date.now()-old.at<8000)return 'waiting';if(old&&old.n>=3)return 'stalled';state[key]={at:Date.now(),n:(old?.n||0)+1};fn();return key;};
 if(password)return act('password',()=>{if(email)fill(email,data.email);fill(password,data.password);submit(password);});
  if(otp){if(/check your (?:email|inbox)|code.{0,30}(?:sent to|email|sms|text message)|(?:sent|send).{0,30}(?:email|phone)|邮箱验证码|短信验证码/.test(body)||!data.otp)return 'needs_input';return act('mfa',()=>{const cells=Array.from(document.querySelectorAll('input[autocomplete=one-time-code],input[maxlength="1"]')).filter(visible);if(cells.length===6&&cells.every(e=>e.maxLength===1))cells.forEach((e,i)=>fill(e,data.otp[i]));else fill(otp,data.otp);submit(otp);});}
 if(email)return act('email',()=>{fill(email,data.email);submit(email);});
 const choice=Array.from(document.querySelectorAll('button,a')).find(e=>visible(e)&&/use (?:a |your )?password|(?:log in|continue) with (?:a |your )?password|使用密码|密码登录/i.test(e.innerText));if(choice)return act('password_choice',()=>choice.click());
 const consent=Array.from(document.querySelectorAll('button')).find(e=>visible(e)&&/^(allow|authorize|continue|accept|continue to codex|允许|授权|继续|同意)[.!。！\s]*$/i.test(e.innerText.trim()));if(consent)return act('consent',()=>consent.click());
 return 'waiting';
}`
