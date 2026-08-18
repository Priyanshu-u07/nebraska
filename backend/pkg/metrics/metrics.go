package metrics

import (
	"fmt"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/flatcar/nebraska/backend/pkg/api"
	"github.com/flatcar/nebraska/backend/pkg/logger"
)

const (
	defaultMetricsUpdateInterval = 15 * time.Second
)

var (
	appInstancePerChannelGaugeMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "nebraska",
			Name:      "application_instances_per_channel",
			Help:      "Number of applications from specific channel running on instances",
		},
		[]string{
			"application",
			"version",
			"channel",
			// Channel names are only unique per architecture, so amd64 and
			// arm64 channels are both called "stable". Without this label the
			// two fleets are reported as one.
			"arch",
		},
	)

	appInstancePerOEMGaugeMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "nebraska",
			Name:      "application_instances_per_oem",
			Help:      "Number of instances running an application, by the OEM/platform they report",
		},
		[]string{
			"application",
			"version",
			"oem",
		},
	)

	failedUpdatesGaugeMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "nebraska",
			Name:      "failed_updates",
			Help:      "Number of failed updates of an application in the last day, by group",
		},
		[]string{
			"application",
			"group",
		},
	)

	// Rollout progress. These mirror the UpdatesStats that Nebraska already
	// computes to enforce update policy, which until now were only ever used
	// internally and never exposed. Reported only for groups with updates
	// enabled: rollout progress is meaningless for a group that never grants
	// updates, and skipping them keeps the series count proportional to the
	// number of groups actually rolling out.
	newRolloutGauge = func(name, help string) *prometheus.GaugeVec {
		return prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "nebraska",
				Name:      name,
				Help:      help,
			},
			[]string{"application", "group", "channel"},
		)
	}

	groupTotalInstancesGauge = newRolloutGauge("group_total_instances",
		"Total active instances in the group")
	groupUpdatesGrantedGauge = newRolloutGauge("group_updates_granted",
		"Instances granted an update to the group's current version")
	groupUpdatesAttemptedGauge = newRolloutGauge("group_updates_attempted",
		"Instances that finished attempting the update, successfully or not")
	groupUpdatesSucceededGauge = newRolloutGauge("group_updates_succeeded",
		"Instances running the group's current version after updating")
	groupUpdatesFailedGauge = newRolloutGauge("group_updates_failed",
		"Instances that attempted the update and are not on the current version")
	groupUpdatesInProgressGauge = newRolloutGauge("group_updates_in_progress",
		"Instances currently updating and still within the group's update timeout")
	groupUpdatesTimedOutGauge = newRolloutGauge("group_updates_timed_out",
		"Instances still updating past the group's update timeout")
	groupUpdatesGrantedInPeriodGauge = newRolloutGauge("group_updates_granted_in_period",
		"Updates granted within the group's current policy period")

	openConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "nebraska",
			Name:      "open_db_connections",
			Help:      "Number of established connections both in use and idle",
		},
	)

	inUseConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "nebraska",
			Name:      "in_use_db_connections",
			Help:      "Number of connections currently in use",
		},
	)

	idleConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "nebraska",
			Name:      "idle_db_connections",
			Help:      "Number of idle connections",
		},
	)

	l = logger.New("nebraska")
)

// registerNebraskaMetrics registers the application metrics collector with the DefaultRegistrer.
func registerNebraskaMetrics() error {
	collectors := []prometheus.Collector{
		appInstancePerChannelGaugeMetric,
		appInstancePerOEMGaugeMetric,
		failedUpdatesGaugeMetric,
		groupTotalInstancesGauge,
		groupUpdatesGrantedGauge,
		groupUpdatesAttemptedGauge,
		groupUpdatesSucceededGauge,
		groupUpdatesFailedGauge,
		groupUpdatesInProgressGauge,
		groupUpdatesTimedOutGauge,
		groupUpdatesGrantedInPeriodGauge,
		openConnections,
		inUseConnections,
		idleConnections,
	}

	for _, collector := range collectors {
		err := prometheus.Register(collector)
		if err != nil {
			return err
		}
	}
	return nil
}

// getMetricsRefreshInterval returns the metrics update Interval key is set in the environment as time.Duration,
// NEBRASKA_METRICS_UPDATE_INTERVAL. The variable must be a string acceptable by time.ParseDuration
// If not returns the default update interval.
func getMetricsRefreshInterval() time.Duration {
	refreshIntervalEnvValue := os.Getenv("NEBRASKA_METRICS_UPDATE_INTERVAL")
	if refreshIntervalEnvValue == "" {
		return defaultMetricsUpdateInterval
	}

	refreshInterval, err := time.ParseDuration(refreshIntervalEnvValue)
	if err != nil || refreshInterval <= 0 {
		l.Warn().Str("value", refreshIntervalEnvValue).Msg("invalid NEBRASKA_METRICS_UPDATE_INTERVAL, it must be acceptable by time.ParseDuration and positive value")
		return defaultMetricsUpdateInterval
	}
	return refreshInterval
}

// registerAndInstrumentMetrics registers the application metrics and instruments them in configurable intervals.
func RegisterAndInstrument(api *api.API) error {
	// register application metrics
	err := registerNebraskaMetrics()
	if err != nil {
		return err
	}

	refreshInterval := getMetricsRefreshInterval()

	metricsTicker := time.NewTicker(refreshInterval)

	go func() {
		for {
			<-metricsTicker.C
			err := calculateMetrics(api)
			if err != nil {
				l.Error().Err(err).Msg("registerAndInstrumentMetrics updating the metrics")
			}
		}
	}()

	return nil
}

// calculateMetrics calculates the application metrics and updates the respective metric.
//
// Every gauge vector is Reset() before being repopulated. Set() only touches
// the series for the labels it is given; it does not retire a series that
// existed on a previous tick and is absent from this one. Without the reset a
// stale series freezes at its last value until the process restarts, which
// happens routinely: an instance moving from one version to the next during a
// rollout, an application or group being renamed, a channel being deleted. The
// visible symptom is a total that keeps climbing past the real fleet size.
func calculateMetrics(api *api.API) error {
	aipcMetrics, err := api.GetAppInstancesPerChannelMetrics()
	if err != nil {
		return fmt.Errorf("failed to get app instances per channel metrics: %w", err)
	}

	appInstancePerChannelGaugeMetric.Reset()
	for _, metric := range aipcMetrics {
		appInstancePerChannelGaugeMetric.WithLabelValues(metric.ApplicationName, metric.Version, metric.ChannelName, metric.ArchLabel()).Set(float64(metric.InstancesCount))
	}

	aipoMetrics, err := api.GetAppInstancesPerOEMMetrics()
	if err != nil {
		return fmt.Errorf("failed to get app instances per oem metrics: %w", err)
	}

	appInstancePerOEMGaugeMetric.Reset()
	for _, metric := range aipoMetrics {
		appInstancePerOEMGaugeMetric.WithLabelValues(metric.ApplicationName, metric.Version, metric.OEM).Set(float64(metric.InstancesCount))
	}

	fuMetrics, err := api.GetFailedUpdatesMetrics()
	if err != nil {
		return fmt.Errorf("failed to get failed update metrics: %w", err)
	}

	failedUpdatesGaugeMetric.Reset()
	for _, metric := range fuMetrics {
		failedUpdatesGaugeMetric.WithLabelValues(metric.ApplicationName, metric.GroupName).Set(float64(metric.FailureCount))
	}

	rolloutMetrics, err := api.GetGroupRolloutMetrics()
	if err != nil {
		return fmt.Errorf("failed to get group rollout metrics: %w", err)
	}

	for _, gauge := range []*prometheus.GaugeVec{
		groupTotalInstancesGauge, groupUpdatesGrantedGauge, groupUpdatesAttemptedGauge,
		groupUpdatesSucceededGauge, groupUpdatesFailedGauge, groupUpdatesInProgressGauge,
		groupUpdatesTimedOutGauge, groupUpdatesGrantedInPeriodGauge,
	} {
		gauge.Reset()
	}
	for _, metric := range rolloutMetrics {
		labels := []string{metric.ApplicationName, metric.GroupName, metric.ChannelName}
		groupTotalInstancesGauge.WithLabelValues(labels...).Set(float64(metric.TotalInstances))
		groupUpdatesGrantedGauge.WithLabelValues(labels...).Set(float64(metric.UpdatesGranted))
		groupUpdatesAttemptedGauge.WithLabelValues(labels...).Set(float64(metric.UpdatesAttempted))
		groupUpdatesSucceededGauge.WithLabelValues(labels...).Set(float64(metric.UpdatesSucceeded))
		groupUpdatesFailedGauge.WithLabelValues(labels...).Set(float64(metric.UpdatesFailed))
		groupUpdatesInProgressGauge.WithLabelValues(labels...).Set(float64(metric.UpdatesInProgress))
		groupUpdatesTimedOutGauge.WithLabelValues(labels...).Set(float64(metric.UpdatesTimedOut))
		groupUpdatesGrantedInPeriodGauge.WithLabelValues(labels...).Set(float64(metric.UpdatesGrantedInPeriod))
	}

	// db stats
	dbStats := api.DbStats()
	openConnections.Set(float64(dbStats.OpenConnections))
	inUseConnections.Set(float64(dbStats.InUse))
	idleConnections.Set(float64(dbStats.Idle))

	return nil
}
