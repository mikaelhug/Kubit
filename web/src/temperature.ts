import type { Temperature } from './api'
import type { Tone } from './tone'

export const sensorOf = (temps: Temperature[] | undefined, sensor: Temperature['sensor']) => temps?.find((t) => t.sensor === sensor)

export function temperatureTone(t: Temperature): Tone {
  if (t.critical && t.celsius >= t.critical) return 'bad'
  if (t.high && t.celsius >= t.high) return 'warn'
  return 'good'
}

export const degrees = (c: number) => `${Math.round(c)} °C`

export function temperatureTitle(temps: Temperature[]) {
  return temps.map((t) => `${t.sensor === 'cpu' ? 'CPU' : 'Disk'} (${t.chip}) ${degrees(t.celsius)}${t.high ? `, high ${degrees(t.high)}` : ''}${t.critical ? `, critical ${degrees(t.critical)}` : ''}`).join('\n')
}
