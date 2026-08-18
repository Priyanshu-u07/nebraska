import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Divider from '@mui/material/Divider';
import LinearProgress from '@mui/material/LinearProgress';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { Cell, Pie, PieChart, Tooltip as ReTooltip } from 'recharts';

import { Group } from '../../api/apiDataTypes';
import { platformColor, platformLabel, UNKNOWN_OEM } from '../../utils/platforms';
import { PlatformData, PlatformStatusRow } from './usePlatformData';

/**
 * Platform (OEM) distribution for a group, cross-tabulated against update
 * status so a platform-specific regression is visible from the group page.
 * Aggregated server-side by /oem_breakdown.
 */

// Contrast against the foreground is at least 4.5:1 for every band, which is
// what WCAG AA requires at these sizes. The obvious mid-band orange (#EF6C00)
// only reaches 3.08:1 against white.
function rateColors(rate: number) {
  if (rate >= 20) return { bg: '#C62828', fg: '#fff' };
  if (rate >= 10) return { bg: '#BF360C', fg: '#fff' };
  if (rate >= 3) return { bg: '#F9A825', fg: 'rgba(0,0,0,0.87)' };
  return { bg: '#E8F5E9', fg: '#1B5E20' };
}

function DistributionBar({ row }: { row: PlatformStatusRow }) {
  const segments = [
    { key: 'complete', value: row.complete, color: '#2E7D32', label: 'On target version' },
    { key: 'inProgress', value: row.inProgress, color: '#1976D2', label: 'Updating' },
    { key: 'failed', value: row.failed, color: '#C62828', label: 'Failed / stalled' },
  ].filter(s => s.value > 0);

  return (
    <Box sx={{ display: 'flex', height: 12, borderRadius: 1, overflow: 'hidden', width: '100%' }}>
      {segments.map(s => (
        <Tooltip key={s.key} title={`${s.label}: ${s.value}`} arrow>
          {/* role="img" because Tooltip hangs an aria-label off this element,
              and an aria-label on a div with no role is ignored by screen
              readers (and flagged by axe). The segment is a graphic with a
              text alternative, which is what role="img" describes. */}
          <Box
            role="img"
            sx={{
              width: `${(s.value / row.total) * 100}%`,
              bgcolor: s.color,
              '&:hover': { filter: 'brightness(1.25)' },
            }}
          />
        </Tooltip>
      ))}
    </Box>
  );
}

export interface GroupPlatformPanelProps {
  data: PlatformData;
  group: Group | null;
}

export default function GroupPlatformPanel({ data, group }: GroupPlatformPanelProps) {
  const { t } = useTranslation();

  const targetVersion = group?.channel?.package?.version || '';
  const { breakdown, matrix, total: instanceCount, failureRate: fleetRate, worst } = data;

  // Memoized so the chart keeps a stable data reference: a fresh array on every
  // render makes recharts restart its entry animation and the donut never
  // settles at full size.
  const pieData = React.useMemo(
    () =>
      breakdown.map(entry => ({
        name: platformLabel(entry.oem),
        value: entry.instances,
        oem: entry.oem,
      })),
    [breakdown]
  );

  if (data.loading) {
    return <LinearProgress />;
  }
  if (data.total === 0) {
    return null;
  }

  const anomaly = !!worst && worst.failureRate >= 10 && worst.failureRate > fleetRate * 1.5;

  return (
    <Box>
      <Typography
        sx={{ fontSize: 18, fontWeight: 700, color: 'text.secondary', mb: 0.5 }}
        component="h3"
      >
        {t('groups|platform_distribution')}
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        {t('groups|platform_distribution_desc')}
      </Typography>

      {anomaly && (
        <Alert severity="error" sx={{ mb: 2 }}>
          <strong>{worst.label}</strong> is failing at{' '}
          <strong>{worst.failureRate.toFixed(1)}%</strong> against a group average of{' '}
          {fleetRate.toFixed(1)}%. {worst.failed} of {worst.total} instances reported an error
          updating to {targetVersion || 'the target version'}.
        </Alert>
      )}

      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 3, alignItems: 'center' }}>
        <Box sx={{ width: 200, height: 200, flexShrink: 0, flexGrow: 0, flexBasis: 200 }}>
          <PieChart width={200} height={200}>
            <Pie
              data={pieData}
              dataKey="value"
              nameKey="name"
              cx={100}
              cy={100}
              innerRadius={56}
              outerRadius={92}
              paddingAngle={2}
              stroke="none"
              isAnimationActive={false}
            >
              {pieData.map(entry => (
                <Cell key={entry.oem} fill={platformColor(entry.oem)} />
              ))}
            </Pie>
            <ReTooltip formatter={(value, name) => [`${value} instances`, name]} />
          </PieChart>
        </Box>

        <Box sx={{ flex: 1, minWidth: 420 }}>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('groups|platform')}</TableCell>
                <TableCell align="right">{t('groups|instances')}</TableCell>
                <TableCell align="right">{t('groups|share')}</TableCell>
                <TableCell align="right">{t('groups|failed')}</TableCell>
                <TableCell align="center">{t('groups|failure_rate')}</TableCell>
                <TableCell sx={{ width: '22%' }}>{t('groups|status')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {matrix.map(row => {
                const c = rateColors(row.failureRate);
                return (
                  <TableRow key={row.oem} hover>
                    <TableCell>
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                        <Box
                          sx={{
                            width: 10,
                            height: 10,
                            borderRadius: '50%',
                            bgcolor: platformColor(row.oem),
                            flexShrink: 0,
                          }}
                        />
                        <Typography
                          variant="body2"
                          sx={{
                            color: row.oem !== UNKNOWN_OEM ? 'text.primary' : 'text.disabled',
                            fontStyle: row.oem !== UNKNOWN_OEM ? 'normal' : 'italic',
                          }}
                        >
                          {row.label}
                        </Typography>
                      </Box>
                    </TableCell>
                    <TableCell align="right">
                      <Typography variant="body2" fontWeight={600}>
                        {row.total}
                      </Typography>
                    </TableCell>
                    <TableCell align="right">
                      <Typography variant="body2" color="text.secondary">
                        {row.percentage.toFixed(1)}%
                      </Typography>
                    </TableCell>
                    <TableCell align="right">
                      <Typography
                        variant="body2"
                        color={row.failed > 0 ? 'error.main' : 'text.secondary'}
                        fontWeight={row.failureRate >= 10 ? 700 : 400}
                      >
                        {row.failed}
                      </Typography>
                    </TableCell>
                    <TableCell align="center">
                      <Box
                        sx={{
                          display: 'inline-block',
                          px: 1,
                          py: 0.25,
                          borderRadius: 1,
                          minWidth: 50,
                          bgcolor: c.bg,
                          color: c.fg,
                          fontSize: 12,
                          fontWeight: 700,
                        }}
                      >
                        {row.failureRate.toFixed(1)}%
                      </Box>
                    </TableCell>
                    <TableCell>
                      <DistributionBar row={row} />
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
          <Typography variant="caption" color="text.secondary" sx={{ mt: 1, display: 'block' }}>
            {instanceCount} instances across {breakdown.length} platforms
            {targetVersion ? ` · target ${targetVersion}` : ''}
          </Typography>
        </Box>
      </Box>
      <Divider sx={{ mt: 2 }} />
    </Box>
  );
}
