package types

const unknownMetricLabel = "unknown"

type AppInstancesPerChannelMetric struct {
	ApplicationName string `db:"app_name" json:"app_name"`
	Version         string `db:"version" json:"version"`
	ChannelName     string `db:"channel_name" json:"channel_name"`
	Arch            *Arch  `db:"arch" json:"arch"`
	InstancesCount  int    `db:"instances_count" json:"instances_count"`
}

func (m AppInstancesPerChannelMetric) ArchLabel() string {
	if m.Arch == nil {
		return unknownMetricLabel
	}
	return m.Arch.String()
}

type AppInstancesPerOEMMetric struct {
	ApplicationName string `db:"app_name" json:"app_name"`
	Version         string `db:"version" json:"version"`
	OEM             string `db:"oem" json:"oem"`
	InstancesCount  int    `db:"instances_count" json:"instances_count"`
}

type FailedUpdatesMetric struct {
	ApplicationName string `db:"app_name" json:"app_name"`
	GroupName       string `db:"group_name" json:"group_name"`
	FailureCount    int    `db:"fail_count" json:"fail_count"`
}

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
