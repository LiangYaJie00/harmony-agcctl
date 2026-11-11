package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	settings, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if settings.BundleName != "com.qianshou.billionclub" || settings.PrivateKeyFile != "/etc/qianshou-harmony/agc/private.json" {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestLoadIgnoresReleaseFlowFields(t *testing.T) {
	path := writeConfig(t, `apiHost: https://connect-api.cloud.huawei.com
appId: "1"
bundleName: a
privateKeyFile: /tmp/k
artifactRoot: /tmp/a
logDir: /tmp/l
runDir: /tmp/r
qaGroupId: group-1
qaGroupName: QA
testWindowDays: 7
`)
	settings, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings.AppID != "1" || settings.TestReleaseType != 6 || settings.TestType != 3 {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestLoadRejectsHTTPHost(t *testing.T) {
	path := writeConfig(t, `apiHost: http://connect-api.cloud.huawei.com
appId: "1"
bundleName: a
privateKeyFile: /tmp/k
artifactRoot: /tmp/a
logDir: /tmp/l
runDir: /tmp/r
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error")
	}
}

func writeConfig(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
