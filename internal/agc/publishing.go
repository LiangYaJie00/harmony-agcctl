package agc

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/LiangYaJie00/harmony-agcctl/internal/apperr"
)

var phasePercent = map[int64]int{1: 1, 2: 2, 3: 5, 4: 10, 5: 20, 6: 50, 7: 100}

var trailingZone = regexp.MustCompile(`(?i)(Z|[+-]\d{2}:?\d{2})$`)

// GetAppInfo reads the current formal version and phased-release state.
func GetAppInfo(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	params := Query("appId", session.Settings.AppID)
	if versionID, ok := String(body["publishVersionId"]); ok {
		params.Set("versionId", versionID)
	}
	exchange, err := session.Call(ctx, "GET", "/api/publish/v3/app-info", CallOptions{Params: params, ExpectJSON: true})
	if err != nil {
		return nil, err
	}
	appInfo, _ := Object(exchange.Body["appInfo"])
	if appInfo == nil {
		appInfo = map[string]any{}
	}
	remote := map[string]any{
		"releaseState": appInfo["releaseState"],
		"releasePhase": appInfo["releasePhase"],
	}
	data := map[string]any{}
	phaseSource := appInfo
	if _, ok := Object(exchange.Body["phasedReleaseInfo"]); ok {
		phaseSource = exchange.Body
	}
	attachPhase(phaseSource, remote, data)
	external := map[string]string{}
	if versionID, ok := String(appInfo["versionId"]); ok {
		external["publishVersionId"] = versionID
	}
	return result(exchange, external, remote, data), nil
}

// UpdatePackage associates the uploaded object with the formal draft.
func UpdatePackage(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	fileName, fileOK := String(body["fileName"])
	objectID, objectOK := String(body["objectId"])
	if !sameKeys(body, "fileName", "objectId", "releasePhase") || !fileOK || !objectOK {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "PUBLISH_PACKAGE_FIELDS", "更新正式软件包需要 fileName、objectId 和 releasePhase")
	}
	phase, err := releasePhase(body)
	if err != nil {
		return nil, err
	}
	exchange, err := session.Call(ctx, "PUT", "/api/publish/v3/app-package-info", CallOptions{
		Params: Query(
			"appId", session.Settings.AppID,
			"releaseType", 1,
			"releasePhase", phase,
		),
		JSONBody:   map[string]any{"fileName": fileName, "objectId": objectID},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	packageID, ok := String(exchange.Body["packageId"])
	if !ok {
		return nil, apperr.New(apperr.ExitNonRetryable, "EXTERNAL_ID_MISSING", "响应缺少 packageId")
	}
	return result(exchange, map[string]string{"packageId": packageID}, map[string]any{"releasePhase": phase}, nil), nil
}

// UpdateLanguage writes the default-language release notes.
func UpdateLanguage(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	if !sameKeys(body, "newFeatures", "releasePhase") {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "PUBLISH_LANGUAGE_FIELDS", "更新语言信息需要 newFeatures 和 releasePhase")
	}
	features := body["newFeatures"]
	phase, err := releasePhase(body)
	if err != nil {
		return nil, err
	}
	if err := checkLength(features, "newFeatures", true, 1, 500); err != nil {
		return nil, err
	}
	exchange, err := session.Call(ctx, "PUT", "/api/publish/v3/app-language-info", CallOptions{
		Params: Query(
			"appId", session.Settings.AppID,
			"releaseType", 1,
			"releasePhase", phase,
		),
		JSONBody:   map[string]any{"lang": session.Settings.DefaultLanguage, "newFeatures": features},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, nil, map[string]any{"language": session.Settings.DefaultLanguage, "releasePhase": phase}, nil), nil
}

// CancelReview cancels review for one formal publishVersionId.
func CancelReview(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	versionID, ok := String(body["publishVersionId"])
	if !sameKeys(body, "publishVersionId") || !ok {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "PUBLISH_VERSION_ID_REQUIRED", "撤销审核只接受正式 publishVersionId")
	}
	exchange, err := session.Call(ctx, "PUT", "/api/publish/v3/version/on-shelf/cancel", CallOptions{
		JSONBody:   map[string]any{"versionId": versionID},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, map[string]string{"publishVersionId": versionID}, map[string]any{"cancelled": true}, nil), nil
}

// Submit either validates a dry-run payload or sends the formal review request.
func Submit(ctx context.Context, session *Session, request map[string]any, dryRun bool) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	payload, err := submitPayload(body)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &Result{ExternalIDs: map[string]string{}, RemoteState: map[string]any{"dryRun": true}, Data: map[string]any{"request": payload}}, nil
	}
	exchange, err := session.Call(ctx, "POST", "/api/publish/v3/app-submit", CallOptions{
		Params:     Query("appId", session.Settings.AppID),
		JSONBody:   payload,
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, nil, map[string]any{"submitted": true}, nil), nil
}

// ListVersions reports all rows but only saves a unique formal candidate.
func ListVersions(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	exchange, err := session.Call(ctx, "POST", "/api/publish/v3/version/brief-info/list", CallOptions{
		JSONBody:   map[string]any{"appId": session.Settings.AppID},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	versions := firstTruthy(exchange.Body["versionList"], exchange.Body["versions"], []any{})
	versionList, ok := List(versions)
	if !ok {
		versionList = []any{}
	}
	versionCode, hasVersionCode := body["versionCode"]
	hasVersionCode = hasVersionCode && versionCode != nil
	matched := matchVersions(versionList, versionCode, hasVersionCode)
	remote := map[string]any{}
	if hasVersionCode {
		remote["candidateCount"] = len(matched)
	} else {
		remote["candidateCount"] = len(versionList)
	}
	data := map[string]any{"versions": versionList}
	external := map[string]string{}
	if len(matched) == 1 {
		if versionID, ok := String(matched[0]["versionId"]); ok {
			external["publishVersionId"] = versionID
		}
		attachPhase(matched[0], remote, data)
	} else if hasVersionCode && len(matched) > 1 {
		remote["multipleCandidates"] = true
	}
	return result(exchange, external, remote, data), nil
}

// GetPackageInfo reconciles a formal package by packageId.
func GetPackageInfo(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	packageID, ok := String(body["packageId"])
	if !sameKeys(body, "packageId") || !ok {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "PUBLISH_PACKAGE_ID_REQUIRED", "查询正式软件包需要 packageId")
	}
	exchange, err := session.Call(ctx, "GET", "/api/publish/v2/app-package-info", CallOptions{
		Params:     Query("packageId", packageID),
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	packageInfo, ok := Object(exchange.Body["packageInfo"])
	if !ok {
		return nil, apperr.New(apperr.ExitNonRetryable, "PACKAGE_INFO_MISSING", "响应缺少 packageInfo")
	}
	return result(exchange, map[string]string{"packageId": packageID}, nil, map[string]any{"packageInfo": packageInfo}), nil
}

// PromoteFull requests full release only after explicit platform confirmation.
func PromoteFull(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	confirmed, ok := body["confirmed"].(bool)
	if !ok || !confirmed {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "PROMOTE_NOT_CONFIRMED", "转全网必须由平台确认后单独调用")
	}
	if Truthy(body["versionMismatch"]) || Truthy(body["resultUnknown"]) || Truthy(body["paused"]) || Truthy(body["cancelled"]) {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "PROMOTE_BLOCKED", "当前版本状态不允许转全网")
	}
	versionID, versionOK := String(body["publishVersionId"])
	_, hasVersionID := body["versionId"]
	_, hasPackageID := body["packageId"]
	if !versionOK || hasVersionID || hasPackageID {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "PUBLISH_VERSION_ID_REQUIRED", "转全网只接受正式 publishVersionId")
	}
	exchange, err := session.Call(ctx, "PUT", "/api/publish/v2/version/phased-release", CallOptions{
		JSONBody:   map[string]any{"versionId": versionID, "releasePhase": 4},
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	return result(exchange, map[string]string{"publishVersionId": versionID}, map[string]any{"releasePhase": 4, "accepted": true}, nil), nil
}

func releasePhase(body map[string]any) (int64, error) {
	phase, ok := StrictInt(body["releasePhase"])
	if !ok || (phase != 0 && phase != 3) {
		return 0, apperr.New(apperr.ExitLocalPrecondition, "RELEASE_PHASE_INVALID", "releasePhase 只能是 0（全网）或 3（7天分阶段）")
	}
	return phase, nil
}

func submitPayload(body map[string]any) (map[string]any, error) {
	mode, _ := body["releaseMode"].(string)
	var phase int
	if mode == "FULL" {
		phase = 0
		if Truthy(body["phasedReleaseDescription"]) {
			return nil, apperr.New(apperr.ExitLocalPrecondition, "PHASED_DESCRIPTION_FORBIDDEN", "全网发布不能携带分阶段说明")
		}
	} else if mode == "PHASED" {
		phase = 3
		if err := checkLength(body["phasedReleaseDescription"], "phasedReleaseDescription", true, 1, 500); err != nil {
			return nil, err
		}
	} else {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "RELEASE_MODE_INVALID", "发布方式必须是 FULL 或 PHASED")
	}
	payload := map[string]any{"releaseType": 1, "releasePhase": phase}
	if mode == "PHASED" {
		payload["phasedReleaseDescription"] = body["phasedReleaseDescription"]
	}
	schedule, _ := body["schedule"].(string)
	if schedule == "IMMEDIATE" {
		if Truthy(body["releaseTime"]) {
			return nil, apperr.New(apperr.ExitLocalPrecondition, "RELEASE_TIME_FORBIDDEN", "立即发布不能携带预约时间")
		}
	} else if schedule == "SCHEDULED" {
		releaseTime, err := futureReleaseTime(body["releaseTime"])
		if err != nil {
			return nil, err
		}
		payload["releaseTime"] = releaseTime
	} else {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "SCHEDULE_INVALID", "发布时间必须是 IMMEDIATE 或 SCHEDULED")
	}
	if remark, present := body["remark"]; present && remark != nil && remark != "" {
		if err := checkLength(remark, "remark", true, 10, 300); err != nil {
			return nil, err
		}
		payload["remark"] = remark
	}
	return payload, nil
}

func futureReleaseTime(value any) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", apperr.New(apperr.ExitLocalPrecondition, "RELEASE_TIME_INVALID", "预约时间必须带时区")
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05-0700",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05-0700",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	var parsed time.Time
	var err error
	for _, layout := range layouts {
		parsed, err = time.Parse(layout, text)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", apperr.New(apperr.ExitLocalPrecondition, "RELEASE_TIME_INVALID", "预约时间格式无效")
	}
	if !trailingZone.MatchString(text) {
		return "", apperr.New(apperr.ExitLocalPrecondition, "RELEASE_TIME_INVALID", "预约时间必须带时区且晚于当前时间")
	}
	if !parsed.After(time.Now().UTC()) {
		return "", apperr.New(apperr.ExitLocalPrecondition, "RELEASE_TIME_INVALID", "预约时间必须带时区且晚于当前时间")
	}
	return text, nil
}

func checkLength(value any, field string, required bool, minimum, maximum int) error {
	text, ok := value.(string)
	if !ok || text == "" {
		if required {
			return apperr.New(apperr.ExitLocalPrecondition, strings.ToUpper(field)+"_INVALID", field+" 不能为空")
		}
		return nil
	}
	length := len([]rune(text))
	if length < minimum || length > maximum {
		return apperr.New(apperr.ExitLocalPrecondition, strings.ToUpper(field)+"_INVALID", field+" 长度必须是 "+itoa(minimum)+" 到 "+itoa(maximum))
	}
	return nil
}

func attachPhase(source, remote, data map[string]any) {
	info, ok := Object(source["phasedReleaseInfo"])
	if !ok {
		return
	}
	remote["phasedReleaseInfo"] = info
	if day, ok := StrictInt(info["currentPhaseDay"]); ok {
		if percent, known := phasePercent[day]; known {
			data["phasePercent"] = percent
		}
	}
}

func matchVersions(versions []any, versionCode any, hasVersionCode bool) []map[string]any {
	matched := make([]map[string]any, 0)
	for _, value := range versions {
		item, ok := Object(value)
		if !ok {
			continue
		}
		if !hasVersionCode {
			matched = append(matched, item)
			continue
		}
		if isPublishVersion(item) && versionCodeMatches(item, versionCode) {
			matched = append(matched, item)
		}
	}
	return matched
}

func isPublishVersion(item map[string]any) bool {
	value, present := item["releaseType"]
	return !present || value == nil || Equal(value, 1)
}

func versionCodeMatches(item map[string]any, versionCode any) bool {
	if Equal(item["versionCode"], versionCode) {
		return true
	}
	packages, ok := List(item["packageList"])
	if !ok {
		return false
	}
	for _, value := range packages {
		pkg, ok := Object(value)
		if ok && Equal(pkg["versionCode"], versionCode) {
			return true
		}
	}
	return false
}

func sameKeys(body map[string]any, keys ...string) bool {
	if len(body) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := body[key]; !ok {
			return false
		}
	}
	return true
}

func itoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	position := len(buf)
	for value > 0 {
		position--
		buf[position] = digits[value%10]
		value /= 10
	}
	return string(buf[position:])
}
