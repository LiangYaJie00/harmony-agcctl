// Package cli wires Cobra commands to the agcctl request/response contract.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/LiangYaJie00/harmony-agcctl/internal/agc"
	"github.com/LiangYaJie00/harmony-agcctl/internal/apperr"
	"github.com/LiangYaJie00/harmony-agcctl/internal/config"
	"github.com/LiangYaJie00/harmony-agcctl/internal/contracts"
	"github.com/LiangYaJie00/harmony-agcctl/internal/doctor"
	"github.com/LiangYaJie00/harmony-agcctl/internal/redact"
	"github.com/LiangYaJie00/harmony-agcctl/internal/version"
	"github.com/spf13/cobra"
)

// Dependencies are injectable for unit tests and keep Cobra out of command logic.
type Dependencies struct {
	Transport    agc.Transport
	DoctorClient doctor.AppInfoClient
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	Now          func() time.Time
}

type handler func(context.Context, *agc.Session, map[string]any, bool) (*agc.Result, error)

type commandSpec struct {
	Group     string
	Action    string
	Operation string
	Handler   handler
}

var commandSpecs = []commandSpec{
	{Group: "upload", Action: "package", Operation: "UPLOAD_PACKAGE", Handler: noDryRun(agc.UploadPackage)},
	{Group: "package", Action: "compile-status", Operation: "PACKAGE_COMPILE_STATUS", Handler: noDryRun(agc.CompileStatus)},
	{Group: "test", Action: "create-version", Operation: "TEST_VERSION_CREATE", Handler: noDryRun(agc.CreateVersion)},
	{Group: "test", Action: "add-package", Operation: "TEST_PACKAGE_ADD", Handler: noDryRun(agc.AddPackage)},
	{Group: "test", Action: "update-version", Operation: "TEST_VERSION_UPDATE", Handler: noDryRun(agc.UpdateVersion)},
	{Group: "test", Action: "submit-version", Operation: "TEST_VERSION_SUBMIT", Handler: noDryRun(agc.SubmitVersion)},
	{Group: "test", Action: "list-groups", Operation: "TEST_GROUP_LIST", Handler: noDryRun(agc.ListGroups)},
	{Group: "test", Action: "list-testers", Operation: "TEST_TESTER_LIST", Handler: noDryRun(agc.ListTesters)},
	{Group: "test", Action: "notify", Operation: "TEST_NOTIFY", Handler: noDryRun(agc.Notify)},
	{Group: "test", Action: "get-version", Operation: "TEST_VERSION_GET", Handler: noDryRun(agc.GetVersion)},
	{Group: "test", Action: "stop-version", Operation: "APPTEST_STOP", Handler: noDryRun(agc.StopVersion)},
	{Group: "test", Action: "delete-version", Operation: "APPTEST_DELETE", Handler: noDryRun(agc.DeleteVersion)},
	{Group: "publish", Action: "get-app-info", Operation: "PUBLISH_APP_INFO", Handler: noDryRun(agc.GetAppInfo)},
	{Group: "publish", Action: "update-package", Operation: "PUBLISH_PACKAGE_UPDATE", Handler: noDryRun(agc.UpdatePackage)},
	{Group: "publish", Action: "update-language", Operation: "PUBLISH_LANGUAGE_UPDATE", Handler: noDryRun(agc.UpdateLanguage)},
	{Group: "publish", Action: "submit", Operation: "PUBLISH_SUBMIT", Handler: submitHandler},
	{Group: "publish", Action: "cancel-review", Operation: "PUBLISH_CANCEL_REVIEW", Handler: noDryRun(agc.CancelReview)},
	{Group: "publish", Action: "list-versions", Operation: "PUBLISH_VERSION_LIST", Handler: noDryRun(agc.ListVersions)},
	{Group: "publish", Action: "get-package-info", Operation: "PUBLISH_PACKAGE_INFO", Handler: noDryRun(agc.GetPackageInfo)},
	{Group: "publish", Action: "promote-full", Operation: "PUBLISH_PROMOTE_FULL", Handler: noDryRun(agc.PromoteFull)},
}

func noDryRun(fn func(context.Context, *agc.Session, map[string]any) (*agc.Result, error)) handler {
	return func(ctx context.Context, session *agc.Session, request map[string]any, _ bool) (*agc.Result, error) {
		return fn(ctx, session, request)
	}
}

func submitHandler(ctx context.Context, session *agc.Session, request map[string]any, dryRun bool) (*agc.Result, error) {
	return agc.Submit(ctx, session, request, dryRun)
}

// Run executes one CLI invocation and returns the contract exit code.
func Run(args []string, deps Dependencies) int {
	if deps.Stdin == nil {
		deps.Stdin = os.Stdin
	}
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	runner := &runner{deps: deps, exitCode: 0}
	root := runner.rootCommand()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(deps.Stderr, err)
		return 2
	}
	return runner.exitCode
}

type runner struct {
	deps     Dependencies
	exitCode int
}

func (r *runner) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "agcctl",
		Short:         "AppGallery Connect one-request publish CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(r.deps.Stdout)
	root.SetErr(r.deps.Stderr)
	root.Version = version.Version
	root.SetVersionTemplate("{{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true

	rootDoctor := &cobra.Command{
		Use:   "doctor",
		Short: "检查配置、私钥、JWT 和华为接口连通性",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, _ := cmd.Flags().GetString("config")
			requestPath, _ := cmd.Flags().GetString("request-file")
			r.exitCode = r.executeDoctor(configPath, requestPath)
			return nil
		},
	}
	addCommonFlags(rootDoctor)
	root.AddCommand(rootDoctor)

	groups := map[string]*cobra.Command{}
	for _, spec := range commandSpecs {
		group, ok := groups[spec.Group]
		if !ok {
			group = &cobra.Command{Use: spec.Group}
			groups[spec.Group] = group
			root.AddCommand(group)
		}
		current := spec
		leaf := &cobra.Command{
			Use: current.Action,
			RunE: func(cmd *cobra.Command, args []string) error {
				configPath, _ := cmd.Flags().GetString("config")
				requestPath, _ := cmd.Flags().GetString("request-file")
				dryRun := false
				if current.Operation == "PUBLISH_SUBMIT" {
					dryRun, _ = cmd.Flags().GetBool("dry-run")
				}
				r.exitCode = r.executeAPI(current, configPath, requestPath, dryRun)
				return nil
			},
		}
		addCommonFlags(leaf)
		if current.Operation == "PUBLISH_SUBMIT" {
			leaf.Flags().Bool("dry-run", false, "只检查请求，不访问华为")
		}
		group.AddCommand(leaf)
	}
	return root
}

func addCommonFlags(command *cobra.Command) {
	command.Flags().String("config", "", "config.yaml 路径")
	command.Flags().String("request-file", "", "请求 JSON 路径；缺省时从标准输入读取")
	_ = command.MarkFlagRequired("config")
}

func (r *runner) executeDoctor(configPath, requestPath string) int {
	started := r.deps.Now()
	operationKey := "UNKNOWN"
	request, err := r.readRequest(requestPath)
	if err != nil {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "REQUEST_INVALID", err.Error(), "DOCTOR", nil)
	}
	operationKey = requestOperationKey(request)
	if err := contracts.ValidateRequest(request); err != nil {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "SCHEMA_INVALID", err.Error(), "DOCTOR", nil)
	}
	if err := requireOperationKey(request); err != nil {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "REQUEST_INVALID", err.Error(), "DOCTOR", nil)
	}
	settings, err := config.Load(configPath)
	if err != nil {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "REQUEST_INVALID", err.Error(), "DOCTOR", nil)
	}
	toolVersion := agc.PyString(request["toolVersion"])
	appID := agc.PyString(request["appId"])
	report := doctor.Run(settings, toolVersion, appID, r.deps.DoctorClient, r.deps.Now)
	checks := make([]any, 0, len(report.Checks))
	for _, check := range report.Checks {
		checks = append(checks, map[string]any{
			"name": check.Name, "ok": check.OK, "skipped": check.Skipped, "message": check.Message,
		})
	}
	response := makeResponse(report.Success(), "DOCTOR", operationKey, report.ExitCode, started, report.Code, report.Message, report.RemoteState, map[string]any{"checks": checks}, nil, nil, nil)
	return r.emitValidated(response, operationKey, started, "DOCTOR")
}

func (r *runner) executeAPI(spec commandSpec, configPath, requestPath string, dryRun bool) int {
	started := r.deps.Now()
	operationKey := "UNKNOWN"
	request, err := r.readRequest(requestPath)
	if err != nil {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "REQUEST_INVALID", err.Error(), spec.Operation, nil)
	}
	operationKey = requestOperationKey(request)
	if err := contracts.ValidateRequest(request); err != nil {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "SCHEMA_INVALID", err.Error(), spec.Operation, nil)
	}
	if err := requireOperationKey(request); err != nil {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "REQUEST_INVALID", err.Error(), spec.Operation, nil)
	}
	if agc.PyString(request["toolVersion"]) != version.Version {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "REQUEST_INVALID", "toolVersion 与当前 CLI 版本不一致", spec.Operation, nil)
	}
	settings, err := config.Load(configPath)
	if err != nil {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "REQUEST_INVALID", err.Error(), spec.Operation, nil)
	}
	if agc.PyString(request["appId"]) != settings.AppID {
		return r.emitFailure(operationKey, started, apperr.ExitLocalPrecondition, "REQUEST_INVALID", "请求 appId 与配置不一致", spec.Operation, nil)
	}
	session, err := agc.NewSession(settings, r.deps.Transport)
	if err != nil {
		return r.emitError(operationKey, started, err, spec.Operation)
	}
	session.SetClock(r.deps.Now)
	outcome, err := spec.Handler(context.Background(), session, request, dryRun)
	if err != nil {
		return r.emitError(operationKey, started, err, spec.Operation)
	}
	if outcome == nil {
		return r.emitFailure(operationKey, started, apperr.ExitToolInternal, "TOOL_INTERNAL_ERROR", "工具内部错误", spec.Operation, nil)
	}
	operation := spec.Operation
	if outcome.Operation != "" {
		operation = outcome.Operation
	}
	response := makeResponse(true, operation, operationKey, apperr.ExitSuccess, started, "OK", "ok", outcome.RemoteState, outcome.Data, outcome.ExternalIDs, outcome.RequestID, nil)
	return r.emitValidated(response, operationKey, started, operation)
}

func (r *runner) emitError(operationKey string, started time.Time, err error, operation string) int {
	var classified *apperr.Error
	if errors.As(err, &classified) {
		return r.emitFailure(operationKey, started, classified.ExitCode, classified.Code, classified.Message, operation, classified.Detail)
	}
	return r.emitFailure(operationKey, started, apperr.ExitToolInternal, "TOOL_INTERNAL_ERROR", "工具内部错误", operation, nil)
}

func (r *runner) readRequest(path string) (map[string]any, error) {
	var raw []byte
	var err error
	if path == "" {
		stdin := r.deps.Stdin
		if file, ok := stdin.(*os.File); ok {
			if info, statErr := file.Stat(); statErr == nil && info.Mode()&os.ModeCharDevice != 0 {
				return nil, errors.New("缺少 --request-file，且标准输入为空")
			}
		}
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, errors.New("无法读取请求文件")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("请求不是合法 JSON")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("请求不是合法 JSON")
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return nil, errors.New("请求必须是 JSON 对象")
	}
	return object, nil
}

func requestOperationKey(request map[string]any) string {
	if value, present := request["operationKey"]; present && agc.Truthy(value) {
		return agc.PyString(value)
	}
	return "UNKNOWN"
}

func requireOperationKey(request map[string]any) error {
	taskID := agc.PyString(request["taskId"])
	operationKey := agc.PyString(request["operationKey"])
	if !strings.HasPrefix(operationKey, taskID+":") {
		return errors.New("operationKey 必须以 taskId 加冒号开头")
	}
	return nil
}

func makeResponse(success bool, operation, operationKey string, exitCode apperr.ExitCode, started time.Time, code, message string, remoteState map[string]any, data map[string]any, externalIDs map[string]string, requestID *string, detail *string) map[string]any {
	if operationKey == "" {
		operationKey = "UNKNOWN"
	}
	if remoteState == nil {
		remoteState = map[string]any{}
	}
	if data == nil {
		data = map[string]any{}
	}
	if externalIDs == nil {
		externalIDs = map[string]string{}
	}
	var errorValue any
	if !success {
		var detailValue any
		if detail != nil {
			detailValue = redact.Text(*detail)
		}
		errorValue = map[string]any{
			"category": apperr.RetryClass(exitCode),
			"code":     code,
			"message":  redact.Text(message),
			"detail":   detailValue,
		}
	}
	return map[string]any{
		"schemaVersion": "1.0",
		"success":       success,
		"operation":     operation,
		"operationKey":  operationKey,
		"toolVersion":   version.Version,
		"externalIds":   externalIDs,
		"remoteState":   remoteState,
		"requestId":     requestID,
		"retryClass":    apperr.RetryClass(exitCode),
		"error":         errorValue,
		"durationMs":    max(0, rMilliseconds(time.Since(started))),
		"data":          data,
	}
}

func rMilliseconds(duration time.Duration) int64 { return duration.Milliseconds() }

func (r *runner) emitFailure(operationKey string, started time.Time, exitCode apperr.ExitCode, code, message, operation string, detail *string) int {
	data := map[string]any{}
	if operation == "DOCTOR" {
		data["checks"] = []any{}
	}
	response := makeResponse(false, operation, operationKey, exitCode, started, code, message, nil, data, nil, nil, detail)
	return r.emitValidated(response, operationKey, started, operation)
}

func (r *runner) emitValidated(response map[string]any, operationKey string, started time.Time, operation string) int {
	if err := contracts.ValidateResponse(response); err != nil {
		response = makeResponse(false, operation, operationKey, apperr.ExitToolInternal, started, "RESPONSE_SCHEMA_INVALID", "CLI 响应不符合 JSON Schema", nil, map[string]any{}, nil, nil, nil)
	}
	r.emit(response)
	switch response["retryClass"] {
	case "NONE":
		return int(apperr.ExitSuccess)
	case "SAFE_RETRY":
		return int(apperr.ExitSafeRetry)
	case "NON_RETRYABLE":
		return int(apperr.ExitNonRetryable)
	case "RECONCILE_FIRST":
		return int(apperr.ExitReconcileFirst)
	case "LOCAL_PRECONDITION_FAILED":
		return int(apperr.ExitLocalPrecondition)
	default:
		return int(apperr.ExitToolInternal)
	}
}

func (r *runner) emit(response map[string]any) {
	errorCategory := any(nil)
	if errorValue, ok := response["error"].(map[string]any); ok {
		errorCategory = errorValue["category"]
	}
	operationKey := agc.PyString(response["operationKey"])
	taskID := operationKey
	if index := strings.Index(operationKey, ":"); index >= 0 {
		taskID = operationKey[:index]
	}
	logRecord := map[string]any{
		"taskId":        taskID,
		"operationKey":  operationKey,
		"action":        response["operation"],
		"toolVersion":   response["toolVersion"],
		"retryClass":    response["retryClass"],
		"durationMs":    response["durationMs"],
		"result":        map[bool]string{true: "ok", false: "error"}[response["success"].(bool)],
		"errorCategory": errorCategory,
	}
	writeJSONLine(r.deps.Stderr, redact.Value(logRecord))
	writeJSONLine(r.deps.Stdout, response)
}

func writeJSONLine(writer io.Writer, value any) {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
