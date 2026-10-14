package api

import (
	"cyd-token-usage-meter/internal/usage"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUsageBoundary(t *testing.T) {
	h := testHandler(t)
	auth := []string{"Bearer " + testToken}
	assertResponse(t, request(h, "GET", "/v3/usage", nil, nil), 401, "unauthorized")
	assertResponse(t, request(h, "GET", "/v3/usage", []string{auth[0], auth[0]}, nil), 401, "unauthorized")
	assertResponse(t, request(h, "POST", "/v3/usage", auth, nil), 405, "method_not_allowed")
	assertResponse(t, request(h, "GET", "/v3/usage?x", auth, nil), 400, "bad_request")
	assertResponse(t, request(h, "GET", "/v3%2fusage", auth, nil), 404, "not_found")
	assertResponse(t, request(h, "GET", "/v3/usage", auth, strings.NewReader("x")), 400, "bad_request")
	empty := request(h, "GET", "/v3/usage", auth, nil)
	assertResponse(t, empty, 200, "")
	var snapshot usage.Snapshot
	if json.Unmarshal(empty.Body.Bytes(), &snapshot) != nil || snapshot.Claude.ObservedAt != 0 || snapshot.Codex.Weekly != nil {
		t.Fatal("invented initial data")
	}
	publish(t, h, fixture(t, "normal.json"))
	w := request(h, "GET", "/v3/usage", auth, nil)
	assertResponse(t, w, 200, "")
	if json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || snapshot.Claude.FiveHour.Remaining != 72 || snapshot.Codex.Weekly.Remaining != 64 {
		t.Fatal("wrong quota")
	}
	if snapshot.Claude.ResetsAvailable != nil || snapshot.Codex.ResetsAvailable == nil || *snapshot.Codex.ResetsAvailable != 2 {
		t.Fatal("wrong reset count")
	}
	h.now = func() time.Time { return time.Unix(1800002000, 0) }
	w = request(h, "GET", "/v3/usage", auth, nil)
	if json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || snapshot.AsOf != 1800002000 || snapshot.Codex.Status(snapshot.AsOf, snapshot.StaleAfter) != "stale" {
		t.Fatal("API freshness did not advance")
	}
	for _, raw := range []string{`{}`, strings.Replace(string(fixture(t, "normal.json")), `"remaining_percent":72`, `"remaining_percent":null`, 1), strings.Repeat("x", 4097)} {
		publish(t, h, []byte(raw))
		w = request(h, "GET", "/v3/usage", auth, nil)
		if w.Code != 503 {
			t.Fatal("accepted malformed snapshot")
		}
	}
	path := filepath.Join(h.config.DataDir, "usage.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h.config.DataDir, "private"), path); err != nil {
		t.Fatal(err)
	}
	if request(h, "GET", "/v3/usage", auth, nil).Code != 503 {
		t.Fatal("followed symlink")
	}
	r := httptest.NewRequest("GET", "/v3/usage", nil)
	r.Header.Set("Authorization", auth[0])
	r.TransferEncoding = []string{"chunked"}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, r)
	assertResponse(t, response, 400, "bad_request")
}
