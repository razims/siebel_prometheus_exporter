package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/razims/siebel_prometheus_exporter/pkg/exporter"
	"github.com/razims/siebel_prometheus_exporter/pkg/logger"
	"github.com/razims/siebel_prometheus_exporter/pkg/servermanager"
	"github.com/razims/siebel_prometheus_exporter/pkg/web"
	"go.uber.org/zap"
)

var (
	// Command line arguments
	listenAddress               = flag.String("web.listen-address", "0.0.0.0:9963", "Address to listen on for web interface and telemetry.")
	metricsPath                 = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	disableExporterMetrics      = flag.Bool("web.disable-exporter-metrics", false, "Exclude metrics about the exporter itself (promhttp_*, process_*, go_*).")
	disableLogs                 = flag.Bool("web.disable-logs", false, "Disable the /logs endpoint and in-memory log storage.")
	maxProcs                    = flag.Int("runtime.gomaxprocs", 0, "The target number of CPUs Go will run on (GOMAXPROCS). 0 means use default (number of logical CPUs).")
	gateway                     = flag.String("siebel.gateway", "", "Siebel Gateway server address.")
	enterprise                  = flag.String("siebel.enterprise", "", "Siebel Enterprise name.")
	server                      = flag.String("siebel.server", "", "Siebel Application server name.")
	user                        = flag.String("siebel.user", "", "Siebel user name.")
	password                    = flag.String("siebel.password", "", "Siebel user password.")
	srvrmgrPath                 = flag.String("siebel.srvrmgr-path", "srvrmgr", "Full path to srvrmgr executable.")
	metricsFile                 = flag.String("siebel.metrics-file", "metrics.toml", "Metrics configuration file.")
	dateFormat                  = flag.String("siebel.date-format", "2006-01-02 15:04:05", "Go datetime formatting layout to use with empty value.")
	disableEmptyMetricsOverride = flag.Bool("siebel.disable-empty-metrics-override", false, "Disable override of empty metrics in results with value of 0.")
	disableExtendedMetrics      = flag.Bool("siebel.disable-extended-metrics", false, "Disable any metric defined as 'Extended' in metrics file.")
	autoReconnect               = flag.Bool("siebel.auto-reconnect", true, "Enable automatic reconnection if connection is lost.")
	reconnectDelay              = flag.Duration("siebel.reconnect-delay", 10*time.Second, "Delay between reconnection attempts.")
	reconnectAfterScrape        = flag.Bool("siebel.reconnect-after-scrape", false, "Reconnect to server after each scrape")
	logLevel                    = flag.String("log.level", "info", "Log level (debug, info, warn, error)")
)

func main() {
	flag.Parse()

	// Set GOMAXPROCS if specified
	if *maxProcs > 0 {
		runtime.GOMAXPROCS(*maxProcs)
		fmt.Printf("Set GOMAXPROCS to %d\n", *maxProcs)
	} else {
		cpus := runtime.NumCPU()
		fmt.Printf("Using default GOMAXPROCS (%d CPUs)\n", cpus)
	}

	// Initialize logger with specified level
	fmt.Printf("Initializing logger with level: %s\n", *logLevel)

	// Validate the log level before passing it to logger.Init
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
		"panic": true,
		"fatal": true,
	}

	// Convert input to lowercase for comparison
	normalizedLevel := strings.ToLower(*logLevel)

	if _, valid := validLogLevels[normalizedLevel]; !valid {
		fmt.Printf("Warning: Invalid log level '%s', defaulting to 'info'\n", *logLevel)
		normalizedLevel = "info"
	}

	// Set disabled logs flag before initializing logger
	logger.SetDisableLogs(*disableLogs)

	// Initialize the logger with the validated level
	logger.Init(logger.Level(normalizedLevel))
	defer logger.Sync()

	logger.Info("Starting Siebel Exporter",
		zap.String("logLevel", normalizedLevel))

	// Test log level
	logger.Debug("This is a DEBUG message - you should see this if debug level is enabled")

	// Create a ServerManagerConfig from command line arguments
	smConfig := servermanager.ServerManagerConfig{
		Gateway:        *gateway,
		Enterprise:     *enterprise,
		Server:         *server,
		User:           *user,
		Password:       *password,
		SrvrmgrPath:    *srvrmgrPath,
		AutoReconnect:  *autoReconnect,
		ReconnectDelay: *reconnectDelay,
		BackoffConfig:  servermanager.DefaultBackoffConfig,
	}

	// Validate configuration
	if smConfig.Gateway == "" || smConfig.Enterprise == "" || smConfig.Server == "" ||
		smConfig.User == "" || smConfig.Password == "" || smConfig.SrvrmgrPath == "" {
		logger.Error("Missing required parameters. All Siebel connection parameters are required.")
		flag.Usage()
		os.Exit(1)
	}

	// Create ServerManager instance
	sm := servermanager.NewServerManager(smConfig)

	// Try to connect to Siebel Server Manager
	logger.Info("Connecting to Siebel Server Manager...",
		zap.String("gateway", smConfig.Gateway),
		zap.String("enterprise", smConfig.Enterprise),
		zap.String("server", smConfig.Server))

	if err := sm.Connect(); err != nil {
		logger.Error("Failed to connect to Siebel Server Manager", zap.Error(err))
		os.Exit(1)
	}
	logger.Info("Successfully connected to Siebel Server Manager")

	// Create exporter configuration
	exporterConfig := &exporter.ExporterConfig{
		ServerManagerConfig:         &smConfig,
		MetricsFile:                 *metricsFile,
		DateFormat:                  *dateFormat,
		DisableEmptyMetricsOverride: *disableEmptyMetricsOverride,
		DisableExtendedMetrics:      *disableExtendedMetrics,
		ReconnectAfterScrape:        *reconnectAfterScrape,
	}

	// Create exporter
	siebelExporter := exporter.NewExporter(sm, exporterConfig)

	// Create web server config
	webConfig := web.ServerConfig{
		ListenAddress:          *listenAddress,
		MetricsPath:            *metricsPath,
		DisableExporterMetrics: *disableExporterMetrics,
		DisableLogs:            *disableLogs,
	}

	// Create and start web server
	webServer := web.NewServer(webConfig, &smConfig, exporterConfig, normalizedLevel)
	webServer.RegisterExporter(siebelExporter)

	// Setup shutdown hook to disconnect ServerManager on exit
	defer func() {
		logger.Info("Disconnecting from Siebel Server Manager...")
		if err := sm.Disconnect(); err != nil {
			logger.Error("Error during disconnection from Siebel Server Manager", zap.Error(err))
		}
	}()

	// Start web server (this blocks until server shutdown)
	logger.Error("HTTP server error", zap.Error(webServer.Start()))
}
