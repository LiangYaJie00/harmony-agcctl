package agc

import (
	"testing"
	"time"
)

func TestFutureReleaseTimeRequiresZoneAndFuture(t *testing.T) {
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	if got, err := futureReleaseTime(future); err != nil || got != future {
		t.Fatalf("future = %q err=%v", got, err)
	}
	if _, err := futureReleaseTime(time.Now().Add(time.Hour).Format("2006-01-02T15:04:05")); err == nil {
		t.Fatal("expected missing timezone to fail")
	}
	if _, err := futureReleaseTime("2020-01-01T00:00:00+08:00"); err == nil {
		t.Fatal("expected past time to fail")
	}
}
