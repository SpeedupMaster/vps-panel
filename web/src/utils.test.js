import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { idsCsv, isHttpUrl, isSafeNextPath, computeDirtyKeys } from './utils.js'

describe('utils.js / helpers', () => {
  it('parses comma-separated positive numeric IDs with idsCsv', () => {
    assert.deepEqual(idsCsv('1, 2, 3'), [1, 2, 3])
    assert.deepEqual(idsCsv('0, -4, abc, 7, , 12'), [7, 12])
    assert.deepEqual(idsCsv('all'), [])
    assert.deepEqual(idsCsv(''), [])
    assert.deepEqual(idsCsv(null), [])
    assert.deepEqual(idsCsv([1, '2', 0, -3]), [1, 2])
  })

  it('validates HTTP(S) URLs and rejects javascript:/data: XSS sinks', () => {
    assert.equal(isHttpUrl('https://example.com/panel'), true)
    assert.equal(isHttpUrl('http://example.com'), true)
    assert.equal(isHttpUrl('  HTTPS://Example.COM/foo  '), true)
    assert.equal(isHttpUrl('javascript:alert(1)'), false)
    assert.equal(isHttpUrl('JaVaScRiPt:alert(1)'), false)
    assert.equal(isHttpUrl('data:text/html,<script>alert(1)</script>'), false)
    assert.equal(isHttpUrl('vbscript:msgbox(1)'), false)
    assert.equal(isHttpUrl('//evil.com'), false)
    assert.equal(isHttpUrl('/relative/path'), false)
    assert.equal(isHttpUrl(''), false)
  })

  it('validates in-app redirect targets and blocks protocol-relative open redirects', () => {
    assert.equal(isSafeNextPath('/'), true)
    assert.equal(isSafeNextPath('/servers/3?tab=xray'), true)
    assert.equal(isSafeNextPath('//evil.com'), false)
    assert.equal(isSafeNextPath('/\\evil.com'), false)
    assert.equal(isSafeNextPath('https://evil.com'), false)
    assert.equal(isSafeNextPath('javascript:alert(1)'), false)
    assert.equal(isSafeNextPath(''), false)
    assert.equal(isSafeNextPath(null), false)
  })

  it('computes dirty keys accurately across strings, nulls, and subsets', () => {
    const orig = { a: '1', b: '', c: 'hello', d: 'same' }
    const cur = { a: '2', b: null, c: 'hello', d: 'diff' }
    assert.deepEqual(computeDirtyKeys(cur, orig), ['a', 'd'])
    assert.deepEqual(computeDirtyKeys(cur, orig, ['b', 'c']), [])
    assert.deepEqual(computeDirtyKeys(cur, orig, ['a', 'b', 'c']), ['a'])
  })
})
