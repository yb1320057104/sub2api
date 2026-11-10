import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { buildEmbeddedUrl, detectTheme } from '../embedded-url'

describe('embedded-url', () => {
  const originalLocation = window.location

  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: {
        origin: 'https://app.example.com',
        href: 'https://app.example.com/user/purchase',
      },
      writable: true,
      configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      value: originalLocation,
      writable: true,
      configurable: true,
    })
    document.documentElement.classList.remove('dark')
    vi.restoreAllMocks()
  })

  it('adds embedded query parameters including locale and source context', () => {
    const result = buildEmbeddedUrl(
      'https://pay.example.com/checkout?plan=pro',
      42,
      'dark',
      'zh-CN',
    )

    const url = new URL(result)
    expect(url.searchParams.get('plan')).toBe('pro')
    expect(url.searchParams.has('user_id')).toBe(false)
    expect(url.searchParams.has('token')).toBe(false)
    expect(url.searchParams.get('theme')).toBe('dark')
    expect(url.searchParams.get('lang')).toBe('zh-CN')
    expect(url.searchParams.get('ui_mode')).toBe('embedded')
    expect(url.searchParams.get('src_host')).toBe('https://app.example.com')
    expect(url.searchParams.get('src_url')).toBe('https://app.example.com/user/purchase')
  })

  it('omits optional params when they are empty', () => {
    const result = buildEmbeddedUrl('https://pay.example.com/checkout', undefined, 'light')

    const url = new URL(result)
    expect(url.searchParams.get('theme')).toBe('light')
    expect(url.searchParams.get('ui_mode')).toBe('embedded')
    expect(url.searchParams.has('user_id')).toBe(false)
    expect(url.searchParams.has('token')).toBe(false)
    expect(url.searchParams.has('lang')).toBe(false)
  })

  it('returns original string for invalid url input', () => {
    expect(buildEmbeddedUrl('not a url', 1)).toBe('not a url')
  })

  it('removes duplicate and encoded legacy JWT parameters while preserving target business parameters', () => {
    const result = buildEmbeddedUrl('https://docs.example.com/guide?token=old-jwt&%74oken=another-jwt&plan=pro#section', 42)
    const url = new URL(result)
    expect(url.searchParams.has('token')).toBe(false)
    expect(result).not.toMatch(/old-jwt|another-jwt/)
    expect(url.searchParams.get('plan')).toBe('pro')
    expect(url.hash).toBe('#section')
  })

  it('never copies source query or fragment credentials into the embedded URL', () => {
    window.location.href = 'https://app.example.com/custom/docs?access_token=query-jwt&code=oauth-code#token=fragment-jwt'
    const result = buildEmbeddedUrl('https://docs.example.com/guide', 42, 'dark', 'en')
    expect(new URL(result).searchParams.get('src_url')).toBe('https://app.example.com/custom/docs')
    expect(result).not.toMatch(/query-jwt|oauth-code|fragment-jwt/)
  })

  it('detects dark mode from document root class', () => {
    document.documentElement.classList.add('dark')
    expect(detectTheme()).toBe('dark')
  })

  it.each(['/integrated-app', '/integrated-app/', '/other-app/dashboard'])('preserves opted-in login for a same-origin application %s', (path) => {
    const result = buildEmbeddedUrl(`https://app.example.com${path}?token=stale`, 42, 'light', 'en', {
      forwardSameOriginToken: true, authToken: 'current-jwt',
    })
    expect(new URL(result).searchParams.getAll('token')).toEqual(['current-jwt'])
    expect(new URL(result).searchParams.get('user_id')).toBe('42')
  })

  it.each([
    'https://shop.example.com/checkout?token=stale&user_id=42',
    'https://elsewhere.example.com/app?token=stale',
    'https://app.example.com:8443/app?token=stale',
    'http://app.example.com/app?token=stale',
    'https://app.example.com.evil.example/app?token=stale',
  ])('does not send the main login to an untrusted embedded URL %s', (target) => {
    const result = buildEmbeddedUrl(target, 42, 'light', 'en', { forwardSameOriginToken: true, authToken: 'current-jwt' })
    expect(new URL(result).searchParams.has('token')).toBe(false)
    expect(new URL(result).searchParams.has('user_id')).toBe(false)
    expect(result).not.toMatch(/current-jwt|stale/)
  })

  it('strips source credentials even for a trusted same-origin application', () => {
    window.location.href = 'https://app.example.com/custom/docs?token=query-jwt#access_token=fragment-jwt'
    const result = buildEmbeddedUrl('https://app.example.com/integrated-app', 42, 'light', 'en', {
      forwardSameOriginToken: true, authToken: 'current-jwt',
    })
    expect(new URL(result).searchParams.get('src_url')).toBe('https://app.example.com/custom/docs')
    expect(result).not.toMatch(/query-jwt|fragment-jwt/)
  })

  it('requires opt-in before forwarding a same-origin login token', () => {
    const result = buildEmbeddedUrl('https://app.example.com/integrated-app?token=stale', 42, 'light', 'en', {
      authToken: 'current-jwt',
    })
    expect(new URL(result).searchParams.has('token')).toBe(false)
  })
})
