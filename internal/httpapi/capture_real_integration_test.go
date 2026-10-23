package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vvvv11112222/cangjie/internal/academic"
	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/capture"
	"github.com/vvvv11112222/cangjie/internal/classroom"
	"github.com/vvvv11112222/cangjie/internal/identity"
	"github.com/vvvv11112222/cangjie/internal/media"
	"github.com/vvvv11112222/cangjie/internal/storage"
)

// Opt-in: real WHIP (H264/Opus), recorder, Go import and Python probe. No paid model calls.
func TestLiveCaptureRealMedia(t *testing.T) {
	mtx, ffmpeg, ffprobe, python := os.Getenv("CAPTURE_TEST_MEDIAMTX"), os.Getenv("CAPTURE_TEST_FFMPEG"), os.Getenv("CAPTURE_TEST_FFPROBE"), os.Getenv("CAPTURE_TEST_PYTHON")
	if mtx == "" || ffmpeg == "" || ffprobe == "" || python == "" {
		t.Skip("CAPTURE_TEST_MEDIAMTX/FFMPEG/FFPROBE/PYTHON not set")
	}
	pool, lesson, source := captureDatabaseFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root, _ := filepath.Abs("../..")
	dir := t.TempDir()
	runtimeCmd := exec.CommandContext(ctx, python, "-m", "workers", "runtime-info")
	runtimeCmd.Dir = root
	runtimeJSON, e := runtimeCmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var runtime struct {
		Processor string `json:"processor_version"`
		SHA       string `json:"ffmpeg_build_sha256"`
	}
	if e = json.Unmarshal(runtimeJSON, &runtime); e != nil {
		t.Fatal(e)
	}
	store, e := storage.NewLocal(filepath.Join(dir, "media"))
	if e != nil {
		t.Fatal(e)
	}
	ids := identity.NewService(pool, time.Hour)
	ms := media.NewService(pool, store, 64<<20, time.Hour)
	as := analysis.NewService(pool, store, analysis.Config{Lease: 120 * time.Second, ProbeTimeout: time.Minute, ASRTimeout: time.Minute, VideoTimeout: time.Minute, MediaJobAttempts: 3, ProcessorVersion: runtime.Processor, FFmpegSHA256: runtime.SHA, ASRModelName: "not-installed", ASRModelRevision: "not-installed", ASRDevice: "cpu", MaxVideoHeight: 1080, MaxMediaDurationMS: 7200000, MaxArtifactBytes: 64 << 20, KeyframeIntervalMS: 30000, MaxKeyframes: 240})
	var handler http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer server.Close()
	port := func() int {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	cp, wp := port(), port()
	udp, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	up := udp.LocalAddr().(*net.UDPAddr).Port
	udp.Close()
	recordRoot := filepath.Join(dir, "recordings")
	secret := strings.Repeat("m", 32)
	eventSecret := strings.Repeat("e", 32)
	cs, e := capture.New(pool, ms, as, ids, capture.Config{PublicURL: "https://capture.test", MediaURL: "https://capture.test/media", ControlURL: fmt.Sprintf("http://127.0.0.1:%d", cp), ControlToken: secret, EventToken: eventSecret, RecordRoot: recordRoot, FFmpeg: ffmpeg, FFprobe: ffprobe, MaxBytes: 64 << 20})
	if e != nil {
		t.Fatal(e)
	}
	workerToken := strings.Repeat("w", 32)
	handler = New(Options{Identity: ids, Academic: academic.NewService(pool), Classroom: classroom.NewService(pool), Media: ms, Analysis: as, Capture: cs, WorkerToken: workerToken, WorkerID: "capture-real", WorkerCapabilities: []string{"probe", "video_analysis"}, PublicOrigin: "http://frontend.test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	config := fmt.Sprintf(`logLevel: warn
authMethod: http
authHTTPAddress: %s/internal/v1/streams/auth
authHTTPExclude: []
api: yes
apiAddress: 127.0.0.1:%d
rtsp: no
rtmp: no
hls: no
srt: no
playback: no
webrtcAddress: 127.0.0.1:%d
webrtcLocalUDPAddress: 127.0.0.1:%d
webrtcIPsFromInterfaces: no
webrtcAdditionalHosts: [127.0.0.1]
pathDefaults:
  source: publisher
  overridePublisher: no
  record: yes
  recordPath: %s/%%path/%%Y-%%m-%%d_%%H-%%M-%%S-%%f
  recordFormat: fmp4
  recordPartDuration: 1s
  recordSegmentDuration: 3s
  recordDeleteAfter: 0s
paths:
  '~^captures/[0-9a-f-]{36}/[0-9]+$': {}
`, server.URL, cp, wp, up, filepath.ToSlash(recordRoot))
	configPath := filepath.Join(dir, "mediamtx.yml")
	if e = os.WriteFile(configPath, []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	var mtxLog bytes.Buffer
	mtxCmd := exec.CommandContext(ctx, mtx, configPath)
	mtxCmd.Stdout = &mtxLog
	mtxCmd.Stderr = &mtxLog
	if e = mtxCmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_ = mtxCmd.Process.Kill()
		_ = mtxCmd.Wait()
		if t.Failed() {
			t.Log(mtxLog.String())
		}
	}()
	ready := false
	for n := 0; n < 40; n++ {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", wp), 50*time.Millisecond)
		if e == nil {
			c.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("MediaMTX failed to start")
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); cs.Run(runCtx) }()
	defer func() { stop(); <-done }()
	client := newTestClient(t)
	csrf := loginTestUser(t, client, server.URL, "admin")
	created := requestWithHeaders(t, client, "POST", server.URL+"/api/v1/sessions/"+lesson+"/live-captures", map[string]any{"source_record_id": source, "config_profile": "phone-demo-v1"}, csrf, map[string]string{"Idempotency-Key": "real-stream"}, 201)
	id := created["id"].(string)
	base := server.URL + "/api/v1/live-captures/" + id
	pair := requestJSON(t, client, "POST", base+"/pairings", map[string]any{}, csrf, 201)
	u, _ := url.Parse(pair["phone_url"].(string))
	f, _ := url.ParseQuery(u.Fragment)
	pub := requestWorker(t, "POST", server.URL+"/api/v1/capture-pairings/exchange", capture.Exchange{Token: f.Get("pairing_token")}, "", "", 200)
	path := queryID(t, pool, `SELECT stream_path FROM teaching.live_captures WHERE id='`+id+`'`)
	var producerLog bytes.Buffer
	producer := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-v", "error", "-re", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=15", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "15", "-c:v", "libx264", "-profile:v", "baseline", "-pix_fmt", "yuv420p", "-tune", "zerolatency", "-g", "30", "-bf", "0", "-c:a", "libopus", "-ac", "2", "-f", "whip", "-authorization", pub["publish_token"].(string), fmt.Sprintf("http://127.0.0.1:%d/%s/whip", wp, path))
	producer.Stderr = &producerLog
	if e = producer.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_ = producer.Process.Kill()
		_ = producer.Wait()
		if t.Failed() {
			t.Log(producerLog.String())
		}
	}()
	wait := func(want string, seconds int) map[string]any {
		t.Helper()
		end := time.Now().Add(time.Duration(seconds) * time.Second)
		for {
			v := requestJSON(t, client, "GET", base, nil, "", 200)
			if v["state"] == want {
				return v
			}
			if v["state"] == "failed" || time.Now().After(end) {
				t.Fatalf("capture state=%v want=%s", v, want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	wait("recording", 15)
	time.Sleep(4 * time.Second)
	requestJSON(t, client, "POST", base+"/viewer-sessions", map[string]any{}, csrf, 201)
	requestJSON(t, client, "POST", base+"/stop", map[string]any{}, csrf, 202)
	end := time.Now().Add(30 * time.Second)
	for {
		v := requestJSON(t, client, "GET", base, nil, "", 200)
		if v["media_prepare_run_id"] != nil {
			break
		}
		if v["state"] == "failed" || time.Now().After(end) {
			t.Fatalf("media import failed: %v", v)
		}
		time.Sleep(100 * time.Millisecond)
	}
	worker := exec.CommandContext(ctx, python, "-m", "workers", "run", "--once")
	worker.Dir = root
	worker.Env = append(os.Environ(), "WORKER_API_BASE="+server.URL+"/internal/v1", "WORKER_TOKEN="+workerToken, "WORKER_ID=capture-real", "WORKER_CAPABILITIES=probe,video_analysis", "WORKER_PROCESSOR_VERSION="+runtime.Processor, "FFMPEG_BUILD_SHA256="+runtime.SHA, "WORKER_TEMP_ROOT="+filepath.Join(dir, "worker"), "PYTHONUTF8=1", "WORKER_CLEANUP_ENABLED=0", "CAPTURE_LIVE_VISION_ENABLED=false")
	if out, e := worker.CombinedOutput(); e != nil {
		t.Fatalf("probe worker: %v %s", e, out)
	}
	v := wait("ready", 15)
	if v["audio_track"] != "present" {
		t.Fatal("audio track missing")
	}
	meta := requestJSON(t, client, "GET", server.URL+"/api/v1/media/"+v["media_asset_id"].(string), nil, "", 200)
	target := server.URL + meta["playback_url"].(string)
	req, _ := http.NewRequest("GET", target, nil)
	req.Header.Set("Range", "bytes=0-31")
	res, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || len(data) != 32 {
		t.Fatalf("playback Range status=%d bytes=%d", res.StatusCode, len(data))
	}
	t.Log("real WHIP H264/Opus -> recording -> normalized media -> Python probe -> ready -> Range 206; duration_ms=" + strconv.FormatFloat(v["duration_ms"].(float64), 'f', 0, 64))
}
