package types

// unknownMetricLabel is the value used for a Prometheus label whose source
// column is NULL. Prometheus has no notion of an absent label value, so an
// explicit bucket is better than dropping the series or emitting "".
const unknownMetricLabel = "unknown"

type AppInstancesPerChannelMetric struct {
	ApplicationName string `db:"app_name" json:"app_name"`
	Version         string `db:"version" json:"version"`
	ChannelName     string `db:"channel_name" json:"channel_name"`
	// Arch is nil when the instance's group has no channel assigned
	// (groups.channel_id is "on delete set null"), in which case there is no
	// architecture to report.
	Arch           *Arch `db:"arch" json:"arch"`
	InstancesCount int   `db:"instances_count" json:"instances_count"`
}

// ArchLabel renders the architecture for use as a Prometheus label value.
// amd64 and arm64 channels share the same names, so without this label the two
// architectures collapse into a single series.
func (m AppInstancesPerChannelMetric) ArchLabel() string {
	if m.Arch == nil {
		return unknownMetricLabel
	}
	return m.Arch.String()
}

// AppInstancesPerOEMMetric counts the instances of an application version by
// the OEM/platform they report on check-in. Instances that report no OEM are
// bucketed under "unknown".
type AppInstancesPerOEMMetric struct {
	ApplicationName string `db:"app_name" json:"app_name"`
	Version         string `db:"version" json:"version"`
	OEM             string `db:"oem" json:"oem"`
	InstancesCount  int    `db:"instances_count" json:"instances_count"`
}

// FailedUpdatesMetric counts update failures per application and group over a
// recent window. The group dimension matters because rollouts are enforced per
// group, so an application-wide count cannot tell an operator which rollout is
// unhealthy.
type FailedUpdatesMetric struct {
	ApplicationName string `db:"app_name" json:"app_name"`
	GroupName       string `db:"group_name" json:"group_name"`
	FailureCount    int    `db:"fail_count" json:"fail_count"`
}

// GroupRolloutMetric mirrors the UpdatesStats that Nebraska already computes
// for policy enforcement, but for every group at once and carrying the labels
// needed to identify the rollout. It is what backs the nebraska_group_* gauges.
type GroupRolloutMetric struct {
	ApplicationName string `db:"app_name" json:"app_name"`
	GroupName       string `db:"group_name" json:"group_name"`
	ChannelName     string `db:"channel_name" json:"channel_name"`

	TotalInstances         int `db:"total_instances" json:"total_instances"`
	UpdatesGranted         int `db:"updates_granted" json:"updates_granted"`
	UpdatesAttempted       int `db:"updates_attempted" json:"updates_attempted"`
	UpdatesSucceeded       int `db:"updates_succeeded" json:"updates_succeeded"`
	UpdatesFailed          int `db:"updates_failed" json:"updates_failed"`
	UpdatesInProgress      int `db:"updates_in_progress" json:"updates_in_progress"`
	UpdatesTimedOut        int `db:"updates_timed_out" json:"updates_timed_out"`
	UpdatesGrantedInPeriod int `db:"updates_granted_in_period" json:"updates_granted_in_period"`
}
