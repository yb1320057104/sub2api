package payment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/binancepay"
)

func makeKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate random key: %v", err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	plaintexts := []string{
		"hello world",
		"short",
		"a longer string with special chars: !@#$%^&*()",
		`{"key":"value","num":42}`,
		"你好世界 unicode test 🎉",
		strings.Repeat("x", 10000),
	}

	for _, pt := range plaintexts {
		encrypted, err := Encrypt(pt, key)
		if err != nil {
			t.Fatalf("Encrypt(%q) error: %v", pt[:min(len(pt), 30)], err)
		}
		decrypted, err := Decrypt(encrypted, key)
		if err != nil {
			t.Fatalf("Decrypt error for plaintext %q: %v", pt[:min(len(pt), 30)], err)
		}
		if decrypted != pt {
			t.Fatalf("round-trip failed: got %q, want %q", decrypted[:min(len(decrypted), 30)], pt[:min(len(pt), 30)])
		}
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	ct1, err := Encrypt("same plaintext", key)
	if err != nil {
		t.Fatalf("first Encrypt error: %v", err)
	}
	ct2, err := Encrypt("same plaintext", key)
	if err != nil {
		t.Fatalf("second Encrypt error: %v", err)
	}
	if ct1 == ct2 {
		t.Fatal("two encryptions of the same plaintext should produce different ciphertexts (random nonce)")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	t.Parallel()
	key1 := makeKey(t)
	key2 := makeKey(t)

	encrypted, err := Encrypt("secret data", key1)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	_, err = Decrypt(encrypted, key2)
	if err == nil {
		t.Fatal("Decrypt with wrong key should fail, but got nil error")
	}
}

func TestEncryptRejectsInvalidKeyLength(t *testing.T) {
	t.Parallel()
	badKeys := [][]byte{
		nil,
		make([]byte, 0),
		make([]byte, 16),
		make([]byte, 31),
		make([]byte, 33),
		make([]byte, 64),
	}
	for _, key := range badKeys {
		_, err := Encrypt("test", key)
		if err == nil {
			t.Fatalf("Encrypt should reject key of length %d", len(key))
		}
	}
}

func TestDecryptRejectsInvalidKeyLength(t *testing.T) {
	t.Parallel()
	badKeys := [][]byte{
		nil,
		make([]byte, 16),
		make([]byte, 33),
	}
	for _, key := range badKeys {
		_, err := Decrypt("dummydata:dummydata:dummydata", key)
		if err == nil {
			t.Fatalf("Decrypt should reject key of length %d", len(key))
		}
	}
}

func TestEncryptEmptyPlaintext(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	encrypted, err := Encrypt("", key)
	if err != nil {
		t.Fatalf("Encrypt empty plaintext error: %v", err)
	}
	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt empty plaintext error: %v", err)
	}
	if decrypted != "" {
		t.Fatalf("expected empty string, got %q", decrypted)
	}
}

func TestEncryptDecryptUnicodeJSON(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	jsonContent := `{"name":"测试用户","email":"test@example.com","balance":100.50}`
	encrypted, err := Encrypt(jsonContent, key)
	if err != nil {
		t.Fatalf("Encrypt JSON error: %v", err)
	}
	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt JSON error: %v", err)
	}
	if decrypted != jsonContent {
		t.Fatalf("JSON round-trip failed: got %q, want %q", decrypted, jsonContent)
	}
}

func TestDecryptInvalidFormat(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	invalidInputs := []string{
		"",
		"nodelimiter",
		"only:two",
		"invalid:base64:!!!",
	}
	for _, input := range invalidInputs {
		_, err := Decrypt(input, key)
		if err == nil {
			t.Fatalf("Decrypt(%q) should fail but got nil error", input)
		}
	}
}

func TestCiphertextFormat(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	encrypted, err := Encrypt("test", key)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	parts := strings.SplitN(encrypted, ":", 3)
	if len(parts) != 3 {
		t.Fatalf("ciphertext should have format iv:authTag:ciphertext, got %d parts", len(parts))
	}
	for i, part := range parts {
		if part == "" {
			t.Fatalf("ciphertext part %d is empty", i)
		}
	}
}

// Keep PK payment protocol regressions in this existing payment test file.
type pkBinanceTransport func(*http.Request) (*http.Response, error)

func (transport pkBinanceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func pkBinanceJSON(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}
}

func pkBinanceTransfer(id, amount, currency, kind string, uid any, at int64) map[string]any {
	return map[string]any{
		"transactionId": id, "amount": amount, "currency": currency, "orderType": kind,
		"transactionTime": at, "receiverInfo": map[string]any{"binanceId": uid},
	}
}

func TestPKBinanceAmountPrecision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		amount string
		units  int64
	}{
		{"0", 0}, {"1", 100000}, {"10.01234", 1001234}, {"10.01234000", 1001234},
		{"0.00001", 1}, {"92233720368547.75807", math.MaxInt64},
	} {
		t.Run(tc.amount, func(t *testing.T) {
			units, err := binancepay.ParseAmount(tc.amount)
			if err != nil || units != tc.units {
				t.Fatalf("ParseAmount(%q) = %d, %v; want %d", tc.amount, units, err, tc.units)
			}
			formatted := binancepay.FormatAmount(units)
			roundTrip, err := binancepay.ParseAmount(formatted)
			if err != nil || roundTrip != units || len(strings.Split(formatted, ".")[1]) != 5 {
				t.Fatalf("five-place round trip failed: %q -> %d, %v", formatted, roundTrip, err)
			}
		})
	}
	for _, amount := range []string{"", "-1", "+1", "1e2", ".1", "1.", " 1", "NaN", "1.000001", "92233720368547.75808", "92233720368548"} {
		if _, err := binancepay.ParseAmount(amount); err == nil {
			t.Errorf("ParseAmount(%q) must reject invalid or lossy amounts", amount)
		}
	}
}

func TestPKBinanceSignedIncomingHistory(t *testing.T) {
	t.Parallel()
	const key, secret, uid = "test-binance-api-key", "test-binance-secret", "9007199254740993"
	serverTime := time.Now().Add(5 * time.Minute).UnixMilli()
	paidAt := serverTime - 10000
	calls := 0
	client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "api.binance.com" {
			t.Fatalf("unexpected destination/method: %s %s", request.Method, request.URL.Host)
		}
		if _, ok := request.Context().Deadline(); !ok {
			t.Fatal("Binance request must have a deadline")
		}
		if request.URL.Path == "/api/v3/time" {
			if request.Header.Get("X-MBX-APIKEY") != "" || request.URL.Query().Get("signature") != "" {
				t.Fatal("public clock request must not carry credentials")
			}
			return pkBinanceJSON(t, 200, map[string]any{"serverTime": serverTime}), nil
		}
		if request.URL.Path != "/sapi/v1/pay/transactions" || request.Header.Get("X-MBX-APIKEY") != key {
			t.Fatal("missing API key or wrong history endpoint")
		}
		query := request.URL.Query()
		signature := query.Get("signature")
		query.Del("signature")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = io.WriteString(mac, query.Encode())
		if signature != hex.EncodeToString(mac.Sum(nil)) || query.Get("recvWindow") != "10000" || query.Get("limit") != "100" {
			t.Fatal("incorrect HMAC signature or query limits")
		}
		timestamp, err := strconv.ParseInt(query.Get("timestamp"), 10, 64)
		if err != nil || timestamp < serverTime-2000 || timestamp > serverTime+2000 {
			t.Fatalf("signed timestamp was not synchronized to Binance: %d", timestamp)
		}
		rows := []any{
			pkBinanceTransfer("c2c", "10.01234000", "USDT", "C2C", json.Number(uid), paidAt),
			pkBinanceTransfer("pay", "20.00001", "USDT", "PAY", uid, paidAt+1),
			pkBinanceTransfer("outgoing", "-10.01234", "USDT", "C2C", uid, paidAt),
			pkBinanceTransfer("precision", "10.012341", "USDT", "C2C", uid, paidAt),
			pkBinanceTransfer("refund", "10.01234", "USDT", "PAY_REFUND", uid, paidAt),
			pkBinanceTransfer("other-uid", "10.01234", "USDT", "PAY", "1234", paidAt),
			pkBinanceTransfer("other-asset", "10.01234", "BTC", "PAY", uid, paidAt),
			pkBinanceTransfer("zero", "0", "USDT", "PAY", uid, paidAt),
			pkBinanceTransfer("old", "10.01234", "USDT", "PAY", uid, serverTime-120000),
		}
		return pkBinanceJSON(t, 200, map[string]any{"code": "000000", "success": true, "data": rows}), nil
	}))
	receipts, err := client.History(context.Background(), key, secret, uid, time.UnixMilli(serverTime-60000), time.UnixMilli(serverTime-1000))
	if err != nil || len(receipts) != 2 || calls != 2 {
		t.Fatalf("incoming history = %#v, %v; calls=%d", receipts, err, calls)
	}
	if receipts[0].TransactionID != "c2c" || receipts[0].AmountUnits != 1001234 || receipts[0].Time.UnixMilli() != paidAt || receipts[1].TransactionID != "pay" || receipts[1].AmountUnits != 2000001 {
		t.Fatalf("receipt precision, identity or provider timestamp changed: %#v", receipts)
	}
}

func TestPKBinanceFullPageSplitsWithoutMissingTransfers(t *testing.T) {
	t.Parallel()
	base := time.Now().Add(-time.Minute).UnixMilli()
	historyCalls := 0
	client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/v3/time" {
			return pkBinanceJSON(t, 200, map[string]any{"serverTime": time.Now().UnixMilli()}), nil
		}
		historyCalls++
		from, _ := strconv.ParseInt(request.URL.Query().Get("startTime"), 10, 64)
		to, _ := strconv.ParseInt(request.URL.Query().Get("endTime"), 10, 64)
		rows := []any{}
		for index := 0; index < 137; index++ {
			at := base + int64(index)
			if at >= from && at <= to && len(rows) < 100 {
				rows = append(rows, pkBinanceTransfer(fmt.Sprintf("transfer-%03d", index), "1.00001", "USDT", "C2C", "42", at))
			}
		}
		return pkBinanceJSON(t, 200, map[string]any{"code": "000000", "success": true, "data": rows}), nil
	}))
	receipts, err := client.History(context.Background(), "test-key", "test-secret", "42", time.UnixMilli(base), time.UnixMilli(base+199))
	if err != nil || len(receipts) != 137 || historyCalls < 3 {
		t.Fatalf("full pages were not fully scanned: receipts=%d calls=%d err=%v", len(receipts), historyCalls, err)
	}
	for index, receipt := range receipts {
		if receipt.TransactionID != fmt.Sprintf("transfer-%03d", index) || receipt.Time.UnixMilli() != base+int64(index) {
			t.Fatalf("missing or duplicated boundary transfer at index %d: %#v", index, receipt)
		}
	}
}

func TestPKBinanceAmbiguousHistoryFailsClosed(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"saturated-timestamp", "conflicting-transaction", "provider-rejected"} {
		t.Run(scenario, func(t *testing.T) {
			base := time.Now().Add(-time.Minute).UnixMilli()
			client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/api/v3/time" {
					return pkBinanceJSON(t, 200, map[string]any{"serverTime": time.Now().UnixMilli()}), nil
				}
				rows := []any{pkBinanceTransfer("same-id", "1.00001", "USDT", "C2C", "42", base)}
				switch scenario {
				case "saturated-timestamp":
					for len(rows) < 100 {
						rows = append(rows, rows[0])
					}
				case "conflicting-transaction":
					rows = append(rows, pkBinanceTransfer("same-id", "1.00002", "USDT", "C2C", "42", base))
				case "provider-rejected":
					return pkBinanceJSON(t, 200, map[string]any{"code": "000001", "success": false, "data": rows}), nil
				}
				return pkBinanceJSON(t, 200, map[string]any{"code": "000000", "success": true, "data": rows}), nil
			}))
			receipts, err := client.History(context.Background(), "test-key", "test-secret", "42", time.UnixMilli(base), time.UnixMilli(base))
			if err == nil || len(receipts) != 0 {
				t.Fatalf("unsafe history must not return settleable receipts: %#v, %v", receipts, err)
			}
		})
	}
}

func TestPKBinanceRateLimitBackoffAndNoRetry(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusTeapot} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Path == "/api/v3/time" {
					return pkBinanceJSON(t, 200, map[string]any{"serverTime": time.Now().UnixMilli()}), nil
				}
				response := pkBinanceJSON(t, status, map[string]string{"message": "must-not-appear-in-error"})
				response.Header.Set("Retry-After", "300")
				return response, nil
			}))
			now := time.Now()
			receipts, err := client.History(context.Background(), "test-key", "test-secret", "42", now.Add(-time.Minute), now)
			var limited *binancepay.RateLimitError
			if !errors.As(err, &limited) || limited.StatusCode != status || limited.RetryAfter < 5*time.Minute || calls != 2 || len(receipts) != 0 {
				t.Fatalf("rate limit must back off without retry: receipts=%v calls=%d err=%v", receipts, calls, err)
			}
			if strings.Contains(err.Error(), "must-not-appear-in-error") {
				t.Fatal("provider response was exposed")
			}
		})
	}
}

func TestPKBinanceRedirectAndTransportErrorsAreSafe(t *testing.T) {
	t.Parallel()
	for _, redirect := range []bool{false, true} {
		t.Run(strconv.FormatBool(redirect), func(t *testing.T) {
			calls := 0
			client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Host != "api.binance.com" {
					t.Fatal("credentials followed a redirect")
				}
				if request.URL.Path == "/api/v3/time" {
					return pkBinanceJSON(t, 200, map[string]any{"serverTime": time.Now().UnixMilli()}), nil
				}
				if !redirect {
					return nil, errors.New("secret-proxy-credential " + request.URL.String())
				}
				response := pkBinanceJSON(t, http.StatusFound, map[string]string{})
				response.Header.Set("Location", "https://untrusted.example/history")
				return response, nil
			}))
			now := time.Now()
			_, err := client.History(context.Background(), "test-key", "test-secret", "42", now.Add(-time.Minute), now)
			if err == nil || calls != 2 || strings.Contains(err.Error(), "signature") || strings.Contains(err.Error(), "secret-proxy-credential") {
				t.Fatalf("redirect/error must be contained and sanitized: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestPKBinanceProbeRequiresReadingAndPayAccess(t *testing.T) {
	t.Parallel()
	for _, reading := range []bool{false, true} {
		t.Run(strconv.FormatBool(reading), func(t *testing.T) {
			historyCalls := 0
			client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
				switch request.URL.Path {
				case "/api/v3/time":
					return pkBinanceJSON(t, 200, map[string]any{"serverTime": time.Now().UnixMilli()}), nil
				case "/sapi/v1/account/apiRestrictions":
					return pkBinanceJSON(t, 200, map[string]bool{"enableReading": reading}), nil
				case "/sapi/v1/pay/transactions":
					historyCalls++
					return pkBinanceJSON(t, 200, map[string]any{"code": "000000", "success": true, "data": []any{}}), nil
				default:
					t.Fatalf("probe must use read-only endpoints, got %s", request.URL.Path)
					return nil, errors.New("unexpected endpoint")
				}
			}))
			err := client.Probe(context.Background(), "test-key", "test-secret")
			if reading && (err != nil || historyCalls != 1) || !reading && (err == nil || historyCalls != 0) {
				t.Fatalf("incorrect permission/access probe: reading=%v historyCalls=%d err=%v", reading, historyCalls, err)
			}
		})
	}
}

func TestPKBinanceBillsKeepAllDirectionsCurrenciesAndExactFields(t *testing.T) {
	t.Parallel()
	base := time.Now().Add(-time.Minute).UnixMilli()
	client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/v3/time" {
			return pkBinanceJSON(t, 200, map[string]any{"serverTime": time.Now().UnixMilli()}), nil
		}
		if request.Method != http.MethodGet || request.URL.Path != "/sapi/v1/pay/transactions" || request.URL.Query().Get("limit") != "100" {
			t.Fatal("bills must use only the read-only Pay history endpoint")
		}
		if request.URL.Query().Get("signature") == "" || request.Header.Get("X-MBX-APIKEY") != "test-key" {
			t.Fatal("bill request must be signed")
		}
		return pkBinanceJSON(t, 200, map[string]any{"success": true, "code": "000000", "data": []any{
			map[string]any{"transactionId": "income", "orderType": "CRYPTO_BOX", "currency": "BTC", "amount": "0.00000001", "transactionTime": base, "signature": "must-hide-signature", "receiverInfo": map[string]any{"binanceId": json.Number("90071992547409931234"), "apiKey": "must-hide-key"}, "walletAssetCost": map[string]any{"1": "0.00000001", "signature": "must-hide-wallet-secret"}},
			map[string]any{"transactionId": "expense", "orderType": "PAY", "currency": "ETH", "amount": "-1.230000000000000001", "transactionTime": base, "receiverInfo": map[string]any{"binanceId": "42"}},
			map[string]any{"transactionId": "zero", "orderType": "C2C", "currency": "USDT", "amount": "0.00000", "transactionTime": base},
		}}), nil
	}))
	page, err := client.Bills(context.Background(), "test-key", "test-secret", "42", time.UnixMilli(base-1), time.UnixMilli(base+1))
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.Limit != 100 || page.PossiblyMore {
		t.Fatalf("unexpected bill page: %#v", page)
	}
	byID := make(map[string]binancepay.Bill)
	for _, bill := range page.Items {
		byID[bill.ID] = bill
	}
	if byID["income"].Amount != "0.00000001" || byID["income"].Currency != "BTC" || byID["income"].Kind != "CRYPTO_BOX" || byID["income"].Direction != "incoming" {
		t.Fatalf("incoming bill changed: %#v", byID["income"])
	}
	if byID["expense"].Direction != "outgoing" || byID["expense"].Amount != "-1.230000000000000001" {
		t.Fatalf("sign or precision lost: %#v", byID["expense"])
	}
	if byID["zero"].Direction != "unknown" {
		t.Fatal("zero is not income or expense")
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"must-hide", "test-key", "test-secret", "signature"} {
		if strings.Contains(string(raw), hidden) {
			t.Fatalf("bills exposed credential field %q", hidden)
		}
	}
	if !strings.Contains(string(raw), "90071992547409931234") || !strings.Contains(string(raw), `"key":"1"`) {
		t.Fatal("large IDs and numbered wallet amounts must retain exact field text")
	}
}

func TestPKBinanceBillsFullPageWarnsWithoutPartialHistoryClaim(t *testing.T) {
	t.Parallel()
	base := time.Now().Add(-time.Minute).UnixMilli()
	calls := 0
	client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/v3/time" {
			return pkBinanceJSON(t, 200, map[string]any{"serverTime": time.Now().UnixMilli()}), nil
		}
		calls++
		rows := make([]any, 100)
		for i := range rows {
			rows[i] = pkBinanceTransfer(strconv.Itoa(i), "1.000000001", "BNB", "PAY", "42", base)
		}
		return pkBinanceJSON(t, 200, map[string]any{"success": true, "code": "000000", "data": rows}), nil
	}))
	page, err := client.Bills(context.Background(), "test-key", "test-secret", "42", time.UnixMilli(base-1), time.UnixMilli(base+1))
	if err != nil {
		t.Fatal(err)
	}
	if !page.PossiblyMore || page.Total != 100 || calls != 1 {
		t.Fatal("full bill page must warn about truncation without claiming complete receipt history")
	}
}

func TestPKBinanceBillsRejectInvalidRangeAndHonorProviderBackoff(t *testing.T) {
	t.Parallel()
	now := time.Now()
	calls := 0
	client := binancepay.NewClientWithTransport(pkBinanceTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path == "/api/v3/time" {
			return pkBinanceJSON(t, 200, map[string]any{"serverTime": time.Now().UnixMilli()}), nil
		}
		return pkBinanceJSON(t, http.StatusTooManyRequests, map[string]any{"message": "hidden-provider-message"}), nil
	}))
	if _, err := client.Bills(context.Background(), "test-key", "test-secret", "42", now.Add(-90*24*time.Hour), now); err == nil || calls != 0 {
		t.Fatal("invalid interval must fail before any API request")
	}
	page, err := client.Bills(context.Background(), "test-key", "test-secret", "42", now.Add(-time.Hour), now)
	var limited *binancepay.RateLimitError
	if !errors.As(err, &limited) || page != nil || calls != 2 {
		t.Fatalf("rate limit must stop bill queries: %v", err)
	}
}

type pkBinanceProxyState struct {
	mu          sync.Mutex
	preflights  int
	connections map[string]string
	requests    map[string][]string
	keys        map[string]map[string]bool
}

// The gateway performs real HTTP CONNECT tunnelling to a local TLS fixture.
// Its certificate is trusted only by the explicitly supplied test transport.
func pkBinanceProxyClient(t *testing.T, base int64) (*binancepay.Client, *pkBinanceProxyState) {
	t.Helper()
	state := &pkBinanceProxyState{connections: make(map[string]string), requests: make(map[string][]string), keys: make(map[string]map[string]bool)}
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.binance.com" || r.Header.Get("X-Binance-Route-Token") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "" {
			t.Error("gateway authentication or route token leaked into Binance TLS request")
		}
		state.mu.Lock()
		token := state.connections[r.RemoteAddr]
		if token == "" {
			t.Error("Binance request did not arrive through a pinned CONNECT route")
		}
		state.requests[token] = append(state.requests[token], r.URL.Path)
		if key := r.Header.Get("X-MBX-APIKEY"); key != "" {
			if state.keys[token] == nil {
				state.keys[token] = make(map[string]bool)
			}
			state.keys[token][key] = true
		}
		state.mu.Unlock()
		var response any
		switch r.URL.Path {
		case "/api/v3/time":
			response = map[string]any{"serverTime": time.Now().UnixMilli()}
		case "/sapi/v1/account/apiRestrictions":
			response = map[string]any{"enableReading": true}
		case "/sapi/v1/pay/transactions":
			from, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
			to, _ := strconv.ParseInt(r.URL.Query().Get("endTime"), 10, 64)
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			rows := []any{}
			for i := 0; i < 137; i++ {
				at := base + int64(i)
				if at >= from && at <= to && len(rows) < limit {
					rows = append(rows, pkBinanceTransfer(strconv.Itoa(i), "1.00001", "USDT", "C2C", "42", at))
				}
			}
			response = map[string]any{"code": "000000", "success": true, "data": rows}
		default:
			t.Error("unexpected Binance endpoint")
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("write local Binance response: %v", err)
		}
	}))
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/_binance/preflight" {
			user, password, ok := r.BasicAuth()
			if !ok || user != "fixture-user" || password != "fixture-password" || r.Header.Get("X-MBX-APIKEY") != "" || r.URL.User != nil || r.URL.RawQuery != "" {
				t.Error("preflight authentication must contain only gateway credentials")
				http.Error(w, "auth", http.StatusUnauthorized)
				return
			}
			state.mu.Lock()
			state.preflights++
			token := fmt.Sprintf("route_%d", state.preflights)
			state.requests[token] = nil
			state.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]string{"route_token": token}); err != nil {
				t.Errorf("write preflight response: %v", err)
			}
			return
		}
		token := r.Header.Get("X-Binance-Route-Token")
		state.mu.Lock()
		_, valid := state.requests[token]
		state.mu.Unlock()
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture-password"))
		if r.Method != http.MethodConnect || r.Host != "api.binance.com:443" || !valid || r.Header.Get("Proxy-Authorization") != wantAuth {
			t.Error("CONNECT must target Binance with gateway authentication and a freshly issued route token")
			http.Error(w, "bad CONNECT", http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp", api.Listener.Addr().String(), 2*time.Second)
		if err != nil {
			t.Errorf("dial local TLS fixture: %v", err)
			http.Error(w, "dial", http.StatusBadGateway)
			return
		}
		defer func() { _ = upstream.Close() }()
		state.mu.Lock()
		state.connections[upstream.LocalAddr().String()] = token
		state.mu.Unlock()
		connection, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack local CONNECT: %v", err)
			return
		}
		defer func() { _ = connection.Close() }()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		finished := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
			close(finished)
		}()
		_, _ = io.Copy(connection, upstream)
		_ = connection.Close()
		<-finished
	}))
	proxy, err := url.Parse(gateway.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy.User = url.UserPassword("fixture-user", "fixture-password")
	transport := api.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = api.Certificate().DNSNames[0]
	// This pre-existing proxy callback must not escape into either client.
	transport.Proxy = func(*http.Request) (*url.URL, error) {
		t.Error("inherited proxy callback must not run")
		return nil, errors.New("unexpected inherited proxy")
	}
	client, err := binancepay.NewClientWithOptions(binancepay.ClientOptions{ProxyURL: proxy.String(), ProxyPreflight: true, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		gateway.Close()
		api.Close()
	})
	return client, state
}

func TestPKBinanceProxyPreflightPinsWholeOperation(t *testing.T) {
	t.Parallel()
	base := time.Now().Add(-time.Minute).UnixMilli()
	client, state := pkBinanceProxyClient(t, base)
	from, to := time.UnixMilli(base), time.UnixMilli(base+199)
	for _, key := range []string{"history-one", "history-two"} {
		receipts, err := client.History(context.Background(), key, "fixture-secret", "42", from, to)
		if err != nil || len(receipts) != 137 {
			t.Fatalf("pinned history must retain all split pages: count=%d err=%v", len(receipts), err)
		}
	}
	if _, err := client.Bills(context.Background(), "bills", "fixture-secret", "42", from, to); err != nil {
		t.Fatal(err)
	}
	if err := client.Probe(context.Background(), "probe", "fixture-secret"); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.preflights != 4 || len(state.requests) != 4 {
		t.Fatalf("one fresh preflight required for each top-level operation: %d", state.preflights)
	}
	for token, requests := range state.requests {
		if len(requests) < 2 || requests[0] != "/api/v3/time" || len(state.keys[token]) != 1 {
			t.Fatalf("time sync and signed calls must share one isolated route: paths=%v keys=%d", requests, len(state.keys[token]))
		}
		if state.keys[token]["history-one"] || state.keys[token]["history-two"] {
			if len(requests) < 4 {
				t.Fatal("history pagination was not exercised on the pinned route")
			}
		}
	}
}

func TestPKBinanceProxyPreflightConcurrentOperationsAreIsolated(t *testing.T) {
	t.Parallel()
	base := time.Now().Add(-time.Minute).UnixMilli()
	client, state := pkBinanceProxyClient(t, base)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, err := client.History(context.Background(), fmt.Sprintf("key-%d", i), "fixture-secret", "42", time.UnixMilli(base), time.UnixMilli(base+199))
			if err != nil {
				t.Errorf("concurrent pinned history failed: %v", err)
			}
		}(i)
	}
	group.Wait()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.preflights != 8 || len(state.keys) != 8 {
		t.Fatalf("parallel operations reused route state: preflights=%d routes=%d", state.preflights, len(state.keys))
	}
	for _, keys := range state.keys {
		if len(keys) != 1 {
			t.Fatal("a route was shared by different account operations")
		}
	}
}

func TestPKBinanceProxyPreflightFailuresNeverReachBinance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unavailable", 503, `{"error":"hidden-proxy-credential"}`},
		{"busy", 429, `{"error":"hidden-proxy-credential"}`},
		{"redirect", 302, `hidden-proxy-credential`},
		{"malformed", 200, `{`},
		{"missing-token", 200, `{}`},
		{"header-injection", 200, `{"route_token":"node\r\nAuthorization: hidden-proxy-credential"}`},
		{"oversized", 200, strings.Repeat("x", 4097)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/_binance/preflight" {
					t.Error("failed preflight must never be followed by CONNECT or redirect")
				}
				w.Header().Set("Location", "/redirect-must-not-follow")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer gateway.Close()
			proxy := strings.Replace(gateway.URL, "://", "://fixture-user:hidden-proxy-credential@", 1)
			client, err := binancepay.NewClientWithOptions(binancepay.ClientOptions{ProxyURL: proxy, ProxyPreflight: true})
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			now := time.Now()
			receipts, err := client.History(context.Background(), "fixture-key", "fixture-secret", "42", now.Add(-time.Minute), now)
			if !errors.Is(err, binancepay.ErrProxyPreflight) || len(receipts) != 0 || calls.Load() != 1 {
				t.Fatalf("preflight failure must fail closed: count=%d requests=%d err=%v", len(receipts), calls.Load(), err)
			}
			if strings.Contains(err.Error(), "hidden-proxy-credential") || strings.Contains(err.Error(), gateway.URL) || strings.Contains(err.Error(), "fixture-key") {
				t.Fatal("preflight error leaked credentials, response body or URL")
			}
		})
	}
}

func TestPKBinanceProxyPreflightCancellationAndConfiguration(t *testing.T) {
	t.Parallel()
	for _, proxy := range []string{"", "socks5://localhost:1234", "socks5h://localhost:1234", "http://localhost:1234", "https://localhost:1234"} {
		client, err := binancepay.NewClient(proxy)
		if err != nil {
			t.Fatalf("disabled preflight must preserve existing dedicated proxy support: %v", err)
		}
		client.CloseIdleConnections()
	}
	for _, proxy := range []string{"", "socks5://fixture-user:fixture-pass@localhost:1234", "socks5h://fixture-user:fixture-pass@localhost:1234", "http://localhost:1234", "https://fixture-user@localhost:1234"} {
		if _, err := binancepay.NewClientWithOptions(binancepay.ClientOptions{ProxyURL: proxy, ProxyPreflight: true}); err == nil {
			t.Error("preflight requires an authenticated HTTP(S) gateway")
		}
	}
	started := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer gateway.Close()
	client, err := binancepay.NewClientWithOptions(binancepay.ClientOptions{ProxyURL: strings.Replace(gateway.URL, "://", "://fixture-user:fixture-password@", 1), ProxyPreflight: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		now := time.Now()
		_, err := client.Bills(ctx, "fixture-key", "fixture-secret", "42", now.Add(-time.Minute), now)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("preflight did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("preflight did not preserve cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled preflight did not stop")
	}
}

func TestPKBinanceProxyPinnedRouteFailureDoesNotRetryOrFallBack(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusForbidden, http.StatusProxyAuthRequired, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var probes, connects atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/_binance/preflight" {
					probes.Add(1)
					_, _ = io.WriteString(w, `{"route_token":"selected_route"}`)
					return
				}
				connects.Add(1)
				if r.Method != http.MethodConnect || r.Header.Get("X-Binance-Route-Token") != "selected_route" {
					t.Error("failed route must still use its pinned CONNECT token")
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "hidden-gateway-response")
			}))
			defer gateway.Close()
			client, err := binancepay.NewClientWithOptions(binancepay.ClientOptions{ProxyURL: strings.Replace(gateway.URL, "://", "://fixture-user:hidden-proxy-password@", 1), ProxyPreflight: true})
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			now := time.Now()
			receipts, err := client.History(context.Background(), "fixture-key", "fixture-secret", "42", now.Add(-time.Minute), now)
			if err == nil || len(receipts) != 0 || probes.Load() != 1 || connects.Load() != 1 {
				t.Fatalf("a rejected pinned route must fail without selecting a new node: probes=%d connects=%d err=%v", probes.Load(), connects.Load(), err)
			}
			for _, hidden := range []string{"hidden-proxy-password", "selected_route", "hidden-gateway-response", gateway.URL} {
				if strings.Contains(err.Error(), hidden) {
					t.Fatal("CONNECT error exposed gateway credentials, route token, body or URL")
				}
			}
		})
	}
}
