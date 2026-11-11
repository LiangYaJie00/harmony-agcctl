package agc

import (
	"testing"
)

func TestClassifyMutationAndQueryFailures(t *testing.T) {
	status500 := 500
	if got := Classify("POST", &Exchange{StatusCode: &status500, Sent: true}); got == nil || got.ExitCode != 30 {
		t.Fatalf("POST 5xx = %#v", got)
	}
	if got := Classify("GET", &Exchange{StatusCode: &status500, Sent: true}); got == nil || got.ExitCode != 10 {
		t.Fatalf("GET 5xx = %#v", got)
	}
	if got := Classify("POST", &Exchange{ErrorKind: "timeout", Sent: true}); got == nil || got.Code != "HTTP_TIMEOUT_AFTER_SEND" {
		t.Fatalf("POST timeout = %#v", got)
	}
	if got := Classify("GET", &Exchange{ErrorKind: "timeout", Sent: true}); got == nil || got.Code != "HTTP_TIMEOUT" {
		t.Fatalf("GET timeout = %#v", got)
	}
}
