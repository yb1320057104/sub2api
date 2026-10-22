import { defineStore } from 'pinia'
import { ref } from 'vue'
import { intelligenceTestAPI } from '@/api/intelligenceTest'
import { useAuthStore } from './auth'

export const useIntelligenceTestStore = defineStore('intelligenceTest', () => {
  const enabled = ref(false)
  const loaded = ref(false)
  let lastLoadedAt = 0
  let identity: number | undefined
  let pending: Promise<boolean> | null = null
  let generation = 0

  function reset() {
    generation++
    enabled.value = false
    loaded.value = false
    lastLoadedAt = 0
    pending = null
  }

  function setEnabled(value: boolean) {
    generation++
    pending = null
    identity = useAuthStore().user?.id
    enabled.value = value
    loaded.value = true
    lastLoadedAt = Date.now()
  }

  async function refresh(force = false): Promise<boolean> {
    const auth = useAuthStore()
    if (!auth.isAuthenticated || identity !== auth.user?.id) {
      reset()
      identity = auth.user?.id
    }
    if (!auth.isAuthenticated) return false
    if (pending) return pending
    if (!force && loaded.value && Date.now() - lastLoadedAt < 15000) return enabled.value
    const currentGeneration = generation
    pending = intelligenceTestAPI.status().then((status) => {
      if (currentGeneration !== generation) return enabled.value
      enabled.value = status.enabled === true
      loaded.value = true
      lastLoadedAt = Date.now()
      return enabled.value
    }).catch(() => {
      if (currentGeneration === generation) {
        enabled.value = false
        loaded.value = false
      }
      return false
    }).finally(() => {
      if (currentGeneration === generation) pending = null
    })
    return pending
  }

  return { enabled, loaded, refresh, setEnabled, reset }
})
