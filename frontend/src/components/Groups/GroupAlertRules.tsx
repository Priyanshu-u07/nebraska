import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Chip from '@mui/material/Chip';
import Collapse from '@mui/material/Collapse';
import Divider from '@mui/material/Divider';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import React from 'react';
import { useTranslation } from 'react-i18next';

import { PlatformData } from './usePlatformData';

/**
 * Alert rules over the OEM/rollout metrics proposed in flatcar/Flatcar#2239.
 *
 * Nebraska deliberately does not become an alerting system: there is no
 * notifier, no alert state machine and no silencing here. What this panel does
 * is publish the Prometheus rules that go with the new metrics, and evaluate
 * them against the group's current data so an operator can see which of them
 * would be firing right now before wiring anything into Alertmanager.
 */

type Severity = 'critical' | 'warning' | 'info';

interface RuleDef {
  name: string;
  severity: Severity;
  forDuration: string;
  summary: (d: PlatformData) => string;
  expr: string;
  /** Current value of the rule expression, in the unit shown. */
  value: (d: PlatformData) => number;
  threshold: number;
  unit: string;
  /** True when the rule would be firing. */
  firing: (d: PlatformData) => boolean;
}

const RULES: RuleDef[] = [
  {
    name: 'NebraskaPlatformUpdateFailures',
    severity: 'critical',
    forDuration: '30m',
    summary: d =>
      d.worst
        ? `${d.worst.label} failing at ${d.worst.failureRate.toFixed(1)}% (group ${d.failureRate.toFixed(1)}%)`
        : 'No platform data',
    expr: `max by (oem) (
  nebraska_update_failures_total / nebraska_update_attempts_total
) > 0.2`,
    value: d => (d.worst ? d.worst.failureRate : 0),
    threshold: 20,
    unit: '%',
    firing: d => !!d.worst && d.worst.failureRate > 20,
  },
  {
    name: 'NebraskaGroupUpdateFailureRate',
    severity: 'warning',
    forDuration: '1h',
    summary: d => `${d.failed} of ${d.total} instances failed to reach the target version`,
    expr: `sum by (group) (nebraska_update_failures_total)
  / sum by (group) (nebraska_update_attempts_total) > 0.1`,
    value: d => d.failureRate,
    threshold: 10,
    unit: '%',
    firing: d => d.failureRate > 10,
  },
  {
    name: 'NebraskaRolloutStalled',
    severity: 'warning',
    forDuration: '24h',
    summary: d =>
      `${((d.complete / (d.total || 1)) * 100).toFixed(1)}% of the group is on the target version`,
    expr: 'nebraska_rollout_progress_ratio < 0.9',
    value: d => (d.complete / (d.total || 1)) * 100,
    threshold: 90,
    unit: '%',
    firing: d => (d.complete / (d.total || 1)) * 100 < 90,
  },
  {
    name: 'NebraskaUnknownPlatformShare',
    severity: 'info',
    forDuration: '6h',
    summary: d => `${d.unknownShare.toFixed(1)}% of instances report no OEM`,
    expr: `sum(nebraska_application_instances_per_oem{oem="unknown"})
  / sum(nebraska_application_instances_per_oem) > 0.1`,
    value: d => d.unknownShare,
    threshold: 10,
    unit: '%',
    firing: d => d.unknownShare > 10,
  },
];

// Each pair clears 4.5:1, the WCAG AA minimum at this size. #EF6C00, the
// natural choice for "warning", only manages 3.08:1 against white.
const SEVERITY_STYLE: { [k in Severity]: { bg: string; fg: string } } = {
  critical: { bg: '#C62828', fg: '#fff' },
  warning: { bg: '#BF360C', fg: '#fff' },
  info: { bg: '#ECEFF1', fg: '#37474F' },
};

function buildYaml(groupName: string): string {
  const rules = RULES.map(r => {
    const expr = r.expr
      .split('\n')
      .map(line => `          ${line.trim()}`)
      .join('\n');
    return `    - alert: ${r.name}
      expr: |
${expr}
      for: ${r.forDuration}
      labels:
        severity: ${r.severity}
      annotations:
        summary: "${r.name} on {{ $labels.group }}"`;
  }).join('\n');

  return `# Prometheus rules for Nebraska update reporting
# Generated from the group "${groupName}"
groups:
  - name: nebraska-updates
    rules:
${rules}`;
}

export interface GroupAlertRulesProps {
  data: PlatformData;
  groupName: string;
}

export default function GroupAlertRules({ data, groupName }: GroupAlertRulesProps) {
  const { t } = useTranslation();
  const [showYaml, setShowYaml] = React.useState(false);
  const [copied, setCopied] = React.useState(false);

  if (data.loading || data.total === 0) {
    return null;
  }

  const yaml = buildYaml(groupName);
  const firingCount = RULES.filter(r => r.firing(data)).length;

  function copyYaml() {
    navigator.clipboard?.writeText(yaml).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 2000);
      },
      () => undefined
    );
  }

  function downloadYaml() {
    const slug = groupName
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-|-$/g, '');
    const blob = new Blob([yaml], { type: 'application/yaml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `nebraska-alerts-${slug || 'group'}.yaml`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  return (
    <Box>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          flexWrap: 'wrap',
          gap: 1,
          mb: 0.5,
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
          <Typography
            sx={{ fontSize: 18, fontWeight: 700, color: 'text.secondary' }}
            component="h3"
          >
            {t('groups|alert_rules')}
          </Typography>
          {firingCount > 0 && (
            <Chip
              label={`${firingCount} firing`}
              size="small"
              sx={{ bgcolor: '#C62828', color: '#fff', fontWeight: 700, height: 22 }}
            />
          )}
        </Box>
        <Box sx={{ display: 'flex', gap: 1 }}>
          <Button size="small" variant="outlined" onClick={() => setShowYaml(v => !v)}>
            {showYaml ? t('frequent|hide') : t('groups|view_yaml')}
          </Button>
          <Button size="small" variant="outlined" onClick={copyYaml}>
            {copied ? t('frequent|copied') : t('frequent|copy')}
          </Button>
          <Button size="small" variant="contained" onClick={downloadYaml}>
            {t('groups|export_rules')}
          </Button>
        </Box>
      </Box>

      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        {t('groups|alert_rules_desc')}
      </Typography>

      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>{t('groups|alert')}</TableCell>
            <TableCell>{t('groups|severity')}</TableCell>
            <TableCell align="right">{t('groups|current')}</TableCell>
            <TableCell align="right">{t('groups|threshold')}</TableCell>
            <TableCell align="center">{t('groups|for_duration')}</TableCell>
            <TableCell align="center">{t('groups|state')}</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {RULES.map(rule => {
            const firing = rule.firing(data);
            const sev = SEVERITY_STYLE[rule.severity];
            return (
              <TableRow key={rule.name} hover>
                <TableCell>
                  {/* describeChild so the expression becomes aria-describedby
                      rather than an aria-label. Without it MUI overrides the
                      accessible name of a cell that already has visible text,
                      so a screen reader announces raw PromQL instead of the
                      rule's name. */}
                  <Tooltip title={rule.expr} arrow placement="top-start" describeChild>
                    <Box>
                      <Typography
                        variant="body2"
                        sx={{ fontFamily: 'monospace', fontSize: 12, fontWeight: 600 }}
                      >
                        {rule.name}
                      </Typography>
                      <Typography variant="caption" color="text.secondary">
                        {rule.summary(data)}
                      </Typography>
                    </Box>
                  </Tooltip>
                </TableCell>
                <TableCell>
                  <Box
                    sx={{
                      display: 'inline-block',
                      px: 1,
                      py: 0.25,
                      borderRadius: 1,
                      bgcolor: sev.bg,
                      color: sev.fg,
                      fontSize: 11,
                      fontWeight: 700,
                      textTransform: 'uppercase',
                    }}
                  >
                    {rule.severity}
                  </Box>
                </TableCell>
                <TableCell align="right">
                  <Typography
                    variant="body2"
                    fontWeight={700}
                    color={firing ? 'error.main' : 'text.primary'}
                  >
                    {rule.value(data).toFixed(1)}
                    {rule.unit}
                  </Typography>
                </TableCell>
                <TableCell align="right">
                  <Typography variant="body2" color="text.secondary">
                    {rule.name === 'NebraskaRolloutStalled' ? '< ' : '> '}
                    {rule.threshold}
                    {rule.unit}
                  </Typography>
                </TableCell>
                <TableCell align="center">
                  <Typography variant="body2" color="text.secondary">
                    {rule.forDuration}
                  </Typography>
                </TableCell>
                <TableCell align="center">
                  <Box
                    sx={{
                      display: 'inline-block',
                      px: 1.25,
                      py: 0.25,
                      borderRadius: 1,
                      minWidth: 62,
                      bgcolor: firing ? '#C62828' : '#E8F5E9',
                      color: firing ? '#fff' : '#1B5E20',
                      fontSize: 12,
                      fontWeight: 700,
                    }}
                  >
                    {firing ? t('groups|firing') : t('groups|ok')}
                  </Box>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>

      <Collapse in={showYaml}>
        <Box
          component="pre"
          sx={{
            mt: 2,
            mb: 0,
            p: 2,
            borderRadius: 1,
            bgcolor: '#1E1E1E',
            color: '#D4D4D4',
            fontSize: 11,
            lineHeight: 1.6,
            overflowX: 'auto',
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
          }}
        >
          {yaml}
        </Box>
      </Collapse>

      <Typography variant="caption" color="text.secondary" sx={{ mt: 1.5, display: 'block' }}>
        {t('groups|alert_rules_note')}
      </Typography>
      <Divider sx={{ mt: 2 }} />
    </Box>
  );
}
