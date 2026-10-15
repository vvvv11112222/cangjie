package capture

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// LoadConfig is called after the application's normal .env loader.
func LoadConfig() (Config, bool, error) {
	c := Config{}
	enabled, e := strconv.ParseBool(value("CAPTURE_ENABLED", "false"))
	if e != nil {
		return c, false, fmt.Errorf("invalid CAPTURE_ENABLED")
	}
	if !enabled {
		return c, false, nil
	}
	c.PublicURL = value("CAPTURE_PUBLIC_URL", "")
	c.MediaURL = value("CAPTURE_MEDIA_URL", "")
	c.ControlURL = value("CAPTURE_CONTROL_URL", "http://127.0.0.1:9997")
	c.ControlToken = os.Getenv("CAPTURE_CONTROL_TOKEN")
	c.EventToken = os.Getenv("CAPTURE_EVENT_TOKEN")
	c.RecordRoot = value("CAPTURE_RECORD_ROOT", "var/capture-recordings")
	c.FFmpeg = value("CAPTURE_FFMPEG", "ffmpeg")
	c.FFprobe = value("CAPTURE_FFPROBE", "ffprobe")
	for _, v := range []struct {
		key      string
		fallback int
		out      *time.Duration
	}{
		{"CAPTURE_PAIRING_SECONDS", 300, &c.PairingTTL}, {"CAPTURE_VIEWER_SECONDS", 300, &c.ViewerTTL},
		{"CAPTURE_MAX_SECONDS", 7200, &c.MaxDuration}, {"CAPTURE_WAITING_SECONDS", 600, &c.WaitingTimeout},
		{"CAPTURE_DISCONNECT_SECONDS", 10, &c.DisconnectTimeout}, {"CAPTURE_FINALIZE_SECONDS", 1800, &c.FinalizeTimeout},
	} {
		n, e := strconv.Atoi(value(v.key, strconv.Itoa(v.fallback)))
		if e != nil || n < 1 || n > 86400 {
			return c, false, fmt.Errorf("invalid %s", v.key)
		}
		*v.out = time.Duration(n) * time.Second
	}
	c.MaxBytes, e = strconv.ParseInt(value("CAPTURE_MAX_BYTES", "4294967296"), 10, 64)
	if e != nil || c.MaxBytes < 1024 || c.MaxBytes > 4<<30 {
		return c, false, fmt.Errorf("invalid CAPTURE_MAX_BYTES")
	}
	return c, true, nil
}
func value(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
