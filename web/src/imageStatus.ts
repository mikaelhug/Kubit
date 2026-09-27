import { api } from './api'
import { useLive } from './useLive'

export function useImageStatus(name: string, updatedAt: string) {
  return useLive(() => api.imageStatus(name), [name], [], { onError: 'null', refresh: [updatedAt] }).data
}
