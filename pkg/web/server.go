package web

import (
	"fmt"
	"html"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/razims/siebel_prometheus_exporter/pkg/exporter"
	"github.com/razims/siebel_prometheus_exporter/pkg/logger"
	"github.com/razims/siebel_prometheus_exporter/pkg/servermanager"
	"go.uber.org/zap"
)

// ServerConfig holds the web server configuration
type ServerConfig struct {
	ListenAddress          string
	MetricsPath            string
	DisableExporterMetrics bool
	DisableLogs            bool
}

// Server represents the web server
type Server struct {
	config         ServerConfig
	registry       *prometheus.Registry
	smConfig       *servermanager.ServerManagerConfig
	exporterConfig *exporter.ExporterConfig
	logLevel       string
	startTime      time.Time
}

// NewServer creates a new web server
func NewServer(config ServerConfig, smConfig *servermanager.ServerManagerConfig, exporterConfig *exporter.ExporterConfig, logLevel string) *Server {
	return &Server{
		config:         config,
		registry:       prometheus.NewRegistry(),
		smConfig:       smConfig,
		exporterConfig: exporterConfig,
		logLevel:       logLevel,
		startTime:      time.Now(),
	}
}

// RegisterExporter registers the Siebel exporter with the Prometheus registry
func (s *Server) RegisterExporter(siebelExporter *exporter.Exporter) {
	s.registry.MustRegister(siebelExporter)

	// If not disabled, register Go collector and process collector
	if !s.config.DisableExporterMetrics {
		s.registry.MustRegister(prometheus.NewGoCollector())
		s.registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
		s.registry.MustRegister(prometheus.NewBuildInfoCollector())
		logger.Info("Registered standard exporters")
	} else {
		logger.Info("Standard exporters disabled")
	}
}

// Start starts the web server
func (s *Server) Start() error {
	// Setup HTTP handlers
	http.Handle(s.config.MetricsPath, promhttp.HandlerFor(
		s.registry,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	))

	http.HandleFunc("/", s.homeHandler)

	// Only register logs handler if not disabled
	if !s.config.DisableLogs {
		http.HandleFunc("/logs", s.logsHandler)
	}

	logger.Info("Starting HTTP server",
		zap.String("address", s.config.ListenAddress),
		zap.String("metricsPath", s.config.MetricsPath),
		zap.Bool("exporterMetricsDisabled", s.config.DisableExporterMetrics),
		zap.Bool("logsDisabled", s.config.DisableLogs))

	return http.ListenAndServe(s.config.ListenAddress, nil)
}

// homeHandler handles the home page
func (s *Server) homeHandler(w http.ResponseWriter, r *http.Request) {
	var html strings.Builder

	html.WriteString(`<html>
<head>
  <title>Siebel Exporter</title>
  <style>
    body { font-family: 'Helvetica Neue', Arial, sans-serif; margin: 0; padding: 20px; color: #333; }
    h1 { color: #1976D2; border-bottom: 1px solid #eee; padding-bottom: 10px; }
    h3 { color: #0D47A1; margin-top: 20px; }
    a { color: #1976D2; text-decoration: none; }
    a:hover { text-decoration: underline; }
    .container { max-width: 960px; margin: 0 auto; }
    .metrics-link { display: inline-block; margin: 10px 0; padding: 8px 16px; background-color: #1976D2; color: white; border-radius: 4px; }
    .metrics-link:hover { background-color: #0D47A1; text-decoration: none; }
    table { width: 100%; border-collapse: collapse; margin: 20px 0; }
    table, th, td { border: 1px solid #ddd; }
    th { background-color: #f5f5f5; padding: 10px; text-align: left; }
    td { padding: 8px 10px; }
    tr:nth-child(even) { background-color: #f9f9f9; }
  </style>
</head>
<body>
  <div class="container">
    <h1>Siebel Exporter</h1>
    <a href="` + s.config.MetricsPath + `" class="metrics-link">View Metrics</a>`)

	// Only show logs link if not disabled
	if !s.config.DisableLogs {
		html.WriteString(`
    <a href="/logs" class="metrics-link" style="margin-left: 10px;">View Logs</a>`)
	}

	html.WriteString(`
    
    <h3>Current Configuration</h3>
    <table>
      <tr>
        <th>Setting</th>
        <th>Value</th>
      </tr>
      <tr>
        <td>Gateway</td>
        <td>` + s.smConfig.Gateway + `</td>
      </tr>
      <tr>
        <td>Enterprise</td>
        <td>` + s.smConfig.Enterprise + `</td>
      </tr>
      <tr>
        <td>Server</td>
        <td>` + s.smConfig.Server + `</td>
      </tr>
      <tr>
        <td>User</td>
        <td>` + s.smConfig.User + `</td>
      </tr>
      <tr>
        <td>Srvrmgr Path</td>
        <td>` + s.smConfig.SrvrmgrPath + `</td>
      </tr>
      <tr>
        <td>Auto Reconnect</td>
        <td>` + fmt.Sprintf("%t", s.smConfig.AutoReconnect) + `</td>
      </tr>
      <tr>
        <td>Reconnect Delay</td>
        <td>` + s.smConfig.ReconnectDelay.String() + `</td>
      </tr>
      <tr>
        <td>Reconnect After Scrape</td>
        <td>` + fmt.Sprintf("%t", s.exporterConfig.ReconnectAfterScrape) + `</td>
      </tr>
      <tr>
        <td>Metrics File</td>
        <td>` + s.exporterConfig.MetricsFile + `</td>
      </tr>
      <tr>
        <td>Date Format</td>
        <td>` + s.exporterConfig.DateFormat + `</td>
      </tr>
      <tr>
        <td>Disable Empty Metrics Override</td>
        <td>` + fmt.Sprintf("%t", s.exporterConfig.DisableEmptyMetricsOverride) + `</td>
      </tr>
      <tr>
        <td>Disable Extended Metrics</td>
        <td>` + fmt.Sprintf("%t", s.exporterConfig.DisableExtendedMetrics) + `</td>
      </tr>
      <tr>
        <td>Web Listen Address</td>
        <td>` + s.config.ListenAddress + `</td>
      </tr>
      <tr>
        <td>Metrics Path</td>
        <td>` + s.config.MetricsPath + `</td>
      </tr>
      <tr>
        <td>Disable Exporter Metrics</td>
        <td>` + fmt.Sprintf("%t", s.config.DisableExporterMetrics) + `</td>
      </tr>
      <tr>
        <td>Disable Logs</td>
        <td>` + fmt.Sprintf("%t", s.config.DisableLogs) + `</td>
      </tr>
      <tr>
        <td>Log Level</td>
        <td>` + s.logLevel + `</td>
      </tr>
    </table>`)

	// Get current memory statistics
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Format memory values
	formatMemory := func(bytes uint64) string {
		const unit = 1024
		if bytes < unit {
			return fmt.Sprintf("%d B", bytes)
		}
		div, exp := uint64(unit), 0
		for n := bytes / unit; n >= unit; n /= unit {
			div *= unit
			exp++
		}
		return fmt.Sprintf("%.2f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
	}

	// Get logs count if logs are enabled
	logCount := 0
	if !s.config.DisableLogs {
		logEntries := logger.GetLogEntries()
		logCount = len(logEntries)
	}

	// Get uptime
	uptime := time.Since(s.startTime).Round(time.Second)

	html.WriteString(`
    <h3>Performance Metrics</h3>
    <table>
      <tr>
        <th>Metric</th>
        <th>Value</th>
      </tr>
      <tr>
        <td>Memory Usage (Alloc)</td>
        <td>` + formatMemory(memStats.Alloc) + `</td>
      </tr>
      <tr>
        <td>Memory Usage (Sys)</td>
        <td>` + formatMemory(memStats.Sys) + `</td>
      </tr>
      <tr>
        <td>Memory Usage (Heap Alloc)</td>
        <td>` + formatMemory(memStats.HeapAlloc) + `</td>
      </tr>
      <tr>
        <td>Memory Usage (Heap Sys)</td>
        <td>` + formatMemory(memStats.HeapSys) + `</td>
      </tr>
      <tr>
        <td>Goroutines</td>
        <td>` + fmt.Sprintf("%d", runtime.NumGoroutine()) + `</td>
      </tr>
      <tr>
        <td>GC Cycles</td>
        <td>` + fmt.Sprintf("%d", memStats.NumGC) + `</td>
      </tr>
      <tr>
        <td>GC Pause Total</td>
        <td>` + time.Duration(memStats.PauseTotalNs).Round(time.Millisecond).String() + `</td>
      </tr>`)

	// Only show logs count if logs are enabled
	if !s.config.DisableLogs {
		html.WriteString(`
      <tr>
        <td>Logs in Memory</td>
        <td>` + fmt.Sprintf("%d entries", logCount) + `</td>
      </tr>`)
	}

	html.WriteString(`
      <tr>
        <td>Uptime</td>
        <td>` + uptime.String() + `</td>
      </tr>
    </table>
    
    <h3>Metrics collected:</h3>
    <ul>
      <li>Siebel Server Status</li>
      <li>Component Groups Status</li>
      <li>Components Status &amp; Tasks</li>
      <li>Server Statistics</li>
      <li>Server State Values</li>
    </ul>
    
    <footer style="margin-top: 30px; padding-top: 10px; border-top: 1px solid #eee; text-align: center; color: #666; font-size: 12px;">
      <p>Siebel Prometheus Exporter &copy; 2025 <a href="https://github.com/razims/siebel_prometheus_exporter" target="_blank">github.com/razims/siebel_prometheus_exporter</a></p>
      <p>Released under MIT License</p>
    </footer>
  </div>
</body>
</html>`)

	w.Write([]byte(html.String()))
}

// logsHandler handles the logs page
func (s *Server) logsHandler(w http.ResponseWriter, r *http.Request) {
	// Skip if logs are disabled
	if s.config.DisableLogs {
		http.Error(w, "Logs endpoint is disabled", http.StatusNotFound)
		return
	}

	entries := logger.GetLogEntries()

	// Simple log level filter
	level := r.URL.Query().Get("level")
	if level != "" {
		level = strings.ToUpper(level)
		var filtered []logger.LogEntry
		for _, entry := range entries {
			if entry.Level == level {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <title>Siebel Exporter - Logs</title>
  <style>
    body { font-family: 'Helvetica Neue', Arial, sans-serif; margin: 0; padding: 20px; color: #333; }
    h1 { color: #1976D2; border-bottom: 1px solid #eee; padding-bottom: 10px; }
    a { color: #1976D2; text-decoration: none; }
    a:hover { text-decoration: underline; }
    .container { max-width: 1200px; margin: 0 auto; }
    .nav { margin-bottom: 20px; }
    .filters { margin: 15px 0; }
    .filter-btn {
      display: inline-block;
      margin-right: 10px;
      padding: 5px 15px;
      border-radius: 4px;
      border: 1px solid #ccc;
      background-color: #f5f5f5;
      cursor: pointer;
    }
    .filter-btn:hover {
      background-color: #e0e0e0;
    }
    .filter-btn.active {
      background-color: #1976D2;
      color: white;
      border-color: #0D47A1;
    }
    .logs {
      background-color: #f5f5f5;
      border: 1px solid #ddd;
      border-radius: 4px;
      padding: 15px;
      font-family: monospace;
      font-size: 13px;
      line-height: 1.5;
      max-height: 700px;
      overflow-y: auto;
      white-space: pre-wrap;
      word-wrap: break-word;
    }
    .log-entry {
      margin: 2px 0;
      padding: 3px 5px;
      border-radius: 2px;
    }
    .log-entry:hover {
      background-color: rgba(0,0,0,0.05);
    }
    .log-DEBUG { color: #2196F3; }
    .log-INFO { color: #4CAF50; }
    .log-WARN { color: #FF9800; }
    .log-ERROR { color: #F44336; }
    .log-FATAL { color: #9C27B0; }
    .timestamp { color: #666; }
    .refresh-btn {
      display: inline-block;
      margin: 10px 0;
      padding: 8px 16px;
      background-color: #1976D2;
      color: white;
      border-radius: 4px;
      border: none;
      cursor: pointer;
    }
    .refresh-btn:hover {
      background-color: #0D47A1;
    }
  </style>
  <script>
    function filterLogs(level) {
      if (level) {
        window.location.href = '/logs?level=' + level;
      } else {
        window.location.href = '/logs';
      }
    }
    
    function refreshLogs() {
      window.location.reload();
    }
    
    document.addEventListener('DOMContentLoaded', function() {
      // Set active filter button
      const urlParams = new URLSearchParams(window.location.search);
      const activeLevel = urlParams.get('level');
      if (activeLevel) {
        document.getElementById('filter-' + activeLevel.toLowerCase()).classList.add('active');
      } else {
        document.getElementById('filter-all').classList.add('active');
      }
      
      // Auto-scroll to bottom of logs
      const logsContainer = document.querySelector('.logs');
      logsContainer.scrollTop = logsContainer.scrollHeight;
    });
  </script>
</head>
<body>
  <div class="container">
    <h1>Siebel Exporter - Logs</h1>
    
    <div class="nav">
      <a href="/">← Back to Dashboard</a>
    </div>
    
    <div class="filters">
      <span class="filter-btn" id="filter-all" onclick="filterLogs('')">All</span>
      <span class="filter-btn" id="filter-debug" onclick="filterLogs('DEBUG')">Debug</span>
      <span class="filter-btn" id="filter-info" onclick="filterLogs('INFO')">Info</span>
      <span class="filter-btn" id="filter-warn" onclick="filterLogs('WARN')">Warning</span>
      <span class="filter-btn" id="filter-error" onclick="filterLogs('ERROR')">Error</span>
      <button class="refresh-btn" onclick="refreshLogs()">Refresh Logs</button>
    </div>
    
    <div class="logs">`)

	// Output log entries
	for _, entry := range entries {
		// Add a class based on log level for styling
		fmt.Fprintf(w, `<div class="log-entry log-%s">%s</div>`,
			entry.Level,
			html.EscapeString(entry.String()))
	}

	fmt.Fprintf(w, `</div>
    
    <footer style="margin-top: 30px; padding-top: 10px; border-top: 1px solid #eee; text-align: center; color: #666; font-size: 12px;">
      <p>Siebel Prometheus Exporter &copy; 2025 <a href="https://github.com/razims/siebel_prometheus_exporter" target="_blank">github.com/razims/siebel_prometheus_exporter</a></p>
      <p>Released under MIT License</p>
    </footer>
  </div>
</body>
</html>`)
}
