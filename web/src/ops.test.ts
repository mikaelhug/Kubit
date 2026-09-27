import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type Operation } from './api'
import { applyStepEvent, mergeLog, mergeOp, opEvents, operations, opsFor, parseLog, pushEvent, reloadLogs, runningCount, runningFor, viewOp } from './ops'

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

describe('mergeLog', () => {
  const live = (message: string, node?: string, step = 'install') => ({ time: 't', level: 'info' as const, step, node, message })
  const stored = parseLog('10:00:00 [install] one\n10:00:01 [install] n1: two\n10:00:02 [install] three: with colon\nerror: boom')

  it('drops live lines the stored log already holds', () => {
    const merged = mergeLog(stored.slice(0, 3), [live('two', 'n1'), live('three: with colon'), live('four')])
    expect(merged.map((e) => e.message)).toEqual(['one', 'two', 'with colon', 'four'])
  })

  it('matches a failure line with its live error event', () => {
    const merged = mergeLog(stored, [live('boom', undefined, 'node.add')])
    expect(merged).toHaveLength(4)
  })

  it('appends everything without overlap', () => {
    expect(mergeLog(stored.slice(0, 1), [live('five')]).map((e) => e.message)).toEqual(['one', 'five'])
    expect(mergeLog([], [live('x')])).toHaveLength(1)
  })

  it('matches multi-line messages line by line', () => {
    const log = parseLog('10:00:00 [install] n1: first\nsecond\nerror: failed\n  detail')
    expect(log).toHaveLength(4)
    const merged = mergeLog(log, [live('first\nsecond', 'n1'), live('failed\n  detail', undefined, 'node.add'), live('next')])
    expect(merged.map((e) => e.message)).toEqual(['first', 'second', 'error: failed', '  detail', 'next'])
  })

  it('keeps a multi-line event whole when only its first line overlaps', () => {
    const merged = mergeLog(parseLog('10:00:00 [install] a'), [live('a\nb')])
    expect(merged.map((e) => e.message)).toEqual(['a', 'a\nb'])
  })

  it('parses and matches lines without a step', () => {
    const log = parseLog('10:00:00 [] n1: plain\n10:00:01 [] done')
    expect(log.map((e) => [e.step, e.node, e.message])).toEqual([['', 'n1', 'plain'], ['', undefined, 'done']])
    expect(mergeLog(log, [live('plain', 'n1', ''), live('done', undefined, ''), live('after', undefined, '')])).toHaveLength(3)
  })
})

describe('mergeOp', () => {
  const steps = (...status: Operation['steps'][number]['status'][]) => status.map((s, i) => ({ id: `s${i}`, title: `s${i}`, status: s }))

  it('keeps a live status that is ahead of a fetched row', () => {
    const cur = { ...op(4, 'a', 'failed'), steps: steps('done', 'failed'), finishedAt: 't2' }
    expect(mergeOp(cur, { ...op(4, 'a', 'running'), steps: steps('done', 'running') })).toBe(cur)
    expect(mergeOp({ ...op(4, 'a', 'running'), steps: steps('done', 'done') }, { ...op(4, 'a', 'running'), steps: steps('running', 'pending') }).steps).toEqual(steps('done', 'done'))
  })

  it('takes rows that are as far or further and drops the log', () => {
    const cur = { ...op(4, 'a', 'running'), steps: steps('running'), artifact: 1 }
    const next = mergeOp(cur, { ...op(4, 'a', 'done'), steps: steps('done'), log: 'x' })
    expect(next).toMatchObject({ status: 'done', artifact: 1, steps: steps('done') })
    expect(next).not.toHaveProperty('log')
    expect(mergeOp(undefined, { ...op(5, 'a', 'running'), steps: undefined as never }).steps).toEqual([])
  })
})

describe('reloadLogs', () => {
  it('replaces a gapped log without duplicating lines', async () => {
    const line = (n: number) => `10:00:0${n} [install] l${n}`
    const fetch = vi.spyOn(api, 'operation').mockResolvedValue({ ...op(2, 'a', 'running'), log: [1, 2].map(line).join('\n') })
    const stop = viewOp(2)
    await vi.waitFor(() => expect(opEvents.value.get(2)).toHaveLength(2))
    pushEvent(2, { time: 't', level: 'info', step: 'install', message: 'l3' })
    fetch.mockResolvedValue({ ...op(2, 'a', 'running'), log: [1, 2, 3, 4, 5].map(line).join('\n') })
    const done = reloadLogs()
    pushEvent(2, { time: 't', level: 'info', step: 'install', message: 'l5' })
    await done
    expect(opEvents.value.get(2)!.map((e) => e.message)).toEqual(['l1', 'l2', 'l3', 'l4', 'l5'])
    stop()
    fetch.mockRestore()
  })
})
