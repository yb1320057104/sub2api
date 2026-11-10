/**
 * Shared URL builder for iframe-embedded pages.
 * Keeps presentation and source context without disclosing credentials to external sites.
 */

const EMBEDDED_USER_ID_QUERY_KEY = 'user_id'
const EMBEDDED_THEME_QUERY_KEY = 'theme'
const EMBEDDED_LANG_QUERY_KEY = 'lang'
const EMBEDDED_UI_MODE_QUERY_KEY = 'ui_mode'
const EMBEDDED_UI_MODE_VALUE = 'embedded'
const EMBEDDED_SRC_HOST_QUERY_KEY = 'src_host'
const EMBEDDED_SRC_QUERY_KEY = 'src_url'

interface EmbeddedUrlOptions {
  forwardSameOriginToken?: boolean
  authToken?: string | null
}

function isTrustedApplication(url: URL): boolean {
  return typeof window !== 'undefined'
    && url.origin === window.location.origin
    && (url.protocol === 'https:' || url.protocol === 'http:')
    && !url.username && !url.password
}

export function buildEmbeddedUrl(
  baseUrl: string,
  userId?: number,
  theme: 'light' | 'dark' = 'light',
  lang?: string,
  options: EmbeddedUrlOptions = {},
): string {
  if (!baseUrl) return baseUrl
  try {
    const url = new URL(baseUrl)
    const sameOrigin = isTrustedApplication(url)
    // External sites do not need our account identity, including stale URL copies.
    url.searchParams.delete(EMBEDDED_USER_ID_QUERY_KEY)
    if (userId && sameOrigin) {
      url.searchParams.set(EMBEDDED_USER_ID_QUERY_KEY, String(userId))
    }
    // This key was previously reserved for the main application's bearer JWT.
    // Remove stale copies as well as stopping automatic credential injection.
    url.searchParams.delete('token')
    if (options.forwardSameOriginToken && options.authToken && sameOrigin) {
      url.searchParams.set('token', options.authToken)
    }
    url.searchParams.set(EMBEDDED_THEME_QUERY_KEY, theme)
    if (lang) {
      url.searchParams.set(EMBEDDED_LANG_QUERY_KEY, lang)
    }
    url.searchParams.set(EMBEDDED_UI_MODE_QUERY_KEY, EMBEDDED_UI_MODE_VALUE)
    // Source tracking: let the embedded page know where it's being loaded from
    if (typeof window !== 'undefined') {
      url.searchParams.set(EMBEDDED_SRC_HOST_QUERY_KEY, window.location.origin)
      const sourceUrl = new URL(window.location.href)
      sourceUrl.search = ''
      sourceUrl.hash = ''
      url.searchParams.set(EMBEDDED_SRC_QUERY_KEY, sourceUrl.toString())
    }
    return url.toString()
  } catch {
    return baseUrl
  }
}

export function detectTheme(): 'light' | 'dark' {
  if (typeof document === 'undefined') return 'light'
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}
