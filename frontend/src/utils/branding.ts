import { sanitizeUrl } from '@/utils/url'

export const DEFAULT_SITE_NAME = 'DP API'
export const DEFAULT_FAVICON_URL = '/dp-api-favicon.png'

export function resolveSiteName(siteName?: string | null): string {
  return !siteName?.trim() || siteName === 'Sub2API' ? DEFAULT_SITE_NAME : siteName
}

export function updateFavicon(logoUrl: string): void {
  const sanitizedLogoUrl = logoUrl.trim()
    ? sanitizeUrl(logoUrl, { allowRelative: true, allowDataUrl: true })
    : DEFAULT_FAVICON_URL
  if (!sanitizedLogoUrl) {
    return
  }

  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }

  const imageTypes: Record<string, string> = {
    png: 'image/png',
    svg: 'image/svg+xml',
    ico: 'image/x-icon',
    jpg: 'image/jpeg',
    jpeg: 'image/jpeg',
    webp: 'image/webp',
    gif: 'image/gif',
    avif: 'image/avif',
    bmp: 'image/bmp',
  }
  const dataType = /^data:(image\/[a-z0-9.+-]+)[;,]/i.exec(sanitizedLogoUrl)?.[1]
  const extension = sanitizedLogoUrl.split(/[?#]/, 1)[0].split('.').pop()?.toLowerCase() || ''
  const imageType = dataType?.toLowerCase() || imageTypes[extension]
  if (imageType) link.type = imageType
  else link.removeAttribute('type')
  if (sanitizedLogoUrl === DEFAULT_FAVICON_URL) link.setAttribute('sizes', '64x64')
  else link.removeAttribute('sizes')
  link.href = sanitizedLogoUrl
}
