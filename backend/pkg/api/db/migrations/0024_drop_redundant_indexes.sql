-- +migrate Up

-- Six indexes are fully covered by other indexes on the same table, so Postgres
-- can already serve every predicate they support. Five of them sit on
-- instance_application and instance_status_history, which are written on every
-- Omaha check-in and every status change respectively, so each one is pure
-- write amplification that scales with fleet size.
--
-- instance_application_instance_id_application_id_idx is not merely a prefix:
-- it is an exact duplicate of instance_application_pkey, which is also
-- (instance_id, application_id). Migration 0011 added it without noticing that
-- 0001 had already declared the same key, so the table has carried two
-- identical B-trees ever since.
--
-- The rest are leading prefixes of wider indexes added later, in 0013, 0016 and
-- 0020. A B-tree on (a, b, c) serves any predicate on (a) alone, so the narrow
-- index only duplicates work.
--
-- instance_application_instance_id_idx is the oldest of these: the primary key
-- that covers it is declared on line 119 of 0001 and the index is created on
-- line 122 of that same file, so it has been redundant since the first
-- migration.

-- exact duplicate of instance_application_pkey (instance_id, application_id)
drop index if exists instance_application_instance_id_application_id_idx;

-- prefix of instance_application_pkey
drop index if exists instance_application_instance_id_idx;

-- prefix of instance_application_group_id_last_check_for_updates_instan_idx
drop index if exists instance_application_group_id_idx;

-- prefix of instance_status_history_instance_id_status_created_ts_idx
drop index if exists instance_status_history_instance_id_idx;

-- prefix of instance_status_history_group_id_status_created_ts_idx
drop index if exists instance_status_history_group_id_idx;

-- prefix of channel_package_floors_pkey (channel_id, package_id)
drop index if exists idx_channel_package_floors_channel;

-- +migrate Down

create index if not exists instance_application_instance_id_application_id_idx on instance_application (instance_id, application_id);

create index if not exists instance_application_instance_id_idx on instance_application (instance_id);

create index if not exists instance_application_group_id_idx on instance_application (group_id);

create index if not exists instance_status_history_instance_id_idx on instance_status_history (instance_id);

create index if not exists instance_status_history_group_id_idx on instance_status_history (group_id);

create index if not exists idx_channel_package_floors_channel on channel_package_floors (channel_id);
