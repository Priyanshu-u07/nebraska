package syncer

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	syncerLastSuccessTimestamp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "nebraska",
			Name:      "syncer_last_success_timestamp_seconds",
			Help:      "Unix timestamp of the last successful update check for a channel/arch",
		},
		[]string{"channel", "arch"},
	)

	syncerCheckFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "nebraska",
			Name:      "syncer_check_failures_total",
			Help:      "Number of failed update checks against the upstream Flatcar servers",
		},
		[]string{"channel", "arch"},
	)

	syncerCheckDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "nebraska",
			Name:      "syncer_check_duration_seconds",
			Help:      "Duration of update checks against the upstream Flatcar servers",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"channel", "arch"},
	)

	syncerPackagesCreated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "nebraska",
			Name:      "syncer_packages_created_total",
			Help:      "Number of packages created by the syncer after finding a new release",
		},
		[]string{"channel", "arch"},
	)

	syncerCollectors = []prometheus.Collector{
		syncerLastSuccessTimestamp,
		syncerCheckFailures,
		syncerCheckDuration,
		syncerPackagesCreated,
	}
)

func RegisterMetrics() error {
	for _, collector := range syncerCollectors {
		if err := prometheus.Register(collector); err != nil {
			return err
		}
	}
	return nil
}

func observeCheck(descriptor channelDescriptor, start time.Time, err error) {
	channel, arch := descriptor.name, descriptor.arch.String()

	syncerCheckDuration.WithLabelValues(channel, arch).Observe(time.Since(start).Seconds())
	if err != nil {
		syncerCheckFailures.WithLabelValues(channel, arch).Inc()
		return
	}
	syncerLastSuccessTimestamp.WithLabelValues(channel, arch).SetToCurrentTime()
}

func observePackageCreated(descriptor channelDescriptor) {
	syncerPackagesCreated.WithLabelValues(descriptor.name, descriptor.arch.String()).Inc()
}
