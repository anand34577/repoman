import type { Asset } from './api'

/** In-app link that opens an asset's details. */
export const assetLink = (a: Asset) =>
  `/repositories/${a.repo}?path=${encodeURIComponent(a.path.split('/').slice(0, -1).join('/'))}&select=${encodeURIComponent(a.path)}`

/** URL that serves an asset through the format's own client endpoint. */
export function downloadUrl(a: Asset, format: string) {
  if (format === 'docker') return `/v2/${a.repo}/${a.path}`
  if (format === 'npm' && a.path.endsWith('/package.json') && !a.path.includes('/-/'))
    return `/repository/${a.repo}/${a.path.slice(0, -'/package.json'.length)}`
  if (format === 'pypi' && a.path.startsWith('simple/')) return `/repository/${a.repo}/${a.path.replace(/index\.html$/, '')}`
  return `/repository/${a.repo}/${a.path}`
}
