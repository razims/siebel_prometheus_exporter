package exporter

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/razims/siebel_exporter/pkg/logger"
	"go.uber.org/zap"
)

func hashFile(h hash.Hash, fn string) error {
	f, err := os.Open(fn)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	return nil
}

func reloadMetricsIfItChanged(defaultMetricsFile, customMetricsFile string) {
	if checkIfMetricsChanged(customMetricsFile) {
		logger.Info("Custom metrics changed, reloading...")
		reloadMetrics(defaultMetricsFile, customMetricsFile)
	}
}

func checkIfMetricsChanged(customMetricsFile string) bool {
	logger.Debug("Checking if metrics files have changed")
	result := false

	for i, _customMetrics := range strings.Split(customMetricsFile, ",") {
		if len(_customMetrics) == 0 {
			continue
		}
		logger.Debug("Checking file for modifications", zap.String("file", _customMetrics))
		h := sha256.New()
		if err := hashFile(h, _customMetrics); err != nil {
			logger.Error("Unable to get file hash", zap.Error(err), zap.String("file", _customMetrics))
			result = false
			break
		}
		// If any of files has been changed reload metrics
		if !bytes.Equal(metricsHashMap[i], h.Sum(nil)) {
			logger.Info("File has changed, will reload metrics", zap.String("file", _customMetrics))
			metricsHashMap[i] = h.Sum(nil)
			result = true
			break
		}
	}

	if result {
		logger.Debug("Metrics files have changed")
	} else {
		logger.Debug("No changes detected in metrics files")
	}

	return result
}

func reloadMetrics(defaultMetricsFile, customMetricsFile string) {
	// Truncate defaultMetrics
	defaultMetrics.Metric = []Metric{}

	// Load default metrics
	if _, err := toml.DecodeFile(defaultMetricsFile, &defaultMetrics); err != nil {
		logger.Error("Failed to load default metrics file",
			zap.Error(err),
			zap.String("file", defaultMetricsFile))
		panic(fmt.Errorf("error while loading %s: %w", defaultMetricsFile, err))
	} else {
		logger.Info("Successfully loaded default metrics", zap.String("file", defaultMetricsFile))
	}

	// If custom metrics, load it
	if strings.Compare(customMetricsFile, "") != 0 {
		for _, _customMetrics := range strings.Split(customMetricsFile, ",") {
			if len(_customMetrics) == 0 {
				continue
			}

			// Reset custom metrics for each file
			customMetrics.Metric = []Metric{}

			if _, err := toml.DecodeFile(_customMetrics, &customMetrics); err != nil {
				logger.Error("Failed to load custom metrics file",
					zap.Error(err),
					zap.String("file", _customMetrics))
				panic(fmt.Errorf("error while loading %s: %w", _customMetrics, err))
			} else {
				logger.Info("Successfully loaded custom metrics",
					zap.String("file", _customMetrics),
					zap.Int("count", len(customMetrics.Metric)))
			}
			defaultMetrics.Metric = append(defaultMetrics.Metric, customMetrics.Metric...)
		}
	} else {
		logger.Info("No custom metrics defined")
	}

	logger.Info("Metrics loading complete", zap.Int("totalMetrics", len(defaultMetrics.Metric)))
}
