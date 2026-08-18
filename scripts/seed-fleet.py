#!/usr/bin/env python3
"""
Seed a realistic multi-platform fleet into a local Nebraska, driving every
instance through the *real* Omaha update lifecycle so statuses are genuine
(not written straight into the database).

It reproduces the scenario the current dashboard cannot show: a rollout that
looks healthy in aggregate while failing badly on one platform.

Flow per instance:
  1. check in on the old version  -> Nebraska grants the update
  2. download started / finished  -> status Downloading / Downloaded
  3. update complete              -> success+reboot, or failure

Usage:  python3 scripts/seed-fleet.py [--url http://localhost:8000]
"""

import argparse
import json
import urllib.error
import urllib.request

APP_ID = "e96281a6-d1af-4bde-9a0a-97b76e56dc57"
OLD_VERSION = "4116.0.0"
NEW_VERSION = "4152.0.0"

# Event types / results, mirroring backend/pkg/api/types/event.go
EV_COMPLETE, EV_DL_STARTED, EV_DL_FINISHED = 3, 13, 14
RES_FAILED, RES_SUCCESS, RES_SUCCESS_REBOOT = 0, 1, 2

# oem -> (instance count, failures, still-downloading)
FLEET = [
    ("azure", 26, 13, 3),   # the platform carrying the regression
    ("ami", 22, 1, 2),
    ("gce", 14, 1, 1),
    ("vmware", 10, 0, 1),
    ("packet", 6, 0, 0),
    ("", 4, 0, 1),          # empty OEM must bucket as "unknown"
]

ALEPH = {"azure": "3033.2.4", "ami": "3510.2.0", "gce": "3602.2.3",
         "vmware": "3815.2.1", "packet": "3033.2.4", "": ""}

PING = """<?xml version="1.0" encoding="UTF-8"?>
<request protocol="3.0" installsource="scheduler">
  <os platform="Chrome OS" version="Indy" sp="{version}_x86_64"></os>
  <app appid="{app}" version="{version}" track="stable" machineid="{mid}"
       oem="{oem}" oemversion="2.5.0" alephversion="{aleph}">
    <updatecheck></updatecheck><ping r="1"></ping>
  </app>
</request>"""

EVENT = """<?xml version="1.0" encoding="UTF-8"?>
<request protocol="3.0" installsource="scheduler">
  <os platform="Chrome OS" version="Indy" sp="{version}_x86_64"></os>
  <app appid="{app}" version="{version}" track="stable" machineid="{mid}"
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


def ensure_package_and_channel(base):
    """Publish NEW_VERSION and point the stable AMD64 channel at it."""
    pkgs = api(f"{base}/api/apps/{APP_ID}/packages?page=1&perpage=100")
    existing = next((p for p in (pkgs.get("packages") or [])
                     if p["version"] == NEW_VERSION), None)
    if existing:
        pkg = existing
        print(f"  package {NEW_VERSION} already present")
    else:
        pkg = api(f"{base}/api/apps/{APP_ID}/packages", "POST", {
            "type": 1,                      # PkgTypeFlatcar
            "arch": 1,                      # AMD64
            "version": NEW_VERSION,
            "url": "https://update.release.flatcar-linux.net/amd64-usr/4152.0.0/",
            "filename": "flatcar_production_update.gz",
            "description": f"Flatcar {NEW_VERSION}",
            "size": "412000000",
            "hash": "aGFzaC1wbGFjZWhvbGRlcg==",
            "application_id": APP_ID,
            "channels_blacklist": [],
        })
        print(f"  published package {NEW_VERSION}")

    groups = api(f"{base}/api/apps/{APP_ID}/groups")
    grp = next(g for g in groups["groups"] if g["track"] == "stable"
               and g["channel"] and g["channel"]["arch"] == 1)
    ch = grp["channel"]

    if ch.get("package_id") != pkg["id"]:
        api(f"{base}/api/apps/{APP_ID}/channels/{ch['id']}", "PUT", {
            "name": ch["name"], "color": ch["color"], "arch": ch["arch"],
            "package_id": pkg["id"], "application_id": APP_ID,
        })
        print(f"  channel '{ch['name']}' now serves {NEW_VERSION}")

    return grp["id"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default="http://localhost:8000")
    args = ap.parse_args()
    base = args.url.rstrip("/")
    endpoint = f"{base}/v1/update/"

    print("Preparing rollout target:")
    ensure_package_and_channel(base)

    print("\nDriving instances through the update lifecycle:")
    totals = {"complete": 0, "failed": 0, "downloading": 0}

    for oem, count, failures, downloading in FLEET:
        name = oem or "nooem"
        aleph = ALEPH[oem]
        for i in range(count):
            mid = f"{name}-{i:03d}"
            common = dict(app=APP_ID, mid=mid, oem=oem, aleph=aleph)

            # 1. check in on the old version -> update granted
            omaha(endpoint, PING.format(version=OLD_VERSION, **common))

            # 2. start downloading
            omaha(endpoint, EVENT.format(version=OLD_VERSION, etype=EV_DL_STARTED,
                                         eresult=RES_SUCCESS, prev=OLD_VERSION,
                                         **common))

            if i < downloading:
                totals["downloading"] += 1
                continue

            omaha(endpoint, EVENT.format(version=OLD_VERSION, etype=EV_DL_FINISHED,
                                         eresult=RES_SUCCESS, prev=OLD_VERSION,
                                         **common))

            if i < downloading + failures:
                # 3a. the update fails on this platform
                omaha(endpoint, EVENT.format(version=OLD_VERSION, etype=EV_COMPLETE,
                                             eresult=RES_FAILED, prev=OLD_VERSION,
                                             **common))
                totals["failed"] += 1
            else:
                # 3b. success, machine reboots onto the new version
                omaha(endpoint, EVENT.format(version=NEW_VERSION, etype=EV_COMPLETE,
                                             eresult=RES_SUCCESS_REBOOT,
                                             prev=OLD_VERSION, **common))
                omaha(endpoint, PING.format(version=NEW_VERSION, **common))
                totals["complete"] += 1

        rate = failures / count * 100
        print(f"  {name:8} {count:3} instances  {failures:2} failed ({rate:4.1f}%)")

    total = sum(totals.values())
    print(f"\n{total} instances: {totals['complete']} complete, "
          f"{totals['failed']} failed, {totals['downloading']} downloading "
          f"({totals['failed'] / total * 100:.1f}% fleet failure rate)")


if __name__ == "__main__":
    main()
