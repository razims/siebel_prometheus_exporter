package main

import (
	"flag"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/razims/siebel_exporter/pkg/exporter"
	"github.com/razims/siebel_exporter/pkg/logger"
	"github.com/razims/siebel_exporter/pkg/servermanager"
	"go.uber.org/zap"
)

var (
	// Command line arguments
	listenAddress               = flag.String("web.listen-address", "0.0.0.0:9963", "Address to listen on for web interface and telemetry.")
	metricsPath                 = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	disableExporterMetrics      = flag.Bool("web.disable-exporter-metrics", false, "Exclude metrics about the exporter itself (promhttp_*, process_*, go_*).")
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
	logLevel                    = flag.String("log.level", "info", "Log level (debug, info, warn, error)")
)

func main() {
	flag.Parse()

	// Set GOMAXPROCS if specified
	if *maxProcs > 0 {
		runtime.GOMAXPROCS(*maxProcs)
		logger.Info("Set GOMAXPROCS", zap.Int("value", *maxProcs))
	} else {
		cpus := runtime.NumCPU()
		logger.Info("Using default GOMAXPROCS", zap.Int("cpus", cpus))
	}

	// Initialize logger with specified level
	logger.Init(logger.Level(*logLevel))
	defer logger.Sync()

	logger.Info("Starting Siebel Exporter")

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

	// Create exporter - use the same file for both default and custom metrics
	siebelExporter := exporter.NewExporter(sm, *metricsFile, "", *dateFormat, *disableEmptyMetricsOverride, *disableExtendedMetrics, &smConfig)

	// Create a new registry
	registry := prometheus.NewRegistry()

	// Register Siebel exporter
	registry.MustRegister(siebelExporter)

	// If not disabled, register Go collector and process collector
	if !*disableExporterMetrics {
		registry.MustRegister(prometheus.NewGoCollector())
		registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
		registry.MustRegister(prometheus.NewBuildInfoCollector())
		logger.Info("Registered standard exporters")
	} else {
		logger.Info("Standard exporters disabled")
	}

	// Setup HTTP server
	http.Handle(*metricsPath, promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
<head><title>Siebel Exporter</title></head>
<body>
<h1>Siebel Exporter</h1>
<p><a href="` + *metricsPath + `">Metrics</a></p>
<p>
  <h3>Metrics collected:</h3>
  <ul>
    <li>Siebel Server Status</li>
    <li>Component Groups Status</li>
    <li>Components Status &amp; Tasks</li>
    <li>Server Statistics</li>
    <li>Server State Values</li>
  </ul>
</p>
</body>
</html>`))
	})

	// Setup shutdown hook to disconnect ServerManager on exit
	defer func() {
		logger.Info("Disconnecting from Siebel Server Manager...")
		if err := sm.Disconnect(); err != nil {
			logger.Error("Error during disconnection from Siebel Server Manager", zap.Error(err))
		}
	}()

	logger.Info("Starting HTTP server",
		zap.String("address", *listenAddress),
		zap.String("metricsPath", *metricsPath),
		zap.Bool("exporterMetricsDisabled", *disableExporterMetrics))
	logger.Error("HTTP server error", zap.Error(http.ListenAndServe(*listenAddress, nil)))
}
