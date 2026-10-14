package collector

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"time"

	"cyd-token-usage-meter/internal/usage"
)

func (s *Store) load() usage.Snapshot {
	raw, err := usage.ReadFile(filepath.Join(s.config.StateDir, "observation.json"), usage.MaxBytes)
	if err == nil {
		if snapshot, err := usage.Decode(raw); err == nil {
			return snapshot
		}
	}
	return usage.Snapshot{Version: 3, StaleAfter: s.config.StaleSeconds}
}

func (s *Store) publish(snapshot usage.Snapshot) error {
	if !snapshot.Valid() {
		return errSourceInvalid
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > usage.MaxBytes {
		return errSourceInvalid
	}
	if err = s.atomicWrite(filepath.Join(s.config.StateDir, "observation.json"), raw, 0600); err != nil {
		return err
	}
	return s.atomicWrite(filepath.Join(s.config.DataDir, "usage.json"), raw, 0640)
}

func apply(previous, next usage.Provider, err error) usage.Provider {
	if err == nil {
		return next
	}
	if next.Scope != "" && next.Scope != previous.Scope {
		previous = usage.Provider{}
	}
	previous.SourceError = err.Error()
	if !usage.ValidError(previous.SourceError) {
		previous.SourceError = "source_unavailable"
	}
	return previous
}

func retryDelay(interval time.Duration, failures int) time.Duration {
	d := interval
	for i := 1; i < failures && d < 30*time.Minute; i++ {
		d *= 2
	}
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

func Run(ctx context.Context, c Config, log io.Writer) error {
	store, err := OpenStore(c)
	if err != nil {
		return err
	}
	defer store.Close()
	client := newClient(c.Timeout)
	defer client.CloseIdleConnections()
	snapshot := store.load()
	paths := []string{c.ClaudeAuthFile, c.AuthFile}
	names := []string{"claude", "codex"}
	var nextPoll [2]time.Time
	var failures [2]int
	var lastEvent [2]string
	for ctx.Err() == nil {
		providers := []*usage.Provider{&snapshot.Claude, &snapshot.Codex}
		changed := false
		for i, previous := range providers {
			if time.Now().Before(nextPoll[i]) {
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, c.Timeout)
			next, sourceErr := fetch(attempt, client, names[i], paths[i], store.key, time.Now)
			cancel()
			if ctx.Err() != nil {
				return nil
			}
			if sourceErr == nil && previous.Scope == next.Scope && next.ObservedAt < previous.ObservedAt {
				sourceErr = errSourceInvalid
			}
			*previous = apply(*previous, next, sourceErr)
			changed = true
			event := previous.SourceError
			if event == "" {
				event = "ok"
				failures[i] = 0
			} else if failures[i] < 10 {
				failures[i]++
			}
			if event != lastEvent[i] {
				if json.NewEncoder(log).Encode(map[string]string{"provider": names[i], "event": event}) != nil {
					return errStorage
				}
				lastEvent[i] = event
			}
			delay := retryDelay(c.Interval, failures[i])
			if sourceErr == errRateLimited && delay < 5*time.Minute {
				delay = 5 * time.Minute
			}
			nextPoll[i] = time.Now().Add(delay)
		}
		if changed {
			now := time.Now().Unix()
			if now < snapshot.UpdatedAt {
				return errSourceInvalid
			}
			snapshot.UpdatedAt = now
			snapshot.AsOf = now
			snapshot.StaleAfter = c.StaleSeconds
			if err := store.publish(snapshot); err != nil {
				return err
			}
		}
		if c.Once {
			if snapshot.Claude.SourceError != "" || snapshot.Codex.SourceError != "" {
				return errSourceUnavailable
			}
			return nil
		}
		timer := time.NewTimer(time.Until(minTime(nextPoll[0], nextPoll[1])))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
