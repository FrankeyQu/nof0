// Code scaffolded by goctl. Safe to edit.
// goctl 1.9.2

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"nof0-api/internal/commandworker"
	"nof0-api/internal/config"
	"nof0-api/internal/handler"
	"nof0-api/internal/preflight"
	"nof0-api/internal/svc"

	"github.com/zeromicro/go-zero/rest"
)

func main() {
	flag.Parse()

	cfg := config.MustLoad()
	if strings.EqualFold(strings.TrimSpace(cfg.Env), "prod") && !truthyEnv(os.Getenv("NOF0_SKIP_SELF_PREFLIGHT")) {
		report := preflight.Run(cfg, preflight.ProductionTradingOptions())
		for _, issue := range report.Issues {
			switch issue.Severity {
			case preflight.SeverityFatal:
				fmt.Printf("[preflight][fatal] %s: %s\n", issue.ID, issue.Message)
			default:
				fmt.Printf("[preflight][warning] %s: %s\n", issue.ID, issue.Message)
			}
		}
		if !report.OK() {
			panic(fmt.Sprintf("production preflight failed: fatal=%d warning=%d", report.FatalCount(), report.WarningCount()))
		}
	}

	server := rest.MustNewServer(cfg.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(*cfg, cfg.MainPath())
	handler.RegisterHandlers(server, ctx)

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerRuntime, err := commandworker.Start(rootCtx, cfg.CommandWorker, ctx)
	if err != nil {
		panic(err)
	}
	if workerRuntime != nil {
		defer workerRuntime.Stop()
	}

	fmt.Printf("Starting server at %s:%d...\n", cfg.Host, cfg.Port)
	server.Start()
}

func truthyEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
