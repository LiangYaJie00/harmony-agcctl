package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDoctorPrintsOneJSONResponse(t *testing.T) {
	env := newTestEnv(t, requestBody("DOCTOR", map[string]any{}))
	code, response, stderr := runCLI(t, env, []string{"doctor"}, nil, successDoctorClient(time.Now()))
	if code != 0 || response["success"] != true || response["operation"] != "DOCTOR" {
		t.Fatalf("unexpected response: code=%d %#v", code, response)
	}
	if !strings.Contains(stderr, `"result":"ok"`) {
		t.Fatalf("missing redacted log line: %s", stderr)
	}
}

func TestInvalidRequestIsSchemaInvalid(t *testing.T) {
	body := requestBody("DOCTOR", map[string]any{})
	delete(body, "operationKey")
	env := newTestEnv(t, body)
	code, response, _ := runCLI(t, env, []string{"doctor"}, nil, successDoctorClient(time.Now()))
	if code != 40 {
		t.Fatalf("exit code = %d", code)
	}
	errorValue := response["error"].(map[string]any)
	if errorValue["code"] != "SCHEMA_INVALID" {
		t.Fatalf("error = %#v", errorValue)
	}
}

func TestUploadStreamsPUTAndHidesSignedURL(t *testing.T) {
	payload := []byte("signed-app-pack")
	env := newTestEnv(t, requestBody("UPLOAD_PACKAGE", map[string]any{}))
	if err := os.WriteFile(env.artifact, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	env = rewriteRequest(t, env, requestBody("UPLOAD_PACKAGE", map[string]any{
		"artifactPath": env.artifact, "fileName": "qianshou.app", "contentLength": len(payload), "sha256": sha256Text(payload),
	}))
	uploadURL := "https://bucket.obs.myhuaweicloud.com/object?Signature=secret"
	transport := &scriptedTransport{responses: []*http.Response{
		jsonResponse(200, map[string]any{"ret": map[string]any{"code": 0}, "urlInfo": map[string]any{"url": uploadURL, "headers": map[string]any{"Authorization": "obs-sign"}, "objectId": "obj-1"}}),
		jsonResponse(200, map[string]any{}),
	}}
	code, response, stderr := runCLI(t, env, []string{"upload", "package"}, transport, nil)
	if code != 0 {
		t.Fatalf("exit code=%d response=%#v", code, response)
	}
	if len(transport.requests) != 2 || transport.requests[1].Method != "PUT" {
		t.Fatalf("requests = %#v", transport.requests)
	}
	if string(transport.bodies[1]) != string(payload) {
		t.Fatalf("uploaded body = %q", transport.bodies[1])
	}
	external := response["externalIds"].(map[string]any)
	if external["objectId"] != "obj-1" {
		t.Fatalf("external ids = %#v", external)
	}
	if strings.Contains(stderr, uploadURL) || strings.Contains(stderr, "obs-sign") {
		t.Fatalf("signed URL leaked to stderr: %s", stderr)
	}
}

func TestUploadHashMismatchDoesNotUseNetwork(t *testing.T) {
	payload := []byte("pack")
	env := newTestEnv(t, requestBody("UPLOAD_PACKAGE", map[string]any{}))
	if err := os.WriteFile(env.artifact, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	env = rewriteRequest(t, env, requestBody("UPLOAD_PACKAGE", map[string]any{
		"artifactPath": env.artifact, "fileName": "qianshou.app", "contentLength": len(payload), "sha256": strings.Repeat("0", 64),
	}))
	transport := &scriptedTransport{}
	code, response, _ := runCLI(t, env, []string{"upload", "package"}, transport, nil)
	if code != 40 || len(transport.requests) != 0 {
		t.Fatalf("code=%d requests=%d response=%#v", code, len(transport.requests), response)
	}
	if response["error"].(map[string]any)["code"] != "ARTIFACT_HASH_MISMATCH" {
		t.Fatalf("response = %#v", response)
	}
}

func TestMutationTimeoutRequiresReconcile(t *testing.T) {
	env := newTestEnv(t, requestBody("TEST_VERSION_CREATE", map[string]any{"testDesc": "local verify"}))
	transport := &scriptedTransport{errors: []error{context.DeadlineExceeded}}
	code, response, _ := runCLI(t, env, []string{"test", "create-version"}, transport, nil)
	if code != 30 || response["retryClass"] != "RECONCILE_FIRST" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	var sent map[string]any
	if err := json.Unmarshal(transport.bodies[0], &sent); err != nil {
		t.Fatal(err)
	}
	if sent["releaseType"] != float64(6) || sent["testType"] != float64(3) || sent["onshelfSelfDetect"] != float64(0) {
		t.Fatalf("sent = %#v", sent)
	}
	if !strings.Contains(transport.requests[0].URL.RawQuery, "appId=123456789") {
		t.Fatalf("url = %s", transport.requests[0].URL)
	}
}

func TestListGroupsDefaultsInternalAndAcceptsExternal(t *testing.T) {
	payload := map[string]any{"rtnCode": 0, "groups": []any{map[string]any{"groupId": "internal-1", "groupType": 1}}}
	for _, test := range []struct {
		body      map[string]any
		groupType float64
	}{{body: map[string]any{}, groupType: 1}, {body: map[string]any{"groupType": 0}, groupType: 0}} {
		env := newTestEnv(t, requestBody("TEST_GROUP_LIST", test.body))
		transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, payload)}}
		code, response, _ := runCLI(t, env, []string{"test", "list-groups"}, transport, nil)
		if code != 0 || response["remoteState"].(map[string]any)["groupType"] != test.groupType {
			t.Fatalf("code=%d response=%#v", code, response)
		}
		if grp := transport.requests[0].URL.Query().Get("groupType"); grp != formatNumber(test.groupType) {
			t.Fatalf("groupType=%s", grp)
		}
	}
}

func TestListTestersUsesGroupHeader(t *testing.T) {
	env := newTestEnv(t, requestBody("TEST_TESTER_LIST", map[string]any{"groupId": "group-1", "current": 1, "pageSize": 25}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{"testerInfo": []any{map[string]any{"id": "tester-1"}}, "pageInfo": map[string]any{"totalPage": 1}})}}
	code, response, _ := runCLI(t, env, []string{"test", "list-testers"}, transport, nil)
	if code != 0 || transport.requests[0].Header.Get("groupId") != "group-1" {
		t.Fatalf("code=%d headers=%v", code, transport.requests[0].Header)
	}
	if transport.requests[0].URL.Query().Get("groupId") != "" {
		t.Fatalf("groupId leaked into query: %s", transport.requests[0].URL)
	}
	if response["data"].(map[string]any)["testerInfo"].([]any)[0].(map[string]any)["id"] != "tester-1" {
		t.Fatalf("response = %#v", response)
	}
}

func TestAddPackageMapsSinglePkgVersion(t *testing.T) {
	env := newTestEnv(t, requestBody("TEST_PACKAGE_ADD", map[string]any{"fileName": "qianshou.app", "objectId": "obj-1"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{"pkgVersion": []any{map[string]any{"pkgId": "pkg-1"}}})}}
	code, response, _ := runCLI(t, env, []string{"test", "add-package"}, transport, nil)
	if code != 0 || response["externalIds"].(map[string]any)["pkgId"] != "pkg-1" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	var sent map[string]any
	_ = json.Unmarshal(transport.bodies[0], &sent)
	if sent["distributeMode"] != float64(2) {
		t.Fatalf("sent = %#v", sent)
	}
}

func TestUpdateVersionBindsGroupInfo(t *testing.T) {
	env := newTestEnv(t, requestBody("TEST_VERSION_UPDATE", map[string]any{"versionId": "v1", "pkgId": "p1", "groupId": "g1", "startTime": 1000, "endTime": 2000}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{})}}
	code, _, _ := runCLI(t, env, []string{"test", "update-version"}, transport, nil)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var sent map[string]any
	_ = json.Unmarshal(transport.bodies[0], &sent)
	open := sent["openTestInfo"].(map[string]any)
	task := open["testTaskInfo"].(map[string]any)
	group := task["groupInfos"].([]any)[0].(map[string]any)
	if group["groupId"] != "g1" || group["bind"] != float64(1) || task["needShareLink"] != float64(1) {
		t.Fatalf("sent = %#v", sent)
	}
}

func TestCompileStatusRequiresUniqueMatch(t *testing.T) {
	env := newTestEnv(t, requestBody("PACKAGE_COMPILE_STATUS", map[string]any{"pkgId": "pkg-1"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{"pkgStateList": []any{map[string]any{"pkgId": "pkg-1", "successStatus": 1}}})}}
	code, response, _ := runCLI(t, env, []string{"package", "compile-status"}, transport, nil)
	if code != 0 || response["remoteState"].(map[string]any)["successStatus"] != float64(1) {
		t.Fatalf("code=%d response=%#v", code, response)
	}
}

func TestGetVersionKeepsShareLinkOutOfStderr(t *testing.T) {
	env := newTestEnv(t, requestBody("TEST_VERSION_GET", map[string]any{"versionId": "v1"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{"appInfo": map[string]any{"releaseState": 0, "shareLink": "https://share.example/secret"}})}}
	code, response, stderr := runCLI(t, env, []string{"test", "get-version"}, transport, nil)
	if code != 0 || response["data"].(map[string]any)["shareLink"] != "https://share.example/secret" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	if strings.Contains(stderr, "share.example") {
		t.Fatalf("share link leaked: %s", stderr)
	}
}

func TestStopAndDeleteTreatMissingAsAlreadySatisfied(t *testing.T) {
	for _, test := range []struct {
		args      []string
		operation string
	}{{[]string{"test", "stop-version"}, "TEST_VERSION_STOP"}, {[]string{"test", "delete-version"}, "TEST_VERSION_DELETE"}} {
		env := newTestEnv(t, requestBody(test.operation, map[string]any{"versionId": "v1"}))
		transport := &scriptedTransport{responses: []*http.Response{jsonResponse(404, map[string]any{})}}
		code, response, _ := runCLI(t, env, test.args, transport, nil)
		if code != 0 || response["operation"] != test.operation || response["remoteState"].(map[string]any)["alreadySatisfied"] != true {
			t.Fatalf("code=%d response=%#v", code, response)
		}
		if len(transport.requests) != 1 {
			t.Fatalf("expected only query, got %d requests", len(transport.requests))
		}
	}
}

func rewriteRequest(t *testing.T, env testEnv, body map[string]any) testEnv {
	t.Helper()
	raw, _ := json.Marshal(body)
	if err := os.WriteFile(env.requestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return env
}

func sha256Text(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func formatNumber(value float64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
