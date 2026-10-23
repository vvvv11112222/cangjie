package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "worker-plan" {
		fmt.Fprintln(os.Stderr, "usage: audio-plan worker-plan <silence-log> <duration_ms>")
		os.Exit(1)
	}
	if err := workerPlan(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "audio segmentation failed")
		os.Exit(1)
	}
}
