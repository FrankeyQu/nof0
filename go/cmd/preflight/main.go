package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	appconfig "nof0-api/internal/config"
	"nof0-api/internal/preflight"
)

func main() {
	profile := flag.String("profile", "trading", "preflight profile: trading|api")
	format := flag.String("format", "text", "output format: text|json")
	flag.Parse()

	opts, err := optionsForProfile(*profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preflight: %v\n", err)
		os.Exit(2)
	}
	report, err := preflight.RunFile(appconfig.ConfigFile(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preflight: load config failed: %v\n", err)
		os.Exit(1)
	}

	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "", "text":
		printText(report)
	case "json":
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "preflight: encode json failed: %v\n", err)
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "preflight: unsupported format %q\n", *format)
		os.Exit(2)
	}

	if !report.OK() {
		os.Exit(1)
	}
}

func optionsForProfile(profile string) (preflight.Options, error) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "trading":
		return preflight.ProductionTradingOptions(), nil
	case "api":
		return preflight.ProductionAPIOptions(), nil
	default:
		return preflight.Options{}, fmt.Errorf("unsupported profile %q", profile)
	}
}

func printText(report preflight.Report) {
	fmt.Printf("Production preflight: %s\n", report.ConfigPath)
	if len(report.Issues) == 0 {
		fmt.Println("OK: no fatal issues or warnings")
		return
	}
	for _, issue := range report.Issues {
		fmt.Printf("[%s] %s: %s\n", issue.Severity, issue.ID, issue.Message)
	}
	fmt.Printf("Summary: fatal=%d warning=%d\n", report.FatalCount(), report.WarningCount())
}
