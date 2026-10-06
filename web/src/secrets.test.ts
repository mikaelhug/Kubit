import { describe, expect, it } from 'vitest'
import { dockerConfig, nameProblem, namespaceProblem, patchOf, rowsOf } from './secrets'

describe('secret rows', () => {
  it('never re-encodes a binary value', () => {
    const blob = btoa(String.fromCharCode(0xff, 0xfe, 0x00))
    const before = rowsOf({ kind: 'Secret' }, [{ path: ['data', 'blob'], value: blob }])
    expect(patchOf(before, [{ ...before[0], key: 'raw' }]).set).toEqual([{ path: ['data', 'raw'], value: blob }])
  })
})

describe('new secrets', () => {
  it('follows Kubernetes rules: a subdomain for names, a label for namespaces', () => {
    expect(nameProblem('db.prod-1')).toBe('')
    expect(nameProblem('Db')).not.toBe('')
    expect(namespaceProblem('a.b')).not.toBe('')
  })

  it('writes the registry auth field kubelet reads', () => {
    const cfg = JSON.parse(dockerConfig('ghcr.io', 'me', 'pw', ''))
    expect(cfg.auths['ghcr.io']).toEqual({ username: 'me', password: 'pw', auth: btoa('me:pw') })
  })
})
