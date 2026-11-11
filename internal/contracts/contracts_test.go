package contracts

import "testing"

func TestValidateKnownResponse(t *testing.T) {
	payload := map[string]any{
		"schemaVersion": "1.0", "success": true, "operation": "DOCTOR", "operationKey": "x:y",
		"toolVersion": "0.1.0", "externalIds": map[string]string{}, "remoteState": map[string]any{},
		"requestId": nil, "retryClass": "NONE", "error": nil, "durationMs": int64(1),
		"data": map[string]any{"checks": []any{}},
	}
	if err := ValidateResponse(payload); err != nil {
		t.Fatal(err)
	}
}
