import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SmartOpsNav from '../SmartOpsNav.vue'
import SmartOperationsView from '@/views/admin/SmartOperationsView.vue'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import { useAuthStore } from '@/stores/auth'
import { useNewFeatureNoticeStore } from '@/stores/newFeatureNotices'
import { NEW_FEATURE_NOTICES } from '@/constants/newFeatureNotices'
import { SMART_OPERATION_TOOLS, legacySmartOperationsLocation, smartOperationLocation, smartOperationTool } from '@/utils/smartOperations'
import type { User } from '@/types'

const lifecycle = vi.hoisted(() => ({ radarMounted: 0, radarUnmounted: 0, credentialsMounted: 0 }))
vi.mock('@/views/admin/RadarObservationView.vue', async () => {
  const { defineComponent, onMounted, onUnmounted } = await import('vue')
  return { default: defineComponent({ setup() {
    onMounted(() => { lifecycle.radarMounted++ })
    onUnmounted(() => { lifecycle.radarUnmounted++ })
  }, template: '<main data-testid="radar-tool"><input /></main>' }) }
})
vi.mock('@/views/admin/ops/TokenGuardV2View.vue', async () => {
  const { defineComponent, onMounted } = await import('vue')
  return { default: defineComponent({ setup() { onMounted(() => { lifecycle.credentialsMounted++ }) }, template: '<main data-testid="credentials-tool" />' }) }
})
vi.mock('@/views/admin/ProfitOperationsView.vue', () => { throw new Error('synthetic chunk load failure') })
const href = (tool: string) => '/admin/smart-ops?tool=' + tool
const hrefs = (wrapper: Awaited<ReturnType<typeof mountAt>>) => wrapper.findAll('nav a').map(link => link.attributes('href'))
async function makeRouter(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: ['/admin/smart-ops', '/admin/account-authorization', ...SMART_OPERATION_TOOLS.map(tool => tool.path)].map(path => ({ path, component: { template: '<div />' } })) })
  await router.push(path); await router.isReady(); return router
}
const makeI18n = () => createI18n({ legacy: false, locale: 'zh', missingWarn: false, fallbackWarn: false, messages: { zh: {} } })
async function mountAt(path: string) {
  return mount(SmartOpsNav, { global: { plugins: [await makeRouter(path), makeI18n()], stubs: { SmartOpsGuide: true } } })
}
beforeEach(() => {
  localStorage.clear(); setActivePinia(createPinia())
  useAuthStore().user = { id: 41, role: 'admin' } as User
  useAdminSettingsStore().smartOpsHiddenMenuItems = []
  Object.assign(lifecycle, { radarMounted: 0, radarUnmounted: 0, credentialsMounted: 0 })
})

describe('SmartOpsNav', () => {
  it('keeps common tools visible and advanced tools collapsed by default', async () => {
    const wrapper = await mountAt(href('radar'))
    expect(hrefs(wrapper).slice(0, 3)).toEqual(['radar', 'credentials', 'notifications'].map(href))
    expect(hrefs(wrapper)).toEqual(SMART_OPERATION_TOOLS.filter(tool => tool.id !== 'captures').map(tool => href(tool.id)))
    expect(wrapper.get('details').attributes('open')).toBeUndefined()
    expect(wrapper.findAll('[aria-current="page"]')).toHaveLength(1)
    expect(wrapper.get('[aria-current="page"]').text()).toBe('radar.title')
    expect(wrapper.getComponent({ name: 'SmartOpsGuide' }).props('tool')).toBe('radar')
    wrapper.unmount()
  })
  it('opens the advanced section and selects by tool rather than shared route path', async () => {
    const wrapper = await mountAt(href('lottery'))
    expect(wrapper.get('details').attributes()).toHaveProperty('open')
    expect(wrapper.findAll('[aria-current="page"]')).toHaveLength(1)
    expect(wrapper.get('[aria-current="page"]').text()).toBe('lottery.adminTitle')
    wrapper.unmount()
  })
  it.each(['/admin/smart-ops/', '/ADMIN/SMART-OPS/'])('selects the current tool for matched path variant %s', async path => {
    const wrapper = await mountAt(path + '?tool=quality')
    expect(wrapper.findAll('[aria-current="page"]')).toHaveLength(1)
    expect(wrapper.get('[aria-current="page"]').text()).toBe('qualityOps.title')
    expect(wrapper.getComponent({ name: 'SmartOpsGuide' }).props('tool')).toBe('quality')
    wrapper.unmount()
  })
  it('updates capture visibility without remounting and keeps feature gates', async () => {
    const wrapper = await mountAt(href('radar'))
    expect(hrefs(wrapper)).not.toContain(href('captures'))
    useAdminSettingsStore().setRequestCaptureEnabledLocal(true); await nextTick()
    expect(hrefs(wrapper)).toContain(href('captures'))
    useAdminSettingsStore().setRequestCaptureEnabledLocal(false); await nextTick()
    expect(hrefs(wrapper)).not.toContain(href('captures'))
    wrapper.unmount()
  })
  it('honors old hidden paths while keeping the combined notification entry', async () => {
    useAdminSettingsStore().smartOpsHiddenMenuItems = ['/admin/token-guard', '/admin/harvest-flow', '/admin/account-ops']
    const wrapper = await mountAt(href('radar'))
    expect(hrefs(wrapper)).not.toContain(href('external-guard'))
    expect(hrefs(wrapper)).not.toContain(href('harvest'))
    expect(hrefs(wrapper)).toContain(href('notifications'))
    wrapper.unmount()
  })
  it('hides AI from observers', async () => {
    useAuthStore().user = { id: 42, role: 'observer' } as User
    const wrapper = await mountAt(href('radar'))
    expect(hrefs(wrapper)).not.toContain(href('ai')); wrapper.unmount()
  })
  it('does not carry a previous tool tab or filter into another tool', async () => {
    const wrapper = await mountAt(href('credentials') + '&tab=proxies&search=private')
    await wrapper.get('a[href="' + href('radar') + '"]').trigger('click'); await flushPromises()
    expect(wrapper.vm.$route.query).toEqual({ tool: 'radar' }); wrapper.unmount()
  })
  it.each([['ai', NEW_FEATURE_NOTICES.aiOpsCodex], ['quality', NEW_FEATURE_NOTICES.qualityRetestRest], ['credentials', NEW_FEATURE_NOTICES.credentialOperationsUnified]])('shares %s notice dismissal across entries', async (tool, notice) => {
    const first = await mountAt(href(tool)), second = await mountAt(href('radar'))
    const selector = 'a[href="' + href(tool) + '"] + [data-testid="new-feature-badge"]'
    expect(second.find(selector).exists()).toBe(true); expect(first.get(selector).classes()).not.toContain('absolute')
    await first.get(selector).trigger('click'); await nextTick()
    expect(second.find(selector).exists()).toBe(false); expect(useNewFeatureNoticeStore().isDismissed(41, notice)).toBe(true)
    first.unmount(); second.unmount()
  })
})

describe('Smart operations route compatibility', () => {
  it.each(SMART_OPERATION_TOOLS)('preserves filters and anchors for $path', tool => {
    for (const path of [tool.path, tool.path + '/', tool.path.toUpperCase() + '/']) {
      const result = legacySmartOperationsLocation({ path, query: { tab: 'details', search: 'account', tool: 'bad' }, hash: '#history' })
      expect(result.path).toBe('/admin/smart-ops')
      expect(result.query).toMatchObject({ tool: tool.id, tab: 'details', search: 'account' }); expect(result.hash).toBe('#history')
      if (tool.id === 'notifications') expect(result.query.notification).toBe('accounts')
    }
  })
  it('keeps authorization tabs and remaps radar aliases', () => {
    for (const tab of ['workbench', 'proxies', 'history']) expect(legacySmartOperationsLocation({ path: '/admin/account-authorization', query: { tab } }).query).toEqual({ tool: 'credentials', tab })
    expect(legacySmartOperationsLocation({ path: '/admin/account-authorization', query: { tab: 'bad' } }).query.tab).toBe('workbench')
    expect(legacySmartOperationsLocation({ path: '/admin/radar', query: { tab: 'authorization' } }).query).toEqual({ tool: 'credentials', tab: 'inspection' })
    expect(legacySmartOperationsLocation({ path: '/admin/radar', query: { tab: 'emails', page: '2' }, hash: '#summary' })).toEqual({ path: '/admin/smart-ops', query: { tool: 'notifications', notification: 'overview', page: '2' }, hash: '#summary' })
  })
  it('normalizes missing and invalid tool IDs without letting query overwrite the selection', () => {
    for (const value of [undefined, null, 'missing', ['radar', 'credentials']]) expect(smartOperationTool(value)).toBe('radar')
    expect(smartOperationLocation('quality', { tool: 'bad' }).query.tool).toBe('quality')
  })
})

describe('Single active tool lifecycle', () => {
  it('shows retry and refresh when a chunk fails, then permits another tool to load', async () => {
    const router = await makeRouter(href('profit'))
    const wrapper = mount(SmartOperationsView, { global: { plugins: [router, makeI18n()], stubs: { AppLayout: { template: '<div><slot /></div>' }, SmartOpsNav: true } } })
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('smartOps.loadFailed')
    expect(wrapper.findAll('button').map(button => button.text())).toEqual(['smartOps.retry', 'smartOps.reload'])
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('smartOps.loadFailed')
    await router.push(href('radar')); await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="radar-tool"]').exists()).toBe(true)
    wrapper.unmount()
  })
  it('keeps drafts on tab changes and unmounts the previous tool before selecting another', async () => {
    const router = await makeRouter(href('radar'))
    const wrapper = mount(SmartOperationsView, { global: { plugins: [router, makeI18n()], stubs: { AppLayout: { template: '<div data-testid="loading-layout"><slot /></div>' }, SmartOpsNav: true } } })
    await flushPromises()
    expect(lifecycle).toEqual({ radarMounted: 1, radarUnmounted: 0, credentialsMounted: 0 })
    expect(wrapper.find('[data-testid="loading-layout"]').exists()).toBe(false)
    await wrapper.get('input').setValue('draft')
    await router.push(href('radar') + '&tab=accounts'); await flushPromises()
    expect(lifecycle.radarMounted).toBe(1); expect((wrapper.get('input').element as HTMLInputElement).value).toBe('draft')
    await router.push(href('credentials') + '&tab=inspection'); await flushPromises()
    expect(lifecycle).toEqual({ radarMounted: 1, radarUnmounted: 1, credentialsMounted: 1 })
    expect(wrapper.find('[data-testid="radar-tool"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="credentials-tool"]').exists()).toBe(true)
    expect(wrapper.findAll('main')).toHaveLength(1); wrapper.unmount()
  })
})
