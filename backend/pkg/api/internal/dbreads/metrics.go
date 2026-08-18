package dbreads

import (
	"database/sql"
	"fmt"

	"github.com/flatcar/nebraska/backend/pkg/api/types"
)

var (
	// appInstancesPerChannelMetricSQL counts active instances per application,
	// version, channel and architecture.
	//
	// Three things here are deliberate and were previously wrong:
	//
	//   - the last_check_for_updates window. Nothing deletes instance_application
	//     rows, so without it a machine decommissioned years ago keeps
	//     contributing to the metric while the UI, which does apply the window,
	//     reports zero.
	//   - the LEFT JOIN on channel. groups.channel_id is "on delete set null",
	//     and an inner join silently drops every instance in a group whose
	//     channel was deleted.
	//   - the arch column. channel is unique on (application_id, name, arch) and
	//     migration 0008 creates arm64 channels reusing the amd64 names, so
	//     grouping by name alone folds two architectures into one series.
	appInstancesPerChannelMetricSQL = fmt.Sprintf(`
SELECT a.name AS app_name, ia.version AS version,
       COALESCE(c.name, 'none') AS channel_name, c.arch AS arch,
       count(*) AS instances_count
FROM instance_application ia
	JOIN application a ON a.id = ia.application_id
	JOIN groups g ON g.id = ia.group_id
	LEFT JOIN channel c ON c.id = g.channel_id
WHERE ia.last_check_for_updates > now() - interval '%[1]s' AND %[2]s
GROUP BY 1, 2, 3, 4
ORDER BY 1, 2, 3, 4
`, validityInterval, ignoreFakeInstanceCondition("ia.instance_id"))

	appInstancesPerOEMMetricSQL = fmt.Sprintf(`
SELECT a.name AS app_name, ia.version AS version,
       COALESCE(NULLIF(i.oem, ''), 'unknown') AS oem, count(*) AS instances_count
FROM instance_application ia
	JOIN application a ON a.id = ia.application_id
	JOIN instance i ON i.id = ia.instance_id
WHERE ia.last_check_for_updates > now() - interval '%[1]s' AND %[2]s
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3
`, validityInterval, ignoreFakeInstanceCondition("ia.instance_id"))

	// failedUpdatesSQL counts UpdateComplete events that reported a failure
	// (event_type.type = 3, result = 0).
	//
	// The window matters: without one this counts every failure ever recorded,
	// so a fleet that has since recovered still reports a permanently elevated
	// number and the metric cannot answer "is the current rollout healthy".
	//
	// The group comes from instance_application rather than the event, which
	// only records the application. Rollout policy is enforced per group, so an
	// application-wide total cannot point at the group that is actually failing.
	failedUpdatesSQL = fmt.Sprintf(`
SELECT a.name AS app_name, g.name AS group_name, count(*) AS fail_count
FROM event e
	JOIN event_type et ON et.id = e.event_type_id
	JOIN application a ON a.id = e.application_id
	JOIN instance_application ia
		ON ia.instance_id = e.instance_id AND ia.application_id = e.application_id
	JOIN groups g ON g.id = ia.group_id
WHERE et.result = 0 AND et.type = 3
	AND e.created_ts > now() - interval '%[1]s' AND %[2]s
GROUP BY 1, 2
ORDER BY 1, 2
`, failedUpdatesInterval, ignoreFakeInstanceCondition("e.instance_id"))

	// groupRolloutMetricSQL is the whole-fleet form of GetGroupUpdatesStats:
	// the same case expressions, evaluated for every group in one pass instead
	// of one query per group.
	//
	// The policy intervals are per-group columns, so they are read from the row
	// and cast to interval rather than being interpolated. Effective values come
	// from COALESCE(group_local override, groups default), matching groupsQuery.
	//
	// Only groups with updates enabled are reported, both because rollout
	// progress is meaningless for a group that never grants updates and to keep
	// the series count down.
	groupRolloutMetricSQL = fmt.Sprintf(`
SELECT
	a.name AS app_name,
	g.name AS group_name,
	COALESCE(c.name, 'none') AS channel_name,
	count(*) AS total_instances,
	COALESCE(sum(CASE WHEN ia.last_update_version = p.version
		THEN 1 ELSE 0 END), 0) AS updates_granted,
	COALESCE(sum(CASE WHEN ia.update_in_progress = false AND ia.last_update_version = p.version
		THEN 1 ELSE 0 END), 0) AS updates_attempted,
	COALESCE(sum(CASE WHEN ia.update_in_progress = false AND ia.last_update_version = p.version
		AND ia.last_update_version = ia.version
		THEN 1 ELSE 0 END), 0) AS updates_succeeded,
	COALESCE(sum(CASE WHEN ia.update_in_progress = false AND ia.last_update_version = p.version
		AND ia.last_update_version <> ia.version
		THEN 1 ELSE 0 END), 0) AS updates_failed,
	COALESCE(sum(CASE WHEN ia.update_in_progress = true
		AND now() - ia.last_update_granted_ts <= COALESCE(gl.policy_update_timeout_override, g.policy_update_timeout)::interval
		THEN 1 ELSE 0 END), 0) AS updates_in_progress,
	COALESCE(sum(CASE WHEN ia.update_in_progress = true
		AND now() - ia.last_update_granted_ts > COALESCE(gl.policy_update_timeout_override, g.policy_update_timeout)::interval
		THEN 1 ELSE 0 END), 0) AS updates_timed_out,
	COALESCE(sum(CASE WHEN ia.last_update_granted_ts > now() - COALESCE(gl.policy_period_interval_override, g.policy_period_interval)::interval
		THEN 1 ELSE 0 END), 0) AS updates_granted_in_period
FROM instance_application ia
	JOIN groups g ON g.id = ia.group_id
	JOIN group_local gl ON gl.group_id = g.id
	JOIN application a ON a.id = ia.application_id
	LEFT JOIN channel c ON c.id = g.channel_id
	LEFT JOIN package p ON p.id = c.package_id
WHERE COALESCE(gl.policy_updates_enabled_override, g.policy_updates_enabled) = true
	AND ia.last_check_for_updates > now() - interval '%[1]s' AND %[2]s
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3
`, validityInterval, ignoreFakeInstanceCondition("ia.instance_id"))
)

// failedUpdatesInterval is the window over which update failures are counted
// for the nebraska_failed_updates metric. It matches validityInterval, the
// window the dashboard uses to decide whether an instance is still active, so
// the failure count and the instance counts describe the same set of machines.
const failedUpdatesInterval = validityInterval

func (q *Queries) GetAppInstancesPerChannelMetrics() ([]types.AppInstancesPerChannelMetric, error) {
	var metrics []types.AppInstancesPerChannelMetric
	rows, err := q.db.Queryx(appInstancesPerChannelMetricSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var metric types.AppInstancesPerChannelMetric
		err := rows.StructScan(&metric)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metrics, nil
}

// GetAppInstancesPerOEMMetrics returns instance counts grouped by application,
// version and reported OEM/platform.
func (q *Queries) GetAppInstancesPerOEMMetrics() ([]types.AppInstancesPerOEMMetric, error) {
	var metrics []types.AppInstancesPerOEMMetric
	rows, err := q.db.Queryx(appInstancesPerOEMMetricSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var metric types.AppInstancesPerOEMMetric
		err := rows.StructScan(&metric)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metrics, nil
}

func (q *Queries) GetFailedUpdatesMetrics() ([]types.FailedUpdatesMetric, error) {
	var metrics []types.FailedUpdatesMetric
	rows, err := q.db.Queryx(failedUpdatesSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var metric types.FailedUpdatesMetric
		err := rows.StructScan(&metric)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metrics, nil
}

// GetGroupRolloutMetrics returns rollout progress counters for every group that
// has updates enabled.
func (q *Queries) GetGroupRolloutMetrics() ([]types.GroupRolloutMetric, error) {
	var metrics []types.GroupRolloutMetric
	rows, err := q.db.Queryx(groupRolloutMetricSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var metric types.GroupRolloutMetric
		err := rows.StructScan(&metric)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metrics, nil
}

func (q *Queries) DbStats() sql.DBStats {
	return q.db.Stats()
}
