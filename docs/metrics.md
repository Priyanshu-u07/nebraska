# Metrics, logging and Prometheus

Nebraska exposes Prometheus metrics on `/metrics`. The endpoint needs no
authentication and is exempt from the OpenAPI request validator, so it can be
scraped directly.

```console
$ curl -s http://localhost:8000/metrics | grep '^nebraska_'
```

## Contents

- [Scraping Nebraska](#scraping-nebraska)
- [What is exported](#what-is-exported)
  - [Fleet composition](#fleet-composition)
  - [Rollout progress](#rollout-progress)
  - [Update failures](#update-failures)
  - [Syncer health](#syncer-health)
  - [Database](#database)
  - [HTTP](#http)
- [What to alert on](#what-to-alert-on)
- [Grafana](#grafana)
- [Logging](#logging)

## Scraping Nebraska

```yaml
scrape_configs:
  - job_name: nebraska
    static_configs:
      - targets: ['nebraska:8000']
```

Nebraska recomputes its database-backed gauges on a timer rather than at scrape
time, because they come from aggregate queries that are too expensive to run on
every scrape. The interval is set with `NEBRASKA_METRICS_UPDATE_INTERVAL`
(a Go duration, default `15s`). Scraping faster than that interval just returns
the same values, so keep `scrape_interval` at or above it.

## What is exported

### Fleet composition

| Metric | Type | Labels |
| --- | --- | --- |
| `nebraska_application_instances_per_channel` | gauge | `application`, `version`, `channel`, `arch` |
| `nebraska_application_instances_per_oem` | gauge | `application`, `version`, `oem` |

Both count instances that have checked in within the last day. An instance that
stops reporting drops out of these metrics, matching what the dashboard shows.

`arch` matters because channel names are only unique per architecture:
migration `0008` creates arm64 channels reusing the amd64 names, so without the
label a `stable` series is the sum of two unrelated fleets.

`oem` is the platform each machine reports on check-in (`azure`, `ami`, `gce`,
`vmware`, `packet`, ...). Machines that report nothing are bucketed as
`unknown` rather than dropped, so the series always add up to the fleet size:

```
nebraska_application_instances_per_oem{application="Flatcar Container Linux",oem="azure",version="4152.0.0"} 10
nebraska_application_instances_per_oem{application="Flatcar Container Linux",oem="unknown",version="4116.0.0"} 2
```

### Rollout progress

Eight gauges, all labelled `application`, `group`, `channel`, and reported only
for groups with updates enabled:

| Metric | Meaning |
| --- | --- |
| `nebraska_group_total_instances` | Active instances in the group |
| `nebraska_group_updates_granted` | Granted an update to the group's current version |
| `nebraska_group_updates_attempted` | Finished attempting, successfully or not |
| `nebraska_group_updates_succeeded` | Now running the current version |
| `nebraska_group_updates_failed` | Attempted and not on the current version |
| `nebraska_group_updates_in_progress` | Updating, still within the update timeout |
| `nebraska_group_updates_timed_out` | Updating, past the group's update timeout |
| `nebraska_group_updates_granted_in_period` | Granted within the current policy period |

These are the same counters Nebraska computes internally to enforce update
policy. Two identities hold, which makes them easy to sanity check:

```
succeeded + failed                      == attempted
attempted + in_progress + timed_out     == granted
```

### Update failures

`nebraska_failed_updates` (gauge, labels `application`, `group`) counts
`UpdateComplete` events that reported a failure, over the last day.

The window matters. Without one the metric counts every failure ever recorded,
so a fleet that has since recovered still reports a permanently elevated number
and the value cannot answer "is the current rollout healthy". The `group` label
matters for the same reason: rollout policy is enforced per group, so an
application-wide total cannot point at the group that is actually failing.

### Syncer health

Exported only when the syncer is enabled (`-enable-syncer`), so that an
installation not running it does not publish metrics pinned at zero that look
like a dead sync loop. All four are labelled `channel` and `arch`.

| Metric | Type | Meaning |
| --- | --- | --- |
| `nebraska_syncer_last_success_timestamp_seconds` | gauge | Unix time of the last successful check |
| `nebraska_syncer_check_failures_total` | counter | Failed checks against upstream |
| `nebraska_syncer_check_duration_seconds` | histogram | How long upstream takes to answer |
| `nebraska_syncer_packages_created_total` | counter | Packages created after finding a new release |

If the syncer's requests to the public Flatcar servers start failing, Nebraska
silently stops receiving new releases. Nothing errors visibly and the dashboard
still looks healthy, so this is the only way to detect it.

Alert on `last_success_timestamp_seconds` rather than on the failure counter. A
Prometheus counter does not exist until it is first incremented, so
`nebraska_syncer_check_failures_total` is absent on a healthy server and a rule
written against it stays silent in exactly the case where the syncer never
manages to run at all.

### Database

| Metric | Type | Labels |
| --- | --- | --- |
| `nebraska_open_db_connections` | gauge | – |
| `nebraska_in_use_db_connections` | gauge | – |
| `nebraska_idle_db_connections` | gauge | – |
| `nebraska_db_query_duration_seconds` | histogram | `operation` |
| `nebraska_db_slow_queries_total` | counter | `operation` |
| `nebraska_db_query_errors_total` | counter | `operation` |

The last three come from query profiling, which is **off by default**:

```bash
NEBRASKA_DB_PROFILING=true
NEBRASKA_DB_SLOW_QUERY_THRESHOLD=250ms   # optional, this is the default
```

Profiling wraps the SQL driver, so it covers every statement Nebraska issues —
reads, writes, the Omaha hot path and anything inside a transaction. Queries
over the threshold are also logged in full:

```
WRN slow query operation=select duration=412ms query="SELECT version, count(*) ..."
```

`operation` is one of `select`, `insert`, `update`, `delete`, `other`. A driver
sees statements rather than callers, so the label reflects the leading SQL
keyword; `WITH` is counted as `select`.

### HTTP

`nebraska_requests_total`, `nebraska_request_duration_seconds`,
`nebraska_request_size_bytes` and `nebraska_response_size_bytes` come from the
Echo Prometheus middleware and are labelled by status code, method and path.

## What to alert on

The dashboard ships these same rules under **Alert rules** on any group page,
where they are evaluated live against the current data, and offers them as a
downloadable `.yaml`.

```yaml
groups:
  - name: nebraska
    rules:
      # A platform failing far worse than the fleet average is the signal that
      # a release is broken on one cloud rather than broken outright.
      - alert: NebraskaPlatformUpdateFailures
        expr: |
          sum by (application, oem) (nebraska_application_instances_per_oem) > 0
          and on (application)
          (sum by (application) (nebraska_group_updates_failed)
           / sum by (application) (nebraska_group_updates_attempted)) > 0.2
        for: 30m
        labels: { severity: critical }

      - alert: NebraskaGroupUpdateFailureRate
        expr: |
          nebraska_group_updates_failed / nebraska_group_updates_attempted > 0.1
        for: 1h
        labels: { severity: warning }

      # Instances that never finished updating within the group's own timeout.
      - alert: NebraskaRolloutStalled
        expr: nebraska_group_updates_timed_out > 0
        for: 24h
        labels: { severity: warning }

      # The syncer has not reached the public Flatcar servers in an hour.
      - alert: NebraskaSyncerStalled
        expr: |
          time() - nebraska_syncer_last_success_timestamp_seconds > 3600
        for: 15m
        labels: { severity: warning }

      - alert: NebraskaDatabaseSlowQueries
        expr: rate(nebraska_db_slow_queries_total[5m]) > 0
        for: 10m
        labels: { severity: warning }
```

Nebraska does not send notifications itself: there is no notifier, no alert
state machine and no silencing. Load these into Prometheus and route them
through Alertmanager.

## Grafana

Rollout progress as a percentage:

```promql
sum(nebraska_group_updates_succeeded) / sum(nebraska_group_total_instances) * 100
```

Update success rate:

```promql
sum(nebraska_group_updates_succeeded) / sum(nebraska_group_updates_attempted) * 100
```

Fleet split by platform:

```promql
sum by (oem) (nebraska_application_instances_per_oem)
```

Slowest query class at the 95th percentile:

```promql
histogram_quantile(0.95,
  sum by (le, operation) (rate(nebraska_db_query_duration_seconds_bucket[5m])))
```

### A note on gauge staleness

Every gauge vector is reset before it is repopulated. This is not cosmetic: a
Prometheus `GaugeVec` only updates the label sets it is given and never retires
one that has stopped appearing. Without the reset, renaming an application
leaves the old name reporting its last value forever, and
`sum(nebraska_application_instances_per_channel)` reads as twice the real fleet
size until the process restarts. The same applies to a version retiring during
a rollout, or a channel being deleted.

If you see a total that exceeds the number of machines you own, check that you
are running a build that includes this behaviour.

## Logging

Nebraska logs to stderr as JSON via zerolog. `-debug` sets the level to debug
and additionally enables Echo's own debug output.

| Logger | Covers |
| --- | --- |
| `nebraska` | server startup, background jobs, retention |
| `syncer` | update checks against the public Flatcar servers |
| `dbreads` | read query internals |
| `dbprofiling` | slow queries, when profiling is enabled |

`-http-log` additionally logs every HTTP request.

### Data retention

Retention pruning is off by default and logs what it removes on each hourly
pass. Use `-retention-dry-run` to see the size of the change before enabling it
for real. A dry run reports once at startup as well as hourly, since it deletes
nothing:

```bash
nebraska -instance-retention 90d -history-retention 30d -retention-dry-run
```

```
INF Retention dry run: rows that would be deleted context=nebraska events=1 instances=12 stats=0 statusHistory=10
```

`instances` counts only the `instance` rows themselves. Their
`instance_application`, `instance_status_history`, `event` and `activity` rows
are removed by `on delete cascade` and are not counted separately.

Durations accept a day suffix (`90d`) as well as anything
`time.ParseDuration` handles (`2160h`). An empty value or `0` disables that
category, so an upgrade never starts deleting an existing deployment's history.
