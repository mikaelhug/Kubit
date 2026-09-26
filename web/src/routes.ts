export const sectionList = [
  ['overview', 'Overview'], ['nodes', 'Nodes'], ['workloads', 'Workloads'], ['network', 'Network'],
  ['storage', 'Storage'], ['addons', 'Add-ons'], ['backups', 'Backups'], ['lifecycle', 'Lifecycle'], ['settings', 'Settings'],
] as const
export type Section = typeof sectionList[number][0]

export const settingsPages = [
  ['general', 'General'], ['discovery', 'Discovery'], ['alerts', 'Alerts'], ['offsite', 'Off-site'],
  ['accounts', 'Accounts'], ['sso', 'Single sign-on'], ['backup', 'Backup'],
] as const
export type SettingsPage = typeof settingsPages[number][0]
