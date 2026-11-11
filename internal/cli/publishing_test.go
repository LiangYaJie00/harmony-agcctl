package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestUpdatePackageSendsPhaseQuery(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_PACKAGE_UPDATE", map[string]any{"fileName": "qianshou.app", "objectId": "obj-1", "releasePhase": 3}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{"packageId": "pkg-1"})}}
	code, response, _ := runCLI(t, env, []string{"publish", "update-package"}, transport, nil)
	if code != 0 || response["externalIds"].(map[string]any)["packageId"] != "pkg-1" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	query := transport.requests[0].URL.Query()
	if query.Get("releaseType") != "1" || query.Get("releasePhase") != "3" || query.Get("appId") != testAppID {
		t.Fatalf("query = %v", query)
	}
}

func TestGetPackageInfoUsesOnlyPackageQuery(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_PACKAGE_INFO", map[string]any{"packageId": "pkg-1"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{"packageInfo": map[string]any{"fileName": "qianshou.app"}})}}
	code, response, _ := runCLI(t, env, []string{"publish", "get-package-info"}, transport, nil)
	if code != 0 || response["data"].(map[string]any)["packageInfo"].(map[string]any)["fileName"] != "qianshou.app" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	query := transport.requests[0].URL.Query()
	if len(query) != 1 || query.Get("packageId") != "pkg-1" {
		t.Fatalf("query = %v", query)
	}
}

func TestSubmitDryRunBuildsPayloadWithoutNetwork(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_SUBMIT", map[string]any{"releaseMode": "PHASED", "schedule": "IMMEDIATE", "phasedReleaseDescription": "7天分阶段发布"}))
	transport := &scriptedTransport{}
	code, response, _ := runCLI(t, env, []string{"publish", "submit", "--dry-run"}, transport, nil)
	if code != 0 || len(transport.requests) != 0 {
		t.Fatalf("code=%d requests=%d response=%#v", code, len(transport.requests), response)
	}
	request := response["data"].(map[string]any)["request"].(map[string]any)
	if request["releaseType"] != float64(1) || request["releasePhase"] != float64(3) || request["phasedReleaseDescription"] != "7天分阶段发布" {
		t.Fatalf("request = %#v", request)
	}
	if _, present := request["releaseTime"]; present {
		t.Fatalf("immediate release included releaseTime: %#v", request)
	}
}

func TestSubmitSendsAppIDQuery(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_SUBMIT", map[string]any{"releaseMode": "FULL", "schedule": "IMMEDIATE"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{})}}
	code, response, _ := runCLI(t, env, []string{"publish", "submit"}, transport, nil)
	if code != 0 || response["remoteState"].(map[string]any)["submitted"] != true {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	if transport.requests[0].URL.Query().Get("appId") != testAppID {
		t.Fatalf("url = %s", transport.requests[0].URL)
	}
	var sent map[string]any
	_ = json.Unmarshal(transport.bodies[0], &sent)
	if len(sent) != 2 || sent["releasePhase"] != float64(0) {
		t.Fatalf("sent = %#v", sent)
	}
}

func TestSubmitFullRejectsPhasedDescription(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_SUBMIT", map[string]any{"releaseMode": "FULL", "schedule": "IMMEDIATE", "phasedReleaseDescription": "not allowed"}))
	code, response, _ := runCLI(t, env, []string{"publish", "submit", "--dry-run"}, &scriptedTransport{}, nil)
	if code != 40 || response["error"].(map[string]any)["code"] != "PHASED_DESCRIPTION_FORBIDDEN" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
}

func TestPromoteFullSendsOnlyPublishVersion(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_PROMOTE_FULL", map[string]any{"confirmed": true, "publishVersionId": "pub-1"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{})}}
	code, response, _ := runCLI(t, env, []string{"publish", "promote-full"}, transport, nil)
	if code != 0 || response["remoteState"].(map[string]any)["releasePhase"] != float64(4) {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	var sent map[string]any
	_ = json.Unmarshal(transport.bodies[0], &sent)
	if len(sent) != 2 || sent["versionId"] != "pub-1" || sent["releasePhase"] != float64(4) {
		t.Fatalf("sent = %#v", sent)
	}
}

func TestPromoteFullRequiresExplicitConfirmation(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_PROMOTE_FULL", map[string]any{"publishVersionId": "pub-1"}))
	code, response, _ := runCLI(t, env, []string{"publish", "promote-full"}, &scriptedTransport{}, nil)
	if code != 40 || response["error"].(map[string]any)["code"] != "PROMOTE_NOT_CONFIRMED" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
}

func TestCancelReviewReportsKnownRetCode(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_CANCEL_REVIEW", map[string]any{"publishVersionId": "pub-1"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{"ret": map[string]any{"code": 204144641, "msg": "versionId 为空"}})}}
	code, response, _ := runCLI(t, env, []string{"publish", "cancel-review"}, transport, nil)
	if code != 20 {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	errorValue := response["error"].(map[string]any)
	if errorValue["code"] != "AGC_RET_ERROR" || errorValue["message"] != "无效入参：versionId 为空" {
		t.Fatalf("error = %#v", errorValue)
	}
	if errorValue["detail"] != "204144641: versionId 为空" {
		t.Fatalf("detail = %#v", errorValue["detail"])
	}
}

func TestListVersionsReportsMultipleCandidates(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_VERSION_LIST", map[string]any{"versionCode": 96}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{
		"versionList": []any{map[string]any{"versionId": "a", "versionCode": 96}, map[string]any{"versionId": "b", "versionCode": 96}},
	})}}
	code, response, _ := runCLI(t, env, []string{"publish", "list-versions"}, transport, nil)
	if code != 0 || response["remoteState"].(map[string]any)["multipleCandidates"] != true {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	if _, present := response["externalIds"].(map[string]any)["publishVersionId"]; present {
		t.Fatalf("unexpected publishVersionId: %#v", response["externalIds"])
	}
}

func TestListVersionsMatchesPackageListFormalVersion(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_VERSION_LIST", map[string]any{"versionCode": 103}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{
		"versionList": []any{
			map[string]any{"versionId": "formal-103", "releaseType": 1, "releaseState": 5, "packageList": []any{map[string]any{"versionCode": 103, "packageId": "pkg-103"}}},
			map[string]any{"versionId": "test-103", "releaseType": 6, "releaseState": 10, "packageList": []any{map[string]any{"versionCode": 103, "packageId": "pkg-103"}}},
			map[string]any{"versionId": "formal-102", "releaseType": 1, "releaseState": 0, "packageList": []any{map[string]any{"versionCode": 102}}},
		},
	})}}
	code, response, _ := runCLI(t, env, []string{"publish", "list-versions"}, transport, nil)
	if code != 0 || response["externalIds"].(map[string]any)["publishVersionId"] != "formal-103" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	if response["remoteState"].(map[string]any)["candidateCount"] != float64(1) || len(response["data"].(map[string]any)["versions"].([]any)) != 3 {
		t.Fatalf("response = %#v", response)
	}
}

func TestGetAppInfoMapsRootPhasePercent(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_APP_INFO", map[string]any{"publishVersionId": "pub-1"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{
		"appInfo":           map[string]any{"versionId": "pub-1", "releaseState": 0},
		"phasedReleaseInfo": map[string]any{"state": "SUSPEND", "currentPhaseDay": 5},
	})}}
	code, response, _ := runCLI(t, env, []string{"publish", "get-app-info"}, transport, nil)
	if code != 0 || response["data"].(map[string]any)["phasePercent"] != float64(20) {
		t.Fatalf("code=%d response=%#v", code, response)
	}
	phase := response["remoteState"].(map[string]any)["phasedReleaseInfo"].(map[string]any)
	if phase["currentPhaseDay"] != float64(5) {
		t.Fatalf("phase = %#v", phase)
	}
}

func TestCancelReviewRejectsOtherIDs(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_CANCEL_REVIEW", map[string]any{"publishVersionId": "pub-1", "versionId": "test-1"}))
	code, response, _ := runCLI(t, env, []string{"publish", "cancel-review"}, &scriptedTransport{}, nil)
	if code != 40 || response["error"].(map[string]any)["code"] != "PUBLISH_VERSION_ID_REQUIRED" {
		t.Fatalf("code=%d response=%#v", code, response)
	}
}

func TestSubmitSendsNoPhasedDescriptionForFull(t *testing.T) {
	env := newTestEnv(t, requestBody("PUBLISH_SUBMIT", map[string]any{"releaseMode": "FULL", "schedule": "IMMEDIATE", "remark": "1234567890"}))
	transport := &scriptedTransport{responses: []*http.Response{jsonResponse(200, map[string]any{})}}
	code, _, _ := runCLI(t, env, []string{"publish", "submit"}, transport, nil)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	var sent map[string]any
	_ = json.Unmarshal(transport.bodies[0], &sent)
	if strings.Contains(string(transport.bodies[0]), "phasedReleaseDescription") || sent["remark"] != "1234567890" {
		t.Fatalf("sent = %#v", sent)
	}
}
