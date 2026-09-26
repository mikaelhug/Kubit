import { describe, expect, it } from 'vitest'
import { levelTone, phaseTone, severityTone, stateTone, toneBg, toneBorder, tonePill, toneText } from './tone'

describe('tone maps', () => {
  it('cover every tone', () => {
    for (const map of [toneText, toneBg, tonePill, toneBorder]) expect(Object.keys(map).sort()).toEqual(['bad', 'good', 'info', 'muted', 'warn'])
  })

  it('map states', () => {
    expect(stateTone('running')).toBe('good')
    expect(stateTone('failed')).toBe('bad')
    expect(stateTone('deploying')).toBe('warn')
    expect(stateTone('cancelled')).toBe('muted')
    expect(stateTone('something new')).toBe('muted')
  })

  it('map pod and volume phases', () => {
    expect(phaseTone('Running')).toBe('good')
    expect(phaseTone('Bound')).toBe('good')
    expect(phaseTone('Pending')).toBe('warn')
    expect(phaseTone('Available')).toBe('info')
    expect(phaseTone('Failed')).toBe('bad')
    expect(phaseTone('CrashLoopBackOff')).toBe('bad')
  })

  it('map severities and levels', () => {
    expect(severityTone('critical')).toBe('bad')
    expect(severityTone('warn')).toBe('warn')
    expect(severityTone('info')).toBe('good')
    expect(levelTone(96, 80, 95)).toBe('bad')
    expect(levelTone(85, 80, 95)).toBe('warn')
    expect(levelTone(10, 80, 95)).toBeUndefined()
  })
})
