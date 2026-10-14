package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"syscall"
	"unicode/utf8"
)

const MaxSourceBytes = 256 * 1024
const MaxAuthBytes = 64 * 1024
const MaxBytes = 4096
const MaxEpoch int64 = 4102444800

var ErrInvalid = errors.New("source_invalid")

func BoundedRead(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrInvalid
	}
	return b, nil
}

func ReadFile(path string, limit int64) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrInvalid
	}
	return BoundedRead(f, limit)
}

// encoding/json alone accepts duplicate keys and repairs invalid UTF-8. Check
// tokens before decoding, with bounded nesting. Field readers check numeric
// ranges; an overflowing optional count must not invalidate a valid quota.
func StrictJSON(b []byte, target any, noExtra bool) error {
	if !utf8.Valid(b) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return ErrInvalid
		}
		tok, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					name, ok := key.(string)
					if err != nil || !ok || seen[name] {
						return ErrInvalid
					}
					seen[name] = true
					if err := value(depth + 1); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := value(depth + 1); err != nil {
						return err
					}
				}
			default:
				return ErrInvalid
			}
			if _, err := d.Token(); err != nil {
				return ErrInvalid
			}
		}
		return nil
	}
	if value(0) != nil {
		return ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if noExtra {
		d.DisallowUnknownFields()
	}
	if d.Decode(target) != nil {
		return ErrInvalid
	}
	return nil
}

func Object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func Number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}
func Integer(v any) (int64, bool) {
	f, ok := Number(v)
	// Every Integer consumed by this protocol fits exactly in IEEE-754.
	if !ok || math.Trunc(f) != f || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int64(f), true
}
func ValidEpoch(n int64) bool { return n >= 1 && n <= MaxEpoch }
