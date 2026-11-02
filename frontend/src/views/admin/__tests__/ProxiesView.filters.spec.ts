import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProxiesView from '../ProxiesView.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const { listProxies, getAllWithCount, batchUpdateProtocol, showError, showInfo, showSuccess } = vi.hoisted(() => ({
  listProxies: vi.fn(),
  getAllWithCount: vi.fn(),
  batchUpdateProtocol: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { proxies: { list: listProxies, getAllWithCount, batchUpdateProtocol } }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showInfo, showSuccess }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key} ${JSON.stringify(params)}` : key })
}))

const mountView = () => shallowMount(ProxiesView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: {
        template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
      },
      DataTable: {
        props: ['data'],
        template: `<div>
          <div data-test="select-all"><slot name="header-select" /></div>
          <div data-test="rows">{{ data.map(row => row.name).join(",") }}</div>
          <div v-for="row in data" :key="row.id" :data-proxy-id="row.id"><slot name="cell-select" :row="row" /></div>
        </div>`
      },
      Pagination: {
        props: ['page'],
        emits: ['update:page'],
        template: '<button data-test="page" @click="$emit(\'update:page\', 2)">{{ page }}</button>'
      },
      Select: {
        props: ['modelValue', 'options', 'placeholder'],
        emits: ['update:modelValue', 'change'],
        template: `<select :data-filter="placeholder" :value="modelValue"
          @change="$emit('update:modelValue', $event.target.value); $emit('change', $event.target.value)">
          <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>`
      }
    }
  }
})

let wrapper: ReturnType<typeof mountView>

beforeEach(() => {
  vi.clearAllMocks()
  getAllWithCount.mockResolvedValue([])
  listProxies.mockResolvedValue({ items: [], total: 100, pages: 5 })
  batchUpdateProtocol.mockResolvedValue({ protocol: 'http', updated_ids: [], skipped: [] })
})

describe('proxy protocol batch selection', () => {
  async function loadRows(count = 2) {
    listProxies.mockResolvedValue({
      items: Array.from({ length: count }, (_, index) => ({ id: index + 1, name: `proxy-${index + 1}` })),
      total: count,
      pages: 1
    })
    wrapper = mountView()
    await flushPromises()
  }

  const protocolDialog = () => wrapper.findAllComponents(ConfirmDialog)
    .find(dialog => dialog.props('title') === 'admin.proxies.batchProtocolTitle')!
  const rowCheckbox = (id: number) => wrapper.get<HTMLInputElement>(`[data-proxy-id="${id}"] input`)
  const openSwitch = (protocol = 'Http') => wrapper.get(`button[title="admin.proxies.batchProtocol${protocol}"]`).trigger('click')

  it('freezes the confirmed selection and prevents duplicate writes while pending', async () => {
    await loadRows()
    let complete!: (value: unknown) => void
    batchUpdateProtocol.mockImplementation(() => new Promise(resolve => { complete = resolve }))
    await rowCheckbox(1).setValue(true)
    await openSwitch()
    expect(protocolDialog().props('show')).toBe(true)
    await rowCheckbox(2).setValue(true)

    protocolDialog().vm.$emit('confirm')
    protocolDialog().vm.$emit('confirm')
    await flushPromises()
    expect(batchUpdateProtocol).toHaveBeenCalledTimes(1)
    expect(batchUpdateProtocol).toHaveBeenCalledWith([1], 'http')
    expect(protocolDialog().props('show')).toBe(false)
    expect(wrapper.get<HTMLButtonElement>('button[title="admin.proxies.batchProtocolHttps"]').element.disabled).toBe(true)

    complete({ protocol: 'http', updated_ids: [1], skipped: [] })
    await flushPromises()
    expect(rowCheckbox(1).element.checked).toBe(false)
    expect(rowCheckbox(2).element.checked).toBe(true)
  })

  it('keeps unsuccessful proxies selected and reports a partial result', async () => {
    await loadRows()
    await wrapper.get('[data-test="select-all"] input').setValue(true)
    batchUpdateProtocol.mockResolvedValue({ protocol: 'https', updated_ids: [1], skipped: [{ id: 2, reason: 'not_found' }] })
    await openSwitch('Https')
    protocolDialog().vm.$emit('confirm')
    await flushPromises()

    expect(batchUpdateProtocol).toHaveBeenCalledWith([1, 2], 'https')
    expect(rowCheckbox(1).element.checked).toBe(false)
    expect(rowCheckbox(2).element.checked).toBe(true)
    expect(showInfo).toHaveBeenCalledWith(expect.stringContaining('admin.proxies.batchProtocolPartial'))
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('rejects more than 500 selected proxies before opening confirmation', async () => {
    await loadRows(501)
    await wrapper.get('[data-test="select-all"] input').setValue(true)
    await openSwitch()

    expect(protocolDialog().props('show')).toBe(false)
    expect(batchUpdateProtocol).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.proxies.batchProtocolLimit {"max":500}')
  })

  it('preserves selection when the update request fails without exposing server details', async () => {
    await loadRows()
    await rowCheckbox(1).setValue(true)
    batchUpdateProtocol.mockRejectedValue({ response: { data: { detail: 'sensitive backend detail' } } })
    await openSwitch()
    protocolDialog().vm.$emit('confirm')
    await flushPromises()

    expect(rowCheckbox(1).element.checked).toBe(true)
    expect(showError).toHaveBeenCalledWith('admin.proxies.batchProtocolFailed')
  })
})

afterEach(() => wrapper?.unmount())

describe('proxy list filter pagination', () => {
  it.each([
    ['protocol', 'admin.proxies.allProtocols', 'socks5', ''],
    ['protocol', 'admin.proxies.allProtocols', '', 'http'],
    ['status', 'admin.proxies.allStatus', 'expired', ''],
    ['status', 'admin.proxies.allStatus', '', 'active']
  ])('starts at page one when changing %s through %s to "%s"', async (field, placeholder, value, initial) => {
    wrapper = mountView()
    await flushPromises()
    const filter = wrapper.get(`select[data-filter="${placeholder}"]`)
    if (initial) {
      await filter.setValue(initial)
      await flushPromises()
    }
    await wrapper.get('[data-test="page"]').trigger('click')
    await flushPromises()
    expect(listProxies.mock.lastCall?.[0]).toBe(2)
    const [, pageSize, previousFilters] = listProxies.mock.lastCall!
    listProxies.mockImplementation(async (page) => ({
      items: page === 1 ? [{ id: 1, name: 'matching-proxy' }] : [],
      total: 1,
      pages: 1
    }))

    await filter.setValue(value)
    await flushPromises()

    expect(listProxies).toHaveBeenLastCalledWith(
      1, pageSize, { ...previousFilters, [field]: value || undefined },
      { signal: expect.any(AbortSignal) }
    )
    expect(wrapper.get('[data-test="page"]').text()).toBe('1')
    expect(wrapper.get('[data-test="rows"]').text()).toBe('matching-proxy')
  })

  it('keeps the current page when refreshing the same filter', async () => {
    wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="page"]').trigger('click')
    await flushPromises()

    await wrapper.get('button[title="common.refresh"]').trigger('click')
    await flushPromises()

    expect(listProxies.mock.lastCall?.[0]).toBe(2)
    expect(wrapper.get('[data-test="page"]').text()).toBe('2')
  })
})
