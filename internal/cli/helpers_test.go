package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LiangYaJie00/harmony-agcctl/internal/doctor"
	"github.com/LiangYaJie00/harmony-agcctl/internal/version"
)

const (
	testAppID      = "123456789"
	testBundleName = "com.qianshou.billionclub"
	testSubAccount = "1000000000000000001"
	testKeyID      = "test-key-id"
)

type scriptedTransport struct {
	responses []*http.Response
	errors    []error
	requests  []*http.Request
	bodies    [][]byte
}

func (s *scriptedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, request)
	var body []byte
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
		_ = request.Body.Close()
	}
	s.bodies = append(s.bodies, body)
	if len(s.errors) > 0 {
		err := s.errors[0]
		s.errors = s.errors[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(s.responses) == 0 {
		return nil, fmt.Errorf("unexpected request %s %s", request.Method, request.URL)
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	if response.Body == nil {
		response.Body = io.NopCloser(strings.NewReader(""))
	}
	return response, nil
}

func jsonResponse(status int, payload any) *http.Response {
	raw, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(raw)),
	}
}

type fakeDoctorClient struct {
	probe  doctor.AppInfoProbe
	lookup doctor.PackageLookup
}

func (f *fakeDoctorClient) GetAppInfo(context.Context, string, string) doctor.AppInfoProbe {
	return f.probe
}

func (f *fakeDoctorClient) LookupPackage(context.Context, string, string) doctor.PackageLookup {
	return f.lookup
}

func successDoctorClient(now time.Time) *fakeDoctorClient {
	return &fakeDoctorClient{
		probe:  doctor.AppInfoProbe{Connected: true, TLSVerified: true, StatusCode: intPointer(200), Date: &now, RetCode: int64Pointer(0), Message: "HTTP 200"},
		lookup: doctor.PackageLookup{Connected: true, TLSVerified: true, StatusCode: intPointer(200), RetCode: int64Pointer(0), AppID: testAppID, Message: "HTTP 200"},
	}
}

func intPointer(value int) *int       { return &value }
func int64Pointer(value int64) *int64 { return &value }

type testEnv struct {
	root        string
	configPath  string
	requestPath string
	artifact    string
}

func newTestEnv(t *testing.T, body map[string]any) testEnv {
	t.Helper()
	root := t.TempDir()
	artifacts := filepath.Join(root, "artifacts")
	logs := filepath.Join(root, "logs")
	runDir := filepath.Join(root, "run")
	for _, path := range []string{artifacts, logs, runDir, filepath.Join(artifacts, "PUBLISH-20261015-001")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	privatePEM, err := generatePrivatePEM()
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(root, "private.json")
	privatePayload, _ := json.Marshal(map[string]any{"key_id": testKeyID, "sub_account": testSubAccount, "private_key": privatePEM})
	if err := os.WriteFile(privatePath, privatePayload, 0o600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`apiHost: https://connect-api.cloud.huawei.com
appId: %q
bundleName: %s
defaultLanguage: zh-CN
testReleaseType: 6
testType: 3
connectTimeoutSeconds: 10
queryTimeoutSeconds: 30
uploadTimeoutSeconds: 1200
privateKeyFile: %s
artifactRoot: %s
logDir: %s
runDir: %s
releaseUser: %s
requireManifest: false
maxClockSkewSeconds: 60
notifyPath: /api/publish/v2/test/user/action/notify
`, testAppID, testBundleName, privatePath, artifacts, logs, runDir, currentUsername())
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(root, "request.json")
	raw, _ := json.Marshal(body)
	if err := os.WriteFile(requestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return testEnv{root: root, configPath: configPath, requestPath: requestPath, artifact: filepath.Join(artifacts, "PUBLISH-20261015-001", "qianshou.app")}
}

func requestBody(operation string, request any) map[string]any {
	return map[string]any{
		"schemaVersion": "1.0",
		"taskId":        "PUBLISH-20261015-001",
		"operationKey":  "PUBLISH-20261015-001:" + operation,
		"toolVersion":   version.Version,
		"appId":         testAppID,
		"request":       request,
	}
}

func runCLI(t *testing.T, env testEnv, args []string, transport *scriptedTransport, doctorClient doctor.AppInfoClient) (int, map[string]any, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(append(args, "--config", env.configPath, "--request-file", env.requestPath), Dependencies{
		Transport: transport, DoctorClient: doctorClient, Stdout: &stdout, Stderr: &stderr,
		Now: time.Now,
	})
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("expected one stdout JSON line, got %q", stdout.String())
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &response); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	return code, response, stderr.String()
}

func generatePrivatePEM() (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func currentUsername() string {
	if account, err := userCurrent(); err == nil {
		return account
	}
	return "root"
}
