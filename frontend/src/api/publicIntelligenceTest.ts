import axios from 'axios'
import type { ApiResponse } from '@/types'
import type { IntelligenceTestDetails, IntelligenceTestGroup, IntelligenceTestMode, IntelligenceTestResult } from './intelligenceTest'
import { getAPIBaseURL } from './url'

export interface PublicIntelligenceTestGroup extends Pick<IntelligenceTestGroup, 'id' | 'name' | 'platform'> {
  modes: IntelligenceTestMode[]
}

export interface PublicIntelligenceTestGroups {
  modes: IntelligenceTestMode[]
  groups: PublicIntelligenceTestGroup[]
}

// Public homepage reads must never refresh a session, attach a bearer token or
// redirect a visitor to login when a test is disabled while they are viewing it.
const publicClient = axios.create({
  baseURL: getAPIBaseURL(),
  timeout: 30000,
  withCredentials: false,
})

async function read<T>(path: string, signal?: AbortSignal, params?: Record<string, string | number>): Promise<T> {
  const response = await publicClient.get<ApiResponse<T>>(`/public/intelligence-test/${path}`, { params, signal })
  if (response.data.code !== 0) throw new Error(response.data.message || 'Could not read public test results')
  return response.data.data
}

export const publicIntelligenceTestAPI = {
  groups(signal?: AbortSignal) {
    return read<PublicIntelligenceTestGroups>('groups', signal)
  },
  results(groupID: number, mode: IntelligenceTestMode, signal?: AbortSignal) {
    return read<IntelligenceTestResult[]>('results', signal, { group_id: groupID, mode })
  },
  artifact(groupID: number, id: number, signal?: AbortSignal) {
    return read<{ artifact: string }>('artifact', signal, { group_id: groupID, id })
  },
  details(groupID: number, id: number, signal?: AbortSignal) {
    return read<Pick<IntelligenceTestDetails, 'prompt' | 'answer'>>('details', signal, { group_id: groupID, id })
  },
}
