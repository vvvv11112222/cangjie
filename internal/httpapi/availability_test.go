package httpapi

import (
	"testing"
	"time"
)

func TestWorkerAnnouncementsExpireAndKeepGroupsSeparate(t *testing.T) {
	var a workerAnnouncements
	now := time.Now()
	caps, visual := a.snapshot(now)
	if caps["probe"] || visual {
		t.Fatal("unseen Worker was presented as ready")
	}
	a.announce([]string{"probe", "video_analysis"}, true, now, "video-job")
	a.announce([]string{"audio_analysis"}, false, now, "audio-job")
	for i := 1; i <= 4; i++ {
		a.heartbeat("video-job", now.Add(time.Duration(i)*30*time.Second))
	}
	caps, visual = a.snapshot(now.Add(121 * time.Second))
	if !caps["probe"] || !caps["video_analysis"] || !visual || caps["audio_analysis"] {
		t.Fatal("video heartbeat revived an unrelated audio process", caps, visual)
	}
	a.heartbeat("unknown-job", now.Add(200*time.Second))
	caps, visual = a.snapshot(now.Add(211 * time.Second))
	if caps["probe"] || visual {
		t.Fatal("stopped process stayed ready", caps, visual)
	}
	a.announce([]string{"video_analysis", "probe"}, false, now.Add(212*time.Second), "")
	caps, visual = a.snapshot(now.Add(212 * time.Second))
	if !caps["video_analysis"] || visual {
		t.Fatal("missing models did not clear visual readiness")
	}
	a.heartbeat("video-job", now.Add(300*time.Second))
	caps, _ = a.snapshot(now.Add(303 * time.Second))
	if caps["probe"] {
		t.Fatal("retired lease revived a newer process declaration")
	}
}
