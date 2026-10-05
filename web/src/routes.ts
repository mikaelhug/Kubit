export const sectionList = [
  ['overview', 'Overview'], ['nodes', 'Nodes'], ['workloads', 'Workloads'], ['network', 'Network'],
  ['storage', 'Storage'], ['addons', 'Add-ons'], ['backups', 'Backups'], ['config', 'Config'],
] as const
export type Section = typeof sectionList[number][0]
