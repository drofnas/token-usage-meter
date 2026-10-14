package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const batteryRequestBytes = 512
const batterySegmentBytes = 16 * 1024 * 1024
const batteryCurrent = "battery.ndjson"
const batteryPrevious = "battery.previous.ndjson"
const batteryEstimateLegacy = "linear-3300-4200mv-v1"
const batteryEstimateObserved = "linear-3300-4236mv-v2"
const batteryEstimateFullRange = "linear-3300-4180mv-v3"

func validBattery(v document) bool {
	fields := "version device boot_id installed start_ms end_ms sample_age_ms valid invalid min_mv mean_mv max_mv percent"
	if estimate, present := v["estimate"]; present {
		if !oneOf(estimate, false, batteryEstimateLegacy, batteryEstimateObserved, batteryEstimateFullRange) {
			return false
		}
		fields += " estimate"
	}
	if !keys(v, fields) || v["version"] != float64(2) {
		return false
	}
	device, ok := v["device"].(string)
	if !ok || len(device) < 1 || len(device) > 63 || strings.Trim(device, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return false
	}
	boot, ok := v["boot_id"].(string)
	if !ok || len(boot) != 16 || strings.Trim(boot, "0123456789abcdef") != "" {
		return false
	}
	installed, ok := v["installed"].(bool)
	if !ok {
		return false
	}
	for _, key := range []string{"start_ms", "end_ms"} {
		if !number(v[key], 0, 9007199254740991, true, false) {
			return false
		}
	}
	start, end := v["start_ms"].(float64), v["end_ms"].(float64)
	if end < start || end-start > 30000 || !number(v["sample_age_ms"], 0, 30000, true, false) {
		return false
	}
	if !number(v["valid"], 0, 1000, true, false) || !number(v["invalid"], 0, 1000, true, false) || v["valid"].(float64)+v["invalid"].(float64) == 0 {
		return false
	}
	if !number(v["percent"], 0, 100, true, true) || (!installed && v["percent"] != nil) {
		return false
	}
	if v["valid"] == float64(0) {
		return v["min_mv"] == nil && v["mean_mv"] == nil && v["max_mv"] == nil && v["percent"] == nil
	}
	for _, key := range []string{"min_mv", "mean_mv", "max_mv"} {
		if !number(v[key], 0, 6600, true, false) {
			return false
		}
	}
	return v["min_mv"].(float64) <= v["mean_mv"].(float64) && v["mean_mv"].(float64) <= v["max_mv"].(float64)
}

func (h *Handler) storeBattery(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, code string) { respond(w, status, document{"version": 2, "error": code}) }
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || r.Header.Get("Content-Encoding") != "" || r.ContentLength <= 0 || r.ContentLength > batteryRequestBytes {
		fail(400, "bad_request")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, batteryRequestBytes+1))
	if err != nil || int64(len(raw)) != r.ContentLength || len(raw) > batteryRequestBytes {
		fail(400, "bad_request")
		return
	}
	v, err := decodeVersion(raw, batteryRequestBytes, 2)
	if err != nil || !validBattery(v) {
		fail(400, "bad_request")
		return
	}
	now := h.now().UnixMilli()
	if now < 1000 || now > maxEpoch*1000 {
		fail(503, "battery_unavailable")
		return
	}
	v["received_at_ms"] = now
	if _, present := v["estimate"]; !present {
		v["estimate"] = batteryEstimateLegacy
	}
	line, err := json.Marshal(v)
	if err != nil {
		fail(400, "bad_request")
		return
	}
	if err = h.appendBattery(append(line, '\n')); err != nil {
		fail(503, "battery_unavailable")
		return
	}
	respond(w, 200, struct {
		Version int    `json:"version"`
		Status  string `json:"status"`
	}{2, "stored"})
}

// Only these two private regular files are opened. Reads never follow symlinks
// and writes cannot change collector snapshots in the separate read-only mount.
func openBattery(path string, write bool) (*os.File, error) {
	flags := syscall.O_RDONLY
	if write {
		flags = syscall.O_WRONLY | syscall.O_APPEND | syscall.O_CREAT
	}
	fd, err := syscall.Open(path, flags|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > batterySegmentBytes {
		f.Close()
		return nil, errors.New("battery_unavailable")
	}
	return f, nil
}

func (h *Handler) appendBattery(line []byte) error {
	h.batteryMu.Lock()
	defer h.batteryMu.Unlock()
	path := filepath.Join(h.config.BatteryDir, batteryCurrent)
	f, err := openBattery(path, true)
	if err != nil {
		return err
	}
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size+int64(len(line)) > batterySegmentBytes {
		if err = f.Close(); err != nil {
			return err
		}
		if err = os.Rename(path, filepath.Join(h.config.BatteryDir, batteryPrevious)); err != nil {
			return err
		}
		f, err = openBattery(path, true)
		if err != nil {
			return err
		}
		size = 0
	}
	n, err := f.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		_ = f.Truncate(size)
		return err
	}
	return nil
}

func (h *Handler) batteryHistory(w http.ResponseWriter) {
	// Snapshot file handles/lengths under the writer lock, then stream without
	// holding it: slow downloads must not prevent new battery reports.
	var files []*os.File
	var sizes []int64
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	h.batteryMu.Lock()
	var readErr error
	for _, name := range []string{batteryPrevious, batteryCurrent} {
		f, err := openBattery(filepath.Join(h.config.BatteryDir, name), false)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			readErr = err
			break
		}
		files = append(files, f)
		info, err := f.Stat()
		if err != nil {
			readErr = err
			break
		}
		sizes = append(sizes, info.Size())
	}
	h.batteryMu.Unlock()
	if readErr != nil {
		respond(w, 503, document{"version": 2, "error": "battery_unavailable"})
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="battery-history.ndjson"`)
	w.WriteHeader(200)
	for i, f := range files {
		if _, err := io.CopyN(w, f, sizes[i]); err != nil {
			return
		}
	}
}
