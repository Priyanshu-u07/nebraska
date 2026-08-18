package runtime

import (
	"fmt"
	"time"
)

const (
	defaultRetentionBatchSize = 1000

	maxRetentionBatches = 1000
)

type RetentionConfig struct {
	Instances time.Duration

	History time.Duration

	Stats time.Duration

	DryRun bool

	BatchSize int
}

func (c RetentionConfig) Enabled() bool {
	return c.Instances > 0 || c.History > 0 || c.Stats > 0
}

type RetentionReport struct {
	Instances            int64
	InstanceStatusEvents int64
	Events               int64
	Stats                int64
	DryRun               bool
}

func (r RetentionReport) Total() int64 {
	return r.Instances + r.InstanceStatusEvents + r.Events + r.Stats
}

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

func postgresInterval(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(d.Seconds()))
}
