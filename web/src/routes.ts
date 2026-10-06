export const sectionList = [
  ['overview', 'Overview'], ['changes', 'Changes'], ['nodes', 'Nodes'], ['workloads', 'Workloads'], ['network', 'Network'],
  ['storage', 'Storage'], ['addons', 'Add-ons'], ['secrets', 'Secrets'], ['backups', 'Backups'], ['repository', 'Repository'],
] as const
export const pendingSections: readonly string[] = ['changes', 'secrets', 'repository']
export type Section = typeof sectionList[number][0]
