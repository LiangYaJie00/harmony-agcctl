// Package config loads the non-secret agcctl runtime configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Settings mirrors config.yaml. Unknown fields are intentionally ignored so the
// same file can be shared with release-flow tooling.
type Settings struct {
	APIHost               string `yaml:"apiHost"`
	AppID                 string `yaml:"appId"`
	BundleName            string `yaml:"bundleName"`
	DefaultLanguage       string `yaml:"defaultLanguage"`
	TestReleaseType       int    `yaml:"testReleaseType"`
	TestType              int    `yaml:"testType"`
	ConnectTimeoutSeconds int    `yaml:"connectTimeoutSeconds"`
	QueryTimeoutSeconds   int    `yaml:"queryTimeoutSeconds"`
	UploadTimeoutSeconds  int    `yaml:"uploadTimeoutSeconds"`
	PrivateKeyFile        string `yaml:"privateKeyFile"`
	ArtifactRoot          string `yaml:"artifactRoot"`
	LogDir                string `yaml:"logDir"`
	RunDir                string `yaml:"runDir"`
	ReleaseUser           string `yaml:"releaseUser"`
	ManifestFile          string `yaml:"manifestFile"`
	RequireManifest       bool   `yaml:"requireManifest"`
	MaxClockSkewSeconds   int    `yaml:"maxClockSkewSeconds"`
	NotifyPath            string `yaml:"notifyPath"`
}

func defaults() Settings {
	return Settings{
		DefaultLanguage:       "zh-CN",
		TestReleaseType:       6,
		TestType:              3,
		ConnectTimeoutSeconds: 10,
		QueryTimeoutSeconds:   30,
		UploadTimeoutSeconds:  1200,
		MaxClockSkewSeconds:   60,
		NotifyPath:            "/api/publish/v2/test/user/action/notify",
	}
}

// Load reads and validates config.yaml.
func Load(path string) (*Settings, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("无法读取配置文件")
	}
	var values map[string]any
	if err := yaml.Unmarshal(raw, &values); err != nil {
		return nil, errors.New("配置不是合法 YAML")
	}
	if values == nil {
		return nil, errors.New("配置必须是 YAML 映射")
	}
	settings := defaults()
	if err := yaml.Unmarshal(raw, &settings); err != nil {
		return nil, errors.New("配置字段无效")
	}
	for _, field := range []string{"apiHost", "appId", "bundleName", "privateKeyFile", "artifactRoot", "logDir", "runDir"} {
		if _, ok := values[field]; !ok {
			return nil, errors.New("配置字段无效")
		}
	}
	if settings.AppID == "" || settings.BundleName == "" || settings.PrivateKeyFile == "" || settings.ArtifactRoot == "" || settings.LogDir == "" || settings.RunDir == "" {
		return nil, errors.New("配置字段无效")
	}
	if !strings.HasPrefix(settings.APIHost, "https://") || strings.HasPrefix(settings.APIHost, "https:///") {
		return nil, errors.New("apiHost must be an https URL")
	}
	parsed, err := url.Parse(settings.APIHost)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, errors.New("apiHost must be an https URL")
	}
	settings.APIHost = strings.TrimRight(settings.APIHost, "/")
	if !strings.HasPrefix(settings.NotifyPath, "/") || strings.HasPrefix(settings.NotifyPath, "//") {
		return nil, errors.New("API path must start with a single slash")
	}
	for name, value := range map[string]int{
		"connectTimeoutSeconds": settings.ConnectTimeoutSeconds,
		"queryTimeoutSeconds":   settings.QueryTimeoutSeconds,
		"uploadTimeoutSeconds":  settings.UploadTimeoutSeconds,
	} {
		if value < 1 {
			return nil, fmt.Errorf("配置字段无效: %s", name)
		}
	}
	if settings.MaxClockSkewSeconds < 0 {
		return nil, errors.New("配置字段无效: maxClockSkewSeconds")
	}
	return &settings, nil
}
