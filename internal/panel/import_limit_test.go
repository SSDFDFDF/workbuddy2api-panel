package panel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workbuddy_manager/internal/auth"
	"workbuddy_manager/internal/pool"
	"workbuddy_manager/internal/upstream"
)

type repeatedByteReader byte

func (r repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// Streaming multipart fixture avoids allocating a 32 MiB body in the test.
func cockpitMultipart(payload io.Reader, size int64) (*http.Request, int64) {
	var prefix bytes.Buffer
	w := multipart.NewWriter(&prefix)
	_, _ = w.CreateFormFile("file", "accounts.json")
	boundary := w.Boundary()
	end := "\r\n--" + boundary + "--\r\n"
	total := int64(prefix.Len()) + size + int64(len(end))
	r := httptest.NewRequest("POST", "/panel/api/import/cockpit", io.MultiReader(bytes.NewReader(prefix.Bytes()), payload, strings.NewReader(end)))
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r, total
}

func TestCockpitImportBodyLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	p := New(Config{APIKey: "secret", AuthDir: filepath.Join(dir, "auths"), Pool: pool.New("")})
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprintf("declared=%v", declared), func(t *testing.T) {
			r, total := cockpitMultipart(io.LimitReader(repeatedByteReader(' '), maxCockpitImportBytes), maxCockpitImportBytes)
			if declared {
				r.ContentLength = total
			} else {
				r.ContentLength = -1
			}
			r.Header.Set("Authorization", "Bearer secret")
			w := httptest.NewRecorder()
			p.ServeHTTP(w, r)
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
			}
			if len(p.cfg.Pool.List()) != 0 {
				t.Fatal("oversized import added accounts")
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary files left after rejection: %v %v", files, err)
			}
		})
	}
}

func TestCockpitImportBoundaryAndCleanup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	p := New(Config{Pool: pool.New("")})
	// Missing credentials are skipped, so no real upstream or credential write.
	const jsonBody = `[{"uid":"skip"}]`
	_, overhead := cockpitMultipart(strings.NewReader(jsonBody), int64(len(jsonBody)))
	overhead -= int64(len(jsonBody))
	for _, total := range []int64{overhead + int64(len(jsonBody)), maxCockpitImportBytes, maxCockpitImportBytes + 1} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			size := total - overhead
			padding := size - int64(len(jsonBody))
			r, _ := cockpitMultipart(io.MultiReader(strings.NewReader(jsonBody), io.LimitReader(repeatedByteReader(' '), padding)), size)
			r.ContentLength = -1
			w := httptest.NewRecorder()
			p.ServeHTTP(w, r)
			want := http.StatusOK
			if total > maxCockpitImportBytes {
				want = http.StatusRequestEntityTooLarge
			}
			if w.Code != want {
				t.Fatalf("code=%d want %d body=%s", w.Code, want, w.Body.String())
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary files left: %v %v", files, err)
			}
		})
	}
}

type importUnreadBody struct{}

func (importUnreadBody) Read([]byte) (int, error) {
	panic("body must not be read before authentication")
}
func (importUnreadBody) Close() error { return nil }

func TestCockpitImportAuthBeforeBodyRead(t *testing.T) {
	p := New(Config{APIKey: "secret"})
	r := httptest.NewRequest("POST", "/panel/api/import/cockpit", importUnreadBody{})
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", w.Code)
	}
}

type importRoundTrip func(*http.Request) (*http.Response, error)

func (f importRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCockpitImportValidAccount(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	client := &http.Client{Transport: importRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || (r.URL.Path != "/v2/billing/meter/daily-checkin" && r.URL.Path != "/v2/billing/meter/get-user-resource") {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{}}`))}, nil
	})}
	p := New(Config{APIKey: "secret", AuthDir: dir, Pool: pool.New(""), Upstream: &upstream.Client{HTTP: client, BillingBaseCN: "https://fake.example"}})
	const body = `[{"uid":"audit-import","access_token":"test-access","refresh_token":"test-refresh","expires_at":2000000000000,"nickname":"imported"}]`
	r, total := cockpitMultipart(strings.NewReader(body), int64(len(body)))
	r.ContentLength = total
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	var result struct{ Imported, Skipped int }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || result.Imported != 1 || result.Skipped != 0 {
		t.Fatalf("code=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
	if calls != 2 || len(p.cfg.Pool.List()) != 1 {
		t.Fatalf("calls=%d accounts=%d", calls, len(p.cfg.Pool.List()))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "workbuddy-audit-import.json"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.Parse(raw)
	if err != nil || a.UID != "audit-import" || a.AccessTokenValue() != "test-access" || a.ExpiresAt != 2000000000 {
		t.Fatal("imported auth did not roundtrip", err)
	}
}
