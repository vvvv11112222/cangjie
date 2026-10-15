package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vvvv11112222/cangjie/internal/analysis"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

type probeInfo struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func copyFile(ctx context.Context, dst io.Writer, src io.Reader, max int64) error {
	n, e := io.Copy(dst, io.LimitReader(contextReader{ctx, src}, max+1))
	if e == nil && n > max {
		return invalid("segment exceeds size limit")
	}
	return e
}

// boundedBuffer prevents a corrupt file/tool from exhausting the API process.
type boundedBuffer struct {
	bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, fmt.Errorf("tool output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (s *Service) probe(ctx context.Context, path string) (probeInfo, int64, error) {
	var v probeInfo
	out := &boundedBuffer{max: 1 << 20}
	cmd := exec.CommandContext(ctx, s.cfg.FFprobe, "-v", "error", "-show_streams", "-show_format", "-of", "json", path)
	cmd.Stdout = out
	cmd.Stderr = &boundedBuffer{max: 1 << 16}
	if e := cmd.Run(); e != nil {
		return v, 0, fmt.Errorf("probe recording: %w", e)
	}
	if e := json.Unmarshal(out.Bytes(), &v); e != nil {
		return v, 0, e
	}
	seconds, e := strconv.ParseFloat(v.Format.Duration, 64)
	if e != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > s.cfg.MaxDuration.Seconds()+1 {
		return v, 0, invalid("invalid recording duration")
	}
	video := false
	for _, st := range v.Streams {
		video = video || st.CodecType == "video"
	}
	if !video {
		return v, 0, invalid("recording has no video")
	}
	return v, int64(math.Round(seconds * 1000)), nil
}
func (s *Service) finalize(ctx context.Context, r record, p identity.Principal, lease string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				var permitted bool
				err := s.pool.QueryRow(ctx, `UPDATE teaching.live_captures SET finalize_until=LEAST(clock_timestamp()+interval '30 seconds',stop_requested_at+$3::interval) WHERE id=$1 AND state='finalizing' AND finalize_token=$2::uuid AND finalize_until>clock_timestamp() AND stop_requested_at+$3::interval>clock_timestamp() RETURNING true`, r.ID, lease, s.cfg.FinalizeTimeout.String()).Scan(&permitted)
				owner, pe := s.identity.PrincipalByID(ctx, r.UserID)
				if err != nil || !permitted || s.valid(ctx, s.pool, r) != nil || pe != nil || s.media.AuthorizeUpload(ctx, owner, r.SessionID) != nil {
					cancel()
					return
				}
			}
		}
	}()
	files, e := s.files(r)
	if e != nil {
		return e
	}
	if len(files) == 0 {
		return s.abort(ctx, r, "CAPTURE_NO_MEDIA")
	}
	work, e := os.MkdirTemp(s.cfg.RecordRoot, ".finalize-"+r.ID+"-")
	if e != nil {
		return e
	}
	// work is generated directly under the fixed root; remove only owned files.
	defer func() {
		entries, _ := os.ReadDir(work)
		for _, v := range entries {
			if !v.IsDir() {
				_ = os.Remove(filepath.Join(work, v.Name()))
			}
		}
		_ = os.Remove(work)
	}()
	var list strings.Builder
	var expected int64
	for n, file := range files {
		_, duration, e := s.probe(ctx, file)
		if e != nil {
			return e
		}
		expected += duration
		// Copy to controlled numeric names; concat syntax never contains caller paths.
		name := fmt.Sprintf("%06d.mp4", n)
		src, e := os.Open(file)
		if e != nil {
			return e
		}
		dst, e := os.OpenFile(filepath.Join(work, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			src.Close()
			return e
		}
		e = copyFile(ctx, dst, src, s.cfg.MaxBytes)
		src.Close()
		ce := dst.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		fmt.Fprintf(&list, "file '%s'\n", name)
	}
	if time.Duration(expected)*time.Millisecond > s.cfg.MaxDuration+time.Second {
		return invalid("recording exceeds duration limit")
	}
	manifest := filepath.Join(work, "inputs.txt")
	if e = os.WriteFile(manifest, []byte(list.String()), 0600); e != nil {
		return e
	}
	output := filepath.Join(work, "recording.mp4")
	cmd := exec.CommandContext(ctx, s.cfg.FFmpeg, "-nostdin", "-v", "error", "-y", "-f", "concat", "-safe", "1", "-i", manifest, "-map", "0:v:0", "-map", "0:a:0?", "-c:v", "copy", "-c:a", "aac", "-movflags", "+faststart", "-fs", strconv.FormatInt(s.cfg.MaxBytes, 10), output)
	cmd.Stdout = &boundedBuffer{max: 1 << 16}
	cmd.Stderr = &boundedBuffer{max: 1 << 16}
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("normalize recording: %w", e)
	}
	info, e := os.Stat(output)
	if e != nil {
		return e
	}
	if info.Size() >= s.cfg.MaxBytes {
		return invalid("normalized recording exceeds limit")
	}
	v, duration, e := s.probe(ctx, output)
	if e != nil {
		return e
	}
	if math.Abs(float64(duration-expected)) > 1000 {
		return bad("TIMELINE_INVALID", "normalized recording does not preserve segment duration")
	}
	f, e := os.Open(output)
	if e != nil {
		return e
	}
	id, e := s.media.ImportRecording(ctx, p, r.ID, lease, f)
	f.Close()
	if e != nil {
		return e
	}
	audio := "absent"
	for _, st := range v.Streams {
		if st.CodecType == "audio" {
			audio = "present"
		}
	}
	_, e = s.pool.Exec(ctx, `UPDATE teaching.live_captures SET duration_ms=$3,audio_track=$4,video_track='present',recording_state='preparing',error_code=NULL WHERE id=$1 AND finalize_token=$2::uuid AND state='finalizing'`, r.ID, lease, duration, audio)
	if e != nil {
		return e
	}
	r.MediaID = &id
	return s.prepare(ctx, r, p)
}
func (s *Service) prepare(ctx context.Context, r record, p identity.Principal) error {
	if r.MediaID == nil {
		return nil
	}
	if r.RunID == nil {
		run, _, e := s.analysis.Create(ctx, p, r.SessionID, "capture:"+r.ID, analysis.CreateRun{MediaAssetID: *r.MediaID, Mode: "media_prepare", ConfigProfile: "p0-v1"})
		if e != nil {
			return e
		}
		_, e = s.pool.Exec(ctx, `UPDATE teaching.live_captures SET media_prepare_run_id=$2 WHERE id=$1 AND state='finalizing' AND media_prepare_run_id IS NULL`, r.ID, run.ID)
		if e != nil {
			return e
		}
		r.RunID = &run.ID
	}
	var state string
	if e := s.pool.QueryRow(ctx, `SELECT status FROM teaching.analysis_runs WHERE id=$1`, *r.RunID).Scan(&state); e != nil {
		return e
	}
	if state == "succeeded" {
		if e := s.valid(ctx, s.pool, r); e != nil {
			return s.abort(ctx, r, "SOURCE_NOT_VERIFIED")
		}
		_, e := s.pool.Exec(ctx, `UPDATE teaching.live_captures SET state='ready',ended_at=now(),recording_state='complete',connection_state='disconnected',error_code=NULL WHERE id=$1 AND state='finalizing'`, r.ID)
		return e
	}
	if state == "failed" || state == "cancelled" || state == "partial" {
		return s.abort(ctx, r, "CAPTURE_PREPARE_FAILED")
	}
	return nil
}
