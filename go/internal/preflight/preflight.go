package preflight

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	appconfig "nof0-api/internal/config"
	"nof0-api/internal/secrets"
	exchangepkg "nof0-api/pkg/exchange"
	_ "nof0-api/pkg/exchange/hyperliquid"
	_ "nof0-api/pkg/exchange/sim"
	executorpkg "nof0-api/pkg/executor"
	llmpkg "nof0-api/pkg/llm"
	managerpkg "nof0-api/pkg/manager"
	marketpkg "nof0-api/pkg/market"
	_ "nof0-api/pkg/market/exchanges/hyperliquid"
)

const (
	allowLiveTradingEnv = "ASTRAQUANT_ALLOW_LIVE_TRADING"
	liveTradingAckEnv   = "ASTRAQUANT_LIVE_TRADING_ACK"
	liveTradingAckValue = "I_UNDERSTAND_THIS_CAN_LOSE_MONEY"
)

type Severity string

const (
	SeverityFatal   Severity = "fatal"
	SeverityWarning Severity = "warning"
)

type Issue struct {
	Severity Severity `json:"severity"`
	ID       string   `json:"id"`
	Message  string   `json:"message"`
}

type Report struct {
	ConfigPath string  `json:"config_path"`
	Issues     []Issue `json:"issues"`
}

type Options struct {
	RequireProdEnv       bool
	RequirePersistence   bool
	RequireCommandWorker bool
	BuildProviders       bool
	CheckRawConfig       bool
}

func ProductionTradingOptions() Options {
	return Options{
		RequireProdEnv:       true,
		RequirePersistence:   true,
		RequireCommandWorker: true,
		BuildProviders:       true,
		CheckRawConfig:       true,
	}
}

func ProductionAPIOptions() Options {
	return Options{
		RequireProdEnv:       true,
		RequirePersistence:   true,
		RequireCommandWorker: false,
		BuildProviders:       true,
		CheckRawConfig:       true,
	}
}

func RunFile(path string, opts Options) (Report, error) {
	cfg, err := appconfig.Load(path)
	if err != nil {
		return Report{ConfigPath: path}, err
	}
	report := Run(cfg, opts)
	if opts.CheckRawConfig {
		report.Issues = append(report.Issues, inspectRawConfig(cfg.MainPath(), cfg)...)
	}
	report.sortIssues()
	return report, nil
}

func Run(cfg *appconfig.Config, opts Options) Report {
	report := Report{}
	if cfg != nil {
		report.ConfigPath = cfg.MainPath()
	}
	if cfg == nil {
		report.add(SeverityFatal, "config.nil", "application config is nil")
		return report
	}

	env := normalizedEnv(cfg.Env)
	if opts.RequireProdEnv && env != "prod" {
		report.add(SeverityFatal, "env.not_prod", fmt.Sprintf("Env must be prod for production preflight, got %q", cfg.Env))
	}
	if strings.TrimSpace(cfg.Name) == "" {
		report.add(SeverityFatal, "app.name_missing", "service Name is required")
	}
	if cfg.Port <= 0 {
		report.add(SeverityFatal, "app.port_invalid", "service Port must be positive")
	}
	if strings.TrimSpace(cfg.Host) == "" {
		report.add(SeverityFatal, "app.host_missing", "service Host is required")
	}
	if cfg.Logging.VerboseLLM {
		report.add(SeverityFatal, "logging.verbose_llm", "Logging.VerboseLLM must be false in production because prompts may contain sensitive trading context")
	}
	if cfg.Logging.VerboseSQL {
		report.add(SeverityWarning, "logging.verbose_sql", "Logging.VerboseSQL should be false in production unless temporarily debugging")
	}

	checkPersistence(&report, cfg, opts)
	checkCommandWorker(&report, cfg, opts)
	checkLLM(&report, cfg.LLM.Value)
	checkExecutor(&report, cfg.Executor.Value)
	checkManager(&report, cfg.Manager.Value, cfg.Exchange.Value, cfg.Market.Value, cfg.LLM.Value)
	if opts.BuildProviders {
		checkProviderConstruction(&report, cfg.Exchange.Value, cfg.Market.Value)
	}

	report.sortIssues()
	return report
}

func (r *Report) add(severity Severity, id string, message string) {
	r.Issues = append(r.Issues, Issue{
		Severity: severity,
		ID:       id,
		Message:  message,
	})
}

func (r *Report) sortIssues() {
	sort.SliceStable(r.Issues, func(i, j int) bool {
		leftRank := severityRank(r.Issues[i].Severity)
		rightRank := severityRank(r.Issues[j].Severity)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return r.Issues[i].ID < r.Issues[j].ID
	})
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityFatal:
		return 0
	case SeverityWarning:
		return 1
	default:
		return 2
	}
}

func (r Report) FatalCount() int {
	count := 0
	for _, issue := range r.Issues {
		if issue.Severity == SeverityFatal {
			count++
		}
	}
	return count
}

func (r Report) WarningCount() int {
	count := 0
	for _, issue := range r.Issues {
		if issue.Severity == SeverityWarning {
			count++
		}
	}
	return count
}

func (r Report) OK() bool {
	return r.FatalCount() == 0
}

func normalizedEnv(env string) string {
	env = strings.ToLower(strings.TrimSpace(env))
	if env == "" {
		return "test"
	}
	return env
}

func checkPersistence(report *Report, cfg *appconfig.Config, opts Options) {
	dsn := strings.TrimSpace(cfg.Postgres.DataSource)
	cacheNodes := configuredCacheNodeCount(cfg)
	if opts.RequirePersistence {
		if dsn == "" {
			report.add(SeverityFatal, "persistence.postgres_missing", "Postgres.DataSource is required for production trading persistence")
		}
		if cacheNodes == 0 {
			report.add(SeverityFatal, "persistence.cache_missing", "at least one Cache node with Host is required for production trading persistence")
		}
	}
	if dsn != "" {
		warnInsecurePostgresDSN(report, dsn)
	}
}

func configuredCacheNodeCount(cfg *appconfig.Config) int {
	if cfg == nil {
		return 0
	}
	count := 0
	for _, node := range cfg.Cache {
		if strings.TrimSpace(node.Host) != "" {
			count++
		}
	}
	return count
}

func warnInsecurePostgresDSN(report *Report, dsn string) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		report.add(SeverityWarning, "persistence.postgres_dsn_parse", "Postgres.DataSource could not be parsed for TLS checks")
		return
	}
	if strings.EqualFold(parsed.Query().Get("sslmode"), "disable") {
		report.add(SeverityWarning, "persistence.postgres_ssl_disabled", "Postgres.DataSource uses sslmode=disable; production should use TLS unless the database is isolated on a private network")
	}
}

func checkCommandWorker(report *Report, cfg *appconfig.Config, opts Options) {
	if !opts.RequireCommandWorker {
		return
	}
	if !cfg.CommandWorker.Enabled {
		report.add(SeverityFatal, "command_worker.disabled", "CommandWorker.Enabled must be true for the API process to execute queued decision approvals")
	}
}

func checkLLM(report *Report, cfg *llmpkg.Config) {
	if cfg == nil {
		report.add(SeverityFatal, "llm.missing", "LLM config is required for production trading")
		return
	}
	if looksPlaceholder(cfg.APIKey) {
		report.add(SeverityFatal, "llm.api_key_missing", "LLM api_key is missing or still looks like a placeholder")
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		report.add(SeverityFatal, "llm.default_model_missing", "LLM default_model is required")
	} else if _, ok := cfg.Model(cfg.DefaultModel); !ok && !strings.Contains(cfg.DefaultModel, "/") {
		report.add(SeverityFatal, "llm.default_model_unknown", fmt.Sprintf("LLM default_model %q is not defined in models and is not fully qualified", cfg.DefaultModel))
	}
	if cfg.Budget == nil {
		report.add(SeverityFatal, "llm.budget_missing", "LLM budget is required for production cost control")
	} else if !cfg.Budget.StrictEnforcement {
		report.add(SeverityFatal, "llm.budget_not_strict", "LLM budget.strict_enforcement must be true in production")
	}
}

func checkExecutor(report *Report, cfg *executorpkg.Config) {
	if cfg == nil {
		report.add(SeverityFatal, "executor.missing", "Executor config is required for production trading")
		return
	}
	if !cfg.PromptValidation.StrictMode {
		report.add(SeverityFatal, "executor.prompt_strict_disabled", "executor prompt_validation.strict_mode must be true")
	}
	if !cfg.PromptValidation.RequireVersionHeader {
		report.add(SeverityFatal, "executor.prompt_version_header_disabled", "executor prompt_validation.require_version_header must be true")
	}
	if strings.TrimSpace(cfg.PromptSchemaVersion) == "" {
		report.add(SeverityFatal, "executor.prompt_schema_version_missing", "executor prompt_schema_version is required")
	}
	if !cfg.OutputValidation.Enabled {
		report.add(SeverityFatal, "executor.output_validation_disabled", "executor output_validation.enabled must be true")
	}
	if cfg.OutputValidation.Enabled && !cfg.OutputValidation.FailOnInvalid {
		report.add(SeverityFatal, "executor.output_validation_not_strict", "executor output_validation.fail_on_invalid must be true")
	}
}

func checkManager(report *Report, managerCfg *managerpkg.Config, exchangeCfg *exchangepkg.Config, marketCfg *marketpkg.Config, llmCfg *llmpkg.Config) {
	if managerCfg == nil {
		report.add(SeverityFatal, "manager.missing", "Manager config is required for production trading")
		return
	}
	if exchangeCfg == nil {
		report.add(SeverityFatal, "exchange.missing", "Exchange config is required for production trading")
	}
	if marketCfg == nil {
		report.add(SeverityFatal, "market.missing", "Market config is required for production trading")
	}
	if managerCfg.Manager.TotalEquityUSD <= 0 {
		report.add(SeverityFatal, "manager.total_equity_missing", "manager.total_equity_usd must be positive for production trading")
	}
	if len(managerCfg.Traders) == 0 {
		report.add(SeverityFatal, "manager.traders_missing", "at least one trader must be configured for production trading")
	}

	autoStartCount := 0
	totalAllocation := 0.0
	for i, trader := range managerCfg.Traders {
		prefix := fmt.Sprintf("manager.traders[%d]", i)
		if trader.ID != "" {
			prefix = fmt.Sprintf("manager.traders[%s]", trader.ID)
		}
		totalAllocation += trader.AllocationPct
		if trader.AutoStart {
			autoStartCount++
		}
		checkTrader(report, prefix, trader, exchangeCfg, marketCfg, llmCfg)
	}
	if len(managerCfg.Traders) > 0 && autoStartCount == 0 {
		report.add(SeverityWarning, "manager.no_auto_start", "no trader has auto_start=true; the trading loop will stay idle until runtime state starts a trader")
	}
	if totalAllocation <= 0 {
		report.add(SeverityFatal, "manager.allocation_zero", "total trader allocation_pct must be positive")
	}
}

func checkTrader(report *Report, prefix string, trader managerpkg.TraderConfig, exchangeCfg *exchangepkg.Config, marketCfg *marketpkg.Config, llmCfg *llmpkg.Config) {
	if strings.TrimSpace(trader.Model) == "" {
		report.add(SeverityFatal, prefix+".model_missing", "trader model is required")
	} else if llmCfg != nil {
		if _, ok := llmCfg.Model(trader.Model); !ok && !strings.Contains(trader.Model, "/") {
			report.add(SeverityFatal, prefix+".model_unknown", fmt.Sprintf("trader model %q is not defined in LLM models and is not fully qualified", trader.Model))
		}
	}

	var exchangeProvider *exchangepkg.ProviderConfig
	if exchangeCfg != nil {
		exchangeProvider = exchangeCfg.Providers[trader.ExchangeProvider]
		if exchangeProvider == nil {
			report.add(SeverityFatal, prefix+".exchange_provider_unknown", fmt.Sprintf("exchange_provider %q is not defined", trader.ExchangeProvider))
		}
	}
	var marketProvider *marketpkg.ProviderConfig
	if marketCfg != nil {
		marketProvider = marketCfg.Providers[trader.MarketProvider]
		if marketProvider == nil {
			report.add(SeverityFatal, prefix+".market_provider_unknown", fmt.Sprintf("market_provider %q is not defined", trader.MarketProvider))
		}
	}

	checkExecutionMode(report, prefix, trader, exchangeProvider, marketProvider)
	checkRisk(report, prefix, trader.RiskParams)
	checkExecGuards(report, prefix, trader.ExecGuards)
	if trader.AllocationPct <= 0 {
		report.add(SeverityFatal, prefix+".allocation_zero", "trader allocation_pct must be positive")
	}
}

func checkExecutionMode(report *Report, prefix string, trader managerpkg.TraderConfig, exchangeProvider *exchangepkg.ProviderConfig, marketProvider *marketpkg.ProviderConfig) {
	mode := managerpkg.ExecutionMode(strings.ToLower(strings.TrimSpace(string(trader.ExecutionMode))))
	switch mode {
	case managerpkg.ExecutionModePaper:
		if exchangeProvider != nil && !isPaperExchangeTarget(trader.ExchangeProvider, exchangeProvider) {
			report.add(SeverityFatal, prefix+".paper_exchange_mismatch", fmt.Sprintf("execution_mode=paper requires a paper/sim exchange provider, got %q", trader.ExchangeProvider))
		}
	case managerpkg.ExecutionModeTestnet:
		if exchangeProvider != nil && !isTestnetExchangeTarget(trader.ExchangeProvider, exchangeProvider) {
			report.add(SeverityFatal, prefix+".testnet_exchange_mismatch", fmt.Sprintf("execution_mode=testnet requires a testnet exchange provider, got %q", trader.ExchangeProvider))
		}
		if marketProvider != nil && !isTestnetMarketTarget(trader.MarketProvider, marketProvider) {
			report.add(SeverityWarning, prefix+".testnet_market_mismatch", fmt.Sprintf("execution_mode=testnet uses market_provider %q which is not marked testnet", trader.MarketProvider))
		}
	case managerpkg.ExecutionModeLive:
		if exchangeProvider != nil && isTestnetExchangeTarget(trader.ExchangeProvider, exchangeProvider) {
			report.add(SeverityFatal, prefix+".live_exchange_is_testnet", fmt.Sprintf("execution_mode=live cannot use testnet exchange provider %q", trader.ExchangeProvider))
		}
		if exchangeProvider != nil && isPaperExchangeTarget(trader.ExchangeProvider, exchangeProvider) {
			report.add(SeverityFatal, prefix+".live_exchange_is_paper", fmt.Sprintf("execution_mode=live cannot use paper/sim exchange provider %q", trader.ExchangeProvider))
		}
		if os.Getenv("CI") != "" {
			report.add(SeverityFatal, prefix+".live_disabled_in_ci", "live trading is disabled in CI")
		}
		if !truthy(os.Getenv(allowLiveTradingEnv)) {
			report.add(SeverityFatal, prefix+".live_ack_missing", fmt.Sprintf("live trading requires %s=true", allowLiveTradingEnv))
		}
		if os.Getenv(liveTradingAckEnv) != liveTradingAckValue {
			report.add(SeverityFatal, prefix+".live_ack_value_missing", fmt.Sprintf("live trading requires %s=%s", liveTradingAckEnv, liveTradingAckValue))
		}
	default:
		report.add(SeverityFatal, prefix+".execution_mode_invalid", fmt.Sprintf("unsupported execution_mode %q", trader.ExecutionMode))
	}
}

func checkRisk(report *Report, prefix string, risk managerpkg.RiskParameters) {
	if len(risk.AllowedSymbols) == 0 {
		report.add(SeverityFatal, prefix+".risk.allowed_symbols_empty", "risk_params.allowed_symbols must be non-empty to provide a hard symbol whitelist")
	}
	if risk.MaxDailyLossUSD <= 0 && risk.MaxDailyLossPct <= 0 {
		report.add(SeverityFatal, prefix+".risk.daily_loss_missing", "risk_params must set max_daily_loss_usd or max_daily_loss_pct")
	}
	if risk.MaxMarginUsagePct <= 0 || risk.MaxMarginUsagePct > 100 {
		report.add(SeverityFatal, prefix+".risk.margin_cap_missing", "risk_params.max_margin_usage_pct must be greater than 0 and at most 100")
	}
	if risk.MinConfidence <= 0 {
		report.add(SeverityFatal, prefix+".risk.min_confidence_missing", "risk_params.min_confidence must be positive")
	}
	if !risk.StopLossEnabled {
		report.add(SeverityFatal, prefix+".risk.stop_loss_disabled", "risk_params.stop_loss_enabled must be true")
	}
	if !risk.TakeProfitEnabled {
		report.add(SeverityFatal, prefix+".risk.take_profit_disabled", "risk_params.take_profit_enabled must be true")
	}
}

func checkExecGuards(report *Report, prefix string, guards managerpkg.ExecGuards) {
	if guards.MaxNewPositionsPerCycle <= 0 {
		report.add(SeverityWarning, prefix+".exec_guards.new_position_cycle_cap_missing", "exec_guards.max_new_positions_per_cycle is not set; per-cycle open count is only bounded by max_positions")
	}
	if guards.LiquidityThresholdUSD <= 0 {
		report.add(SeverityWarning, prefix+".exec_guards.liquidity_threshold_missing", "exec_guards.liquidity_threshold_usd is not set; illiquid symbols are not filtered by open-interest value")
	}
	if guards.CandidateLimit <= 0 {
		report.add(SeverityWarning, prefix+".exec_guards.candidate_limit_missing", "exec_guards.candidate_limit is not set; manager defaults to 10 candidates")
	}
}

func checkProviderConstruction(report *Report, exchangeCfg *exchangepkg.Config, marketCfg *marketpkg.Config) {
	if exchangeCfg != nil {
		exchangeCfg.SetSecretStore(secrets.NewEnvStore())
		if _, err := exchangeCfg.BuildProvidersWithSecrets(context.Background(), secrets.NewEnvStore()); err != nil {
			report.add(SeverityFatal, "exchange.build_failed", fmt.Sprintf("exchange providers could not be constructed: %v", secrets.RedactError(err)))
		}
	}
	if marketCfg != nil {
		if _, err := marketCfg.BuildProviders(); err != nil {
			report.add(SeverityFatal, "market.build_failed", fmt.Sprintf("market providers could not be constructed: %v", err))
		}
	}
}

func inspectRawConfig(path string, cfg *appconfig.Config) []Issue {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []Issue{{
			Severity: SeverityWarning,
			ID:       "raw_config.read_failed",
			Message:  fmt.Sprintf("could not read raw config for secondary checks: %v", err),
		}}
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return []Issue{{
			Severity: SeverityWarning,
			ID:       "raw_config.parse_failed",
			Message:  fmt.Sprintf("could not parse raw config for secondary checks: %v", err),
		}}
	}
	cors, ok := raw["Cors"].(map[string]any)
	if !ok {
		return nil
	}
	origins := rawStringList(cors["AllowOrigins"])
	for _, origin := range origins {
		if strings.TrimSpace(origin) == "*" && cfg != nil && normalizedEnv(cfg.Env) == "prod" {
			return []Issue{{
				Severity: SeverityFatal,
				ID:       "cors.wildcard_origin",
				Message:  "Cors.AllowOrigins contains wildcard '*'; remove it or restrict exact origins before enabling CORS in production",
			}}
		}
	}
	return nil
}

func rawStringList(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out
	case []string:
		return typed
	case string:
		return []string{typed}
	default:
		return nil
	}
}

func looksPlaceholder(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return true
	}
	needles := []string{
		"your-",
		"change-me",
		"changeme",
		"placeholder",
		"example",
		"todo",
	}
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func isPaperExchangeTarget(name string, provider *exchangepkg.ProviderConfig) bool {
	joined := strings.ToLower(strings.TrimSpace(name) + " " + strings.TrimSpace(provider.Type))
	return strings.Contains(joined, "paper") || strings.Contains(joined, "sim")
}

func isTestnetExchangeTarget(name string, provider *exchangepkg.ProviderConfig) bool {
	if provider.Testnet {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(name)), "testnet")
}

func isTestnetMarketTarget(name string, provider *marketpkg.ProviderConfig) bool {
	if provider.Testnet {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(name)), "testnet")
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
