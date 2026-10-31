package service

// Temporary token-exchange probe. Deleted after the run.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/imroc/req/v3"
)

func pkLogin(t *testing.T, proxy, email, password, secret string) (string, string, string) {
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	state := base64.RawURLEncoding.EncodeToString(raw[:16])
	authURL := openai.BuildAuthorizationURL(state, base64.RawURLEncoding.EncodeToString(sum[:]), openai.DefaultRedirectURI)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	callback, err := runAuthorizationBrowser(ctx, proxy, authURL, &AuthorizationSecret{Email: email, Password: password, TwoFA: secret}, func(string, string) error { return nil })
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	parsed, err := url.Parse(callback)
	if err != nil {
		t.Fatal(err)
	}
	return verifier, state, parsed.Query().Get("code")
}

func pkExchange(t *testing.T, label, proxy, verifier, code, ua, originator string, impersonate bool) {
	client := req.C()
	if impersonate {
		client = client.ImpersonateFirefox()
	}
	client.SetProxyURL(proxy).SetTimeout(60 * time.Second)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", openai.ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", openai.DefaultRedirectURI)
	form.Set("code_verifier", verifier)
	resp, err := client.R().
		SetHeader("User-Agent", ua).
		SetHeader("originator", originator).
		SetFormDataFromValues(form).
		Post(openai.TokenURL)
	if err != nil {
		t.Logf("%s: transport error %v", label, err)
		return
	}
	body := resp.String()
	if len(body) > 240 {
		body = body[:240] + "..."
	}
	t.Logf("%s: status=%d body=%s", label, resp.StatusCode, body)
}

func TestPKExchangeProbe(t *testing.T) {
	email := os.Getenv("PK_PROBE_EMAIL")
	password := os.Getenv("PK_PROBE_PASSWORD")
	secret := os.Getenv("PK_PROBE_TOTP")
	proxy := os.Getenv("PK_PROBE_PROXY")
	if email == "" || proxy == "" {
		t.Fatal("missing env")
	}
	ua, originator := CodexCanonicalAuthIdentity()
	t.Logf("ua=%q originator=%q", ua, originator)

	verifier, _, code := pkLogin(t, proxy, email, password, secret)
	pkExchange(t, "PLAIN(no impersonation)", proxy, verifier, code, ua, originator, false)
	pkExchange(t, "PLAIN(retry same code)", proxy, verifier, code, ua, originator, false)

	verifier2, _, code2 := pkLogin(t, proxy, email, password, secret)
	pkExchange(t, "FIREFOX-impersonated", proxy, verifier2, code2, ua, originator, true)
}
