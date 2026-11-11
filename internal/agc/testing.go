package agc

import (
	"context"

	"github.com/LiangYaJie00/harmony-agcctl/internal/apperr"
)

var stoppedStates = map[int64]struct{}{2: {}, 10: {}}

// CreateVersion creates the AppTest draft and saves its versionId.
func CreateVersion(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	testDesc, ok := body["testDesc"].(string)
	if !ok || len([]rune(testDesc)) < 1 || len([]rune(testDesc)) > 50 {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "TEST_DESC_INVALID", "testDesc 必须是 1 到 50 个字符")
	}
	selfDetectValue, present := body["onshelfSelfDetect"]
	if !present {
		selfDetectValue = int64(0)
	}
	selfDetect, valid := StrictInt(selfDetectValue)
	if !valid || (selfDetect != 0 && selfDetect != 1) {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "SELF_DETECT_INVALID", "onshelfSelfDetect 只能是 0 或 1，缺省不开启上架自检")
	}
	exchange, err := session.Call(ctx, "POST", "/api/publish/v2/test/app/version", CallOptions{
		Params: Query("appId", session.Settings.AppID),
		JSONBody: map[string]any{
			"releaseType":       session.Settings.TestReleaseType,
			"testType":          session.Settings.TestType,
			"testDesc":          testDesc,
			"onshelfSelfDetect": selfDetect,
		},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	versionID, err := requiredID(exchange.Body, "versionId")
	if err != nil {
		return nil, err
	}
	return result(exchange, map[string]string{"versionId": versionID}, map[string]any{
		"releaseState":      releaseState(exchange.Body),
		"onshelfSelfDetect": selfDetect,
	}, nil), nil
}

// AddPackage selects the uploaded object into the AppTest draft.
func AddPackage(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	for key := range body {
		if key != "versionId" && key != "fileName" && key != "objectId" {
			return nil, apperr.New(apperr.ExitLocalPrecondition, "TEST_PACKAGE_FIELDS", "添加软件包只接受 fileName 和 objectId")
		}
	}
	fileName, fileOK := String(body["fileName"])
	objectID, objectOK := String(body["objectId"])
	if !fileOK || !objectOK {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "TEST_PACKAGE_FIELDS", "添加软件包只接受 fileName 和 objectId")
	}
	exchange, err := session.Call(ctx, "POST", "/api/publish/v2/test/version/pkg", CallOptions{
		Params: Query("appId", session.Settings.AppID),
		JSONBody: map[string]any{
			"distributeMode": 2,
			"file":           map[string]any{"fileName": fileName, "objectId": objectID},
		},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	pkgID, err := singlePackageVersion(exchange.Body)
	if err != nil {
		return nil, err
	}
	return result(exchange, map[string]string{"pkgId": pkgID}, nil, nil), nil
}

// CompileStatus queries one package and preserves processing state.
func CompileStatus(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	pkgValue, present := body["pkgId"]
	if !present {
		return nil, requestInvalid("缺少字段 pkgId")
	}
	pkgID := PyString(pkgValue)
	exchange, err := session.Call(ctx, "GET", "/api/publish/v3/package/compile/status", CallOptions{
		Params:     Query("appId", session.Settings.AppID, "pkgIds", pkgID),
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	states, _ := List(exchange.Body["pkgStateList"])
	matches := make([]map[string]any, 0, 1)
	for _, item := range states {
		state, ok := Object(item)
		if ok && PyString(state["pkgId"]) == pkgID {
			matches = append(matches, state)
		}
	}
	if len(matches) != 1 {
		return nil, apperr.New(apperr.ExitNonRetryable, "PACKAGE_STATE_NOT_UNIQUE", "编译状态没有唯一匹配的软件包")
	}
	status, valid := StrictInt(matches[0]["successStatus"])
	if !valid || status < 0 || status > 2 {
		return nil, apperr.New(apperr.ExitNonRetryable, "PACKAGE_STATE_UNKNOWN", "编译状态取值无法识别")
	}
	return result(exchange, nil, map[string]any{"pkgId": pkgID, "successStatus": status}, map[string]any{"packageState": matches[0]}), nil
}

// UpdateVersion binds the draft, package, group, and test window.
func UpdateVersion(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	var groupIDs []string
	groupValue, hasGroupIDs := body["groupIds"]
	if hasGroupIDs && groupValue != nil {
		raw, ok := List(groupValue)
		if !ok {
			return nil, apperr.New(apperr.ExitLocalPrecondition, "TEST_GROUP_REQUIRED", "更新测试版本必须绑定测试群组")
		}
		for _, item := range raw {
			text, valid := item.(string)
			if !valid || text == "" {
				return nil, apperr.New(apperr.ExitLocalPrecondition, "TEST_GROUP_REQUIRED", "更新测试版本必须绑定测试群组")
			}
			groupIDs = append(groupIDs, text)
		}
	} else if groupID, ok := String(body["groupId"]); ok {
		groupIDs = []string{groupID}
	}
	if len(groupIDs) == 0 {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "TEST_GROUP_REQUIRED", "更新测试版本必须绑定测试群组")
	}
	startTime, startOK := StrictInt(body["startTime"])
	endTime, endOK := StrictInt(body["endTime"])
	if !startOK || !endOK || endTime <= startTime {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "TEST_WINDOW_INVALID", "测试开始和结束时间必须是毫秒时间戳，且结束时间晚于开始时间")
	}
	versionID, present := body["versionId"]
	if !present {
		return nil, requestInvalid("缺少字段 versionId")
	}
	pkgID, present := body["pkgId"]
	if !present {
		return nil, requestInvalid("缺少字段 pkgId")
	}
	groupInfos := make([]any, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		groupInfos = append(groupInfos, map[string]any{"groupId": groupID, "bind": 1})
	}
	exchange, err := session.Call(ctx, "PUT", "/api/publish/v2/test/app/version", CallOptions{
		Params: Query("appId", session.Settings.AppID),
		JSONBody: map[string]any{
			"versionId": versionID,
			"pkgId":     pkgID,
			"openTestInfo": map[string]any{
				"startTime": startTime,
				"endTime":   endTime,
				"testTaskInfo": map[string]any{
					"groupInfos":    groupInfos,
					"needShareLink": 1,
					"displayArea":   "1",
					"needNotify":    0,
				},
			},
		},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, nil, map[string]any{"needShareLink": 1, "displayArea": "1", "needNotify": 0}, nil), nil
}

// SubmitVersion submits the AppTest draft.
func SubmitVersion(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	versionID, present := body["versionId"]
	if !present {
		return nil, requestInvalid("缺少字段 versionId")
	}
	exchange, err := session.Call(ctx, "POST", "/api/publish/v2/test/app/version/submit", CallOptions{
		Params:     Query("appId", session.Settings.AppID),
		JSONBody:   map[string]any{"versionId": versionID},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, nil, map[string]any{"releaseState": releaseState(exchange.Body)}, nil), nil
}

// ListGroups queries internal (default) or external test groups.
func ListGroups(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	for key := range body {
		if key != "groupType" {
			return nil, apperr.New(apperr.ExitLocalPrecondition, "GROUP_TYPE_INVALID", "groupType 只能是 0 或 1，缺省查询内部群组")
		}
	}
	groupType := int64(1)
	if value, present := body["groupType"]; present {
		parsed, valid := StrictInt(value)
		if !valid || (parsed != 0 && parsed != 1) {
			return nil, apperr.New(apperr.ExitLocalPrecondition, "GROUP_TYPE_INVALID", "groupType 只能是 0 或 1，缺省查询内部群组")
		}
		groupType = parsed
	}
	exchange, err := session.Call(ctx, "GET", "/api/app-test/v1/test-group/list", CallOptions{
		Params:     Query("appId", session.Settings.AppID, "groupType", groupType),
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	groups := firstTruthy(exchange.Body["groups"], exchange.Body["groupList"], []any{})
	return result(exchange, nil, map[string]any{"groupType": groupType}, map[string]any{"groups": groups}), nil
}

// ListTesters queries one page of a test group.
func ListTesters(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	groupID, groupOK := String(body["groupId"])
	current, currentOK := StrictInt(body["current"])
	pageSize, sizeOK := StrictInt(body["pageSize"])
	if !groupOK || !currentOK || current < 1 || !sizeOK || pageSize < 1 {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "TESTER_QUERY_INVALID", "查询测试成员需要 groupId、current 和 pageSize")
	}
	exchange, err := session.Call(ctx, "GET", "/api/app-test/v1/test-user", CallOptions{
		Params:     Query("appId", session.Settings.AppID, "current", current, "pageSize", pageSize),
		Headers:    map[string]string{"groupId": groupID},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	testers := firstTruthy(exchange.Body["testerInfo"], []any{})
	pageInfo := firstTruthy(exchange.Body["pageInfo"], map[string]any{})
	return result(exchange, nil, nil, map[string]any{"testerInfo": testers, "pageInfo": pageInfo}), nil
}

// Notify sends the official AppTest invitation.
func Notify(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	testerValues, present := body["testerIds"]
	if !present {
		return nil, requestInvalid("缺少字段 testerIds")
	}
	testerIDs, ok := List(testerValues)
	if !ok || len(testerIDs) == 0 || len(testerIDs) > 10000 {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "TESTER_IDS_INVALID", "testerIds 必须是 1 到 10000 个成员 ID")
	}
	groupID, present := body["groupId"]
	if !present {
		return nil, requestInvalid("缺少字段 groupId")
	}
	exchange, err := session.Call(ctx, "POST", session.Settings.NotifyPath, CallOptions{
		JSONBody:   map[string]any{"groupId": groupID, "testerIds": testerIDs},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, nil, map[string]any{"notified": true}, map[string]any{"acceptedCount": len(testerIDs)}), nil
}

// GetVersion returns AppTest release state and share link.
func GetVersion(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	versionValue, present := body["versionId"]
	if !present {
		return nil, requestInvalid("缺少字段 versionId")
	}
	versionID := PyString(versionValue)
	exchange, err := session.Call(ctx, "GET", "/api/publish/v3/app-info", CallOptions{
		Params: Query(
			"appId", session.Settings.AppID,
			"releaseType", session.Settings.TestReleaseType,
			"versionId", versionID,
		),
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	appInfo, _ := Object(exchange.Body["appInfo"])
	var release any
	if appInfo != nil {
		release = appInfo["releaseState"]
	}
	var shareLink any
	if appInfo != nil {
		shareLink = appInfo["shareLink"]
	}
	return result(exchange, nil, map[string]any{"releaseState": release, "versionId": versionID}, map[string]any{"shareLink": shareLink}), nil
}

// StopVersion queries first and avoids repeating an already-satisfied stop.
func StopVersion(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	versionValue, present := body["versionId"]
	if !present {
		return nil, requestInvalid("缺少字段 versionId")
	}
	versionID := PyString(versionValue)
	current, err := queryVersion(ctx, session, versionID)
	if err != nil {
		return nil, err
	}
	if current.StatusCode != nil && *current.StatusCode == 404 {
		return already("TEST_VERSION_STOP", versionID, "测试版本已不存在"), nil
	}
	if state, ok := StrictInt(releaseState(current.Body)); ok {
		if _, stopped := stoppedStates[state]; stopped {
			return already("TEST_VERSION_STOP", versionID, "测试版本已经停止"), nil
		}
	}
	exchange, err := session.Call(ctx, "POST", "/api/publish/v2/test/version/stop", CallOptions{
		Params:     Query("appId", session.Settings.AppID),
		JSONBody:   map[string]any{"versionId": versionID},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, map[string]string{"versionId": versionID}, map[string]any{"stopped": true}, nil), nil
}

// DeleteVersion queries first and keeps HTTP 404 idempotent.
func DeleteVersion(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	versionValue, present := body["versionId"]
	if !present {
		return nil, requestInvalid("缺少字段 versionId")
	}
	versionID := PyString(versionValue)
	current, err := queryVersion(ctx, session, versionID)
	if err != nil {
		return nil, err
	}
	if current.StatusCode != nil && *current.StatusCode == 404 {
		return already("TEST_VERSION_DELETE", versionID, "测试版本已不存在"), nil
	}
	exchange, err := session.Call(ctx, "DELETE", "/api/publish/v2/test/app/version", CallOptions{
		Params:     Query("appId", session.Settings.AppID, "versionId", versionID),
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, map[string]string{"versionId": versionID}, map[string]any{"deleted": true}, nil), nil
}

func queryVersion(ctx context.Context, session *Session, versionID string) (*Exchange, error) {
	exchange, err := session.Call(ctx, "GET", "/api/publish/v3/app-info", CallOptions{
		Params: Query(
			"appId", session.Settings.AppID,
			"releaseType", session.Settings.TestReleaseType,
			"versionId", versionID,
		),
		ExpectJSON: false,
	})
	if err != nil {
		return nil, err
	}
	if exchange.StatusCode != nil && *exchange.StatusCode == 404 {
		return exchange, nil
	}
	if classified := Classify("GET", exchange); classified != nil {
		return nil, classified
	}
	return exchange, nil
}

func singlePackageVersion(body map[string]any) (string, error) {
	values, ok := List(body["pkgVersion"])
	if !ok || len(values) != 1 {
		return "", apperr.New(apperr.ExitNonRetryable, "PKG_VERSION_NOT_UNIQUE", "pkgVersion 必须恰好有一个元素")
	}
	if text, ok := String(values[0]); ok {
		return text, nil
	}
	if item, ok := Object(values[0]); ok {
		for _, key := range []string{"pkgId", "id"} {
			if text, ok := String(item[key]); ok {
				return text, nil
			}
		}
	}
	return "", apperr.New(apperr.ExitNonRetryable, "PKG_VERSION_NOT_UNIQUE", "pkgVersion[0] 不是软件包 ID")
}

func requiredID(body map[string]any, key string) (string, error) {
	if text, ok := String(body[key]); ok {
		return text, nil
	}
	if nested, ok := Object(body["appInfo"]); ok {
		if text, ok := String(nested[key]); ok {
			return text, nil
		}
	}
	return "", apperr.New(apperr.ExitNonRetryable, "EXTERNAL_ID_MISSING", "响应缺少 "+key)
}

func releaseState(body map[string]any) any {
	if appInfo, ok := Object(body["appInfo"]); ok {
		if value, present := appInfo["releaseState"]; present {
			return value
		}
	}
	return body["releaseState"]
}

func already(operation, versionID, message string) *Result {
	return &Result{
		Operation:   operation,
		ExternalIDs: map[string]string{"versionId": versionID},
		RemoteState: map[string]any{"alreadySatisfied": true},
		Data:        map[string]any{"message": message},
	}
}

func firstTruthy(values ...any) any {
	for _, value := range values {
		if Truthy(value) {
			return value
		}
	}
	if len(values) > 0 {
		return values[len(values)-1]
	}
	return nil
}
