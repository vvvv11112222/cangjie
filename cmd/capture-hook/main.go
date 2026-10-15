// capture-hook adapts MediaMTX v1.15.3 command hooks to authenticated Go events.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"github.com/vvvv11112222/cangjie/internal/capture"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "capture event delivery failed")
		os.Exit(1)
	}
}
func run() error {
	_ = godotenv.Load()
	if len(os.Args) != 2 {
		return fmt.Errorf("expected event type")
	}
	v := capture.Event{Type: os.Args[1], Path: os.Getenv("MTX_PATH"), ConnectionID: os.Getenv("MTX_SOURCE_ID"), OccurredAt: time.Now().UTC()}
	if p := os.Getenv("MTX_SEGMENT_PATH"); p != "" {
		abs, e := filepath.Abs(p)
		if e != nil {
			return e
		}
		v.SegmentPath = &abs
	}
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	v.ID = hex.EncodeToString(h[:])
	raw, _ = json.Marshal(v)
	target := os.Getenv("CAPTURE_EVENT_URL")
	token := os.Getenv("CAPTURE_EVENT_TOKEN")
	if target == "" || len(token) < 32 {
		return fmt.Errorf("missing event configuration")
	}
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for n := 0; n < 4; n++ {
		r, e := http.NewRequest("POST", target, bytes.NewReader(raw))
		if e != nil {
			return e
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		res, e := client.Do(r)
		if e == nil {
			res.Body.Close()
			if res.StatusCode == 204 {
				return nil
			}
			if res.StatusCode >= 400 && res.StatusCode < 500 {
				return fmt.Errorf("event rejected")
			}
		}
		time.Sleep(time.Duration(n+1) * time.Second)
	}
	return fmt.Errorf("event delivery exhausted")
}
