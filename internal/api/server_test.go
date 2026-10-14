package api

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const fixtures = "../../contracts/v3/fixtures"
const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(fixtures, name))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func testHandler(t testing.TB) *Handler {
	t.Helper()
	h, e := NewHandler(Config{DataDir: t.TempDir(), Token: testToken, Timezone: "America/Los_Angeles", StaleSeconds: 900})
	if e != nil {
		t.Fatal(e)
	}
	h.now = func() time.Time { return time.Unix(1800000000, 0) }
	return h
}
func publish(t testing.TB, h *Handler, raw []byte) {
	t.Helper()
	p := filepath.Join(h.config.DataDir, "next")
	if e := os.WriteFile(p, raw, 0640); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(p, filepath.Join(h.config.DataDir, "usage.json")); e != nil {
		t.Fatal(e)
	}
}
func request(h *Handler, method, path string, auth []string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	for _, v := range auth {
		r.Header.Add("Authorization", v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func assertResponse(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" || w.Body.Len() > MaxBytes {
		t.Fatal("response headers/size")
	}
	if code != "" {
		var v document
		if json.Unmarshal(w.Body.Bytes(), &v) != nil || v["error"] != code || len(v) != 2 || w.Body.Len() > 256 {
			t.Fatalf("error envelope %s", w.Body.String())
		}
	}
}
