// adapt-report is an offline team integration tool, not a publication endpoint.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"cangjie-demo/integration/reportadapter"
)

func main() {
	report := flag.String("report", "", "demo Report JSON (not a complete job export)")
	input := flag.String("model-input", "", "exact final upstream ModelInput UTF-8 bytes")
	context := flag.String("context", "", "trusted server bindings and identity JSON")
	flag.Parse()
	if *report == "" || *input == "" || *context == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*report, *input, *context); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(report, input, context string) error {
	read := func(name string) ([]byte, error) {
		info, err := os.Stat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return nil, fmt.Errorf("input missing, not regular or over 8 MiB")
		}
		return os.ReadFile(name)
	}
	r, err := read(report)
	if err != nil {
		return err
	}
	i, err := read(input)
	if err != nil {
		return err
	}
	c, err := read(context)
	if err != nil {
		return err
	}
	var ctx reportadapter.Context
	if err := json.Unmarshal(c, &ctx); err != nil {
		return fmt.Errorf("invalid context JSON")
	}
	result, err := reportadapter.Convert(r, i, ctx)
	if err != nil {
		return err
	}
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	return e.Encode(result)
}
