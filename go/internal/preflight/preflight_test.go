package preflight

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"

	appconfig "nof0-api/internal/config"
	"nof0-api/pkg/confkit"
	exchangepkg "nof0-api/pkg/exchange"
	executorpkg "nof0-api/pkg/executor"
	llmpkg "nof0-api/pkg/llm"
	managerpkg "nof0-api/pkg/manager"
	marketpkg "nof0-api/pkg/market"
)

func TestProductionTradingPreflightPassesValidPaperConfig(t *testing.T) {
	report := Run(validPreflightConfig(), ProductionTradingOptions())
	if !report.OK() {
		t.Fatalf("expected preflight to pass, got issues: %+v", report.Issues)
	}
}

func TestProductionTradingPreflightRequiresProdEnv(t *testing.T) {
	cfg := validPreflightConfig()
	cfg.Env = "dev"

	report := Run(cfg, ProductionTradingOptions())
	if !hasIssue(report, "env.not_prod") {
		t.Fatalf("expected env.not_prod issue, got %+v", report.Issues)
	}
}

func TestProductionTradingPreflightRequiresPersistence(t *testing.T) {
	cfg := validPreflightConfig()
	cfg.Postgres.DataSource = ""
	cfg.Cache = nil

	report := Run(cfg, ProductionTradingOptions())
	if !hasIssue(report, "persistence.postgres_missing") {
		t.Fatalf("expected persistence.postgres_missing issue, got %+v", report.Issues)
	}
	if !hasIssue(report, "persistence.cache_missing") {
		t.Fatalf("expected persistence.cache_missing issue, got %+v", report.Issues)
	}
}

func TestProductionTradingPreflightRequiresHardRisk(t *testing.T) {
	cfg := validPreflightConfig()
	trader := &cfg.Manager.Value.Traders[0]
	trader.RiskParams.AllowedSymbols = nil
	trader.RiskParams.MaxDailyLossUSD = 0
	trader.RiskParams.MaxDailyLossPct = 0

	report := Run(cfg, ProductionTradingOptions())
	if !hasIssue(report, "manager.traders[paper].risk.allowed_symbols_empty") {
		t.Fatalf("expected allowed symbol issue, got %+v", report.Issues)
	}
	if !hasIssue(report, "manager.traders[paper].risk.daily_loss_missing") {
		t.Fatalf("expected daily loss issue, got %+v", report.Issues)
	}
}

func TestInspectRawConfigFailsWildcardCorsInProd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nof0.yaml")
	if err := os.WriteFile(path, []byte("Cors:\n  AllowOrigins: ['*']\n"), 0o600); err != nil {
		t.Fatalf("write raw config: %v", err)
	}

	issues := inspectRawConfig(path, &appconfig.Config{Env: "prod"})
	if len(issues) != 1 || issues[0].ID != "cors.wildcard_origin" || issues[0].Severity != SeverityFatal {
		t.Fatalf("unexpected cors issues: %+v", issues)
	}
}

func TestProductionConfigTemplatePassesWithSampleEnv(t *testing.T) {
	t.Setenv("ZENMUX_API_KEY", "preflight-test-key")
	t.Setenv("Postgres__DataSource", "postgres://nof0:secret@db.example.com:5432/nof0?sslmode=require&default_query_exec_mode=simple_protocol")
	t.Setenv("Cache__0__Host", "redis.example.com:6379")
	t.Setenv("Cache__0__Pass", "redis-secret")
	t.Setenv("Cache__0__User", "default")
	t.Setenv("ASTRAQUANT_CORS_ORIGIN", "https://app.example.com")
	t.Setenv("ASTRAQUANT_TRADER_ID", "prod_testnet_trader")
	t.Setenv("ASTRAQUANT_SESSION_ID", "prod_session")
	t.Setenv("ASTRAQUANT_SECRET_PROD_TESTNET_TRADER_PROD_SESSION_HYPERLIQUID_PRIVATE_KEY", "0000000000000000000000000000000000000000000000000000000000000001")
	t.Setenv("HYPERLIQUID_MAIN_ADDRESS", "0x1b673761f69Ff78C9C8aDCA4c351574dc873105E")
	t.Setenv("ENV", "prod")

	path := filepath.Join("..", "..", "etc", "nof0.prod.yaml")
	report, err := RunFile(path, ProductionTradingOptions())
	if err != nil {
		t.Fatalf("RunFile: %v", err)
	}
	if !report.OK() {
		t.Fatalf("expected production template to pass, got issues: %+v", report.Issues)
	}
}

func hasIssue(report Report, id string) bool {
	for _, issue := range report.Issues {
		if issue.ID == id {
			return true
		}
	}
	return false
}

func validPreflightConfig() *appconfig.Config {
	return &appconfig.Config{
		RestConf: rest.RestConf{
			ServiceConf: service.ServiceConf{Name: "nof0"},
			Host:        "0.0.0.0",
			Port:        8888,
		},
		Env:      "prod",
		DataPath: "../mcp/data",
		Postgres: appconfig.PostgresConf{
			DataSource:  "postgres://nof0:secret@db.example.com:5432/nof0?sslmode=require",
			MaxOpen:     10,
			MaxIdle:     5,
			MaxLifetime: 5 * time.Minute,
		},
		Cache: cache.CacheConf{
			{
				RedisConf: redis.RedisConf{
					Host: "redis.example.com:6379",
					Type: "node",
				},
				Weight: 100,
			},
		},
		TTL: appconfig.CacheTTL{
			Short:  10,
			Medium: 60,
			Long:   300,
		},
		CommandWorker: appconfig.CommandWorkerConf{
			Enabled:   true,
			Interval:  5 * time.Second,
			BatchSize: 10,
		},
		LLM: confkit.Section[llmpkg.Config]{
			Value: &llmpkg.Config{
				BaseURL:      "https://llm.example.com/v1",
				APIKey:       "prod-secret-key",
				DefaultModel: "gpt-5",
				Timeout:      60 * time.Second,
				MaxRetries:   3,
				Models: map[string]llmpkg.ModelConfig{
					"gpt-5": {Provider: "openai", ModelName: "openai/gpt-5"},
				},
				Budget: &llmpkg.BudgetConfig{
					DailyTokenLimit:   100000,
					AlertThresholdPct: 80,
					StrictEnforcement: true,
				},
			},
		},
		Executor: confkit.Section[executorpkg.Config]{
			Value: &executorpkg.Config{
				MajorCoinLeverage:   5,
				AltcoinLeverage:     3,
				MinConfidence:       70,
				MinRiskReward:       2,
				MaxPositions:        2,
				PromptSchemaVersion: "v1.0.0",
				PromptValidation: executorpkg.PromptValidation{
					StrictMode:           true,
					RequireVersionHeader: true,
				},
				OutputValidation: executorpkg.OutputValidation{
					Enabled:       true,
					SchemaPath:    "schemas/decision_output.json",
					FailOnInvalid: true,
				},
			},
		},
		Manager: confkit.Section[managerpkg.Config]{
			Value: &managerpkg.Config{
				Manager: managerpkg.ManagerConfig{
					TotalEquityUSD:      1000,
					ReserveEquityPct:    10,
					AllocationStrategy:  "fixed",
					RebalanceInterval:   time.Hour,
					StateStorageBackend: "file",
					StateStoragePath:    "state.json",
				},
				Traders: []managerpkg.TraderConfig{
					{
						ID:               "paper",
						Name:             "Paper Trader",
						ExchangeProvider: "paper_trading",
						MarketProvider:   "hyperliquid",
						ExecutionMode:    managerpkg.ExecutionModePaper,
						OrderStyle:       managerpkg.OrderStyleLimitIOC,
						PromptTemplate:   "manager.tmpl",
						ExecutorTemplate: "executor.tmpl",
						Model:            "gpt-5",
						DecisionInterval: time.Minute,
						AllocationPct:    90,
						AutoStart:        true,
						RiskParams: managerpkg.RiskParameters{
							MaxPositions:       2,
							MaxPositionSizeUSD: 100,
							MaxMarginUsagePct:  20,
							MaxDailyLossUSD:    50,
							MaxDailyLossPct:    5,
							MajorCoinLeverage:  5,
							AltcoinLeverage:    3,
							MinRiskRewardRatio: 2,
							MinConfidence:      70,
							StopLossEnabled:    true,
							TakeProfitEnabled:  true,
							AllowedSymbols:     []string{"BTC", "ETH"},
						},
						ExecGuards: managerpkg.ExecGuards{
							MaxNewPositionsPerCycle: 1,
							LiquidityThresholdUSD:   1000000,
							CandidateLimit:          2,
						},
					},
				},
				Monitoring: managerpkg.MonitoringConfig{
					UpdateInterval:  time.Minute,
					MetricsExporter: "prometheus",
				},
			},
		},
		Exchange: confkit.Section[exchangepkg.Config]{
			Value: &exchangepkg.Config{
				Default: "paper_trading",
				Providers: map[string]*exchangepkg.ProviderConfig{
					"paper_trading": {Type: "sim"},
				},
			},
		},
		Market: confkit.Section[marketpkg.Config]{
			Value: &marketpkg.Config{
				Default: "hyperliquid",
				Providers: map[string]*marketpkg.ProviderConfig{
					"hyperliquid": {
						Type:        "hyperliquid",
						Timeout:     8 * time.Second,
						HTTPTimeout: 10 * time.Second,
						MaxRetries:  3,
					},
				},
			},
		},
	}
}
