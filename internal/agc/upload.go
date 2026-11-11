package agc

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/LiangYaJie00/harmony-agcctl/internal/apperr"
	"github.com/LiangYaJie00/harmony-agcctl/internal/artifact"
)

const uploadURLPath = "/api/publish/v2/upload-url/for-obs"

// UploadPackage verifies and streams one APP Pack to the signed OBS target.
func UploadPackage(ctx context.Context, session *Session, request map[string]any) (*Result, error) {
	body, err := Body(request)
	if err != nil {
		return nil, err
	}
	artifactPath, err := RequiredString(body, "artifactPath")
	if err != nil {
		return nil, err
	}
	taskID := PyString(request["taskId"])
	path, err := artifact.Resolve(session.Settings.ArtifactRoot, taskID, artifactPath)
	if err != nil {
		return nil, err
	}
	size, digest, err := artifact.SizeAndSHA256(path)
	if err != nil {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_MISSING", "APP Pack 文件不存在")
	}
	contentLength, present := body["contentLength"]
	if !present {
		return nil, requestInvalid("缺少字段 contentLength")
	}
	expectedLength, numeric := AsNumber(contentLength)
	if !numeric || expectedLength != float64(size) {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_HASH_MISMATCH", "APP Pack 大小或 SHA-256 与请求不一致")
	}
	digestValue, present := body["sha256"]
	if !present || strings.ToLower(PyString(digestValue)) != digest {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_HASH_MISMATCH", "APP Pack 大小或 SHA-256 与请求不一致")
	}
	fileName, err := RequiredString(body, "fileName")
	if err != nil {
		return nil, err
	}
	if filepath.Base(path) != fileName {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_NAME_MISMATCH", "APP Pack 文件名与请求不一致")
	}

	exchange, err := session.Call(ctx, "GET", uploadURLPath, CallOptions{
		Params: Query(
			"appId", session.Settings.AppID,
			"fileName", fileName,
			"contentLength", size,
			"sha256", digest,
		),
		ExpectJSON: true,
	})
	if err != nil {
		return nil, err
	}
	target, headers, objectID, err := uploadTarget(exchange.Body)
	if err != nil {
		return nil, err
	}
	if err := requireHTTPS(target); err != nil {
		return nil, err
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, apperr.New(apperr.ExitLocalPrecondition, "ARTIFACT_MISSING", "APP Pack 文件不存在")
	}
	defer handle.Close()
	put := session.SendRaw(ctx, "PUT", target, headers, handle, size, 0)
	if classified := Classify("PUT", put); classified != nil {
		return nil, classified
	}
	if put.StatusCode == nil || *put.StatusCode != 200 {
		return nil, apperr.New(apperr.ExitNonRetryable, "UPLOAD_NOT_ACCEPTED", "上传地址没有返回 HTTP 200")
	}
	requestID := put.RequestID
	if requestID == "" {
		requestID = exchange.RequestID
	}
	var requestIDPtr *string
	if requestID != "" {
		requestIDPtr = &requestID
	}
	return &Result{
		ExternalIDs: map[string]string{"objectId": objectID, "fileName": fileName},
		RemoteState: map[string]any{"uploaded": true},
		Data:        map[string]any{"sha256": digest, "contentLength": size},
		RequestID:   requestIDPtr,
	}, nil
}

func uploadTarget(body map[string]any) (string, map[string]string, string, error) {
	urlInfo, hasURLInfo := Object(body["urlInfo"])
	if !hasURLInfo {
		urlInfo = body
	}
	target, targetOK := String(urlInfo["url"])
	objectID, objectOK := String(urlInfo["objectId"])
	if !objectOK {
		objectID, objectOK = String(body["objectId"])
	}
	headers := map[string]string{}
	if rawHeaders, ok := Object(urlInfo["headers"]); ok {
		for key, value := range rawHeaders {
			headers[key] = PyString(value)
		}
	}
	if !targetOK || !objectOK {
		return "", nil, "", apperr.New(apperr.ExitNonRetryable, "UPLOAD_TARGET_INVALID", "上传地址响应缺少 url 或 objectId")
	}
	return target, headers, objectID, nil
}

func requireHTTPS(target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return apperr.New(apperr.ExitNonRetryable, "UPLOAD_URL_NOT_HTTPS", "上传地址必须是带主机名的 HTTPS URL")
	}
	return nil
}
