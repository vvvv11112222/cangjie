// Print the bundled prompt identity without loading .env, connecting a database,
// or contacting a model provider.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vvvv11112222/cangjie/internal/analysis"
)

func main() {
	version := flag.String("version", analysis.TeacherPromptVersion, "bundled prompt version")
	flag.Parse()
	_, sha, err := analysis.ReportPrompt(*version)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("REPORT_PROMPT_VERSION=%s\nREPORT_PROMPT_SHA256=%s\n", *version, sha)
}
