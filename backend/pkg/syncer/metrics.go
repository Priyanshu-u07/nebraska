package syncer

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Observability for the sync loop.
//
// The syncer is what keeps a self-hosted Nebraska supplied with new Flatcar
// releases: it polls the public update servers on a timer and creates packages
// when it finds something new. If those requests start failing — a firewall
// change, an expired CA bundle, upstream moving — Nebraska simply stops
// receiving releases. Nothing errors visibly, the dashboard looks healthy, and
// the only evidence is a line in the debug log.
//
// These four metrics make that state alertable. They are labelled by channel
// and arch to match the syncer's own channelDescriptor, so a single failing
// channel is distinguishable from a completely dead loop.
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
			// The default buckets top out at 10s, which is too short to
			// distinguish "slow upstream" from "hung": these requests go over
			// the public internet and can legitimately take seconds.
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
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

// RegisterMetrics registers the syncer's Prometheus collectors. It is separate
// from package init so that a Nebraska started with the syncer disabled does
// not publish metrics that would sit at zero forever and read as a broken sync
// loop.
func RegisterMetrics() error {
	for _, collector := range syncerCollectors {
		if err := prometheus.Register(collector); err != nil {
			return err
		}
	}
	return nil
}

// observeCheck records the outcome and duration of one channel/arch check.
func observeCheck(descriptor channelDescriptor, start time.Time, err error) {
	channel, arch := descriptor.name, descriptor.arch.String()

	syncerCheckDuration.WithLabelValues(channel, arch).Observe(time.Since(start).Seconds())
	if err != nil {
		syncerCheckFailures.WithLabelValues(channel, arch).Inc()
		return
	}
	// Reported as an absolute timestamp rather than an age so that the value
	// does not depend on when Prometheus happens to scrape: alert with
	// time() - nebraska_syncer_last_success_timestamp_seconds > 3600.
	syncerLastSuccessTimestamp.WithLabelValues(channel, arch).SetToCurrentTime()
}

// observePackageCreated records that a genuinely new package was created,
// distinguishing a syncer that is working from one that is merely reachable.
func observePackageCreated(descriptor channelDescriptor) {
	syncerPackagesCreated.WithLabelValues(descriptor.name, descriptor.arch.String()).Inc()
}
