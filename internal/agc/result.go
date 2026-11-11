package agc

// Result is the handler output before the CLI wraps it in the common response.
type Result struct {
	Operation   string
	ExternalIDs map[string]string
	RemoteState map[string]any
	Data        map[string]any
	RequestID   *string
}

func result(exchange *Exchange, externalIDs map[string]string, remoteState map[string]any, data map[string]any) *Result {
	if externalIDs == nil {
		externalIDs = map[string]string{}
	}
	if remoteState == nil {
		remoteState = map[string]any{}
	}
	if data == nil {
		data = map[string]any{}
	}
	var requestID *string
	if exchange != nil && exchange.RequestID != "" {
		value := exchange.RequestID
		requestID = &value
	}
	return &Result{ExternalIDs: externalIDs, RemoteState: remoteState, Data: data, RequestID: requestID}
}

func Body(request map[string]any) (map[string]any, error) {
	body, ok := Object(request["request"])
	if !ok {
		return nil, requestInvalid("request 必须是 JSON 对象")
	}
	return body, nil
}

func RequiredString(body map[string]any, key string) (string, error) {
	value, present := body[key]
	if !present {
		return "", requestInvalid("缺少字段 %s", key)
	}
	return PyString(value), nil
}

func RequiredInt(body map[string]any, key string) (int64, error) {
	value, present := body[key]
	if !present {
		return 0, requestInvalid("缺少字段 %s", key)
	}
	number, ok := StrictInt(value)
	if !ok {
		return 0, requestInvalid("字段 %s 必须是整数", key)
	}
	return number, nil
}

func requiredMapString(body map[string]any, key string) (string, error) {
	value, present := body[key]
	if !present {
		return "", requestInvalid("缺少字段 %s", key)
	}
	text, ok := String(value)
	if !ok {
		return "", requestInvalid("字段 %s 必须是非空字符串", key)
	}
	return text, nil
}
