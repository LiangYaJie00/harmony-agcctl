// Package doctor validates local prerequisites and read-only Huawei connectivity.
package doctor

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/LiangYaJie00/harmony-agcctl/internal/apperr"
	"github.com/LiangYaJie00/harmony-agcctl/internal/auth"
	"github.com/LiangYaJie00/harmony-agcctl/internal/config"
	"github.com/LiangYaJie00/harmony-agcctl/internal/version"
)

const (
	AppInfoPath   = "/api/publish/v3/app-info"
	AppIDListPath = "/api/publish/v2/appid-list"
	HarmonyType   = "7"
)

// Check is one stable item in the doctor response.
type Check struct {
	Name     string          `json:"name"`
	OK       bool            `json:"ok"`
	Skipped  bool            `json:"skipped"`
	Message  string          `json:"message"`
	ExitCode apperr.ExitCode `json:"-"`
}

// Report aggregates all checks and the most severe exit code.
type Report struct {
	Checks      []Check
	ExitCode    apperr.ExitCode
	Code        string
	Message     string
	RemoteState map[string]any
}

func (r *Report) Success() bool { return r.ExitCode == apperr.ExitSuccess }

// AppInfoProbe is the outcome of the read-only application query.
type AppInfoProbe struct {
	Connected   bool
	TLSVerified bool
	StatusCode  *int
	Date        *time.Time
	RetCode     *int64
	ErrorCode   string
	Message     string
}

// PackageLookup resolves one bundle name to its App ID.
type PackageLookup struct {
	Connected   bool
	TLSVerified bool
	StatusCode  *int
	RetCode     *int64
	ErrorCode   string
	AppID       string
	Message     string
}

// AppInfoClient is injected in tests.
type AppInfoClient interface {
	GetAppInfo(ctx context.Context, appID, token string) AppInfoProbe
	LookupPackage(ctx context.Context, packageName, token string) PackageLookup
}

// HTTPClient is the default read-only Huawei client used by doctor.
type HTTPClient struct {
	Settings  *config.Settings
	Transport http.RoundTripper
}

// Run performs all checks.
func Run(settings *config.Settings, toolVersion, appID string, client AppInfoClient, now func() time.Time) *Report {
	if now == nil {
		now = time.Now
	}
	report := &Report{ExitCode: apperr.ExitSuccess, Code: "OK", Message: "doctor passed", RemoteState: map[string]any{}}
	report.Checks = append(report.Checks, checkToolVersion(toolVersion))
	report.Checks = append(report.Checks, checkAppID(settings, appID))
	report.Checks = append(report.Checks, checkManifest(settings))
	report.Checks = append(report.Checks, checkDirectory(settings.ArtifactRoot, "artifact_dir", false))
	report.Checks = append(report.Checks, checkDirectory(settings.LogDir, "log_dir", true))
	report.Checks = append(report.Checks, checkDirectory(settings.RunDir, "run_dir", true))

	account, permissionCheck := loadAccount(settings)
	report.Checks = append(report.Checks, permissionCheck)
	if account == nil {
		report.Checks = append(report.Checks,
			skip("jwt", "私钥不可用，跳过 JWT"),
			skip("api_host", "私钥不可用，跳过接口探测"),
			skip("clock", "没有完成接口探测，跳过时钟比对"),
			skip("app_identity", "没有完成接口探测，跳过包名比对"),
			skip("service_account_permission", "没有完成接口探测，跳过权限探测"),
		)
		return finish(report)
	}
	token, err := auth.BuildJWT(account, now().Unix())
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "jwt", OK: false, Message: "JWT 签名失败：" + typeName(err), ExitCode: apperr.ExitLocalPrecondition})
		return finish(report)
	}
	if strings.Count(token, ".") != 2 {
		report.Checks = append(report.Checks, Check{Name: "jwt", OK: false, Message: "JWT 结构无效", ExitCode: apperr.ExitToolInternal})
		return finish(report)
	}
	report.Checks = append(report.Checks, Check{Name: "jwt", OK: true, Message: "PS256 JWT 已在内存中生成"})
	if client == nil {
		client = &HTTPClient{Settings: settings}
	}
	probe := client.GetAppInfo(context.Background(), settings.AppID, token)
	var lookup *PackageLookup
	if probe.TLSVerified && probe.Connected && probe.ErrorCode == "" && (probe.RetCode == nil || *probe.RetCode == 0) {
		value := client.LookupPackage(context.Background(), settings.BundleName, token)
		lookup = &value
	}
	var status any
	if probe.StatusCode != nil {
		status = *probe.StatusCode
	}
	report.RemoteState = map[string]any{"path": AppInfoPath, "connected": probe.Connected, "statusCode": status}
	report.Checks = append(report.Checks, probeChecks(settings, probe, now(), lookup)...)
	return finish(report)
}

func finish(report *Report) *Report {
	failed := make([]Check, 0)
	codes := make([]apperr.ExitCode, 0)
	for _, check := range report.Checks {
		if !check.OK && !check.Skipped {
			failed = append(failed, check)
			codes = append(codes, check.ExitCode)
		}
	}
	if len(failed) == 0 {
		return report
	}
	report.ExitCode = apperr.WorstExit(codes)
	for _, check := range failed {
		if check.ExitCode == report.ExitCode {
			report.Code = strings.ToUpper(check.Name)
			report.Message = check.Message
			break
		}
	}
	return report
}

func checkToolVersion(toolVersion string) Check {
	if toolVersion != version.Version {
		return Check{Name: "tool_version", Message: fmt.Sprintf("请求 toolVersion 是 %s，当前 CLI 是 %s", toolVersion, version.Version), ExitCode: apperr.ExitLocalPrecondition}
	}
	return Check{Name: "tool_version", OK: true, Message: version.Version}
}

func checkAppID(settings *config.Settings, appID string) Check {
	if appID != settings.AppID {
		return Check{Name: "app_id", Message: "请求 appId 与配置不一致", ExitCode: apperr.ExitLocalPrecondition}
	}
	return Check{Name: "app_id", OK: true, Message: "appId 与配置一致"}
}

func checkManifest(settings *config.Settings) Check {
	if settings.ManifestFile == "" {
		if settings.RequireManifest {
			return Check{Name: "manifest", Message: "未配置 manifestFile", ExitCode: apperr.ExitLocalPrecondition}
		}
		return skip("manifest", "未配置 manifestFile")
	}
	path := settings.ManifestFile
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Check{Name: "manifest", Message: "MANIFEST.json 不存在", ExitCode: apperr.ExitLocalPrecondition}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: "manifest", Message: "MANIFEST.json 无法读取或字段缺失", ExitCode: apperr.ExitLocalPrecondition}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Check{Name: "manifest", Message: "MANIFEST.json 无法读取或字段缺失", ExitCode: apperr.ExitLocalPrecondition}
	}
	manifestVersion, ok1 := payload["version"].(string)
	expected, ok2 := payload["sha256"].(string)
	subject, ok3 := payload["payload"].(string)
	if !ok1 || !ok2 || !ok3 {
		return Check{Name: "manifest", Message: "MANIFEST.json 无法读取或字段缺失", ExitCode: apperr.ExitLocalPrecondition}
	}
	content, err := os.ReadFile(subject)
	if err != nil {
		return Check{Name: "manifest", Message: "MANIFEST.json 无法读取或字段缺失", ExitCode: apperr.ExitLocalPrecondition}
	}
	digest := sha256.Sum256(content)
	if manifestVersion != version.Version || hex.EncodeToString(digest[:]) != expected {
		return Check{Name: "manifest", Message: "MANIFEST 版本或 SHA-256 不一致", ExitCode: apperr.ExitLocalPrecondition}
	}
	return Check{Name: "manifest", OK: true, Message: "MANIFEST 版本和 SHA-256 一致"}
}

func checkDirectory(path, name string, writable bool) Check {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Check{Name: name, Message: fmt.Sprintf("%s 不是可读目录", path), ExitCode: apperr.ExitLocalPrecondition}
	}
	if _, err := os.ReadDir(path); err != nil {
		return Check{Name: name, Message: fmt.Sprintf("%s 不可读", path), ExitCode: apperr.ExitLocalPrecondition}
	}
	if !writable {
		return Check{Name: name, OK: true, Message: fmt.Sprintf("%s 可读", path)}
	}
	probe := filepath.Join(path, ".agcctl-doctor-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return Check{Name: name, Message: fmt.Sprintf("%s 不可写", path), ExitCode: apperr.ExitLocalPrecondition}
	}
	if err := os.Remove(probe); err != nil {
		return Check{Name: name, Message: fmt.Sprintf("%s 不可写", path), ExitCode: apperr.ExitLocalPrecondition}
	}
	return Check{Name: name, OK: true, Message: fmt.Sprintf("%s 可写", path)}
}

func loadAccount(settings *config.Settings) (*auth.ServiceAccount, Check) {
	info, err := os.Lstat(settings.PrivateKeyFile)
	if err != nil {
		return nil, Check{Name: "private_key_permissions", Message: "private.json 不存在或不可访问", ExitCode: apperr.ExitLocalPrecondition}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, Check{Name: "private_key_permissions", Message: "private.json 不能是符号链接", ExitCode: apperr.ExitLocalPrecondition}
	}
	if !info.Mode().IsRegular() {
		return nil, Check{Name: "private_key_permissions", Message: "private.json 必须是普通文件", ExitCode: apperr.ExitLocalPrecondition}
	}
	if info.Mode().Perm() != 0o600 {
		return nil, Check{Name: "private_key_permissions", Message: "private.json 权限必须是 0600", ExitCode: apperr.ExitLocalPrecondition}
	}
	owner := ownerName(info)
	expected := settings.ReleaseUser
	if expected == "" {
		expected = currentUserName()
	}
	if owner != expected {
		return nil, Check{Name: "private_key_permissions", Message: "private.json 属主与 releaseUser 不一致", ExitCode: apperr.ExitLocalPrecondition}
	}
	account, err := auth.LoadServiceAccount(settings.PrivateKeyFile)
	if err != nil {
		return nil, Check{Name: "private_key_permissions", Message: err.Error(), ExitCode: apperr.ExitLocalPrecondition}
	}
	return account, Check{Name: "private_key_permissions", OK: true, Message: "权限 0600，属主正确"}
}

func ownerName(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	uid := strconv.FormatUint(uint64(stat.Uid), 10)
	if account, err := user.LookupId(uid); err == nil {
		return account.Username
	}
	return uid
}

func currentUserName() string {
	if account, err := user.Current(); err == nil {
		return account.Username
	}
	return strconv.Itoa(os.Getuid())
}

func probeChecks(settings *config.Settings, probe AppInfoProbe, now time.Time, lookup *PackageLookup) []Check {
	if !probe.TLSVerified {
		return []Check{
			{Name: "api_host", Message: "TLS 证书校验失败", ExitCode: apperr.ExitNonRetryable},
			skip("clock", "TLS 校验失败，跳过时钟比对"),
			skip("app_identity", "TLS 校验失败，跳过包名比对"),
			skip("service_account_permission", "TLS 校验失败，跳过权限探测"),
		}
	}
	if !probe.Connected {
		return []Check{
			{Name: "api_host", Message: "华为 API Host 不可达", ExitCode: apperr.ExitSafeRetry},
			skip("clock", "接口不可达，跳过时钟比对"),
			skip("app_identity", "接口不可达，跳过包名比对"),
			skip("service_account_permission", "接口不可达，跳过权限探测"),
		}
	}
	return []Check{
		{Name: "api_host", OK: true, Message: "华为 API Host 可达，TLS 校验通过"},
		clockCheck(settings, probe, now),
		identityCheck(settings, probe, lookup),
		permissionCheck(probe),
	}
}

func clockCheck(settings *config.Settings, probe AppInfoProbe, now time.Time) Check {
	if probe.Date == nil {
		return Check{Name: "clock", Message: "响应没有 Date 头", ExitCode: apperr.ExitLocalPrecondition}
	}
	skew := now.Sub(*probe.Date)
	if skew < 0 {
		skew = -skew
	}
	if skew > time.Duration(settings.MaxClockSkewSeconds)*time.Second {
		return Check{Name: "clock", Message: "本机时间与华为响应时间相差过大", ExitCode: apperr.ExitLocalPrecondition}
	}
	return Check{Name: "clock", OK: true, Message: "本机时间与响应 Date 一致"}
}

func identityCheck(settings *config.Settings, probe AppInfoProbe, lookup *PackageLookup) Check {
	switch probe.ErrorCode {
	case "PERMISSION_DENIED", "HTTP_429", "HTTP_5XX", "HTTP_UNEXPECTED":
		return skip("app_identity", "接口未返回应用信息，跳过包名比对")
	}
	if probe.RetCode != nil && *probe.RetCode != 0 {
		return skip("app_identity", "应用信息业务码非成功，跳过包名比对")
	}
	if lookup == nil {
		return skip("app_identity", "没有完成包名查询，跳过包名比对")
	}
	if !lookup.TLSVerified {
		return Check{Name: "app_identity", Message: "包名查询 TLS 证书校验失败", ExitCode: apperr.ExitNonRetryable}
	}
	if !lookup.Connected {
		return Check{Name: "app_identity", Message: "包名查询不可达", ExitCode: apperr.ExitSafeRetry}
	}
	switch lookup.ErrorCode {
	case "PERMISSION_DENIED":
		return Check{Name: "app_identity", Message: "Service Account 无权按包名查询应用", ExitCode: apperr.ExitNonRetryable}
	case "HTTP_429":
		return Check{Name: "app_identity", Message: "包名查询被限流", ExitCode: apperr.ExitSafeRetry}
	case "HTTP_5XX":
		return Check{Name: "app_identity", Message: "包名查询暂时失败", ExitCode: apperr.ExitSafeRetry}
	case "HTTP_UNEXPECTED":
		return Check{Name: "app_identity", Message: lookup.Message, ExitCode: apperr.ExitNonRetryable}
	}
	if lookup.RetCode != nil && *lookup.RetCode != 0 {
		return Check{Name: "app_identity", Message: "包名查询业务失败", ExitCode: apperr.ExitNonRetryable}
	}
	if lookup.AppID == settings.AppID {
		return Check{Name: "app_identity", OK: true, Message: "bundleName 对应的 appId 与配置一致"}
	}
	return Check{Name: "app_identity", Message: "bundleName 没有对应到配置的 appId", ExitCode: apperr.ExitNonRetryable}
}

func permissionCheck(probe AppInfoProbe) Check {
	switch probe.ErrorCode {
	case "PERMISSION_DENIED":
		return Check{Name: "service_account_permission", Message: "Service Account 无权查询应用", ExitCode: apperr.ExitNonRetryable}
	case "HTTP_429":
		return Check{Name: "service_account_permission", Message: "华为接口限流", ExitCode: apperr.ExitSafeRetry}
	case "HTTP_5XX":
		return Check{Name: "service_account_permission", Message: "华为接口暂时失败", ExitCode: apperr.ExitSafeRetry}
	case "HTTP_UNEXPECTED":
		return Check{Name: "service_account_permission", Message: probe.Message, ExitCode: apperr.ExitNonRetryable}
	}
	if probe.RetCode != nil && *probe.RetCode != 0 {
		return Check{Name: "service_account_permission", Message: "应用查询业务失败", ExitCode: apperr.ExitNonRetryable}
	}
	return Check{Name: "service_account_permission", OK: true, Message: "只读应用查询成功"}
}

func skip(name, message string) Check {
	return Check{Name: name, OK: true, Skipped: true, Message: message}
}

func typeName(err error) string {
	if err == nil {
		return ""
	}
	name := fmt.Sprintf("%T", err)
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index+1:]
	}
	return name
}

// GetAppInfo performs the doctor's read-only application query.
func (c *HTTPClient) GetAppInfo(ctx context.Context, appID, token string) AppInfoProbe {
	outcome, err := c.get(ctx, AppInfoPath, url.Values{"appId": []string{appID}}, token, appID)
	if err != nil {
		return transportProbe(err)
	}
	status := outcome.StatusCode
	return AppInfoProbe{
		Connected: true, TLSVerified: true, StatusCode: &status,
		Date: headerDate(outcome.Header), RetCode: retCode(outcome.Body),
		ErrorCode: httpErrorCode(status), Message: fmt.Sprintf("HTTP %d", status),
	}
}

// LookupPackage resolves a HarmonyOS bundle name through appid-list.
func (c *HTTPClient) LookupPackage(ctx context.Context, packageName, token string) PackageLookup {
	outcome, err := c.get(ctx, AppIDListPath, url.Values{"packageName": []string{packageName}, "packageTypes": []string{HarmonyType}}, token, c.Settings.AppID)
	if err != nil {
		probe := transportProbe(err)
		return PackageLookup{Connected: probe.Connected, TLSVerified: probe.TLSVerified, StatusCode: probe.StatusCode, RetCode: probe.RetCode, ErrorCode: probe.ErrorCode, Message: probe.Message}
	}
	status := outcome.StatusCode
	return PackageLookup{
		Connected: true, TLSVerified: true, StatusCode: &status,
		RetCode: retCode(outcome.Body), ErrorCode: httpErrorCode(status),
		AppID: matchedAppID(outcome.Body), Message: fmt.Sprintf("HTTP %d", status),
	}
}

type responseOutcome struct {
	StatusCode int
	Header     http.Header
	Body       map[string]any
}

func (c *HTTPClient) get(ctx context.Context, path string, params url.Values, token, appID string) (*responseOutcome, error) {
	timeout := time.Duration(c.Settings.QueryTimeoutSeconds) * time.Second
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	target := strings.TrimRight(c.Settings.APIHost, "/") + path + "?" + params.Encode()
	request, err := http.NewRequestWithContext(timeoutCtx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("appId", appID)
	transport := c.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			ForceAttemptHTTP2:   true,
			DialContext:         (&net.Dialer{Timeout: time.Duration(c.Settings.ConnectTimeoutSeconds) * time.Second}).DialContext,
			TLSHandshakeTimeout: time.Duration(c.Settings.ConnectTimeoutSeconds) * time.Second,
		}
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	body := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err == nil {
		if object, ok := value.(map[string]any); ok {
			body = object
		}
	}
	return &responseOutcome{StatusCode: response.StatusCode, Header: response.Header, Body: body}, nil
}

func transportProbe(err error) AppInfoProbe {
	tlsFailed := false
	var certificateError *tls.CertificateVerificationError
	if errors.As(err, &certificateError) {
		tlsFailed = true
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "certificate") || strings.Contains(text, "x509:") || strings.Contains(text, "tls:") {
		tlsFailed = true
	}
	if tlsFailed {
		return AppInfoProbe{Connected: false, TLSVerified: false, ErrorCode: "TLS_VERIFY_FAILED", Message: typeName(err)}
	}
	return AppInfoProbe{Connected: false, TLSVerified: true, ErrorCode: "API_UNREACHABLE", Message: typeName(err)}
}

func headerDate(header http.Header) *time.Time {
	raw := header.Get("Date")
	if raw == "" {
		return nil
	}
	parsed, err := http.ParseTime(raw)
	if err != nil {
		return nil
	}
	return &parsed
}

func retCode(body map[string]any) *int64 {
	ret, ok := body["ret"].(map[string]any)
	if !ok {
		return nil
	}
	value, ok := ret["code"]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return &parsed
		}
	case float64:
		if typed == float64(int64(typed)) {
			parsed := int64(typed)
			return &parsed
		}
	case int64:
		return &typed
	}
	return nil
}

func httpErrorCode(status int) string {
	if status >= 200 && status < 300 {
		return ""
	}
	if status == 401 || status == 403 {
		return "PERMISSION_DENIED"
	}
	if status == 429 {
		return "HTTP_429"
	}
	if status >= 500 {
		return "HTTP_5XX"
	}
	return "HTTP_UNEXPECTED"
}

// MatchedAppID exposes the single-value rule for focused tests.
func MatchedAppID(body map[string]any) string {
	return matchedAppID(body)
}

func matchedAppID(body map[string]any) string {
	appIDs, ok := body["appids"].([]any)
	if !ok {
		return ""
	}
	found := make([]string, 0)
	for _, value := range appIDs {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		appID, ok := item["value"].(string)
		if !ok || appID == "" {
			continue
		}
		duplicate := false
		for _, existing := range found {
			if existing == appID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			found = append(found, appID)
		}
	}
	if len(found) != 1 {
		return ""
	}
	return found[0]
}
