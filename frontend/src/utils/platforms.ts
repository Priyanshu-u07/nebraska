import { Instance } from '../api/apiDataTypes';

export const UNKNOWN_OEM = 'unknown';

export const PLATFORM_LABELS: { [key: string]: string } = {
  azure: 'Azure',
  ami: 'AWS (AMI)',
  gce: 'Google Cloud',
  vmware: 'VMware',
  packet: 'Equinix Metal',
  [UNKNOWN_OEM]: 'Unknown',
};

export const PLATFORM_COLORS: { [key: string]: string } = {
  azure: '#0078D4',
  ami: '#FF9900',
  gce: '#34A853',
  vmware: '#607078',
  packet: '#ED2E7E',
  [UNKNOWN_OEM]: '#9E9E9E',
};

export function instanceOEM(instance: Pick<Instance, 'oem'>): string {
  return instance.oem || UNKNOWN_OEM;
}

export function platformLabel(oem: string): string {
  return PLATFORM_LABELS[oem] ?? oem;
}

export function platformColor(oem: string): string {
  return PLATFORM_COLORS[oem] ?? '#78909C';
}
