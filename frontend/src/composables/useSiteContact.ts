import { computed } from 'vue'
import { useAppStore } from '@/stores'

export function useSiteContact() {
  const appStore = useAppStore()
  const contactText = computed(() =>
    (appStore.cachedPublicSettings?.contact_info ?? appStore.contactInfo ?? '').trim(),
  )
  const contactQRCode = computed(() => {
    const value = (appStore.cachedPublicSettings?.contact_qr_code ?? appStore.contactQRCode ?? '').trim()
    // Contact QR codes are uploaded raster images, never remote or inline SVG content.
    return value.length <= 3 * 1024 * 1024
      && /^data:image\/(?:png|jpeg|webp);base64,[A-Za-z0-9+/]+={0,2}$/.test(value)
      ? value : ''
  })
  const hasContact = computed(() => Boolean(contactText.value || contactQRCode.value))
  return { contactText, contactQRCode, hasContact }
}
