import type { LocationQuery } from 'vue-router'
import { NEW_FEATURE_NOTICES } from '@/constants/newFeatureNotices'

export const SMART_OPERATIONS_PATH = '/admin/smart-ops'
export const SMART_OPERATIONS_ROUTE_NAME = 'AdminSmartOperations'
export const SMART_OPERATION_TOOLS = [
  { id: 'radar', path: '/admin/radar', label: 'radar.title', common: true },
  { id: 'credentials', path: '/admin/token-guard-v2', label: 'tokenGuardV2.title', common: true, noticeId: NEW_FEATURE_NOTICES.credentialOperationsUnified },
  { id: 'notifications', path: '/admin/account-ops', label: 'smartOps.notifications.title', common: true },
  { id: 'quality', path: '/admin/account-quality', label: 'qualityOps.title', noticeId: NEW_FEATURE_NOTICES.qualityRetestRest },
  { id: 'auto-config', path: '/admin/auto-config', label: 'autoConfig.title', noticeId: NEW_FEATURE_NOTICES.oauthInitialModelSelection },
  { id: 'priority', path: '/admin/priority-scheduling', label: 'priorityScheduling.title' },
  { id: 'channels', path: '/admin/channel-operations', label: 'channelOps.title' },
  { id: 'profit', path: '/admin/profit-operations', label: 'profitOps.title' },
  { id: 'experiments', path: '/admin/controlled-experiments', label: 'controlledExperiments.title' },
  { id: 'external-guard', path: '/admin/token-guard', label: 'tokenGuard.title' },
  { id: 'pelican', path: '/admin/pelican-tests', label: 'pelicanTests.title' },
  { id: 'cache-tests', path: '/admin/group-cache-tests', label: 'admin.groupCacheTest.title' },
  { id: 'captures', path: '/admin/request-captures', label: 'admin.requestCapture.title' },
  { id: 'ai', path: '/admin/ai-ops', label: 'aiOps.title', noticeId: NEW_FEATURE_NOTICES.aiOpsCodex },
  { id: 'harvest', path: '/admin/harvest-flow', label: 'nav.harvestFlow' },
  { id: 'lottery', path: '/admin/lottery', label: 'lottery.adminTitle' },
] as const

export type SmartOperationTool = typeof SMART_OPERATION_TOOLS[number]['id']
export interface SmartOperationEntry {
  id: SmartOperationTool
  path: string
  label: string
  common?: boolean
  noticeId?: string
}

export function smartOperationTool(value: unknown): SmartOperationTool {
  return SMART_OPERATION_TOOLS.find(tool => tool.id === value)?.id ?? 'radar'
}

export function smartOperationLocation(tool: SmartOperationTool, query: LocationQuery = {}, hash = '') {
  return { path: SMART_OPERATIONS_PATH, query: { ...query, tool }, hash }
}

function normalizedSmartOperationPath(path: string): string {
  // Vue Router's default matcher accepts case differences and a trailing slash.
  // Redirect destinations should depend on that matched path, not its spelling.
  return path.toLowerCase().replace(/\/+$/, '')
}

// Preserve bookmarks, filters and anchors. Each tool's tab is independent of
// the tool selector, including the former radar authorization/email tabs.
export function legacySmartOperationsLocation(route: { path: string; query: LocationQuery; hash?: string }) {
  const query = { ...route.query }
  const path = normalizedSmartOperationPath(route.path)
  let tool = SMART_OPERATION_TOOLS.find(item => item.path === path)?.id ?? 'radar'
  if (path === '/admin/account-authorization') {
    tool = 'credentials'
    query.tab = query.tab === 'history' || query.tab === 'proxies' ? query.tab : 'workbench'
  } else if (path === '/admin/radar' && query.tab === 'authorization') {
    tool = 'credentials'
    query.tab = 'inspection'
  } else if (path === '/admin/radar' && query.tab === 'emails') {
    tool = 'notifications'
    query.notification = 'overview'
    delete query.tab
  } else if (path === '/admin/account-ops') {
    query.notification = 'accounts'
  }
  return smartOperationLocation(tool, query, route.hash)
}

export function smartOperationForRoute(route: { path: string; name?: unknown; query: LocationQuery }): SmartOperationTool {
  if (route.name === SMART_OPERATIONS_ROUTE_NAME || normalizedSmartOperationPath(route.path) === SMART_OPERATIONS_PATH) return smartOperationTool(route.query.tool)
  return smartOperationTool(legacySmartOperationsLocation(route).query.tool)
}

export function visibleSmartOperationTools(options: {
  hiddenPaths: readonly string[]
  requestCaptureEnabled: boolean
  isAdmin: boolean
  simpleMode?: boolean
}): SmartOperationEntry[] {
  return SMART_OPERATION_TOOLS.filter(tool =>
    // Notifications now include the former radar alerts. Hiding the old
    // account-notification menu must not hide that combined common entry.
    (tool.id === 'notifications' || !options.hiddenPaths.includes(tool.path))
    && (tool.id !== 'captures' || options.requestCaptureEnabled)
    && (tool.id !== 'ai' || options.isAdmin)
    && (tool.id !== 'lottery' || !options.simpleMode),
  )
}
