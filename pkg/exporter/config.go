package exporter

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/razims/siebel_exporter/pkg/servermanager"
)

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
	namespace                   string
	subsystem                   string
	dateFormat                  string
	disableEmptyMetricsOverride bool
	disableExtendedMetrics      bool
	defaultMetricsFile          string
	customMetricsFile           string
	srvrmgr                     *servermanager.ServerManager
	srvrmgrConfig               *servermanager.ServerManagerConfig
	duration, error             prometheus.Gauge
	totalScrapes                prometheus.Counter
	scrapeErrors                prometheus.Counter
	gatewayServerUp             prometheus.Gauge
	applicationServerUp         prometheus.Gauge
}

var (
	defaultMetrics Metrics                // Default metrics to scrap. Use external file (default-metrics.toml)
	customMetrics  Metrics                // Custom metrics to scrap. Use custom external file (if provided)
	metricsHashMap = make(map[int][]byte) // Metrics Files HashMap
)
