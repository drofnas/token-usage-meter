package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const batteryExample = `{"version":2,"device":"cyd-meter-1","boot_id":"0123456789abcdef","installed":true,"start_ms":1000,"end_ms":10900,"sample_age_ms":100,"valid":100,"invalid":0,"min_mv":3900,"mean_mv":3950,"max_mv":4000,"percent":72}`

func postBattery(h *Handler, raw string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v2/battery", strings.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if auth {
		r.Header.Set("Authorization", "Bearer "+testToken)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestBatteryPersistenceAndAuth(t *testing.T) {
	h := testHandler(t)
	h.config.BatteryDir = t.TempDir()
	auth := []string{"Bearer " + testToken}
	assertResponse(t, postBattery(h, batteryExample, false), 401, "unauthorized")
	assertResponse(t, request(h, "GET", "/v2/battery/history", nil, nil), 401, "unauthorized")
	assertResponse(t, request(h, "GET", "/v2/battery", auth, nil), 405, "method_not_allowed")
	assertResponse(t, request(h, "POST", "/v2/battery/history", auth, nil), 405, "method_not_allowed")
	assertResponse(t, request(h, "GET", "/v2/battery/history?x", auth, nil), 400, "bad_request")
	if got := request(h, "GET", "/v2/battery/history", auth, nil); got.Code != 200 || got.Body.Len() != 0 {
		t.Fatal("initial history")
	}
	w := postBattery(h, batteryExample, true)
	assertResponse(t, w, 200, "")
	if w.Body.String() != `{"version":2,"status":"stored"}` {
		t.Fatal("firmware acknowledgement")
	}
	// New handler simulates process/container restart using the retained volume.
	next, err := NewHandler(h.config)
	if err != nil {
		t.Fatal(err)
	}
	w = request(next, "GET", "/v2/battery/history", auth, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatal("history response")
	}
	var row document
	if json.Unmarshal(w.Body.Bytes(), &row) != nil || row["received_at_ms"] != float64(h.now().UnixMilli()) || row["mean_mv"] != float64(3950) || row["boot_id"] != "0123456789abcdef" {
		t.Fatal("persisted evidence")
	}
	if _, err := os.Stat(filepath.Join(h.config.DataDir, batteryCurrent)); !os.IsNotExist(err) {
		t.Fatal("wrote collector directory")
	}
}

func TestBatteryValidation(t *testing.T) {
	h := testHandler(t)
	h.config.BatteryDir = t.TempDir()
	cases := []string{"", "{}", batteryExample + "{}", strings.Repeat(" ", 513),
		strings.Replace(batteryExample, `"version":2`, `"version":2,"version":2`, 1),
		strings.Replace(batteryExample, `"installed":true`, `"installed":false`, 1),
		strings.Replace(batteryExample, `"device":"cyd-meter-1"`, `"device":"../usage.json"`, 1),
		strings.Replace(batteryExample, `"mean_mv":3950`, `"mean_mv":NaN`, 1),
		strings.Replace(batteryExample, `"mean_mv":3950`, `"mean_mv":4100`, 1),
		strings.Replace(batteryExample, `"percent":72`, `"percent":101`, 1),
		strings.Replace(batteryExample, `"sample_age_ms":100`, `"sample_age_ms":30001`, 1),
		strings.Replace(batteryExample, `"start_ms":1000`, `"start_ms":11000`, 1),
		strings.Replace(batteryExample, `"valid":100`, `"valid":0`, 1)}
	for _, raw := range cases {
		assertResponse(t, postBattery(h, raw, true), 400, "bad_request")
	}
	v, _ := decodeVersion([]byte(batteryExample), 512, 2)
	for key := range v {
		copy, _ := decodeVersion([]byte(batteryExample), 512, 2)
		delete(copy, key)
		raw, _ := json.Marshal(copy)
		assertResponse(t, postBattery(h, string(raw), true), 400, "bad_request")
	}
	v["valid"], v["invalid"], v["min_mv"], v["mean_mv"], v["max_mv"], v["percent"] = 0, 100, nil, nil, nil, nil
	raw, _ := json.Marshal(v)
	assertResponse(t, postBattery(h, string(raw), true), 200, "")
}

func TestBatteryEstimateHistory(t *testing.T) {
	h := testHandler(t)
	h.config.BatteryDir = t.TempDir()
	// Old firmware omits the estimate. New reports carry their own scale so
	// updating the firmware cannot relabel the stored discharge/recharge run.
	cases := []struct {
		input, stored string
		percent       float64
	}{
		{"", batteryEstimateLegacy, 72},
		{batteryEstimateObserved, batteryEstimateObserved, 69},
		{batteryEstimateFullRange, batteryEstimateFullRange, 74},
		{batteryEstimateLegacy, batteryEstimateLegacy, 72},
	}
	for _, c := range cases {
		v, _ := decodeVersion([]byte(batteryExample), 512, 2)
		if c.input != "" {
			v["estimate"] = c.input
		}
		v["percent"] = c.percent
		raw, _ := json.Marshal(v)
		assertResponse(t, postBattery(h, string(raw), true), 200, "")
	}
	w := request(h, "GET", "/v2/battery/history", []string{"Bearer " + testToken}, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatal("history response")
	}
	decoder := json.NewDecoder(w.Body)
	for i, c := range cases {
		var row document
		if err := decoder.Decode(&row); err != nil || row["estimate"] != c.stored {
			t.Fatalf("history row %d lost its estimate: %v", i, err)
		}
		if row["percent"] != c.percent {
			t.Fatal("history percentage was rewritten")
		}
	}
	var extra document
	if decoder.Decode(&extra) != io.EOF {
		t.Fatal("unexpected history row")
	}
	for _, estimate := range []any{nil, false, 2, "", "unknown", []string{batteryEstimateObserved}} {
		v, _ := decodeVersion([]byte(batteryExample), 512, 2)
		v["estimate"] = estimate
		raw, _ := json.Marshal(v)
		assertResponse(t, postBattery(h, string(raw), true), 400, "bad_request")
	}
	duplicate := strings.Replace(batteryExample, `"version":2`, `"version":2,"estimate":"linear-3300-4236mv-v2","estimate":"linear-3300-4200mv-v1"`, 1)
	assertResponse(t, postBattery(h, duplicate, true), 400, "bad_request")
}

func TestBatteryRotationAndStorageFailures(t *testing.T) {
	h := testHandler(t)
	h.config.BatteryDir = t.TempDir()
	path := filepath.Join(h.config.BatteryDir, batteryCurrent)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(batterySegmentBytes); err != nil {
		t.Fatal(err)
	}
	f.Close()
	assertResponse(t, postBattery(h, batteryExample, true), 200, "")
	info, err := os.Stat(filepath.Join(h.config.BatteryDir, batteryPrevious))
	if err != nil || info.Size() != batterySegmentBytes {
		t.Fatal("rotation lost previous segment")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(h.config.DataDir, "usage.json"), path); err != nil {
		t.Fatal(err)
	}
	assertResponse(t, postBattery(h, batteryExample, true), 503, "battery_unavailable")
	assertResponse(t, request(h, "GET", "/v2/battery/history", []string{"Bearer " + testToken}, nil), 503, "battery_unavailable")
}

func TestBatteryConcurrentAppendAndRead(t *testing.T) {
	h := testHandler(t)
	h.config.BatteryDir = t.TempDir()
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Go(func() {
			if postBattery(h, batteryExample, true).Code != 200 {
				t.Error("write failed")
			}
		})
		group.Go(func() {
			if request(h, "GET", "/v2/battery/history", []string{"Bearer " + testToken}, nil).Code != 200 {
				t.Error("read failed")
			}
		})
	}
	group.Wait()
	w := request(h, "GET", "/v2/battery/history", []string{"Bearer " + testToken}, nil)
	d := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	count := 0
	for {
		var row document
		err := d.Decode(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("torn record", err)
		}
		count++
	}
	if count != 20 {
		t.Fatal("lost records", count)
	}
}
