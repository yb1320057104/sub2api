package payment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
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
