// Package asrrequest shares the delivered demo's cloud ASR request with the main API.
package asrrequest

import (
	"encoding/base64"
	"strings"
)

const Version = "demo-asr-v1"

func Body(audio []byte, model, hints string) map[string]any {
	options := map[string]any{"enable_itn": false}
	languages := []string{}
	for _, hint := range strings.Split(hints, ",") {
		if hint = strings.TrimSpace(hint); hint != "" {
			languages = append(languages, hint)
		}
	}
	if len(languages) > 0 {
		options["language_hints"] = languages
	}
	return map[string]any{"model": model, "stream": false, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(audio)}}}}}, "asr_options": options}
}
