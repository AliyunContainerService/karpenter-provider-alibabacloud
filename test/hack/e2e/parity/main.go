package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("parity", flag.ContinueOnError)
	repo := fs.String("repo", ".", "repository root to audit")
	format := fs.String("format", "text", "output format: text or json")
	strict := fs.Bool("strict", false, "exit non-zero when parity is incomplete")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report, err := Audit(*repo)
	if err != nil {
		return err
	}

	switch *format {
	case "text":
		fmt.Print(FormatText(report))
	case "json":
		data, err := FormatJSON(report)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	default:
		return fmt.Errorf("unknown format %q", *format)
	}

	if *strict && !report.Complete() {
		return fmt.Errorf("E2E parity is incomplete")
	}
	return nil
}
