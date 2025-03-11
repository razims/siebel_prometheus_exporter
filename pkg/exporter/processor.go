package exporter

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/razims/siebel_exporter/pkg/logger"
	"github.com/razims/siebel_exporter/pkg/servermanager"
	"go.uber.org/zap"
)

// generic method for retrieving metrics.
func scrapeGenericValues(namespace string, dateFormat string, disableEmptyMetricsOverride bool, smgr *servermanager.ServerManager, ch *chan<- prometheus.Metric, metric Metric) error {
	logger.Debug("Scraping generic values")

	siebelData, err := getSiebelData(smgr, metric.Command, dateFormat, disableEmptyMetricsOverride)
	if err != nil {
		return err
	}

	metricsCount, err := generatePrometheusMetrics(siebelData, namespace, ch, metric)
	logger.Debug("Metrics processed", zap.Int("count", metricsCount))
	if err != nil {
		return err
	}

	if metricsCount == 0 && !metric.IgnoreZeroResult {
		return fmt.Errorf("no metrics found while parsing (metrics count: %d)", metricsCount)
	}

	return err
}

func getSiebelData(smgr *servermanager.ServerManager, command string, dateFormat string, disableEmptyMetricsOverride bool) ([]map[string]string, error) {
	siebelData := []map[string]string{}

	// Use smgr directly, it's already a pointer
	lines, err := smgr.SendCommand(command)
	if err != nil {
		return nil, err
	}

	// Check and parse srvrmgr output...
	if len(lines) < 3 {
		return nil, errors.New("command output is not valid")
	}

	columnsRow := lines[0]
	separatorsRow := lines[1]
	rawDataRows := lines[2:]

	// Get column names
	columnsNames := strings.Split(trimHeadRow(columnsRow), " ")

	// Get column max lengths (calc from separator length)
	spacerLength := getSpacerLength(separatorsRow)
	separators := strings.Split(trimHeadRow(separatorsRow), " ")
	lengths := make([]int, len(separators))
	for i, s := range separators {
		lengths[i] = len(s) + spacerLength
	}

	// Parse data-rows
	logger.Debug("Parsing rows with data", zap.Int("rowCount", len(rawDataRows)))
	for i, rawRow := range rawDataRows {
		if logger.Log.Core().Enabled(zap.DebugLevel) {
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
	}

	return siebelData, nil
}

// Parse srvrmgr result and call parsing function to each row
func generatePrometheusMetrics(data []map[string]string, namespace string, ch *chan<- prometheus.Metric, metric Metric) (int, error) {
	logger.Debug("Generating Prometheus metrics", zap.Int("rowCount", len(data)))

	metricsCount := 0

	// Track unique metric combinations to avoid duplicates
	seenMetrics := make(map[string]bool)

	dataRowToPrometheusMetricConverter := func(row map[string]string) error {
		if logger.Log.Core().Enabled(zap.DebugLevel) {
			logger.Debug("Converting row to metric", zap.Any("row", row))
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

		// Construct Prometheus values to send back
		for metricName, metricHelp := range metric.Help {
			metricType := getMetricType(metricName, metric.Type)
			metricNameCleaned := cleanName(metricName)

			// Handle field to append for the metric name
			if strings.Compare(metric.FieldToAppend, "") != 0 {
				fieldValue := row[metric.FieldToAppend]
				if strings.TrimSpace(fieldValue) == "" {
					// Skip this metric if the field to append is empty (avoid empty metric name)
					logger.Warn("Skipping metric with empty field to append",
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
					for key, mappedValue := range metricMap {
						if cleanName(key) == cleanName(metricValue) {
							logger.Debug("Mapping value",
								zap.String("from", metricValue),
								zap.String("to", mappedValue))
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
				*ch <- prometheus.MustNewConstMetric(promMetricDesc, metricType, metricValueParsed, labelsValues...)
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
				*ch <- prometheus.MustNewConstHistogram(promMetricDesc, count, metricValueParsed, buckets, labelsValues...)
			}
			metricsCount++
		}
		return nil
	}

	for _, row := range data {
		// Convert parsed row to Prometheus Metric
		if err := dataRowToPrometheusMetricConverter(row); err != nil {
			return metricsCount, err
		}
	}

	return metricsCount, nil
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
