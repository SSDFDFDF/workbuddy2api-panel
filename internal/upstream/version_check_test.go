package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchFeedVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"version": "5.7.6.40409493",
			"productVersion": "5.7.6.40409493",
			"url": "https://download.codebuddy.cn/workbuddy/saas/win32-x64-user/WorkBuddy-win32-x64-user-5.7.6.40409493-306add2a.exe",
			"timestamp": 1791132693
		}`))
	}))
	defer srv.Close()

	semver, build, err := fetchFeedVersion(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchFeedVersion err: %v", err)
	}
	if semver != "5.7.6" {
		t.Errorf("semver = %q want 5.7.6", semver)
	}
	if build != "5.7.6.40409493" {
		t.Errorf("build = %q want 5.7.6.40409493", build)
	}
}

func TestGetLatestVersionInfoDefault(t *testing.T) {
	info := GetLatestVersionInfo()
	if info.LatestCN == "" || info.LatestGlobal == "" {
		t.Errorf("default version info should not be empty: %+v", info)
	}
}
