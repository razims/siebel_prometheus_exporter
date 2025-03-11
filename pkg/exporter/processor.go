package exporter

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/razims/siebel_prometheus_exporter/pkg/logger"
	"github.com/razims/siebel_prometheus_exporter/pkg/servermanager"
	"go.uber.org/zap"
)

// Process in chunks to avoid memory issues with large datasets
const chunkSize = 1000 // Process results in chunks of 1000 rows

// generic method for retrieving metrics.
func scrapeGenericValues(namespace string, dateFormat string, disableEmptyMetricsOverride bool, smgr *servermanager.ServerManager, ch *chan<- prometheus.Metric, metric Metric) error {
	logger.Debug("Scraping generic values",
		zap.String("command", metric.Command),
		zap.String("subsystem", metric.Subsystem))

	startTime := time.Now()
	siebelData, err := getSiebelData(smgr, metric.Command, dateFormat, disableEmptyMetricsOverride)
	dataFetchTime := time.Since(startTime)

	logger.Debug("Data fetched from Siebel",
		zap.Duration("fetchTime", dataFetchTime),
		zap.Int("rowCount", len(siebelData)),
		zap.Bool("hasError", err != nil))

	if err != nil {
		return err
	}

	processingStart := time.Now()
	metricsCount, err := generatePrometheusMetrics(siebelData, namespace, ch, metric)
	processingTime := time.Since(processingStart)

	logger.Debug("Metrics processed",
		zap.Int("count", metricsCount),
		zap.Bool("hasError", err != nil),
		zap.Duration("processingTime", processingTime))

	if err != nil {
		return err
	}

	if metricsCount == 0 && !metric.IgnoreZeroResult {
		logger.Warn("No metrics found while parsing",
			zap.String("command", metric.Command),
			zap.String("subsystem", metric.Subsystem))
		return fmt.Errorf("no metrics found while parsing (metrics count: %d)", metricsCount)
	}

	totalTime := time.Since(startTime)
	logger.Debug("Scraping completed successfully",
		zap.Duration("totalTime", totalTime),
		zap.Duration("fetchTime", dataFetchTime),
		zap.Duration("processingTime", processingTime),
		zap.Int("metricCount", metricsCount))

	return nil
}

func getSiebelData(smgr *servermanager.ServerManager, command string, dateFormat string, disableEmptyMetricsOverride bool) ([]map[string]string, error) {
	siebelData := []map[string]string{}

	logger.Debug("Sending command to Siebel Server Manager", zap.String("command", command))
	startTime := time.Now()

	// Use smgr directly, it's already a pointer
	lines, err := smgr.SendCommand(command)

	commandTime := time.Since(startTime)
	logger.Debug("Command completed",
		zap.Duration("executionTime", commandTime),
		zap.Int("resultLines", len(lines)),
		zap.Bool("hasError", err != nil))

	if err != nil {
		logger.Error("Error executing command",
			zap.String("command", command),
			zap.Error(err))
		return nil, err
	}

	// Check and parse srvrmgr output...
	if len(lines) < 3 {
		logger.Error("Command output too short to be valid",
			zap.String("command", command),
			zap.Int("lines", len(lines)))
		return nil, errors.New("command output is not valid")
	}

	columnsRow := lines[0]
	separatorsRow := lines[1]
	rawDataRows := lines[2:]

	logger.Debug("Parsing column headers",
		zap.String("columnsRow", columnsRow),
		zap.String("separatorsRow", separatorsRow))

	// Get column names
	columnsNames := strings.Split(trimHeadRow(columnsRow), " ")
	logger.Debug("Column names parsed", zap.Strings("columns", columnsNames))

	// Get column max lengths (calc from separator length)
	spacerLength := getSpacerLength(separatorsRow)
	separators := strings.Split(trimHeadRow(separatorsRow), " ")
	lengths := make([]int, len(separators))
	for i, s := range separators {
		lengths[i] = len(s) + spacerLength
	}

	if logger.Log.Core().Enabled(zap.DebugLevel) {
		logger.Debug("Column lengths calculated",
			zap.Int("spacerLength", spacerLength),
			zap.Any("lengths", lengths))
	}

	// Parse data-rows
	parseStart := time.Now()
	validRows := 0
	logger.Debug("Parsing rows with data", zap.Int("rowCount", len(rawDataRows)))

	for i, rawRow := range rawDataRows {
		// Skip completely empty lines
		if strings.TrimSpace(rawRow) == "" {
			logger.Debug("Skipping empty row", zap.Int("index", i))
			continue
		}

		if logger.Log.Core().Enabled(zap.DebugLevel) && (i == 0 || i == len(rawDataRows)-1 || i%100 == 0) {
			logger.Debug("Processing row", zap.Int("index", i), zap.String("rawRow", rawRow))
		}

		parsedRow := make(map[string]string)
		rowLen := len(rawRow)
		for colIndex, colName := range columnsNames {
			if colIndex >= len(lengths) {
				logger.Warn("Column index out of bounds",
					zap.Int("colIndex", colIndex),
					zap.Int("lengthsLen", len(lengths)),
					zap.String("colName", colName))
				continue
			}

			colMaxLen := lengths[colIndex]
			if colMaxLen > rowLen {
				colMaxLen = rowLen
			}

			if colMaxLen <= 0 {
				continue
			}

			colValue := strings.TrimSpace(rawRow[:colMaxLen])

			// If value is empty then set it to default "0"
			if len(colValue) == 0 && !disableEmptyMetricsOverride {
				colValue = "0"
			}

			// Try to convert date-string to Unix timestamp
			if len(colValue) == len(dateFormat) {
				colValue = convertDateStringToTimestamp(colValue, dateFormat)
			}

			parsedRow[colName] = colValue

			// Cut off used value from row
			if rowLen > colMaxLen {
				rawRow = rawRow[colMaxLen:]
				rowLen = len(rawRow)
			} else {
				rawRow = ""
				rowLen = 0
			}
		}

		siebelData = append(siebelData, parsedRow)
		validRows++
	}

	parseTime := time.Since(parseStart)
	logger.Debug("Data parsing completed",
		zap.Int("rowsParsed", validRows),
		zap.Int("totalRows", len(rawDataRows)),
		zap.Int("skippedRows", len(rawDataRows)-validRows),
		zap.Duration("parseTime", parseTime))

	return siebelData, nil
}

// Convert a single row to metrics
func convertRowToMetrics(row map[string]string, namespace string, metric Metric, seenMetrics map[string]bool) ([]prometheus.Metric, error) {
	metrics := []prometheus.Metric{}

	// Skip processing completely if the required field to append is empty
	if metric.FieldToAppend != "" && strings.TrimSpace(row[metric.FieldToAppend]) == "" {
		// Skip this entire row if the field to append is empty
		logger.Debug("Skipping row with empty field to append",
			zap.String("fieldToAppend", metric.FieldToAppend))
		return metrics, nil
	}

	// Construct labels name and value
	labelsNamesCleaned := []string{}
	labelsValues := []string{}
	for _, label := range metric.Labels {
		// Skip empty label values to avoid duplicates
		labelValue := row[label]
		if strings.TrimSpace(labelValue) == "" {
			logger.Debug("Empty label value, using default",
				zap.String("label", label))
			labelValue = "unknown"
		}

		labelsNamesCleaned = append(labelsNamesCleaned, cleanName(label))
		labelsValues = append(labelsValues, labelValue)
	}

	// Construct Prometheus values
	for metricName, metricHelp := range metric.Help {
		metricType := getMetricType(metricName, metric.Type)
		metricNameCleaned := cleanName(metricName)

		// Handle field to append for the metric name
		if strings.Compare(metric.FieldToAppend, "") != 0 {
			fieldValue := row[metric.FieldToAppend]
			fieldValueTrimmed := strings.TrimSpace(fieldValue)

			// This extra check should not be necessary now, but keeping it as a safeguard
			if fieldValueTrimmed == "" {
				logger.Debug("Skipping metric with empty field to append (secondary check)",
					zap.String("metricName", metricName),
					zap.String("fieldToAppend", metric.FieldToAppend))
				continue
			}

			metricNameCleaned = cleanName(fieldValue)

			// Additional sanity check to ensure metric name is not empty
			if metricNameCleaned == "" {
				logger.Warn("Empty metric name after cleaning, using default name",
					zap.String("originalField", fieldValue),
					zap.String("metricName", metricName))
				metricNameCleaned = fmt.Sprintf("unknown_%s", cleanName(metricName))
			}
		}

		// Final check to ensure metric name is never empty
		if metricNameCleaned == "" {
			logger.Warn("Empty metric name, using fallback", zap.String("metricName", metricName))
			metricNameCleaned = "unknown_metric"
		}

		// Dynamic help
		if dinHelpName, exists1 := metric.HelpField[metricName]; exists1 {
			if dinHelpValue, exists2 := row[dinHelpName]; exists2 {
				logger.Debug("Appending dynamic help",
					zap.String("baseHelp", metricHelp),
					zap.String("dynamicValue", dinHelpValue))
				metricHelp = metricHelp + " " + dinHelpValue
			}
		}

		metricValue := row[metricName]

		// Skip completely empty values (after trimming)
		if strings.TrimSpace(metricValue) == "" {
			// For time-related fields, special handling: log at debug level and skip
			if strings.Contains(strings.ToLower(metricName), "time") ||
				strings.Contains(strings.ToLower(metricHelp), "time") {
				logger.Debug("Skipping empty time field",
					zap.String("metricName", metricName),
					zap.String("help", metricHelp))
				continue
			}

			// For state-related fields, if we have a mapping, use a default state of 0
			if strings.Contains(strings.ToLower(metricName), "state") &&
				metric.ValueMap != nil && len(metric.ValueMap[metricName]) > 0 {
				logger.Debug("Using default value 0 for empty state field",
					zap.String("metricName", metricName))
				metricValue = "0"
			} else {
				// For all other empty fields, use 0 to avoid parse errors
				logger.Debug("Using default value 0 for empty field",
					zap.String("metricName", metricName))
				metricValue = "0"
			}
		}

		// Value mapping
		if metricMap, exists1 := metric.ValueMap[metricName]; exists1 {
			if len(metricMap) > 0 {
				// First log the original value
				logger.Debug("Processing value mapping",
					zap.String("metricName", metricName),
					zap.String("originalValue", metricValue),
					zap.Int("mappingCount", len(metricMap)))

				for key, mappedValue := range metricMap {
					if cleanName(key) == cleanName(metricValue) {
						logger.Debug("Mapping value",
							zap.String("from", metricValue),
							zap.String("to", mappedValue),
							zap.String("originalKey", key))
						metricValue = mappedValue
						break
					}
				}
				// Add mapping to help
				mappingHelp := " Value mapping: "
				mapStrings := []string{}
				for src, dst := range metricMap {
					mapStrings = append(mapStrings, dst+" - '"+src+"', ")
				}
				sort.Strings(mapStrings)
				for _, mapStr := range mapStrings {
					mappingHelp = mappingHelp + mapStr
				}
				mappingHelp = strings.TrimRight(mappingHelp, ", ")
				mappingHelp = mappingHelp + "."
				metricHelp = metricHelp + mappingHelp
			}
		}

		// If not a float, skip current metric
		metricValueParsed, err := strconv.ParseFloat(metricValue, 64)
		if err != nil {
			// Only log as error for non-empty values
			if metricValue != "" {
				logger.Error("Unable to convert value to float",
					zap.String("metricName", metricName),
					zap.String("value", metricValue),
					zap.String("help", metricHelp),
					zap.Error(err))
			} else {
				logger.Debug("Skipping empty value",
					zap.String("metricName", metricName),
					zap.String("help", metricHelp))
			}
			continue
		}

		// Create a unique key for this metric + label combination
		metricKey := createMetricKey(namespace, metric.Subsystem, metricNameCleaned, labelsValues)

		// Skip if we've already seen this exact metric + label combination
		if _, exists := seenMetrics[metricKey]; exists {
			logger.Debug("Skipping duplicate metric",
				zap.String("metricName", metricNameCleaned),
				zap.Strings("labels", labelsValues))
			continue
		}

		// Mark as seen for future checks
		seenMetrics[metricKey] = true

		promMetricDesc := prometheus.NewDesc(prometheus.BuildFQName(namespace, metric.Subsystem, metricNameCleaned), metricHelp, labelsNamesCleaned, nil)

		if metricType == prometheus.GaugeValue || metricType == prometheus.CounterValue {
			logger.Debug("Creating gauge/counter metric",
				zap.String("name", metricNameCleaned),
				zap.Float64("value", metricValueParsed),
				zap.Strings("labels", labelsValues))
			metrics = append(metrics, prometheus.MustNewConstMetric(promMetricDesc, metricType, metricValueParsed, labelsValues...))
		} else {
			// For histograms, verify we have a "count" field
			countValue, ok := row["count"]
			if !ok || strings.TrimSpace(countValue) == "" {
				logger.Error("Missing count field for histogram",
					zap.String("metricName", metricName))
				continue
			}

			count, err := strconv.ParseUint(strings.TrimSpace(countValue), 10, 64)
			if err != nil {
				logger.Error("Unable to convert count value to int",
					zap.String("metricName", metricName),
					zap.String("count", countValue),
					zap.String("help", metricHelp),
					zap.Error(err))
				continue
			}
			buckets := make(map[float64]uint64)
			for field, le := range metric.Buckets[metricName] {
				lelimit, err := strconv.ParseFloat(strings.TrimSpace(le), 64)
				if err != nil {
					logger.Error("Unable to convert bucket limit to float",
						zap.String("metricName", metricName),
						zap.String("bucketLimit", le),
						zap.String("help", metricHelp),
						zap.Error(err))
					continue
				}

				// Get the bucket value, use 0 if empty
				bucketValue := row[field]
				if strings.TrimSpace(bucketValue) == "" {
					bucketValue = "0"
				}

				counter, err := strconv.ParseUint(strings.TrimSpace(bucketValue), 10, 64)
				if err != nil {
					logger.Error("Unable to convert field value to int",
						zap.String("metricName", metricName),
						zap.String("field", field),
						zap.String("value", bucketValue),
						zap.String("help", metricHelp),
						zap.Error(err))
					continue
				}
				buckets[lelimit] = counter
			}
			logger.Debug("Creating histogram metric",
				zap.String("name", metricNameCleaned),
				zap.Float64("sum", metricValueParsed),
				zap.Uint64("count", count),
				zap.Any("buckets", buckets))
			metrics = append(metrics, prometheus.MustNewConstHistogram(promMetricDesc, count, metricValueParsed, buckets, labelsValues...))
		}
	}

	return metrics, nil
}

// Parse srvrmgr result and call parsing function to each row
func generatePrometheusMetrics(data []map[string]string, namespace string, ch *chan<- prometheus.Metric, metric Metric) (int, error) {
	totalRows := len(data)
	logger.Debug("Generating Prometheus metrics",
		zap.Int("totalRows", totalRows),
		zap.String("subsystem", metric.Subsystem))

	metricsCount := 0

	// Track unique metric combinations to avoid duplicates
	seenMetrics := make(map[string]bool)

	// Process data in chunks to avoid memory spikes
	for startIndex := 0; startIndex < totalRows; startIndex += chunkSize {
		endIndex := startIndex + chunkSize
		if endIndex > totalRows {
			endIndex = totalRows
		}

		currentChunk := data[startIndex:endIndex]
		logger.Debug("Processing chunk",
			zap.Int("startIndex", startIndex),
			zap.Int("endIndex", endIndex),
			zap.Int("chunkSize", len(currentChunk)))

		// Process this chunk of data
		chunkStart := time.Now()
		chunkCount, err := processDataChunk(currentChunk, namespace, ch, metric, seenMetrics)
		chunkTime := time.Since(chunkStart)

		if err != nil {
			logger.Error("Error processing chunk",
				zap.Int("startIndex", startIndex),
				zap.Int("endIndex", endIndex),
				zap.Error(err))
			return metricsCount, err
		}

		logger.Debug("Chunk processed successfully",
			zap.Int("startIndex", startIndex),
			zap.Int("endIndex", endIndex),
			zap.Int("metricsGenerated", chunkCount),
			zap.Duration("processingTime", chunkTime))

		metricsCount += chunkCount

		// Allow some time for GC to run between chunks if we have a large dataset
		if totalRows > chunkSize*2 {
			logger.Debug("Running garbage collection between chunks")
			runtime.GC()
		}
	}

	logger.Debug("Metrics processing completed",
		zap.Int("totalMetricsGenerated", metricsCount),
		zap.Int("uniqueMetrics", len(seenMetrics)))

	return metricsCount, nil
}

// Process a chunk of data rows
func processDataChunk(chunk []map[string]string, namespace string, ch *chan<- prometheus.Metric, metric Metric, seenMetrics map[string]bool) (int, error) {
	chunkMetricsCount := 0

	for rowIndex, row := range chunk {
		// Log progress for large chunks
		if logger.Log.Core().Enabled(zap.DebugLevel) && (rowIndex == 0 || rowIndex == len(chunk)-1 || rowIndex%100 == 0) {
			logger.Debug("Processing row in chunk",
				zap.Int("rowIndex", rowIndex),
				zap.Int("totalRows", len(chunk)))
		}

		// Process each row and convert to metrics
		rowStart := time.Now()
		rowMetrics, err := convertRowToMetrics(row, namespace, metric, seenMetrics)

		if err != nil {
			logger.Error("Error converting row to metrics",
				zap.Int("rowIndex", rowIndex),
				zap.Error(err))
			return chunkMetricsCount, err
		}

		// Send the metrics to the channel
		for _, m := range rowMetrics {
			*ch <- m
		}

		rowTime := time.Since(rowStart)
		if rowTime > 100*time.Millisecond {
			// Log slow row processing
			logger.Debug("Slow row processing detected",
				zap.Int("rowIndex", rowIndex),
				zap.Duration("processingTime", rowTime),
				zap.Int("metricsGenerated", len(rowMetrics)))
		}

		chunkMetricsCount += len(rowMetrics)
	}

	return chunkMetricsCount, nil
}

// createMetricKey creates a unique key for a metric based on its name and labels
func createMetricKey(namespace, subsystem, name string, labelValues []string) string {
	fqName := prometheus.BuildFQName(namespace, subsystem, name)
	return fmt.Sprintf("%s{%s}", fqName, strings.Join(labelValues, ","))
}

func getMetricType(metricName string, metricsTypes map[string]string) prometheus.ValueType {
	var strToPromType = map[string]prometheus.ValueType{
		"gauge":     prometheus.GaugeValue,
		"counter":   prometheus.CounterValue,
		"histogram": prometheus.UntypedValue,
	}
	strType, exists := metricsTypes[metricName]
	if !exists {
		return prometheus.GaugeValue
	}
	strType = strings.ToLower(strType)
	valueType, exists := strToPromType[strType]
	if !exists {
		logger.Error("Unknown metric type", zap.String("type", strType))
		return prometheus.GaugeValue
	}
	return valueType
}

func trimHeadRow(s string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(strings.Trim(s, " \n	"), " ")
}

func getSpacerLength(s string) int {
	result := 0
	logger.Debug("Determining spacer length", zap.String("input", s))
	if match := regexp.MustCompile(`(\s+)`).FindStringSubmatch(strings.Trim(s, " \n	")); len(match) < 2 {
		logger.Error("Could not determine spacer length", zap.String("input", s))
		result = 0
	} else {
		result = len(match[1])
	}
	logger.Debug("Spacer length determined", zap.Int("length", result))
	return result
}

func convertDateStringToTimestamp(s string, dateFormat string) string {
	if s == "0000-00-00 00:00:00" {
		return "0"
	}
	t, err := time.Parse(dateFormat, s)
	if err != nil {
		return s
	}
	return fmt.Sprint(t.Unix())
}

// If Siebel gives us some ugly names back, this function cleans it up for Prometheus.
// https://prometheus.io/docs/concepts/data_model/#metric-names-and-labels
func cleanName(s string) string {
	s = strings.TrimSpace(s)                                        // Trim spaces
	s = strings.Replace(s, " ", "_", -1)                            // Remove spaces
	s = regexp.MustCompile(`[^a-zA-Z0-9_]`).ReplaceAllString(s, "") // Remove other bad chars
	s = strings.ToLower(s)                                          // Switch case to lower
	return s
}
