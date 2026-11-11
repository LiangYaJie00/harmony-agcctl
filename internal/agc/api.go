// Package agc implements the one-request AppGallery Connect commands.
package agc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LiangYaJie00/harmony-agcctl/internal/apperr"
	"github.com/LiangYaJie00/harmony-agcctl/internal/auth"
	"github.com/LiangYaJie00/harmony-agcctl/internal/config"
	"github.com/LiangYaJie00/harmony-agcctl/internal/retcodes"
)

var mutatingMethods = map[string]struct{}{"POST": {}, "PUT": {}, "DELETE": {}}

// Transport performs exactly one HTTP round trip. Tests inject a fake.
type Transport interface {
	RoundTrip(req *http.Request) (*http.Response, error)
}

// Exchange captures the transport outcome without retrying.
type Exchange struct {
	Method     string
	StatusCode *int
	Body       map[string]any
	Sent       bool
	ErrorKind  string
	RequestID  string
	RetryAfter string
}

// CallOptions describes one authenticated AGC API request.
type CallOptions struct {
	Params        url.Values
	JSONBody      any
	Headers       map[string]string
	BaseURL       string
	Timeout       time.Duration
	ExpectJSON    bool
	Content       io.Reader
	ContentLength int64
}

// Session owns the Service Account and transport used for a single command.
type Session struct {
	Settings  *config.Settings
	Account   *auth.ServiceAccount
	Transport Transport
	now       func() time.Time
}

// NewSession validates the private key before any command performs a request.
func NewSession(settings *config.Settings, transport Transport) (*Session, error) {
	account, err := auth.LoadServiceAccount(settings.PrivateKeyFile)
	if err != nil {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "PRIVATE_KEY", err.Error())
	}
	if transport == nil {
		transport = newHTTPTransport(settings.ConnectTimeoutSeconds)
	}
	return &Session{Settings: settings, Account: account, Transport: transport, now: time.Now}, nil
}

// SetClock is used by doctor and unit tests.
func (s *Session) SetClock(now func() time.Time) { s.now = now }

// Call executes one authenticated request and classifies common failures.
func (s *Session) Call(ctx context.Context, method, path string, options CallOptions) (*Exchange, error) {
	token, err := auth.BuildJWT(s.Account, s.now().Unix())
	if err != nil {
		return nil, apperr.New(apperr.ExitToolInternal, "TOOL_INTERNAL_ERROR", "工具内部错误")
	}
	base := options.BaseURL
	if base == "" {
		base = s.Settings.APIHost
	}
	target, err := joinURL(base, path, options.Params)
	if err != nil {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "REQUEST_INVALID", err.Error())
	}
	headers := map[string]string{"Authorization": "Bearer " + token, "appId": s.Settings.AppID}
	for key, value := range options.Headers {
		headers[key] = value
	}
	body := options.Content
	contentLength := options.ContentLength
	var encoded []byte
	if options.JSONBody != nil {
		encoded, err = json.Marshal(options.JSONBody)
		if err != nil {
			return nil, apperr.New(apperr.ExitToolInternal, "TOOL_INTERNAL_ERROR", "工具内部错误")
		}
		body = bytes.NewReader(encoded)
		contentLength = int64(len(encoded))
		headers["Content-Type"] = "application/json"
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = time.Duration(s.Settings.QueryTimeoutSeconds) * time.Second
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(timeoutCtx, method, target, body)
	if err != nil {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "REQUEST_INVALID", err.Error())
	}
	request.ContentLength = contentLength
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	exchange := s.send(request, method)
	if options.ExpectJSON {
		if classified := Classify(method, exchange); classified != nil {
			return nil, classified
		}
	}
	return exchange, nil
}

// SendRaw executes a pre-signed request (the OBS upload PUT) without AGC auth.
func (s *Session) SendRaw(ctx context.Context, method, target string, headers map[string]string, body io.Reader, contentLength int64, timeout time.Duration) *Exchange {
	if timeout <= 0 {
		timeout = time.Duration(s.Settings.UploadTimeoutSeconds) * time.Second
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(timeoutCtx, method, target, body)
	if err != nil {
		return &Exchange{Method: method, ErrorKind: "connect"}
	}
	request.ContentLength = contentLength
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return s.send(request, method)
}

func (s *Session) send(request *http.Request, method string) *Exchange {
	response, err := s.Transport.RoundTrip(request)
	if err != nil {
		return classifyTransportError(method, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	status := response.StatusCode
	return &Exchange{
		Method:     method,
		StatusCode: &status,
		Body:       decodeObject(payload),
		Sent:       true,
		RequestID:  firstHeader(response.Header, "x-request-id", "request-id"),
		RetryAfter: response.Header.Get("retry-after"),
	}
}

func classifyTransportError(method string, err error) *Exchange {
	var urlErr *url.Error
	timeout := errors.Is(err, context.DeadlineExceeded)
	operation := ""
	if errors.As(err, &urlErr) {
		timeout = timeout || urlErr.Timeout()
		var netErr *net.OpError
		if errors.As(urlErr.Err, &netErr) {
			operation = netErr.Op
		}
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) && operation == "" {
		operation = netErr.Op
	}
	if isTLSFailure(err) {
		return &Exchange{Method: method, ErrorKind: "tls", Sent: false}
	}
	if timeout {
		if operation == "dial" {
			return &Exchange{Method: method, ErrorKind: "connect", Sent: false}
		}
		return &Exchange{Method: method, ErrorKind: "timeout", Sent: true}
	}
	return &Exchange{Method: method, ErrorKind: "connect", Sent: false}
}

func isTLSFailure(err error) bool {
	if err == nil {
		return false
	}
	var certificateError *tls.CertificateVerificationError
	if errors.As(err, &certificateError) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "certificate") || strings.Contains(text, "x509:") || strings.Contains(text, "tls:")
}

// Classify maps one transport exchange to the documented exit-code categories.
func Classify(method string, exchange *Exchange) *apperr.Error {
	switch exchange.ErrorKind {
	case "tls":
		return apperr.New(apperr.ExitNonRetryable, "TLS_VERIFY_FAILED", "TLS 证书校验失败")
	case "connect":
		return apperr.New(apperr.ExitSafeRetry, "CONNECTION_FAILED", "请求发送前连接失败")
	case "timeout":
		if _, mutating := mutatingMethods[strings.ToUpper(method)]; mutating && exchange.Sent {
			return apperr.New(apperr.ExitReconcileFirst, "HTTP_TIMEOUT_AFTER_SEND", "请求结果无法确认")
		}
		return apperr.New(apperr.ExitSafeRetry, "HTTP_TIMEOUT", "查询超时")
	}
	if exchange.StatusCode == nil {
		return apperr.New(apperr.ExitToolInternal, "HTTP_NO_STATUS", "没有收到 HTTP 状态")
	}
	status := *exchange.StatusCode
	if status == 401 {
		return apperr.New(apperr.ExitSafeRetry, "JWT_REJECTED", "鉴权被拒绝，可重新生成 JWT 后重试一次")
	}
	if status == 400 || status == 403 {
		return apperr.New(apperr.ExitNonRetryable, fmt.Sprintf("HTTP_%d", status), fmt.Sprintf("华为接口返回 HTTP %d", status))
	}
	if status == 404 {
		return apperr.New(apperr.ExitNonRetryable, "HTTP_404", "华为资源不存在")
	}
	if status == 429 {
		return apperr.New(apperr.ExitSafeRetry, "HTTP_429", "华为接口限流")
	}
	if status >= 500 {
		if _, mutating := mutatingMethods[strings.ToUpper(method)]; mutating {
			return apperr.New(apperr.ExitReconcileFirst, "HTTP_5XX", "变更请求结果无法确认")
		}
		return apperr.New(apperr.ExitSafeRetry, "HTTP_5XX", "华为接口暂时失败")
	}
	if status >= 400 {
		return apperr.New(apperr.ExitNonRetryable, "HTTP_UNEXPECTED", fmt.Sprintf("华为接口返回 HTTP %d", status))
	}
	if ret, ok := Object(exchange.Body["ret"]); ok {
		code, present := ret["code"]
		if present && !IsZero(code) {
			message, detail := retcodes.Failure(code, ret["msg"])
			return apperr.NewDetail(apperr.ExitNonRetryable, "AGC_RET_ERROR", message, detail)
		}
	}
	return nil
}

func newHTTPTransport(connectTimeoutSeconds int) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(connectTimeoutSeconds) * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   time.Duration(connectTimeoutSeconds) * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func joinURL(base, path string, params url.Values) (string, error) {
	baseURL, err := url.Parse(strings.TrimRight(base, "/") + "/")
	if err != nil {
		return "", err
	}
	reference, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	resolved := baseURL.ResolveReference(reference)
	if params != nil {
		query := resolved.Query()
		for key, values := range params {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		resolved.RawQuery = query.Encode()
	}
	return resolved.String(), nil
}

func decodeObject(payload []byte) map[string]any {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return map[string]any{}
	}
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func firstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func Values(items map[string]any) url.Values {
	values := url.Values{}
	for key, item := range items {
		if item == nil {
			continue
		}
		values.Set(key, PyString(item))
	}
	return values
}

func Query(values ...any) url.Values {
	query := url.Values{}
	for i := 0; i+1 < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok || values[i+1] == nil {
			continue
		}
		query.Set(key, PyString(values[i+1]))
	}
	return query
}

// Object returns a JSON object value.
func Object(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

// List returns a JSON array value.
func List(value any) ([]any, bool) {
	list, ok := value.([]any)
	return list, ok
}

// String returns a string and whether it is non-empty.
func String(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok && text != ""
}

// StrictInt accepts integer JSON values but rejects booleans and fractional numbers.
func StrictInt(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	}
	return 0, false
}

// AsNumber returns the numeric token for equality and formatting.
func AsNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	}
	return 0, false
}

// IsZero reports whether a JSON number is zero.
func IsZero(value any) bool {
	if number, ok := AsNumber(value); ok {
		return number == 0
	}
	return value == nil
}

// Equal compares JSON scalar values with Python-like numeric equality.
func Equal(left, right any) bool {
	if l, ok := AsNumber(left); ok {
		if r, ok := AsNumber(right); ok {
			return l == r
		}
	}
	return fmt.Sprint(left) == fmt.Sprint(right)
}

// Truthy mirrors Python truthiness for the values present in request JSON.
func Truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case json.Number:
		number, err := typed.Float64()
		return err == nil && number != 0
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

// PyString reproduces the string conversion used by existing request handling.
func PyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func NumberString(value any) string {
	if number, ok := AsNumber(value); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return PyString(value)
}
