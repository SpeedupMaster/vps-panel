// Pure utility functions with zero browser/Vue dependencies (directly unit-testable in Node).

/**
 * Parse a comma-separated list of positive numeric IDs.
 * @param {string | number[] | null | undefined} s
 * @returns {number[]}
 */
export function idsCsv(s) {
  if (Array.isArray(s)) return s.map(Number).filter((x) => Number.isFinite(x) && x > 0)
  return String(s || '').split(',').map((x) => Number(x.trim())).filter((x) => Number.isFinite(x) && x > 0)
}

/**
 * Returns true if the URL uses an http:// or https:// scheme (blocks javascript:, data:, etc.).
 * @param {string | null | undefined} u
 * @returns {boolean}
 */
export function isHttpUrl(u) {
  return /^https?:\/\//i.test(String(u || '').trim())
}

/**
 * Returns true if a post-login redirect target is a safe relative in-app path.
 * Blocks protocol-relative URLs (`//evil.com`, `/\evil.com`) and external schemes.
 * @param {unknown} p
 * @returns {boolean}
 */
export function isSafeNextPath(p) {
  return typeof p === 'string' && p.startsWith('/') && !p.startsWith('//') && !p.startsWith('/\\')
}

/**
 * Returns keys whose values differ between current and original form objects.
 * @param {Record<string, unknown>} current
 * @param {Record<string, unknown>} original
 * @param {string[]} [keys]
 * @returns {string[]}
 */
export function computeDirtyKeys(current, original, keys) {
  const list = keys || Object.keys(current || {})
  return list.filter((k) => (current?.[k] ?? '') !== (original?.[k] ?? ''))
}
