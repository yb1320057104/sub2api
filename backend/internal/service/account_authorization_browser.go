package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/browserruntime"

	"github.com/gorilla/websocket"
	"github.com/pquerna/otp/totp"
	xproxy "golang.org/x/net/proxy"
)

type authorizationBrowserError struct {
	stage, message string
	needsInput     bool
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

func authorizationBrowserPath() string {
	// Preserve explicit custom paths; the historical image default should not
	// override a newer browser delivered with a web-installed executable.
	configured := os.Getenv("PK_AUTH_BROWSER_PATH")
	if configured == "" || configured == "/usr/bin/chromium" {
		if path := browserruntime.Path(); path != "" {
			return path
		}
	}
	if path := configured; path != "" {
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
			} else if m.Method == "Network.responseReceived" || m.Method == "Fetch.requestPaused" {
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

func runAuthorizationBrowser(ctx context.Context, proxyURL, authURL string, secret *AuthorizationSecret, progress func(string, string) error) (string, error) {
	path := authorizationBrowserPath()
	if path == "" {
		return "", authorizationBrowserFailure("browser_missing", "授权浏览器未就绪，请使用包含浏览器的正式更新包并重启；检查启动日志、数据目录权限和磁盘空间")
	}
	if proxyURL == "" {
		return "", authorizationBrowserFailure("proxy_missing", "尚未配置授权代理")
	}
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
	args := []string{"--headless=new", "--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-sync", "--disable-quic", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir=" + profile, "--proxy-server=" + bridge, "--proxy-bypass-list=<-loopback>", "about:blank"}
	if os.Getenv("PK_AUTH_BROWSER_NO_SANDBOX") == "true" {
		args = append(args, "--no-sandbox")
	}
	cmd := exec.CommandContext(browserCtx, path, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return "", authorizationBrowserFailure("browser_failed", "授权浏览器启动失败，请检查运行依赖")
	}
	defer func() { stop(); _ = cmd.Wait() }()
	var port string
	for i := 0; i < 100; i++ {
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
	var precheckStatus atomic.Int64
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
		for {
			select {
			case <-browserCtx.Done():
				return
			case <-cdp.done:
				return
			case event := <-cdp.events:
				if event.Method == "Network.responseReceived" {
					var v struct {
						FrameID  string `json:"frameId"`
						Type     string `json:"type"`
						Response struct {
							URL    string `json:"url"`
							Status int    `json:"status"`
						} `json:"response"`
					}
					if json.Unmarshal(event.Params, &v) == nil && v.Type == "Document" && v.FrameID == rootFrame {
						u, e := url.Parse(v.Response.URL)
						if e == nil && (u.Host == "chatgpt.com" || u.Host == "www.chatgpt.com") {
							precheckStatus.Store(int64(v.Response.Status))
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
				if v.ResponseStatusCode > 0 {
					if e == nil && u.Scheme == "https" && u.Host == "auth.openai.com" && strings.HasPrefix(u.Path, "/api/accounts/") {
						go func(requestID string) {
							commandCtx, cancel := context.WithTimeout(browserCtx, 10*time.Second)
							defer cancel()
							var body struct {
								Body          string `json:"body"`
								Base64Encoded bool   `json:"base64Encoded"`
							}
							if cdp.call(commandCtx, "Fetch.getResponseBody", map[string]any{"requestId": requestID}, &body) == nil {
								if body.Base64Encoded {
									if decoded, decodeErr := base64.StdEncoding.DecodeString(body.Body); decodeErr == nil {
										body.Body = string(decoded)
									} else {
										body.Body = ""
									}
								}
								if authorizationBanJSON(body.Body) {
									select {
									case accountBanned <- struct{}{}:
									default:
									}
								}
							}
							body.Body = ""
							cancel()
							// A slow response-body read must not consume the budget for
							// resuming the paused response and stall the login page.
							continueCtx, continueCancel := context.WithTimeout(browserCtx, 10*time.Second)
							defer continueCancel()
							_ = cdp.call(continueCtx, "Fetch.continueResponse", map[string]any{"requestId": requestID}, nil)
						}(v.RequestID)
					}
					continue
				}
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
	if err = cdp.call(ctx, "Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}, {"urlPattern": "https://auth.openai.com/api/accounts/*", "requestStage": "Response"}}}, nil); err != nil {
		return "", err
	}
	if err = progress("proxy_check", "正在通过授权代理 GET 检查 chatgpt.com"); err != nil {
		return "", err
	}
	if err = cdp.call(ctx, "Page.navigate", map[string]any{"url": "https://chatgpt.com/"}, nil); err != nil {
		return "", authorizationBrowserFailure("proxy_failed", "授权代理无法访问 chatgpt.com")
	}
	passed := false
	for i := 0; i < 45; i++ {
		if err = progress("", ""); err != nil {
			return "", err
		}
		var result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		_ = cdp.call(ctx, "Runtime.evaluate", map[string]any{"expression": `(()=>{if(!document.body||document.readyState==='loading')return 'loading';const t=(document.title+' '+document.body.innerText).toLowerCase();if(/just a moment|verify.{0,20}human|security verification|enable javascript and cookies|验证您是真人|人机验证/.test(t))return 'challenge';return document.contentType==='text/html'&&document.documentElement?'html':'invalid'})()`, "returnByValue": true}, &result)
		code := precheckStatus.Load()
		if result.Result.Value == "html" && code >= 200 && code < 300 {
			passed = true
			break
		}
		if err = authorizationPause(ctx, time.Second); err != nil {
			return "", err
		}
	}
	if !passed {
		return "", authorizationBrowserFailure("proxy_precheck_failed", "授权出口未通过 chatgpt.com GET 预检（网络、HTTP错误或人工验证），未提交账号密码")
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
	for i := 0; i < 240; i++ {
		if err = progress("", ""); err != nil {
			return "", err
		}
		select {
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
			switch stage {
			case "account_banned":
				return "", authorizationBannedFailure()
			case "needs_input", "invalid_credentials", "security":
				return "", &authorizationBrowserError{stage: stage, message: "官方登录需要人工验证，或密码/2FA未被接受；请检查历史凭据后重试", needsInput: true}
			case "email", "password", "mfa", "consent":
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
 if(/deleted or deactivated|\b(?:account|user)[_ -](?:deactivated|deleted|disabled|banned|suspended)\b|\b(?:this |your )?account (?:has been |was |is )(?:deleted|deactivated|disabled|banned|suspended)\b|(?:账号|账户)(?:已被|已经|已)(?:删除|停用|封禁|封号|禁用)/i.test(body))return 'account_banned';
 if(location.pathname==='/error'){try{const encoded=new URL(location.href).searchParams.get('payload')||'';if(encoded.length<16384){const payload=JSON.parse(atob(encoded.replace(/-/g,'+').replace(/_/g,'/')));if(/^(?:account|user)_(?:deactivated|deleted|disabled|banned|suspended)$/.test(payload.errorCode||payload.error?.code||''))return 'account_banned';}}catch{}}
 if(/incorrect password|wrong password|invalid credentials|invalid code|incorrect code|密码错误|验证码错误/.test(body))return 'invalid_credentials';
 if(/verify.{0,20}human|complete.{0,20}captcha|人机验证/.test(body))return 'security';
 const visible=e=>e&&!e.disabled&&e.getClientRects().length>0;
 const find=s=>Array.from(document.querySelectorAll(s)).find(visible);
 const fill=(e,v)=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,v);e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));};
 const submit=e=>{const form=e.closest('form');const b=Array.from((form||document).querySelectorAll('button')).find(x=>visible(x)&&(x.type==='submit'||/^(continue|next|sign in|log in|verify|继续|下一步|登录|验证)$/i.test(x.innerText.trim())));if(b)b.click();else if(form)form.requestSubmit();else return false;return true;};
 const email=find('input[type=email],input[name=username],input[autocomplete=username]');
 const password=find('input[type=password]');
 const otp=find('input[autocomplete=one-time-code],input[name=code],input[name=otp],input[name=totp],input[name=verification_code],input[maxlength="1"][inputmode=numeric]');
 const state=window.__pkAuthorizationActions||(window.__pkAuthorizationActions={});
 const act=(key,fn)=>{const old=state[key];if(old&&Date.now()-old.at<8000)return 'waiting';if(old&&old.n>=3)return 'needs_input';state[key]={at:Date.now(),n:(old?.n||0)+1};fn();return key;};
 if(password)return act('password',()=>{if(email)fill(email,data.email);fill(password,data.password);submit(password);});
 if(otp){if(/check your (?:email|inbox)|code.{0,30}(?:sent to|email|sms|text message)|(?:sent|send).{0,30}(?:email|phone)|邮箱验证码|短信验证码/.test(body)||!data.otp)return 'needs_input';return act('mfa',()=>{const cells=Array.from(document.querySelectorAll('input[autocomplete=one-time-code],input[maxlength="1"]')).filter(visible);if(cells.length===6&&cells.every(e=>e.maxLength===1))cells.forEach((e,i)=>fill(e,data.otp[i]));else fill(otp,data.otp);submit(otp);});}
 if(email)return act('email',()=>{fill(email,data.email);submit(email);});
 const choice=Array.from(document.querySelectorAll('button,a')).find(e=>visible(e)&&/use (?:a |your )?password|(?:log in|continue) with (?:a |your )?password|使用密码|密码登录/i.test(e.innerText));if(choice)return act('password_choice',()=>choice.click());
 const consent=Array.from(document.querySelectorAll('button')).find(e=>visible(e)&&/^(allow|authorize|continue|accept|continue to codex|允许|授权|继续|同意)[.!。！\s]*$/i.test(e.innerText.trim()));if(consent)return act('consent',()=>consent.click());
 return 'waiting';
}`
