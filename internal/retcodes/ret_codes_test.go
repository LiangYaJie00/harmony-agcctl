package retcodes

import "testing"

func TestFailureMessages(t *testing.T) {
	for _, test := range []struct {
		code, msg       any
		message, detail string
	}{
		{204144641, "versionId 为空", "无效入参：versionId 为空", "204144641: versionId 为空"},
		{204144642, "unknown error", "系统错误：unknown error。可通过在线工单联系华为", "204144642: unknown error"},
		{42, nil, "华为接口返回业务错误（42）", "42: "},
	} {
		message, detail := Failure(test.code, test.msg)
		if message != test.message || detail != test.detail {
			t.Fatalf("got %q, %q", message, detail)
		}
	}
}
