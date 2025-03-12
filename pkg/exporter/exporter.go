package exporter

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/razims/siebel_prometheus_exporter/pkg/logger"
	"github.com/razims/siebel_prometheus_exporter/pkg/servermanager"
	"go.uber.org/zap"
)

// NewExporter returns a new Siebel exporter for the provided args.
func NewExporter(srvrmgr *servermanager.ServerManager, config *ExporterConfig) *Exporter {
	logger.Debug("Creating new exporter",
		zap.String("metricsFile", config.MetricsFile))

	const (
		namespace = "siebel"
		subsystem = "exporter"
	)

	// Load metrics from file
	loadMetrics(config.MetricsFile)

	return &Exporter{
		namespace: namespace,
		subsystem: subsystem,
		config:    config,
		srvrmgr:   srvrmgr,
		duration: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "last_scrape_duration_seconds",
			Help:      "Duration of the last scrape of metrics from Siebel.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "scrapes_total",
			Help:      "Total number of times Siebel was scraped for metrics.",
		}),
		scrapeErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "scrape_errors_total",
			Help:      "Total number of times an error occurred scraping a Siebel.",
		}),
		error: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "last_scrape_error",
			Help:      "Whether the last scrape of metrics from Siebel resulted in an error (1 for error, 0 for success).",
		}),
		gatewayServerUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "gateway_server_up",
			Help:      "Whether the Siebel Gateway Server is up (1 for up, 0 for down).",
		}),
		applicationServerUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "application_server_up",
			Help:      "Whether the Siebel Application Server is up (1 for up, 0 for down).",
		}),
		reconnectsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "reconnects_total",
			Help:      "Total number of reconnections performed.",
		}),
		reconnectErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "reconnect_errors_total",
			Help:      "Total number of reconnection errors.",
		}),
		lastReconnectDuration: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "last_reconnect_duration_seconds",
			Help:      "Duration of the last reconnection attempt in seconds.",
		}),
	}
}

// Describe describes all the metrics exported by the Siebel exporter.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	logger.Debug("Describing exporter metrics")

	metricCh := make(chan prometheus.Metric)
	doneCh := make(chan struct{})

	go func() {
		for m := range metricCh {
			ch <- m.Desc()
		}
		close(doneCh)
	}()

	e.Collect(metricCh)
	close(metricCh)
	<-doneCh
}

// Collect implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	logger.Debug("Collecting metrics")
	e.scrape(ch)
	ch <- e.duration
	ch <- e.totalScrapes
	ch <- e.error
	e.scrapeErrors.Collect(ch)
	ch <- e.gatewayServerUp
	ch <- e.applicationServerUp

	// Emit reconnection metrics
	e.reconnectsTotal.Collect(ch)
	e.reconnectErrors.Collect(ch)
	ch <- e.lastReconnectDuration
}

func (e *Exporter) scrape(ch chan<- prometheus.Metric) {
	logger.Debug("Starting metric scrape")

	e.totalScrapes.Inc()
	e.gatewayServerUp.Set(0)
	e.applicationServerUp.Set(0)

	var err error
	defer func(begun time.Time) {
		e.duration.Set(time.Since(begun).Seconds())
		if err == nil {
			e.error.Set(0)
		} else {
			e.error.Set(1)
		}
	}(time.Now())

	if !checkConnection(e.srvrmgr, e.config.ServerManagerConfig) {
		return
	}

	if err = pingGatewayServer(e.srvrmgr); err != nil {
		return
	}
	e.gatewayServerUp.Set(1)

	if err = pingApplicationServer(e.srvrmgr); err != nil {
		return
	}
	e.applicationServerUp.Set(1)

	reloadMetricsIfItChanged(e.config.MetricsFile)

	for _, metric := range defaultMetrics.Metric {
		logMetricDesc(metric)

		if !validateMetricDesc(metric) {
			continue
		}

		if metric.Extended && e.config.DisableExtendedMetrics {
			logger.Debug("Skipping extended metric")
			continue
		}

		scrapeStart := time.Now()

		if err = scrapeGenericValues(e.namespace, e.config.DateFormat, e.config.DisableEmptyMetricsOverride, e.srvrmgr, &ch, metric); err != nil {
			logger.Error("Error scraping metric",
				zap.String("subsystem", metric.Subsystem),
				zap.Any("help", metric.Help),
				zap.Error(err))
			e.scrapeErrors.Inc()
		} else {
			scrapeEnd := time.Since(scrapeStart)
			logger.Debug("Successfully scraped metric",
				zap.String("subsystem", metric.Subsystem),
				zap.Any("help", metric.Help),
				zap.Duration("duration", scrapeEnd))
		}
	}

	// If reconnectAfterScrape is enabled, reconnect to the server
	if e.config.ReconnectAfterScrape {
		logger.Info("Reconnecting after scrape as configured")
		reconnectStart := time.Now()
		e.reconnectsTotal.Inc()

		// First disconnect
		disconnectErr := e.srvrmgr.Disconnect()
		if disconnectErr != nil {
			logger.Warn("Error during disconnect for after-scrape reconnection",
				zap.Error(disconnectErr))
			// Continue with reconnect anyway
		}

		// Short pause to ensure clean disconnection
		time.Sleep(1 * time.Second)

		// Now reconnect
		if reconnectErr := e.srvrmgr.Connect(); reconnectErr != nil {
			logger.Error("Failed to reconnect after scrape", zap.Error(reconnectErr))
			e.reconnectErrors.Inc()
			e.error.Set(1)
		} else {
			logger.Info("Successfully reconnected after scrape")
		}

		// Record reconnection duration
		e.lastReconnectDuration.Set(time.Since(reconnectStart).Seconds())
	}
}

// Check srvrmgr connection status
func checkConnection(smgr *servermanager.ServerManager, config *servermanager.ServerManagerConfig) bool {
	status := smgr.GetStatus()

	switch status {
	case servermanager.Connected:
		logger.Debug("srvrmgr connected to Siebel Gateway Server")
		return true

	case servermanager.Disconnected, servermanager.ConnectionError:
		// Try to reconnect if status is Disconnected or ConnectionError
		shouldReconnectMsg := "Attempting to reconnect"
		if !config.AutoReconnect {
			shouldReconnectMsg = "Auto-reconnect disabled, not attempting reconnection"
		}

		logger.Warn("Connection issue detected",
			zap.String("status", string(status)),
			zap.Bool("autoReconnect", config.AutoReconnect),
			zap.String("action", shouldReconnectMsg))

		if config.AutoReconnect {
			logger.Info("Attempting to reconnect to Siebel Gateway Server")

			// Force disconnect in case of ConnectionError to clean up resources
			if status == servermanager.ConnectionError {
				logger.Debug("Cleaning up existing connection before reconnect")
				err := smgr.Disconnect()
				if err != nil {
					logger.Warn("Error during disconnect before reconnect", zap.Error(err))
				}

				// Add a small delay to ensure cleanup is complete
				time.Sleep(500 * time.Millisecond)
			}

			// Attempt to connect
			if err := smgr.Connect(); err != nil {
				logger.Error("Failed to reconnect to Siebel Gateway Server", zap.Error(err))
				return false
			}

			logger.Info("Successfully reconnected to Siebel Gateway Server")
			return true
		}

		return false

	case servermanager.Disconnecting:
		logger.Warn("Unable to scrape: srvrmgr is in process of disconnection from Siebel Gateway Server.")
		return false

	case servermanager.Connecting:
		logger.Warn("Unable to scrape: srvrmgr is in process of connection to Siebel Gateway Server.")
		return false

	case servermanager.Reconnecting:
		logger.Info("ServerManager is currently reconnecting, waiting for completion")

		// Wait briefly for reconnection to complete
		for i := 0; i < 5; i++ {
			time.Sleep(500 * time.Millisecond)

			// Check if connection completed
			currentStatus := smgr.GetStatus()
			if currentStatus == servermanager.Connected {
				logger.Info("Reconnection completed successfully")
				return true
			} else if currentStatus != servermanager.Reconnecting {
				logger.Warn("Reconnection status changed", zap.String("newStatus", string(currentStatus)))
				break
			}
		}

		logger.Warn("Timed out waiting for reconnection to complete")
		return false

	default:
		logger.Error("Unable to scrape: unknown status of srvrmgr connection", zap.String("status", string(status)))

		// If auto-reconnect is enabled, attempt reconnection even for unknown status
		if config.AutoReconnect {
			logger.Info("Attempting to reconnect despite unknown status")

			// Clean up first
			smgr.Disconnect()

			// Attempt to connect
			if err := smgr.Connect(); err != nil {
				logger.Error("Failed to reconnect from unknown state", zap.Error(err))
				return false
			}

			logger.Info("Successfully reconnected from unknown state")
			return true
		}

		return false
	}
}

func pingGatewayServer(smgr *servermanager.ServerManager) error {
	logger.Debug("Pinging Siebel Gateway Server...")
	if _, err := smgr.SendCommand("list ent param MaxThreads show PA_VALUE"); err != nil {
		logger.Error("Error pinging Siebel Gateway Server", zap.Error(err))
		logger.Warn("Unable to scrape: srvrmgr was lost connection to the Siebel Gateway Server. Will try to reconnect on next scrape")
		smgr.Disconnect()
		return err
	}
	logger.Debug("Successfully pinged Siebel Gateway Server")
	return nil
}

func pingApplicationServer(smgr *servermanager.ServerManager) error {
	logger.Debug("Pinging Siebel Application Server...")
	if _, err := smgr.SendCommand("list state values show STATEVAL_NAME"); err != nil {
		logger.Error("Error pinging Siebel Application Server", zap.Error(err))
		logger.Warn("Unable to scrape: srvrmgr was lost connection to the Siebel Application Server. Will try to reconnect on next scrape")
		smgr.Disconnect()
		return err
	}
	logger.Debug("Successfully pinged Siebel Application Server")
	return nil
}

func logMetricDesc(metric Metric) {
	if logger.Log.Core().Enabled(zap.DebugLevel) {
		logger.Debug("About to scrape metric",
			zap.String("command", metric.Command),
			zap.String("subsystem", metric.Subsystem),
			zap.Any("help", metric.Help),
			zap.Any("helpField", metric.HelpField),
			zap.Any("type", metric.Type),
			zap.Any("buckets", metric.Buckets),
			zap.Any("valueMap", metric.ValueMap),
			zap.Any("labels", metric.Labels),
			zap.String("fieldToAppend", metric.FieldToAppend),
			zap.Bool("ignoreZeroResult", metric.IgnoreZeroResult),
			zap.Bool("extended", metric.Extended))
	}
}

func validateMetricDesc(metric Metric) bool {
	if len(metric.Command) == 0 {
		logger.Error("Missing 'command' in metric definition",
			zap.Any("help", metric.Help))
		return false
	}

	if len(metric.Help) == 0 {
		logger.Error("Missing 'help' in metric definition",
			zap.String("command", metric.Command))
		return false
	}

	for columnName, metricType := range metric.Type {
		if strings.ToLower(metricType) == "histogram" {
			if len(metric.Buckets) == 0 {
				logger.Error("Missing 'buckets' for histogram metric",
					zap.String("command", metric.Command))
				return false
			}
			_, exists := metric.Buckets[columnName]
			if !exists {
				logger.Error("Missing bucket configuration for column",
					zap.String("command", metric.Command),
					zap.String("column", columnName))
				return false
			}
		}
	}

	return true
}
