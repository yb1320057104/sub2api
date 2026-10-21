import { beforeEach, describe, expect, it } from 'vitest'
import { DEFAULT_FAVICON_URL, resolveSiteName, updateFavicon } from '@/utils/branding'

describe('resolveSiteName', () => {
  it.each([undefined, null, '', '  ', 'Sub2API'])('uses DP API for the default name %s', (name) => {
    expect(resolveSiteName(name)).toBe('DP API')
  })

  it.each(['DP API', 'My Team API', 'Sub2API Team', 'sub2api'])('preserves the custom name %s', (name) => {
    expect(resolveSiteName(name)).toBe(name)
  })
})

describe('updateFavicon', () => {
  beforeEach(() => {
    document.head.innerHTML = '<link rel="icon" type="image/svg+xml" sizes="any" href="/logo.svg">'
  })

  it.each(['', ' ', '\t\n  '])('uses the 64px DP favicon for an empty configured logo (%j)', (logoUrl) => {
    updateFavicon(logoUrl)

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(DEFAULT_FAVICON_URL).toBe('/dp-api-favicon.png')
    expect(link?.getAttribute('href')).toBe(DEFAULT_FAVICON_URL)
    expect(link?.type).toBe('image/png')
    expect(link?.getAttribute('sizes')).toBe('64x64')
  })

  it('creates the default favicon link when the document has none', () => {
    document.head.innerHTML = ''

    updateFavicon('')

    const links = document.querySelectorAll<HTMLLinkElement>('link[rel="icon"]')
    expect(links).toHaveLength(1)
    expect(links[0].getAttribute('href')).toBe('/dp-api-favicon.png')
    expect(links[0].type).toBe('image/png')
    expect(links[0].getAttribute('sizes')).toBe('64x64')
  })

  it('replaces the default favicon with the configured logo', () => {
    updateFavicon('')
    updateFavicon('https://example.com/custom-logo.png')

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.href).toBe('https://example.com/custom-logo.png')
    expect(link?.type).toBe('image/png')
    expect(link?.hasAttribute('sizes')).toBe(false)
    expect(document.querySelectorAll('link[rel="icon"]')).toHaveLength(1)
  })

  it.each([
    ['PNG', 'image/png'],
    ['JPG', 'image/jpeg'],
    ['JPEG', 'image/jpeg'],
    ['SVG', 'image/svg+xml'],
    ['WEBP', 'image/webp'],
    ['GIF', 'image/gif'],
    ['ICO', 'image/x-icon'],
    ['AVIF', 'image/avif'],
    ['BMP', 'image/bmp']
  ])('sets the MIME type for a custom %s logo without using its query or hash', (extension, mimeType) => {
    const logoUrl = `https://example.com/custom-logo.${extension}?download=icon.gif#icon.svg`
    updateFavicon(logoUrl)

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.href).toBe(logoUrl)
    expect(link?.type).toBe(mimeType)
    expect(link?.hasAttribute('sizes')).toBe(false)
  })

  it('preserves a configured relative logo and infers its MIME type', () => {
    updateFavicon(' /assets/custom-logo.WeBp?v=1 ')

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.getAttribute('href')).toBe('/assets/custom-logo.WeBp?v=1')
    expect(link?.type).toBe('image/webp')
    expect(link?.hasAttribute('sizes')).toBe(false)
  })

  it.each([
    ['data:image/png;base64,iVBORw0KGgo=', 'image/png'],
    ['data:image/SVG+xml,%3Csvg%20xmlns=%22http://www.w3.org/2000/svg%22/%3E', 'image/svg+xml'],
    ['data:image/x-icon;base64,AAABAA==', 'image/x-icon']
  ])('uses the image MIME declared by a custom data URL (%s)', (logoUrl, mimeType) => {
    updateFavicon(logoUrl)

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.getAttribute('href')).toBe(logoUrl)
    expect(link?.type).toBe(mimeType)
    expect(link?.hasAttribute('sizes')).toBe(false)
  })

  it.each([
    'https://example.com/favicon?filename=logo.png#logo.svg',
    '/assets/favicon.unknown'
  ])('removes stale type and size hints for an unknown custom format (%s)', (logoUrl) => {
    updateFavicon('')
    updateFavicon(logoUrl)

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.getAttribute('href')).toBe(logoUrl)
    expect(link?.hasAttribute('type')).toBe(false)
    expect(link?.hasAttribute('sizes')).toBe(false)
  })

  it('restores the DP favicon after a custom logo is cleared', () => {
    updateFavicon('https://example.com/custom-logo.svg')
    updateFavicon('  ')

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.getAttribute('href')).toBe('/dp-api-favicon.png')
    expect(link?.type).toBe('image/png')
    expect(link?.getAttribute('sizes')).toBe('64x64')
  })

  it.each([
    'javascript:alert(1)',
    '  javascript:alert(1)  ',
    'data:text/html,<script>alert(1)</script>',
    '//example.com/logo.png',
    'https://'
  ])('ignores unsafe or invalid nonempty logo URLs without changing the current favicon (%s)', (logoUrl) => {
    updateFavicon(logoUrl)

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.getAttribute('href')).toBe('/logo.svg')
    expect(link?.type).toBe('image/svg+xml')
    expect(link?.getAttribute('sizes')).toBe('any')
    expect(document.querySelectorAll('link[rel="icon"]')).toHaveLength(1)
  })

  it('does not create a favicon link for an unsafe nonempty URL', () => {
    document.head.innerHTML = ''

    updateFavicon('javascript:alert(1)')

    expect(document.querySelector('link[rel="icon"]')).toBeNull()
  })
})
