package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/flatcar/nebraska/backend/pkg/api/internal/dbconn"
	"github.com/flatcar/nebraska/backend/pkg/api/types"
)

func archPtr(a types.Arch) *types.Arch { return &a }

func TestGetAppInstancesPerChannelMetrics(t *testing.T) {
	a := newForTest(t)
	defer a.Close()

	// defaultTeamID constant is defined in users_test.go
	metrics, err := a.GetAppInstancesPerChannelMetrics()
	require.NoError(t, err)
	expectedMetrics := []types.AppInstancesPerChannelMetric{
		{
			ApplicationName: "Sample application",
			Version:         "1.0.1",
			ChannelName:     "Failing",
			Arch:            archPtr(types.ArchAMD64),
			InstancesCount:  1,
		},
		{
			ApplicationName: "Sample application",
			Version:         "1.0.1",
			ChannelName:     "Master",
			Arch:            archPtr(types.ArchAMD64),
			InstancesCount:  1,
		},
		{
			ApplicationName: "Sample application",
			Version:         "1.0.1",
			ChannelName:     "Stable",
			Arch:            archPtr(types.ArchAMD64),
			InstancesCount:  1,
		},
		{
			ApplicationName: "Sample application",
			Version:         "1.0.2",
			ChannelName:     "Master",
			Arch:            archPtr(types.ArchAMD64),
			InstancesCount:  1,
		},
		{
			ApplicationName: "Sample application",
			Version:         "1.0.2",
			ChannelName:     "Stable",
			Arch:            archPtr(types.ArchAMD64),
			InstancesCount:  2,
		},
		{
			ApplicationName: "Sample application",
			Version:         "1.0.3",
			ChannelName:     "Master",
			Arch:            archPtr(types.ArchAMD64),
			InstancesCount:  1,
		},
		{
			ApplicationName: "Sample application",
			Version:         "1.0.3",
			ChannelName:     "Stable",
			Arch:            archPtr(types.ArchAMD64),
			InstancesCount:  4,
		},
		{
			ApplicationName: "Sample application",
			Version:         "1.0.4",
			ChannelName:     "Master",
			Arch:            archPtr(types.ArchAMD64),
			InstancesCount:  1,
		},
	}

	require.Equal(t, expectedMetrics, metrics)
}

func TestGetAppInstancesPerChannelMetricsExcludesInactiveInstances(t *testing.T) {
	a := newForTest(t)
	defer a.Close()

	before, err := a.GetAppInstancesPerChannelMetrics()
	require.NoError(t, err)
	require.NotEmpty(t, before)

	_, err = dbconn.DB(a.Conn()).Exec(
		`UPDATE instance_application SET last_check_for_updates = now() - interval '400 days'`)
	require.NoError(t, err)

	after, err := a.GetAppInstancesPerChannelMetrics()
	require.NoError(t, err)
	assert.Empty(t, after, "instances that have not checked in for 400 days must not be counted")
}

func TestGetAppInstancesPerChannelMetricsKeepsGroupsWithoutChannel(t *testing.T) {
	a := newForTest(t)
	defer a.Close()

	before, err := a.GetAppInstancesPerChannelMetrics()
	require.NoError(t, err)
	totalBefore := 0
	for _, m := range before {
		totalBefore += m.InstancesCount
	}

	_, err = dbconn.DB(a.Conn()).Exec(`UPDATE groups SET channel_id = NULL`)
	require.NoError(t, err)

	after, err := a.GetAppInstancesPerChannelMetrics()
	require.NoError(t, err)

	totalAfter := 0
	for _, m := range after {
		totalAfter += m.InstancesCount
		assert.Equal(t, "none", m.ChannelName)
		assert.Nil(t, m.Arch, "a group with no channel has no architecture")
		assert.Equal(t, "unknown", m.ArchLabel())
	}

	assert.Equal(t, totalBefore, totalAfter,
		"dropping the channel must not drop the instances from the metric")
}

func TestGetFailedUpdatesMetrics(t *testing.T) {
	a := newForTest(t)
	defer a.Close()

	// defaultTeamID constant is defined in users_test.go
	metrics, err := a.GetFailedUpdatesMetrics()
	require.NoError(t, err)
	expectedMetrics := []types.FailedUpdatesMetric{
		{
			ApplicationName: "Sample application",
			GroupName:       "Failing Qa-Dev",
			FailureCount:    1,
		},
	}
	require.Equal(t, expectedMetrics, metrics)
}

func TestGetFailedUpdatesMetricsUsesRecentWindow(t *testing.T) {
	a := newForTest(t)
	defer a.Close()

	before, err := a.GetFailedUpdatesMetrics()
	require.NoError(t, err)
	require.NotEmpty(t, before)

	_, err = dbconn.DB(a.Conn()).Exec(`UPDATE event SET created_ts = now() - interval '30 days'`)
	require.NoError(t, err)

	after, err := a.GetFailedUpdatesMetrics()
	require.NoError(t, err)
	assert.Empty(t, after, "failures from 30 days ago must not count towards current rollout health")
}

func TestGetGroupRolloutMetrics(t *testing.T) {
	a := newForTest(t)
	defer a.Close()

	metrics, err := a.GetGroupRolloutMetrics()
	require.NoError(t, err)
	require.NotEmpty(t, metrics, "sample data has groups with updates enabled")

	for _, m := range metrics {
		assert.NotEmpty(t, m.ApplicationName)
		assert.NotEmpty(t, m.GroupName)

		assert.Equal(t, m.UpdatesAttempted, m.UpdatesSucceeded+m.UpdatesFailed,
			"succeeded + failed must equal attempted for group %q", m.GroupName)
		assert.Equal(t, m.UpdatesGranted,
			m.UpdatesAttempted+m.UpdatesInProgress+m.UpdatesTimedOut,
			"attempted + in progress + timed out must equal granted for group %q", m.GroupName)
		assert.LessOrEqual(t, m.UpdatesGranted, m.TotalInstances,
			"cannot grant more updates than there are instances in group %q", m.GroupName)
	}
}

func TestGetGroupRolloutMetricsOnlyCoversEnabledGroups(t *testing.T) {
	a := newForTest(t)
	defer a.Close()

	require.NotEmpty(t, mustRolloutMetrics(t, a))

	_, err := dbconn.DB(a.Conn()).Exec(`UPDATE groups SET policy_updates_enabled = false`)
	require.NoError(t, err)
	_, err = dbconn.DB(a.Conn()).Exec(`UPDATE group_local SET policy_updates_enabled_override = NULL`)
	require.NoError(t, err)

	assert.Empty(t, mustRolloutMetrics(t, a),
		"no group has updates enabled, so nothing should be reported")
}

func mustRolloutMetrics(t *testing.T, a *API) []types.GroupRolloutMetric {
	t.Helper()
	metrics, err := a.GetGroupRolloutMetrics()
	require.NoError(t, err)
	return metrics
}
