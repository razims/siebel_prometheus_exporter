package exporter

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/razims/siebel_prometheus_exporter/pkg/servermanager"
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
	reconnectAfterScrape        bool
	metricsFile                 string
	srvrmgr                     *servermanager.ServerManager
	srvrmgrConfig               *servermanager.ServerManagerConfig
	duration, error             prometheus.Gauge
	totalScrapes                prometheus.Counter
	scrapeErrors                prometheus.Counter
	gatewayServerUp             prometheus.Gauge
	applicationServerUp         prometheus.Gauge
	// Reconnection metrics
	reconnectsTotal       prometheus.Counter
	reconnectErrors       prometheus.Counter
	lastReconnectDuration prometheus.Gauge
}

var (
	defaultMetrics Metrics                // Default metrics to scrap
	metricsHashMap = make(map[int][]byte) // Metrics Files HashMap
)
