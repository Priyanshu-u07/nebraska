#!/usr/bin/env python3
"""
Drive the built-in "Sample application" fleet through the *real* Omaha update
lifecycle, the same way seed-fleet.py does for Flatcar.

The 12 instances that ship in backend/pkg/api/db/sample_data.sql are inserted
straight into the database: they have no events, no status history, and an
empty oem, so every platform-aware view reports them as "unknown". This
re-registers them over the Omaha endpoint so their status, history and platform
are all genuinely produced by Nebraska.

Where seed-fleet.py exercises one big single-group rollout, this exercises the
dimensions that one cannot: four groups, three channels, and three different
target versions at once. That is what makes the group label on
nebraska_failed_updates and the per-group rollout gauges observable.

Usage:  python3 scripts/seed-sample-app.py [--url http://localhost:8000]
"""

import argparse
import json
import urllib.error
import urllib.request

APP_ID = "b6458005-8f40-4627-b33b-be70a718c48e"

# Event types / results, mirroring backend/pkg/api/types/event.go
EV_COMPLETE, EV_DL_STARTED, EV_DL_FINISHED = 3, 13, 14
RES_FAILED, RES_SUCCESS, RES_SUCCESS_REBOOT = 0, 1, 2

# The sample groups route by track, and their track is the group's own UUID.
#
# name, group id (= track), old version, target version, keep policy as-is
GROUPS = [
    ("Prod EC2 us-east-1", "7074264a-2070-4b84-96ed-8a269dba5021", "1.0.1", "1.0.3", False),
    ("Prod EC2 us-west-2", "bcaa68bc-5f82-11e5-9d70-feff819cdc9f", "1.0.2", "1.0.3", False),
    ("Qa-Dev",             "b110813a-5f82-11e5-9d70-feff819cdc9f", "1.0.3", "1.0.4", False),
    # Left exactly as the sample data ships it: safe mode on, 2 updates per
    # period. With a single instance that is enough to grant one update, and
    # the failure then trips safe mode, which is the point of the group.
    ("Failing Qa-Dev",     "cccccccc-5f82-11e5-9d70-feff819cdc9f", "1.0.4", "1.0.5", True),
]

# group id -> [(instance suffix, oem, outcome)]
# outcome: "ok" | "fail" | "downloading"
FLEET = {
    "7074264a-2070-4b84-96ed-8a269dba5021": [
        ("1", "ami", "ok"), ("2", "ami", "ok"),
        ("3", "ami", "ok"), ("4", "azure", "fail"),
    ],
    "bcaa68bc-5f82-11e5-9d70-feff819cdc9f": [
        ("5", "ami", "ok"), ("6", "ami", "ok"), ("7", "gce", "ok"),
    ],
    "b110813a-5f82-11e5-9d70-feff819cdc9f": [
        ("8", "gce", "ok"), ("9", "vmware", "fail"),
        ("10", "packet", "downloading"), ("11", "", "ok"),
    ],
    "cccccccc-5f82-11e5-9d70-feff819cdc9f": [
        ("12", "azure", "fail"),
    ],
}

ALEPH = {"azure": "3033.2.4", "ami": "3510.2.0", "gce": "3602.2.3",
         "vmware": "3815.2.1", "packet": "3033.2.4", "": ""}

PING = """<?xml version="1.0" encoding="UTF-8"?>
<request protocol="3.0" installsource="scheduler">
  <os platform="Chrome OS" version="Indy" sp="{version}_x86_64"></os>
  <app appid="{app}" version="{version}" track="{track}" machineid="{mid}"
       oem="{oem}" oemversion="2.5.0" alephversion="{aleph}">
    <updatecheck></updatecheck><ping r="1"></ping>
  </app>
</request>"""

EVENT = """<?xml version="1.0" encoding="UTF-8"?>
<request protocol="3.0" installsource="scheduler">
  <os platform="Chrome OS" version="Indy" sp="{version}_x86_64"></os>
  <app appid="{app}" version="{version}" track="{track}" machineid="{mid}"
       oem="{oem}" alephversion="{aleph}">
    <event eventtype="{etype}" eventresult="{eresult}"
           previousversion="{prev}"></event>
  </app>
</request>"""


def omaha(url, body):
    req = urllib.request.Request(url, data=body.encode(),
                                 headers={"Content-Type": "text/xml"})
    with urllib.request.urlopen(req, timeout=10) as r:
        return r.read().decode()


def api(url, method="GET", payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            body = r.read().decode()
            return json.loads(body) if body else None
    except urllib.error.HTTPError as e:
        print(f"  ! {method} {url} -> {e.code} {e.read().decode()[:200]}")
        raise


def relax_policy(base, group_id):
    """Allow the whole group to update at once.

    The sample groups ship with policy_max_updates_per_period = 2 and safe mode
    on. With more instances than that, Nebraska correctly puts the rest on hold
    (status 8) and never grants them an update, so no rollout ever happens and
    every platform reports a 0% failure rate. Raising the limit is what lets
    the lifecycle actually run.
    """
    group = api(f"{base}/api/apps/{APP_ID}/groups/{group_id}")
    api(f"{base}/api/apps/{APP_ID}/groups/{group_id}", "PUT", {
        "name": group["name"],
        "description": group.get("description") or "",
        "application_id": APP_ID,
        "channel_id": group["channel_id"],
        "policy_updates_enabled": True,
        "policy_safe_mode": False,
        "policy_office_hours": group["policy_office_hours"],
        "policy_timezone": group["policy_timezone"],
        "policy_period_interval": group["policy_period_interval"],
        "policy_max_updates_per_period": 999999,
        "policy_update_timeout": group["policy_update_timeout"],
        "track": group["track"],
    })


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://localhost:8000")
    args = parser.parse_args()
    base = args.url.rstrip("/")
    endpoint = f"{base}/v1/update/"

    print("Preparing groups:")
    for name, gid, _old, target, keep_policy in GROUPS:
        if keep_policy:
            print(f"  {name:20} target {target}  (policy left as shipped)")
            continue
        relax_policy(base, gid)
        print(f"  {name:20} target {target}  (unlimited parallel updates)")

    print("\nDriving instances through the update lifecycle:")
    totals = {"ok": 0, "fail": 0, "downloading": 0}

    for name, gid, old, target, _keep in GROUPS:
        counts = {"ok": 0, "fail": 0, "downloading": 0}

        for suffix, oem, outcome in FLEET[gid]:
            mid = f"instance{suffix}"
            common = dict(app=APP_ID, mid=mid, oem=oem,
                          aleph=ALEPH[oem], track=gid)

            # 1. check in on the old version -> Nebraska grants the update
            omaha(endpoint, PING.format(version=old, **common))

            # 2. download started
            omaha(endpoint, EVENT.format(version=old, etype=EV_DL_STARTED,
                                         eresult=RES_SUCCESS, prev="", **common))
            if outcome == "downloading":
                counts[outcome] += 1
                totals[outcome] += 1
                continue

            # 3. download finished
            omaha(endpoint, EVENT.format(version=old, etype=EV_DL_FINISHED,
                                         eresult=RES_SUCCESS, prev="", **common))

            if outcome == "fail":
                omaha(endpoint, EVENT.format(version=old, etype=EV_COMPLETE,
                                             eresult=RES_FAILED, prev=old, **common))
            else:
                omaha(endpoint, EVENT.format(version=target, etype=EV_COMPLETE,
                                             eresult=RES_SUCCESS_REBOOT, prev=old,
                                             **common))
                omaha(endpoint, PING.format(version=target, **common))

            counts[outcome] += 1
            totals[outcome] += 1

        total = sum(counts.values())
        rate = counts["fail"] * 100.0 / total if total else 0.0
        print(f"  {name:20} {total:2} instances  {counts['fail']} failed "
              f"({rate:5.1f}%)  {old} -> {target}")

    n = sum(totals.values())
    print(f"\n{n} instances: {totals['ok']} complete, {totals['fail']} failed, "
          f"{totals['downloading']} downloading")


if __name__ == "__main__":
    main()
