import { api } from './api'
import { useLive } from './useLive'

export function useConfigStatus(name: string) {
  return useLive(() => api.configStatus(name), [name], [[name, 'plan']], { onError: 'null' }).data
}

export const behindText = (n: number) => `${n} node${n === 1 ? ' is' : 's are'} behind the declaration.`
