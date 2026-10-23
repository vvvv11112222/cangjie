package httpapi

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveVisionBrokerBoundedAndExpired(t *testing.T) {
	b := &liveVisionBroker{}
	first := b.offer("capture", liveFrame{Sequence: 1, Image: "synthetic"}, 640, 360, "")
	if first == nil || b.offer("another", liveFrame{}, 640, 360, "") != nil {
		t.Fatal("broker must keep one frame globally")
	}
	first.expires = time.Now().Add(-time.Second)
	next := b.offer("capture", liveFrame{Sequence: 2}, 640, 360, "")
	if next == nil || next.ID == first.ID || next.Token == first.Token {
		t.Fatal("expired frame was not replaced")
	}
	b.drop(first)
	if b.work != next {
		t.Fatal("late cleanup dropped a newer frame")
	}
	b.drop(next)
	if b.work != nil {
		t.Fatal("frame was retained")
	}
}
func TestLiveVisionDisabledDoesNotRequireAnIdentityService(t *testing.T) {
	w := httptest.NewRecorder()
	New(Options{}).ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/live-captures/00000000-0000-4000-8000-000000000001/vision-frames", strings.NewReader("{}")))
	if w.Code != 503 {
		t.Fatalf("disabled preview status=%d", w.Code)
	}
}
func TestLiveVisionResultValidation(t *testing.T) {
	v := liveResult{Width: 640, Height: 360, People: []livePerson{{ID: "L1", Box: [4]float64{.1, .1, .2, .2}}}}
	if !validLiveResult(v, 640, 360) {
		t.Fatal("valid unknown observation rejected")
	}
	v.People[0].Box[0] = math.NaN()
	if validLiveResult(v, 640, 360) {
		t.Fatal("nonfinite box accepted")
	}
	v.People[0].Box = [4]float64{.1, .1, .2, .2}
	v.People = append(v.People, v.People[0])
	if validLiveResult(v, 640, 360) {
		t.Fatal("duplicate identity accepted")
	}
}
func TestLiveVisionWorkerAuthenticationAndCompletionFence(t *testing.T) {
	b := &liveVisionBroker{}
	s := &server{liveVision: b, workerToken: "synthetic-worker", workerCapabilities: map[string]bool{"video_analysis": true}}
	task := b.offer("capture", liveFrame{Sequence: 1}, 640, 360, "")
	request := func(method, path, body, worker, lease string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+worker)
		r.Header.Set("X-Lease-Token", lease)
		w := httptest.NewRecorder()
		mux := http.NewServeMux()
		s.registerLiveVisionRoutes(mux)
		mux.ServeHTTP(w, r)
		return w
	}
	if request("POST", "/internal/v1/live-vision/claim", "{}", "wrong", "").Code != 401 {
		t.Fatal("anonymous worker accepted")
	}
	if request("POST", "/internal/v1/live-vision/claim", "{}", s.workerToken, "").Code != 200 {
		t.Fatal("claim failed")
	}
	if request("POST", "/internal/v1/live-vision/claim", "{}", s.workerToken, "").Code != 204 {
		t.Fatal("claimed frame replayed")
	}
	body := `{"sequence":1,"width":640,"height":360,"people":[],"error_code":null}`
	path := "/internal/v1/live-vision/" + task.ID + "/complete"
	if request("POST", path, body, s.workerToken, "wrong").Code != 409 {
		t.Fatal("wrong lease accepted")
	}
	if request("POST", path, body, s.workerToken, task.Token).Code != 204 {
		t.Fatal("completion failed")
	}
	if request("POST", path, body, s.workerToken, task.Token).Code != 409 {
		t.Fatal("completion replay accepted")
	}
}
