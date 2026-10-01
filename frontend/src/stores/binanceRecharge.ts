import { defineStore } from 'pinia'
import { ref } from 'vue'
import { binanceRechargeAPI } from '@/api/binanceRecharge'
import type { BinanceRechargeConfig } from '@/types/binanceRecharge'

export const useBinanceRechargeStore = defineStore('binanceRecharge', () => {
  const config = ref<BinanceRechargeConfig | null>(null)
  let request: Promise<BinanceRechargeConfig | null> | null = null

  function fetchConfig(force = false): Promise<BinanceRechargeConfig | null> {
    if (request) return request
    if (config.value && !force) return Promise.resolve(config.value)
    request = binanceRechargeAPI.getConfig()
      .then(({ data }) => { config.value = data; return data })
      .catch(() => { config.value = null; return null })
      .finally(() => { request = null })
    return request
  }

  return { config, fetchConfig }
})
