import type { NavItem } from '../types'

export const navItems: NavItem[] = [
  {
    id: 'dashboard',
    label: 'Dashboard',
    eyebrow: 'Broker overview',
    title: 'Live broker snapshot',
    description: 'Connection state and the latest topic traffic from the configured Mosquitto broker.',
  },
  {
    id: 'topics',
    label: 'Topics',
    eyebrow: 'Traffic',
    title: 'Topic explorer',
    description: 'Inspect incoming topic names and safe payload previews as messages arrive.',
  },
  {
    id: 'logs',
    label: 'Logs',
    eyebrow: 'Operations',
    title: 'Realtime broker logs',
    description: 'Monitor connection transitions and MCM broker operational events as they are ingested.',
  },
  {
    id: 'users',
    label: 'Users',
    eyebrow: 'Identity',
    title: 'MQTT user directory',
    description: 'Provision MQTT users, toggle account status, reset credentials, and remove stale accounts.',
  },
  {
    id: 'acls',
    label: 'ACLs',
    eyebrow: 'Authorization',
    title: 'ACL policy workspace',
    description: 'Topic permissions, policy reviews, and audit-safe change workflows.',
  },
  {
    id: 'deploy',
    label: 'Deploy',
    eyebrow: 'Operations',
    title: 'Mosquitto configuration deploy',
    description: 'Preview, apply, and track configuration changes to the Mosquitto broker.',
  },
  {
    id: 'security',
    label: 'Security',
    eyebrow: 'Audit',
    title: 'Recent security events',
    description: 'Review failed admin logins, disabled-user login attempts, protected API failures, and ACL API audit hooks.',
  },
  {
    id: 'audit',
    label: 'Audit',
    eyebrow: 'Security',
    title: 'Administrative audit log',
    description: 'Review recent administrative changes, actors, affected resources, and outcomes.',
  },
  {
    id: 'settings',
    label: 'Settings',
    eyebrow: 'System',
    title: 'Platform settings placeholder',
    description: 'Global defaults, integration configuration, and broker-level safety controls.',
  },
]
