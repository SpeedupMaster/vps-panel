import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  bytes, rate, pct, ms, valid, ago, dur, countdown,
  dt, date, daysUntil, flag, overallLoss, lossText, lossClass, latClass,
} from './format.js'

describe('format.js', () => {
  it('formats byte sizes and transfer rates', () => {
    assert.equal(bytes(0), '0 B')
    assert.equal(bytes(512), '512 B')
    assert.equal(bytes(1024), '1.0 KB')
    assert.equal(bytes(150 * 1024), '150 KB')
    assert.equal(bytes(1.5 * 1024 ** 3), '1.5 GB')
    assert.equal(rate(2048), '2.0 KB/s')
  })

  it('formats percentages and milliseconds', () => {
    assert.equal(pct(null), '—')
    assert.equal(pct(Number.NaN), '—')
    assert.equal(pct(42.6), '43%')
    assert.equal(pct(42.6, 1), '42.6%')
    assert.equal(ms(0), '—')
    assert.equal(ms(-1), '—')
    assert.equal(ms(123.6), '124 ms')
  })

  it('validates timestamps and formats durations', () => {
    assert.equal(valid(0), false)
    assert.equal(valid('0001-01-01T00:00:00Z'), false)
    assert.equal(valid('2026-01-01T00:00:00Z'), true)
    assert.equal(valid(1700000000), true)

    assert.equal(dur(0), '0 秒')
    assert.equal(dur(59), '59 秒')
    assert.equal(dur(60), '1 分钟')
    assert.equal(dur(3600), '1.0 小时')
    assert.equal(dur(90000), '1 天')
  })

  it('formats install token countdowns cleanly (>= 1h as Xh Ym, < 1h as MM:SS)', () => {
    const base = Date.UTC(2026, 5, 1, 12, 0, 0)
    assert.equal(countdown('', base), '')
    assert.equal(countdown(base - 5000, base), '00:00')
    assert.equal(countdown(base + 65 * 1000, base), '01:05')
    assert.equal(countdown(base + 3599 * 1000, base), '59:59')
    assert.equal(countdown(base + 3600 * 1000, base), '1h 0m')
    assert.equal(countdown(new Date(base + (23 * 3600 + 45 * 60) * 1000).toISOString(), base), '23h 45m')
    // Supports Unix timestamps in seconds as well.
    assert.equal(countdown(Math.floor((base + 125 * 1000) / 1000), base), '02:05')
  })

  it('formats dates, flags, and relative time safely', () => {
    assert.equal(ago('0001-01-01T00:00:00Z'), '—')
    assert.equal(dt('0001-01-01T00:00:00Z'), '—')
    assert.equal(date('0001-01-01T00:00:00Z'), '—')
    assert.equal(daysUntil('0001-01-01T00:00:00Z'), null)
    assert.equal(flag('hk'), '🇭🇰')
    assert.equal(flag('US'), '🇺🇸')
    assert.equal(flag(''), '🏳')
  })

  it('computes overall loss and latency/loss classes', () => {
    assert.equal(overallLoss([]), null)
    assert.equal(overallLoss([{ loss: 0 }, { loss: 20 }]), 10)
    assert.equal(lossText(null), '—')
    assert.equal(lossText(0), '0%')
    assert.equal(lossText(0.05), '<0.1%')
    assert.equal(lossText(2.5), '2.5%')
    assert.equal(lossText(25.4), '25%')
    assert.equal(lossClass(0.5), 'muted')
    assert.equal(lossClass(5), 'warn-text')
    assert.equal(lossClass(25), 'bad-text')
    assert.equal(latClass(0), 'off')
    assert.equal(latClass(50), 'ok')
    assert.equal(latClass(180), 'warn')
    assert.equal(latClass(300), 'bad')
  })
})
