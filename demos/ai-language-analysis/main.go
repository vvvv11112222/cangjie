package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

//go:embed web/* prompt.txt reading_prompt.txt events_prompt.txt audit_prompt.txt rubric.md
var assets embed.FS
var busy = make(chan bool, 1)
var jobs *jobManager

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatal("无法加载当前目录的 .env，请在 demo 目录启动；检查文件格式（错误详情不输出以避免泄露密钥）")
	}
	for _, tool := range []struct{ key, fallback string }{{"FFPROBE_PATH", "ffprobe"}, {"FFMPEG_PATH", "ffmpeg"}} {
		path := env(tool.key, tool.fallback)
		resolved, err := exec.LookPath(path)
		if err != nil {
			log.Fatalf("%s 无法找到程序：%s；检查 .env 和同名环境变量", tool.key, path)
		}
		log.Printf("%s=%s", tool.key, resolved)
	}
	addr := env("DEMO_HOST", "127.0.0.1") + ":" + env("DEMO_PORT", "8090")
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal("服务地址无法监听，请先停止占用端口的旧程序；刷新网页不会切换后端")
	}
	defer listener.Close()
	store, err := newJobStore(env("DEMO_DATA_DIR", "data"))
	if err != nil {
		log.Fatalf("任务数据目录不可用: %v", err)
	}
	jobs = newJobManager(store)
	mux := newDemoMux(jobs, env("DEMO_DATA_DIR", "data"))
	log.Print("http://" + addr)
	log.Printf("报告程序：%s；正文规则：%s；全文复核：%s", analysisVersion, narrativeProtocol, reportAuditVersion)
	log.Printf("媒体流程：%s；上传上限60分钟/2GB；长课按窗口分析", mediaPipelineVersion)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopSignal)
	go func() {
		<-stopSignal
		jobs.stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		jobs.stop()
		log.Printf("服务退出: %v", err)
	}
}
func newDemoMux(manager *jobManager, root string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /report.css", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("web/report.css")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /transcript.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("web/transcript.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /jobs.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("web/jobs.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /recovery.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("web/recovery.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /analysis.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("web/analysis.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJobJSON(w, 200, map[string]any{"status": "ok", "rubric_version": rubricVersion,
			"analysis_version": analysisVersion, "narrative_protocol": narrativeProtocol,
			"media_pipeline_version": mediaPipelineVersion, "max_duration_seconds": maxMediaSeconds, "max_upload_bytes": maxUploadBytes,
			"advice_protocol": adviceProtocol, "audit_version": reportAuditVersion,
			"semantic_protocol": semanticProtocol})
	})
	mux.HandleFunc("GET /events.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("web/events.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := assets.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("POST /api/run", run)
	manager.registerRoutes(mux, root)
	return mux
}
func run(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		http.Error(w, "origin rejected", 403)
		return
	}
	select {
	case busy <- true:
		defer func() { <-busy }()
	default:
		http.Error(w, "已有任务运行", 409)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	if e := r.ParseMultipartForm(8 << 20); e != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		http.Error(w, "上传失败或超过2GB", 400)
		return
	}
	defer r.MultipartForm.RemoveAll()
	key, base := os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("BAILIAN_BASE_URL")
	if key == "" || base == "" || strings.Contains(base, "YOUR_") {
		http.Error(w, "请配置 DASHSCOPE_API_KEY 与 BAILIAN_BASE_URL 环境变量", 422)
		return
	}
	if r.FormValue("consent") != "yes" {
		http.Error(w, "请确认云端处理授权和可能费用", 422)
		return
	}
	f, header, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "请选择视频", 400)
		return
	}
	defer f.Close()
	if header.Size > maxUploadBytes {
		http.Error(w, "文件超过2GB，请压缩视频后上传", http.StatusRequestEntityTooLarge)
		return
	}
	dir, err := os.MkdirTemp("", "classroom-demo-")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.RemoveAll(dir)
	video := filepath.Join(dir, "input.media")
	dst, err := os.Create(video)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_, err = io.Copy(dst, f)
	ce := dst.Close()
	if err != nil || ce != nil {
		http.Error(w, "文件保存失败", 500)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), jobTimeout)
	defer cancel()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	emit := func(t string, d any) {
		json.NewEncoder(w).Encode(map[string]any{"type": t, "data": d})
		w.(http.Flusher).Flush()
	}
	fail := func(e error) { emit("error", e.Error()) }
	emit("progress", "检查视频并提取音频")
	out, err := exec.CommandContext(ctx, env("FFPROBE_PATH", "ffprobe"), "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", video).Output()
	if err != nil {
		fail(fmt.Errorf("ffprobe检查失败: %w", err))
		return
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || validateMediaDuration(seconds) != nil {
		fail(fmt.Errorf("视频时长无效或超过60分钟"))
		return
	}
	audio := filepath.Join(dir, "audio.wav")
	if err = exec.CommandContext(ctx, env("FFMPEG_PATH", "ffmpeg"), "-nostdin", "-v", "error", "-y", "-i", video, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", audio).Run(); err != nil {
		fail(fmt.Errorf("提取失败: %w", err))
		return
	}
	segments := []Segment{}
	metas := []ModelMeta{}
	emit("progress", "检测音频停顿，规划弹性分段")
	// Probe extracted audio: a video can continue after its audio track ends.
	out, err = exec.CommandContext(ctx, env("FFPROBE_PATH", "ffprobe"), "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", audio).Output()
	if err != nil {
		fail(fmt.Errorf("音频时长检查失败: %w", err))
		return
	}
	seconds, err = strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || validateMediaDuration(seconds) != nil {
		fail(fmt.Errorf("音频时长无效或超过60分钟"))
		return
	}
	duration := int64(math.Round(seconds * 1000))
	pauses, err := detectSilences(ctx, env("FFMPEG_PATH", "ffmpeg"), audio, duration)
	if err != nil {
		fail(err)
		return
	}
	chunks := planChunks(duration, pauses)
	emit("segmentation", chunks)
	for i, interval := range chunks {
		start, end := float64(interval.Start)/1000, float64(interval.End)/1000
		emit("progress", fmt.Sprintf("语音转写：第 %d / %d 段（%.1f–%.1f 秒）", i+1, len(chunks), start, end))
		chunk := filepath.Join(dir, "chunk.wav")
		err = exec.CommandContext(ctx, env("FFMPEG_PATH", "ffmpeg"), "-nostdin", "-v", "error", "-y", "-ss", fmt.Sprintf("%.3f", start), "-i", audio, "-t", fmt.Sprintf("%.3f", end-start), "-c:a", "pcm_s16le", chunk).Run()
		if err != nil {
			fail(err)
			return
		}
		b, e := os.ReadFile(chunk)
		if e != nil {
			fail(e)
			return
		}
		asrOptions := map[string]any{"enable_itn": false}
		if hints := strings.TrimSpace(env("BAILIAN_ASR_LANGUAGE_HINTS", "zh,en")); hints != "" {
			parts := strings.Split(hints, ",")
			clean := make([]string, 0, len(parts))
			for _, part := range parts {
				if part = strings.TrimSpace(part); part != "" {
					clean = append(clean, part)
				}
			}
			if len(clean) > 0 {
				asrOptions["language_hints"] = clean
			}
		}
		body := map[string]any{"model": env("BAILIAN_ASR_MODEL", "qwen3-asr-flash"), "stream": false, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(b)}}}}}, "asr_options": asrOptions}
		text, meta, e := callModel(ctx, base, key, body, false)
		if e != nil {
			fail(e)
			return
		}
		metas = append(metas, meta)
		if strings.TrimSpace(text) != "" {
			segments = append(segments, Segment{ID: fmt.Sprintf("e-%04d", len(segments)+1), Start: interval.Start, End: interval.End, Text: text, Boundary: interval.Boundary})
		}
		emit("transcript", segments)
	}
	if len(segments) == 0 {
		fail(fmt.Errorf("未识别到文本"))
		return
	}
	emit("progress", "结合上下文整理跨段语句（保留原始转写）")
	reading := organizeReading(ctx, base, key, segments)
	emit("reading", reading)
	if ctx.Err() != nil {
		return
	}
	emit("progress", "提取教学事件并建立证据链")
	events, eventMeta, err := extractEvents(ctx, base, key, segments)
	if err != nil {
		fail(fmt.Errorf("教学事件提取失败: %w", err))
		return
	}
	emit("events", events)
	emit("progress", "基于教学事件生成六维报告")
	text, meta, err := requestSixDimReport(ctx, base, key, segments, reading, events)
	if err != nil {
		fail(err)
		return
	}
	draft, err := decodeReportDraft(text)
	if err != nil {
		fail(err)
		return
	}
	report, err := bindReportDraft(draft, events.Events, segments)
	if err != nil {
		fail(errors.New(reportBindingMessage(err)))
		return
	}
	normalizeReportReferenceDuplicates(&report)
	normalizeReportCoverage(&report, events.Events)
	if err = validateReport(report, segments); err != nil {
		fail(err)
		return
	}
	if err = validateEventReport(report, events.Events, segments); err != nil {
		fail(err)
		return
	}
	emit("progress", "核对整段上下文与建议")
	report.Audit = auditReport(ctx, base, key, report, segments, events)
	if ctx.Err() != nil {
		return
	}
	emit("result", map[string]any{"segments": segments, "audio_chunks": chunks, "reading": reading, "events": events, "report": report, "timestamp_precision": "按停顿弹性切分的音频块（最长40秒），并非句子或词级对齐", "rubric_version": rubricVersion, "asr_model": env("BAILIAN_ASR_MODEL", "qwen3-asr-flash"), "text_model": env("BAILIAN_TEXT_MODEL", "qwen-plus"), "asr_requests": metas, "event_request": eventMeta, "text_request": meta})
}
