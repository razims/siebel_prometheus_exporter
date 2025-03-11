package exporter

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/razims/siebel_prometheus_exporter/pkg/logger"
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

func reloadMetricsIfItChanged(metricsFile string) {
	if checkIfMetricsChanged(metricsFile) {
		logger.Info("Metrics file changed, reloading...", zap.String("file", metricsFile))
		loadMetrics(metricsFile)
	}
}

func checkIfMetricsChanged(metricsFile string) bool {
	logger.Debug("Checking if metrics file has changed", zap.String("file", metricsFile))

	h := sha256.New()
	if err := hashFile(h, metricsFile); err != nil {
		logger.Error("Unable to get file hash", zap.Error(err), zap.String("file", metricsFile))
		return false
	}

	// Check if file has been changed
	currentHash := h.Sum(nil)
	if !bytes.Equal(metricsHashMap[0], currentHash) {
		logger.Info("File has changed, will reload metrics", zap.String("file", metricsFile))
		metricsHashMap[0] = currentHash
		return true
	}

	logger.Debug("No changes detected in metrics file")
	return false
}

func loadMetrics(metricsFile string) {
	// Clear existing metrics
	defaultMetrics.Metric = []Metric{}

	// Load metrics from file
	if _, err := toml.DecodeFile(metricsFile, &defaultMetrics); err != nil {
		logger.Error("Failed to load metrics file",
			zap.Error(err),
			zap.String("file", metricsFile))
		panic(fmt.Errorf("error while loading %s: %w", metricsFile, err))
	}

	logger.Info("Successfully loaded metrics",
		zap.String("file", metricsFile),
		zap.Int("count", len(defaultMetrics.Metric)))
}
