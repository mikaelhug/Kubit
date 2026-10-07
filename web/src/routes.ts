export const sectionList = [
  ['overview', 'Overview'], ['changes', 'Changes'], ['nodes', 'Nodes'], ['workloads', 'Workloads'], ['network', 'Network'],
  ['storage', 'Storage'], ['addons', 'Add-ons'], ['secrets', 'Secrets'], ['backups', 'Backups'], ['settings', 'Settings'],
] as const
export const pendingSections: readonly string[] = ['changes', 'secrets', 'settings']
export type Section = typeof sectionList[number][0]
