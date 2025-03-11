# Siebel Exporter for Prometheus

A Prometheus exporter for Oracle Siebel CRM that collects metrics via the Siebel Server Manager (srvrmgr) command-line interface.

This project was inspired by [barkadron/siebel_exporter](https://github.com/barkadron/siebel_exporter) and builds upon its ideas with additional features, improved error handling, and memory optimizations.

## Features

- Collection of Siebel server status metrics
- Component groups, components, and tasks monitoring
- Server statistics and state values
- Flexible metric configuration via TOML files
- Automatic reconnection with exponential backoff
- Memory-efficient processing of large datasets
- Extensive logging and diagnostics

## Requirements

- Go 1.20 or higher
- Access to a Siebel environment with the `srvrmgr` utility
- Prometheus server for metrics collection

## Installation

### Building from source

```bash
# Clone the repository
git clone https://github.com/razims/siebel_prometheus_exporter.git
cd siebel_prometheus_exporter

# Build the exporter
go build -o siebel_exporter ./cli
```

### Using pre-built binaries

Download the latest release from the [Releases](https://github.com/razims/siebel_exporter/releases) page.

## Usage

```bash
./siebel_exporter \
  --siebel.gateway=gateway.example.com \
  --siebel.enterprise=SBA_83 \
  --siebel.server=SIEBSRVR_01 \
  --siebel.user=SADMIN \
  --siebel.password=password \
  --web.listen-address=0.0.0.0:9963 \
  --log.level=info
```

### Command-line Options

| Option | Default | Description |
|--------|---------|-------------|
| `--web.listen-address` | `0.0.0.0:9963` | Address to listen on for web interface and telemetry |
| `--web.telemetry-path` | `/metrics` | Path under which to expose metrics |
| `--web.disable-exporter-metrics` | `false` | Exclude metrics about the exporter itself |
| `--runtime.gomaxprocs` | `0` | Set GOMAXPROCS, 0 means use default |
| `--siebel.gateway` | | Siebel Gateway server address |
| `--siebel.enterprise` | | Siebel Enterprise name |
| `--siebel.server` | | Siebel Application server name |
| `--siebel.user` | | Siebel user name |
| `--siebel.password` | | Siebel user password |
| `--siebel.srvrmgr-path` | `srvrmgr` | Path to srvrmgr executable |
| `--siebel.metrics-file` | `metrics.toml` | Metrics configuration file |
| `--siebel.date-format` | `2006-01-02 15:04:05` | Date format for timestamp conversion |
| `--siebel.disable-empty-metrics-override` | `false` | Disable override of empty metrics in results with value of 0 |
| `--siebel.disable-extended-metrics` | `false` | Disable any metric defined as 'Extended' in metrics file |
| `--siebel.auto-reconnect` | `true` | Enable automatic reconnection if connection is lost |
| `--siebel.reconnect-delay` | `10s` | Delay between reconnection attempts |
| `--siebel.reconnect-after-scrape` | `false` | Reconnect to server after each scrape |
| `--log.level` | `info` | Log level (debug, info, warn, error) |

## Prometheus Configuration

Add the following to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: siebel
    static_configs:
      - targets: ['localhost:9963']
```

## Metrics Configuration

Metrics are defined in a TOML file. The default is `metrics.toml` in the current directory.

```toml
[[Metric]]
Command = "list server show SBLSRVR_STATE, START_TIME, END_TIME"
Subsystem = "list_server"
[Metric.Help]
SBLSRVR_STATE = "State of the Siebel Application Server."
START_TIME = "Time the Siebel Application Server was started."
END_TIME = "Time the Siebel Application Server was stopped."
[Metric.ValueMap.SBLSRVR_STATE]
"Running" = "2"
"Shutting Down" = "3"
# ... additional states
```

### Metric Definition Structure

| Field | Description |
|-------|-------------|
| `Command` | The srvrmgr command to execute |
| `Subsystem` | The Prometheus subsystem name |
| `Help` | Help text for each metric |
| `ValueMap` | Maps string values to numeric values for Prometheus |
| `Labels` | List of columns to use as labels |
| `FieldToAppend` | Field to append to the metric name |
| `IgnoreZeroResult` | Don't error if no metrics found |
| `Extended` | Mark as extended metric (can be disabled) |

## Troubleshooting

### Logging

Use `--log.level=debug` for verbose logging during troubleshooting.

### Connection Issues

- Verify srvrmgr works directly when run manually
- Check network connectivity to the Siebel Gateway
- Ensure the credentials have sufficient permissions
- Try `--siebel.reconnect-after-scrape` if connections appear to become stale

### Memory Usage

For large Siebel environments with many components:
- Monitor memory usage
- Consider using `--siebel.disable-extended-metrics` to reduce the amount of data collected

## Development

### Building

```bash
go build -o siebel_exporter ./cli
```

### Testing

```bash
go test ./...
```

## License

MIT

## Acknowledgments

- [Prometheus](https://prometheus.io/)
- [Uber zap](https://github.com/uber-go/zap) for structured logging
- [barkadron/siebel_exporter](https://github.com/barkadron/siebel_exporter) for the initial shell implementation and inspiration