package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	_ "time/tzdata"

	"cyd-token-usage-meter/internal/usage"
)

type Config struct {
	DataDir, Address, Token, Timezone, BatteryDir string
	StaleSeconds                                  int64
}
type Handler struct {
	config    Config
	token     [32]byte
	now       func() time.Time
	batteryMu sync.Mutex
}

func NewHandler(c Config) (*Handler, error) {
	if !hexToken(c.Token) || c.DataDir == "" || c.StaleSeconds < 30 || c.StaleSeconds > 3600 {
		return nil, errors.New("invalid_configuration")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil || c.Timezone == "" || c.Timezone == "Local" {
		return nil, errors.New("invalid_configuration")
	}
	return &Handler{config: c, token: sha256.Sum256([]byte("Bearer " + c.Token)), now: time.Now}, nil
}
func NewServer(c Config) (*http.Server, error) {
	h, err := NewHandler(c)
	if err != nil {
		return nil, err
	}
	return &http.Server{Addr: c.Address, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 * 1024, ErrorLog: log.New(io.Discard, "", 0)}, nil
}
func respond(w http.ResponseWriter, status int, value any) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxBytes {
		status = 503
		raw = []byte(`{"version":3,"error":"snapshot_invalid"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	batteryPost := r.URL.Path == "/v2/battery"
	batteryHistory := r.URL.Path == "/v2/battery/history"
	version := 3
	if batteryPost || batteryHistory {
		version = 2
	}
	fail := func(status int, code string) { respond(w, status, document{"version": version, "error": code}) }
	if (r.URL.Path != "/v3/usage" && r.URL.Path != "/healthz" && !batteryPost && !batteryHistory) || r.URL.EscapedPath() != r.URL.Path {
		fail(404, "not_found")
		return
	}
	method := http.MethodGet
	if batteryPost {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(405, "method_not_allowed")
		return
	}
	if r.URL.Path != "/healthz" {
		values := r.Header.Values("Authorization")
		candidate := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if len(values) != 1 || subtle.ConstantTimeCompare(candidate[:], h.token[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(401, "unauthorized")
			return
		}
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || (!batteryPost && r.ContentLength != 0) || len(r.TransferEncoding) != 0 {
		fail(400, "bad_request")
		return
	}
	if batteryPost || batteryHistory {
		if h.config.BatteryDir == "" {
			fail(503, "battery_unavailable")
			return
		}
		if batteryPost {
			h.storeBattery(w, r)
		} else {
			h.batteryHistory(w)
		}
		return
	}
	if r.URL.Path == "/healthz" {
		respond(w, 200, document{"version": 3, "status": "ok"})
		return
	}
	now := h.now().Unix()
	if !usage.ValidEpoch(now) {
		fail(503, "snapshot_invalid")
		return
	}
	raw, err := usage.ReadFile(filepath.Join(h.config.DataDir, "usage.json"), usage.MaxBytes)
	if os.IsNotExist(err) {
		respond(w, 200, usage.Snapshot{Version: 3, UpdatedAt: now, AsOf: now, StaleAfter: h.config.StaleSeconds})
		return
	}
	if err != nil {
		fail(503, "snapshot_unreadable")
		return
	}
	snapshot, err := usage.Decode(raw)
	if err != nil {
		fail(503, "snapshot_invalid")
		return
	}
	snapshot.AsOf = now
	snapshot.StaleAfter = min(snapshot.StaleAfter, h.config.StaleSeconds)
	respond(w, 200, snapshot)
}
