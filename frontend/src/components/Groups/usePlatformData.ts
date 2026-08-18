import React from 'react';

import API from '../../api/API';
import { Instance, OEMBreakdownEntry } from '../../api/apiDataTypes';
import { instanceOEM, platformLabel } from '../../utils/platforms';

/**
 * Platform data for a group. The distribution comes from /oem_breakdown; the
 * status cross-tab is folded from the instance list, since there is no
 * status-by-OEM endpoint yet.
 */

/** Mirrors backend/pkg/api/types/instance.go. */
export const InstanceStatus = {
  Undefined: 1,
  UpdateGranted: 2,
  Error: 3,
  Complete: 4,
  Installed: 5,
  Downloaded: 6,
  Downloading: 7,
  OnHold: 8,
} as const;

export interface PlatformStatusRow {
  oem: string;
  label: string;
  total: number;
  percentage: number;
  complete: number;
  inProgress: number;
  failed: number;
  /** Reported nothing yet, or explicitly on hold. Not counted as a success. */
  unreported: number;
  failureRate: number;
}

export interface PlatformData {
  loading: boolean;
  instances: Instance[];
  breakdown: OEMBreakdownEntry[];
  matrix: PlatformStatusRow[];
  total: number;
  failed: number;
  inProgress: number;
  complete: number;
  failureRate: number;
  worst: PlatformStatusRow | null;
  unknownShare: number;
}

const EMPTY: PlatformData = {
  loading: true,
  instances: [],
  breakdown: [],
  matrix: [],
  total: 0,
  failed: 0,
  inProgress: 0,
  complete: 0,
  failureRate: 0,
  worst: null,
  unknownShare: 0,
};

/**
 * Cross-tabulate update status by platform. A null status is counted separately
 * rather than as a success, which would understate the failure rate.
 */
function statusMatrixFromInstances(instances: Instance[]): PlatformStatusRow[] {
  const rows = new Map<string, PlatformStatusRow>();

  instances.forEach(instance => {
    const oem = instanceOEM(instance);
    let row = rows.get(oem);
    if (!row) {
      row = {
        oem,
        label: platformLabel(oem),
        total: 0,
        percentage: 0,
        complete: 0,
        inProgress: 0,
        failed: 0,
        unreported: 0,
        failureRate: 0,
      };
      rows.set(oem, row);
    }

    row.total += 1;

    const status = instance.application?.status;
    switch (status) {
      case InstanceStatus.Error:
        row.failed += 1;
        break;
      case InstanceStatus.UpdateGranted:
      case InstanceStatus.Installed:
      case InstanceStatus.Downloaded:
      case InstanceStatus.Downloading:
        row.inProgress += 1;
        break;
      case InstanceStatus.Complete:
        row.complete += 1;
        break;
      default:
        // null, Undefined or OnHold
        row.unreported += 1;
    }
  });

  const total = instances.length || 1;
  return Array.from(rows.values())
    .map(row => ({
      ...row,
      percentage: (row.total / total) * 100,
      failureRate: row.total ? (row.failed / row.total) * 100 : 0,
    }))
    .sort((a, b) => b.total - a.total);
}

export function usePlatformData(appID: string, groupID: string, duration: string): PlatformData {
  const [breakdown, setBreakdown] = React.useState<OEMBreakdownEntry[] | null>(null);
  const [instances, setInstances] = React.useState<Instance[] | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    setBreakdown(null);
    setInstances(null);

    API.getGroupOEMBreakdown(appID, groupID)
      .then(entries => !cancelled && setBreakdown(entries))
      .catch(err => {
        console.error('Error getting OEM breakdown for group', groupID, '\nError:', err);
        if (!cancelled) {
          setBreakdown([]);
        }
      });

    API.getInstances(appID, groupID, { duration, status: 0, perpage: 1000, page: 1 })
      .then(result => !cancelled && setInstances(result.instances || []))
      .catch(err => {
        console.error('Error getting instances for group', groupID, '\nError:', err);
        if (!cancelled) {
          setInstances([]);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [appID, groupID, duration]);

  return React.useMemo(() => {
    if (breakdown === null || instances === null) {
      return EMPTY;
    }
    if (breakdown.length === 0) {
      return { ...EMPTY, loading: false };
    }

    const matrix = statusMatrixFromInstances(instances);
    const total = instances.length;
    const failed = matrix.reduce((n, r) => n + r.failed, 0);
    const inProgress = matrix.reduce((n, r) => n + r.inProgress, 0);
    const complete = matrix.reduce((n, r) => n + r.complete, 0);
    const worst = [...matrix].sort((a, b) => b.failureRate - a.failureRate)[0] || null;
    const unknown = breakdown.find(entry => entry.oem === 'unknown');

    return {
      loading: false,
      instances,
      breakdown,
      matrix,
      total,
      failed,
      inProgress,
      complete,
      failureRate: total ? (failed / total) * 100 : 0,
      worst,
      unknownShare: unknown ? unknown.percentage : 0,
    };
  }, [breakdown, instances]);
}
