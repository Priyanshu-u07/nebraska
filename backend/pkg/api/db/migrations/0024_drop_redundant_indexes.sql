-- +migrate Up

-- Six indexes are fully covered by others on the same table. Five sit on
-- instance_application and instance_status_history, written on every check-in
-- and status change, so each is write amplification that scales with the fleet.

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
