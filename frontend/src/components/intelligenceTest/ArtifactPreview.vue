<template>
  <iframe
    :srcdoc="document"
    :title="title"
    sandbox=""
    referrerpolicy="no-referrer"
    loading="lazy"
    class="h-44 w-full border-0 bg-white"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import DOMPurify from 'dompurify'

const props = defineProps<{ artifact: string; title: string }>()

const animationTags = new Set(['animate', 'set', 'animatecolor', 'animatemotion', 'animatetransform'])
const animationAttributes = new Set([
  'x', 'y', 'x1', 'y1', 'x2', 'y2', 'cx', 'cy', 'r', 'rx', 'ry', 'width', 'height',
  'd', 'points', 'transform', 'opacity', 'fill', 'fill-opacity', 'stroke', 'stroke-width',
  'stroke-opacity', 'stroke-dasharray', 'stroke-dashoffset', 'stroke-miterlimit',
  'color', 'font-size', 'letter-spacing', 'word-spacing', 'viewbox', 'rotate',
  'offset', 'stop-color', 'stop-opacity', 'display', 'visibility',
])

const document = computed(() => {
  // Model output never joins the application's DOM. Strip active/navigation
  // content before placing it in an opaque-origin, scriptless sandbox.
  const fragment = DOMPurify.sanitize(props.artifact, {
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    // DOMPurify's SVG profile excludes animate/set/use and from/to/calcmode by
    // default. Permit them only with the additional SMIL checks below.
    ADD_TAGS: ['style', 'animate', 'set', 'use'],
    ADD_ATTR: ['from', 'to', 'calcmode'],
    FORBID_TAGS: ['script', 'iframe', 'frame', 'object', 'embed', 'base', 'meta', 'link', 'form', 'a', 'area', 'foreignObject'],
    FORBID_ATTR: ['srcdoc', 'action', 'formaction', 'target', 'ping', 'autofocus'],
    FORCE_BODY: true,
    RETURN_DOM_FRAGMENT: true,
  })
  for (const element of fragment.querySelectorAll('*')) {
    const tag = element.localName.toLowerCase()
    if (animationTags.has(tag)) {
      const attribute = (element.getAttribute('attributeName') ?? element.getAttribute('attributename') ?? '').trim().toLowerCase()
      const safeAttribute = tag === 'animatemotion'
        ? !attribute || attribute === 'transform'
        : tag === 'animatetransform'
          ? attribute === 'transform'
          : animationAttributes.has(attribute)
      const resourceValue = ['from', 'to', 'by', 'values'].some(name => /url\s*\(|\\|@/i.test(element.getAttribute(name) ?? ''))
      // Never let SMIL rewrite href/src, event handlers, style, navigation or
      // another animation's attributeName. Only geometry and appearance move.
      if (!safeAttribute || resourceValue || element.namespaceURI !== 'http://www.w3.org/2000/svg') {
        element.remove()
        continue
      }
    }
    for (const attr of Array.from(element.attributes)) {
      const name = attr.name.toLowerCase()
      if (['href', 'xlink:href', 'src', 'srcset', 'poster', 'background'].includes(name)) {
        // Fragment references support SVG gradients/use; only inline raster
        // images are accepted. CSP separately rejects external CSS resources.
        const value = attr.value.trim()
        const localReference = /^#[\w:.-]+$/.test(value)
        const rasterImage = /^data:image\/(?:png|jpeg|gif|webp);base64,[a-z0-9+/=\s]+$/i.test(value)
        const localOnly = animationTags.has(tag) || tag === 'use' || tag === 'mpath'
        if (!localReference && (localOnly || !rasterImage)) {
          element.removeAttribute(attr.name)
        }
      }
    }
  }
  // Keep serialization in an inert document: even a surviving resource element
  // must never be adopted into the application's browsing document.
  const body = window.document.implementation.createHTMLDocument('').createElement('div')
  body.append(fragment)
  const policy = "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; font-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"
  return `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="${policy}"><meta charset="utf-8"><meta name="referrer" content="no-referrer"><style>html,body{margin:0;height:100%;background:#fff}body{display:grid;place-items:center;padding:8px;box-sizing:border-box;overflow:auto}svg{max-width:100%;max-height:156px}*{box-sizing:border-box}</style></head><body>${body.innerHTML}</body></html>`
})
</script>
