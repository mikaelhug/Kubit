import { beforeEach, describe, expect, it } from 'vitest'
import type { Operation } from './api'
import { applyStepEvent, operations, opsFor, runningCount, runningFor } from './store'

const op = (id: number, cluster: string, status: Operation['status']): Operation => ({ id, cluster, kind: 'node.add', status, startedAt: '2026-01-01T00:00:00Z', steps: [] })

beforeEach(() => {
  operations.value = new Map([op(1, 'a', 'done'), op(2, 'a', 'running'), op(3, 'b', 'running')].map((o) => [o.id, o]))
})

describe('operation indexes', () => {
  it('group operations per cluster', () => {
    expect(opsFor('a').map((o) => o.id)).toEqual([1, 2])
    expect(opsFor('none')).toEqual([])
    expect(runningFor('a').map((o) => o.id)).toEqual([2])
    expect(runningCount.value).toBe(2)
  })
})

describe('applyStepEvent', () => {
  it('adds a running step', () => {
    applyStepEvent(2, { time: 't1', level: 'info', step: 'install', message: 'x' })
    expect(operations.value.get(2)!.steps).toEqual([{ id: 'install', title: 'install', status: 'running', startedAt: 't1' }])
  })

  it('leaves the store untouched when nothing changes', () => {
    applyStepEvent(2, { time: 't1', level: 'info', step: 'install', message: 'x' })
    const before = operations.value
    applyStepEvent(2, { time: 't2', level: 'info', step: 'install', message: 'y' })
    applyStepEvent(2, { time: 't3', level: 'info', step: '', message: 'z' })
    applyStepEvent(2, { time: 't4', kind: 'steps', level: 'info', step: '', message: '', steps: [{ id: 'install', title: 'Install', status: 'pending' }] })
    applyStepEvent(99, { time: 't5', level: 'info', step: 'install', message: 'w' })
    expect(operations.value).toBe(before)
  })

  it('finishes a step once', () => {
    applyStepEvent(2, { time: 't1', kind: 'step', level: 'info', step: 'install', message: '', status: 'done' })
    const before = operations.value
    applyStepEvent(2, { time: 't1', kind: 'step', level: 'info', step: 'install', message: '', status: 'done' })
    expect(operations.value).toBe(before)
    expect(before.get(2)!.steps[0]).toMatchObject({ status: 'done', finishedAt: 't1' })
  })
})
