package runtime

import (
	"fmt"
	"time"
)

// Retention pruning for the tables that grow with fleet churn.
//
// Nebraska has never deleted instance data. instance, instance_application,
// instance_status_history, event, activity and instance_stats only ever grow,
// so a machine decommissioned two years ago is still a row with its whole
// history attached. Fleets that add and remove nodes hourly hit this fast, and
// the growth feeds back into autovacuum: its thresholds scale with table size,
// so the bigger these tables get the less often it runs.
//
// Retention is off by default. Turning it on is a destructive, irreversible
// operation on someone else's production data, so it must be an explicit
// choice, and DryRun exists so an operator can see the blast radius first.

const (
	// defaultRetentionBatchSize caps how many rows a single DELETE touches.
	// instance_application is written on every Omaha check-in, so an unbounded
	// delete would hold row locks across a hot table for the whole scan.
	defaultRetentionBatchSize = 1000

	// maxRetentionBatches bounds one pruning pass. A very large backlog is
	// worked off over successive runs rather than in one long transaction that
	// blocks check-ins.
	maxRetentionBatches = 1000
)

// RetentionConfig describes what to prune. A zero duration disables that
// category, which is the default.
type RetentionConfig struct {
	// Instances removes instances whose most recent check-in is older than
	// this, falling back to instance.created_ts for an instance that has never
	// checked in. instance_application, instance_status_history, event and
	// activity all declare "references instance (id) on delete cascade", so
	// deleting the instance takes its history with it.
	Instances time.Duration

	// History removes instance_status_history and event rows older than this,
	// independently of whether their instance is still alive. This is what
	// bounds the history of a long-lived, still-reporting machine.
	History time.Duration

	// Stats removes instance_stats rows older than this. instance_stats is
	// written hourly by a background job and is a pure time series.
	Stats time.Duration

	// DryRun counts what would be removed and removes nothing.
	DryRun bool

	// BatchSize overrides defaultRetentionBatchSize when positive.
	BatchSize int
}

// Enabled reports whether any category is configured to prune.
func (c RetentionConfig) Enabled() bool {
	return c.Instances > 0 || c.History > 0 || c.Stats > 0
}

// RetentionReport counts the rows removed (or, in dry-run, matched) per table.
type RetentionReport struct {
	Instances            int64
	InstanceStatusEvents int64
	Events               int64
	Stats                int64
	DryRun               bool
}

// Total returns the number of rows across all categories. Rows removed by
// cascade are not counted: only the instance row itself is.
func (r RetentionReport) Total() int64 {
	return r.Instances + r.InstanceStatusEvents + r.Events + r.Stats
}

// PruneOldData applies the retention policy once. It is safe to call
// concurrently with normal traffic: work is done in bounded batches, and each
// batch is an independent statement rather than one long transaction.
func (s *Service) PruneOldData(cfg RetentionConfig) (*RetentionReport, error) {
	report := &RetentionReport{DryRun: cfg.DryRun}

	if !cfg.Enabled() {
		return report, nil
	}

	batch := cfg.BatchSize
	if batch <= 0 {
		batch = defaultRetentionBatchSize
	}

	if cfg.Instances > 0 {
		n, err := s.pruneBatched(cfg, batch,
			`SELECT count(*) FROM instance i
			 LEFT JOIN (
				SELECT instance_id, max(last_check_for_updates) AS last_seen
				FROM instance_application GROUP BY instance_id
			 ) ia ON ia.instance_id = i.id
			 WHERE COALESCE(ia.last_seen, i.created_ts) < now() - $1::interval`,
			`DELETE FROM instance WHERE id IN (
				SELECT i.id FROM instance i
				LEFT JOIN (
					SELECT instance_id, max(last_check_for_updates) AS last_seen
					FROM instance_application GROUP BY instance_id
				) ia ON ia.instance_id = i.id
				WHERE COALESCE(ia.last_seen, i.created_ts) < now() - $1::interval
				LIMIT $2
			 )`,
			cfg.Instances)
		if err != nil {
			return nil, fmt.Errorf("pruning instances: %w", err)
		}
		report.Instances = n
	}

	if cfg.History > 0 {
		n, err := s.pruneBatched(cfg, batch,
			`SELECT count(*) FROM instance_status_history WHERE created_ts < now() - $1::interval`,
			`DELETE FROM instance_status_history WHERE id IN (
				SELECT id FROM instance_status_history
				WHERE created_ts < now() - $1::interval LIMIT $2
			 )`,
			cfg.History)
		if err != nil {
			return nil, fmt.Errorf("pruning instance status history: %w", err)
		}
		report.InstanceStatusEvents = n

		n, err = s.pruneBatched(cfg, batch,
			`SELECT count(*) FROM event WHERE created_ts < now() - $1::interval`,
			`DELETE FROM event WHERE id IN (
				SELECT id FROM event WHERE created_ts < now() - $1::interval LIMIT $2
			 )`,
			cfg.History)
		if err != nil {
			return nil, fmt.Errorf("pruning events: %w", err)
		}
		report.Events = n
	}

	if cfg.Stats > 0 {
		// instance_stats has no surrogate key, so batches are addressed by
		// ctid. That is safe here because each batch re-selects.
		n, err := s.pruneBatched(cfg, batch,
			`SELECT count(*) FROM instance_stats WHERE timestamp < now() - $1::interval`,
			`DELETE FROM instance_stats WHERE ctid IN (
				SELECT ctid FROM instance_stats
				WHERE timestamp < now() - $1::interval LIMIT $2
			 )`,
			cfg.Stats)
		if err != nil {
			return nil, fmt.Errorf("pruning instance stats: %w", err)
		}
		report.Stats = n
	}

	return report, nil
}

// pruneBatched runs deleteSQL in batches until it stops matching rows, or just
// evaluates countSQL when the config is in dry-run mode.
func (s *Service) pruneBatched(cfg RetentionConfig, batch int, countSQL, deleteSQL string, age time.Duration) (int64, error) {
	interval := postgresInterval(age)

	if cfg.DryRun {
		var count int64
		if err := s.db.QueryRow(countSQL, interval).Scan(&count); err != nil {
			return 0, err
		}
		return count, nil
	}

	var total int64
	for i := 0; i < maxRetentionBatches; i++ {
		res, err := s.db.Exec(deleteSQL, interval, batch)
		if err != nil {
			return total, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += affected
		if affected < int64(batch) {
			break
		}
	}
	return total, nil
}

// postgresInterval renders a Go duration as a Postgres interval literal. It is
// expressed in whole seconds so the value is always a plain integer, which
// keeps it safe to hand to Postgres as a parameter regardless of locale or
// IntervalStyle.
func postgresInterval(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(d.Seconds()))
}
