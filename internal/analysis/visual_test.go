package analysis

import (
	"encoding/json"
	"os"
	"testing"
)

func visualFixture(t *testing.T) (VideoResult, Parameters) {
	t.Helper()
	raw, err := os.ReadFile("../../contracts/examples/visual-video-result.json")
	if err != nil {
		t.Fatal(err)
	}
	var v VideoResult
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v, Parameters{ConfigProfile: "vision-v1", KeyframeIntervalMS: 500, MaxKeyframes: 240, Visual: &VisualParameters{Model: *v.Model, MaxSamples: 15000, MaxPosePeople: 80, HeadDownEnabled: true}}
}
func TestVisualValidation(t *testing.T) {
	v, p := visualFixture(t)
	if err := validateVisual(v, p, v.Visual.InputSHA256, 2000, 160, 120); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*VideoResult){
		"wrong-model":      func(v *VideoResult) { v.Model.Revision = "wrong" },
		"wrong-source":     func(v *VideoResult) { v.Visual.InputSHA256 = "wrong" },
		"outside-frame":    func(v *VideoResult) { v.Visual.Persons[0].Observations[0].BBox[2] = 500 },
		"unsampled-time":   func(v *VideoResult) { v.Visual.Persons[0].Observations[0].TimestampMS = 100 },
		"duplicate-person": func(v *VideoResult) { v.Visual.Persons = append(v.Visual.Persons, v.Visual.Persons[0]) },
		"missing-behavior": func(v *VideoResult) { delete(v.Visual.Persons[0].Observations[0].Behaviors, "head_down") },
		"foreign-image":    func(v *VideoResult) { id := "missing"; v.Visual.Persons[0].Observations[0].FrameAssetID = &id },
		"image-wrong-time": func(v *VideoResult) { v.Frames[0].TimestampMS = 1 },
		"foreign-event-track": func(v *VideoResult) {
			var e VisualEvent
			json.Unmarshal(v.Events[0], &e)
			e.TrackID = "T9999"
			v.Events[0], _ = json.Marshal(e)
		},
		"event-without-positive": func(v *VideoResult) { b := false; v.Visual.Persons[0].Observations[0].Behaviors["hand_raise"] = &b },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			v, p := visualFixture(t)
			sha := v.Visual.InputSHA256
			change(&v)
			if validateVisual(v, p, sha, 2000, 160, 120) == nil {
				t.Fatal("accepted invalid visual result")
			}
		})
	}
}
func TestVisualSnapshotPinsDifferentProfile(t *testing.T) {
	s := NewService(nil, nil, Config{VisionEnabled: true, VisionModelRevision: "pinned", ASRDevice: "cpu", KeyframeIntervalMS: 30000, MaxKeyframes: 240})
	visual := s.newSnapshot("visual_only")
	p0 := s.newSnapshot("full")
	if len(visual.StagePlan) != 2 || visual.Parameters.Visual == nil || p0.Parameters.Visual != nil || visual.Executions["probe"].ParametersSHA256 == p0.Executions["probe"].ParametersSHA256 {
		t.Fatal("profile isolation failed")
	}
	var claim Claim
	raw, _ := os.ReadFile("../../contracts/examples/claim-visual.json")
	if err := json.Unmarshal(raw, &claim); err != nil {
		t.Fatal(err)
	}
	s.cfg.VisionModelRevision = claim.Parameters.Visual.Model.Revision
	if s.newSnapshot("visual_only").Executions["video_analysis"].ParametersSHA256 != claim.Execution.ParametersSHA256 {
		t.Fatal("Go/Python visual parameter hash differs")
	}
}

func TestVisualShapeDoesNotAcceptMissingKeysOrExtraBoxCoordinates(t *testing.T) {
	v, _ := visualFixture(t)
	raw, _ := json.Marshal(v)
	if err := validateVisualShape(raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	json.Unmarshal(raw, &root)
	data := root["visual"].(map[string]any)
	people := data["persons"].([]any)
	o := people[0].(map[string]any)["observations"].([]any)[0].(map[string]any)
	delete(o, "frame_asset_id")
	bad, _ := json.Marshal(root)
	if validateVisualShape(bad) == nil {
		t.Fatal("accepted missing explicit nullable field")
	}
	o["frame_asset_id"] = nil
	o["bbox"] = []int{0, 0, 10, 10, 20}
	bad, _ = json.Marshal(root)
	if validateVisualShape(bad) == nil {
		t.Fatal("accepted silently truncated box")
	}
}
