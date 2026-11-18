package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

type kiloCompatibilityObserver func(int, http.Header, []byte)

// This boundary receives an already translated request. Every retry is the same
// Key and routing chain, and only follows an explicit pre-execution HTTP 400.
// Transport failures, account errors and incomplete streams are never replayed.
func doKiloCompatibleUpstream(request *http.Request, account *Account, allowRetry bool, observe kiloCompatibilityObserver, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if !isKiloCLIGatewayAccount(account) || request == nil || request.URL == nil || request.URL.Host != "ai-gateway.kilo.ai" || request.Method != http.MethodPost || (request.URL.Path != "/api/v1/responses" && request.URL.Path != "/api/v1/responses/compact") {
		return send(request)
	}
	if request.GetBody == nil {
		return nil, kiloInferenceDenied("KILO_REQUEST_BODY_UNAVAILABLE", "无法核对Kilo最终请求载荷，未发送上游")
	}
	reader, err := request.GetBody()
	if err != nil {
		return nil, err
	}
	original, err := io.ReadAll(io.LimitReader(reader, 64<<20+1))
	_ = reader.Close()
	if err != nil {
		return nil, err
	}
	if len(original) > 64<<20 {
		return nil, infraerrors.New(http.StatusRequestEntityTooLarge, "KILO_PAYLOAD_TOO_LARGE", "Kilo请求超过入口大小上限")
	}
	compact := request.URL.Path == "/api/v1/responses/compact"
	mode, _ := account.Extra["kilo_compaction_mode"].(string)
	schemaTried, imageTried := false, false
	var constraint *kiloSchemaConstraint
	for {
		prepared, tools, prepErr := PrepareKiloToolSearch(original)
		if prepErr != nil {
			return nil, kiloCompatibilityError(prepErr)
		}
		prepared, prepErr = prepareKiloRequestPayload(prepared, compact, mode)
		if prepErr != nil {
			return nil, kiloCompatibilityError(prepErr)
		}
		attempt := request.Clone(request.Context())
		attempt.Header = request.Header.Clone()
		attempt.Body = io.NopCloser(bytes.NewReader(prepared))
		attempt.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(prepared)), nil }
		attempt.ContentLength = int64(len(prepared))
		attempt.Header.Del("Content-Length")
		response, sendErr := send(attempt)
		if sendErr != nil || response == nil || response.Body == nil {
			return response, sendErr
		}
		if allowRetry && !compact && response.StatusCode == http.StatusBadRequest {
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
			if readErr != nil {
				_ = response.Body.Close()
				return nil, readErr
			}
			// Preserve the original body and its close/release ownership when a large
			// or unrelated error is not an eligible compatibility rejection.
			response.Body = &kiloReplayPrefixBody{Reader: io.MultiReader(bytes.NewReader(raw), response.Body), closer: response.Body}
			if len(raw) <= 1<<20 {
				if !schemaTried {
					retry, next, retryErr := prepareKiloSchemaRetry(original, response.StatusCode, raw)
					if retryErr != nil {
						_ = response.Body.Close()
						return nil, kiloCompatibilityError(retryErr)
					}
					if next != nil {
						_ = response.Body.Close()
						original = retry
						constraint = next
						schemaTried = true
						continue
					}
				}
				if !imageTried {
					retry, changed, retryErr := retryKiloImageTools(original, response.StatusCode, raw)
					if retryErr != nil {
						_ = response.Body.Close()
						return nil, kiloCompatibilityError(retryErr)
					}
					if changed {
						_ = response.Body.Close()
						original = retry
						imageTried = true
						continue
					}
				}
			}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return response, nil
		}
		upstreamSSE := strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
		if upstreamSSE {
			// Observe real failures before validation consumes the upstream stream.
			// Schema sees original function calls; restoring tool_search_call first
			// would reject a legitimate tools-only structured-output turn.
			response.Body = newKiloCompatibilityStreamBody(response.Body, nil, response.Header, observe)
			response.ContentLength = -1
			response.Header.Del("Content-Length")
		}
		if constraint != nil {
			response.Body, err = constraint.bufferResponse(response.Body, upstreamSSE)
			if err != nil {
				return nil, kiloCompatibilityError(err)
			}
			response.ContentLength = -1
			response.Header.Del("Content-Length")
		}
		if tools.Active() {
			if upstreamSSE {
				response.Body = newKiloCompatibilityStreamBody(response.Body, tools, response.Header, nil)
			} else {
				response.Body = &kiloCompatibilityJSONBody{source: response.Body, tools: tools}
			}
			response.ContentLength = -1
			response.Header.Del("Content-Length")
		}
		return response, nil
	}
}

func kiloCompatibilityError(err error) error {
	var payload *kiloPayloadError
	if errors.As(err, &payload) {
		return infraerrors.New(payload.StatusCode, "KILO_"+strings.ToUpper(payload.Code), payload.Message)
	}
	var tool *KiloToolSearchError
	if errors.As(err, &tool) {
		return infraerrors.New(tool.Status, "KILO_"+strings.ToUpper(tool.Code), tool.Message)
	}
	var schema *kiloSchemaError
	if errors.As(err, &schema) {
		return infraerrors.New(schema.Status, "KILO_"+strings.ToUpper(schema.Code), schema.Message)
	}
	return err
}

type kiloReplayPrefixBody struct {
	io.Reader
	closer io.Closer
}

func (b *kiloReplayPrefixBody) Close() error { return b.closer.Close() }

type kiloCompatibilityJSONBody struct {
	source io.ReadCloser
	tools  *KiloToolSearchAdapter
	once   sync.Once
	reader *bytes.Reader
	err    error
}

func (b *kiloCompatibilityJSONBody) Read(p []byte) (int, error) {
	b.once.Do(func() {
		raw, err := io.ReadAll(io.LimitReader(b.source, 16<<20+1))
		if err == nil && len(raw) > 16<<20 {
			err = invalidKiloToolSearchResponse()
		}
		if err == nil {
			raw, err = b.tools.TransformJSON(raw)
		}
		b.err = err
		b.reader = bytes.NewReader(raw)
	})
	if b.err != nil {
		return 0, b.err
	}
	return b.reader.Read(p)
}
func (b *kiloCompatibilityJSONBody) Close() error { return b.source.Close() }

type kiloCompatibilityStreamBody struct {
	source  io.ReadCloser
	scanner *bufio.Scanner
	tools   *KiloToolSearchAdapter
	observe kiloCompatibilityObserver
	headers http.Header
	pending []byte
}

func newKiloCompatibilityStreamBody(source io.ReadCloser, tools *KiloToolSearchAdapter, headers http.Header, observe kiloCompatibilityObserver) io.ReadCloser {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	scanner.Split(kiloSchemaSSESplit)
	return &kiloCompatibilityStreamBody{source: source, scanner: scanner, tools: tools, observe: observe, headers: headers}
}
func (b *kiloCompatibilityStreamBody) Read(p []byte) (int, error) {
	for len(b.pending) == 0 {
		if !b.scanner.Scan() {
			if err := b.scanner.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		frame := bytes.Clone(b.scanner.Bytes())
		if b.observe != nil {
			value, err := kiloSchemaParseEvent(frame)
			if err == nil && value != nil {
				kind, _ := value["type"].(string)
				if kind == "error" || kind == "response.failed" {
					raw, _ := json.Marshal(value)
					status := openAIStreamFailedEventSemanticStatus(raw, extractUpstreamErrorMessage(raw))
					for _, path := range openAIStreamErrorStatusPaths {
						if int(gjson.GetBytes(raw, path).Int()) == http.StatusPaymentRequired {
							status = http.StatusPaymentRequired
							break
						}
					}
					b.observe(status, b.headers, raw)
				}
			}
		}
		converted, err := b.tools.TransformSSE(frame)
		if err != nil {
			return 0, err
		}
		if len(converted) == 0 {
			continue
		}
		b.pending = converted
		if !bytes.HasSuffix(b.pending, []byte("\n\n")) {
			b.pending = append(bytes.TrimRight(b.pending, "\r\n"), '\n', '\n')
		}
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}
func (b *kiloCompatibilityStreamBody) Close() error { return b.source.Close() }
