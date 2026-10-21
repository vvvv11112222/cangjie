package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Only local segmentation; no .env, credentials, model calls or demo job store.
func workerPlan(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("worker-plan requires silence log and duration_ms")
	}
	duration, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || duration <= 0 || duration > 7200000 {
		return fmt.Errorf("invalid audio duration")
	}
	log, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	pauses := parseSilences(string(log), duration)
	return json.NewEncoder(os.Stdout).Encode(planChunks(duration, pauses))
}
