export const sectionList = [
  ['overview', 'Overview'], ['changes', 'Changes'], ['nodes', 'Nodes'], ['workloads', 'Workloads'], ['network', 'Network'],
  ['storage', 'Storage'], ['addons', 'Add-ons'], ['secrets', 'Secrets'], ['backups', 'Backups'], ['settings', 'Settings'],
] as const
export const pendingSections: readonly string[] = ['changes', 'secrets', 'settings']
export type Section = typeof sectionList[number][0]

const viewParams = ['scope', 'view']

export function sameView(cluster: string, path: string, query: Record<string, string>) {
  const section = path.startsWith('/clusters/') ? path.split('/')[3] || 'overview' : 'overview'
  const q = new URLSearchParams(viewParams.filter((k) => query[k]).map((k) => [k, query[k]])).toString()
  return `/clusters/${cluster}/${section}${q ? `?${q}` : ''}`
}
