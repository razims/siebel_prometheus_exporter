package exporter

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/razims/siebel_prometheus_exporter/pkg/servermanager"
)

// ExporterConfig contains all configuration parameters for the Exporter
type ExporterConfig struct {
	// Siebel server connection config (directly from server manager)
	ServerManagerConfig *servermanager.ServerManagerConfig

	// Metrics configuration
	MetricsFile string
	DateFormat  string

	// Behavior configuration
	DisableEmptyMetricsOverride bool
	DisableExtendedMetrics      bool
	ReconnectAfterScrape        bool
}

// NewDefaultExporterConfig creates a new ExporterConfig with default values
func NewDefaultExporterConfig() *ExporterConfig {
	return &ExporterConfig{
		ServerManagerConfig:         &servermanager.ServerManagerConfig{},
		MetricsFile:                 "metrics.toml",
		DateFormat:                  "2006-01-02 15:04:05",
		DisableEmptyMetricsOverride: false,
		DisableExtendedMetrics:      false,
		ReconnectAfterScrape:        false,
	}
}

// Metric object description
type Metric struct {
	Command          string
	Subsystem        string
	Help             map[string]string
	HelpField        map[string]string
	Type             map[string]string
	Buckets          map[string]map[string]string
	ValueMap         map[string]map[string]string
	Labels           []string
	FieldToAppend    string
	IgnoreZeroResult bool
	Extended         bool
}

// Metrics used to load multiple metrics from file
type Metrics struct {
	Metric []Metric
}

// Exporter collects Siebel metrics. It implements prometheus.Collector.
type Exporter struct {
	namespace             string
	subsystem             string
	config                *ExporterConfig
	srvrmgr               *servermanager.ServerManager
	duration, error       prometheus.Gauge
	totalScrapes          prometheus.Counter
	scrapeErrors          prometheus.Counter
	gatewayServerUp       prometheus.Gauge
	applicationServerUp   prometheus.Gauge
	reconnectsTotal       prometheus.Counter
	reconnectErrors       prometheus.Counter
	lastReconnectDuration prometheus.Gauge
}

var (
	defaultMetrics Metrics                // Default metrics to scrap
	metricsHashMap = make(map[int][]byte) // Metrics Files HashMap
)
