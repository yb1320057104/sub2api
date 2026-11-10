import adminAccessKeys from './adminAccessKeys'
import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import binanceRecharge from './binanceRecharge'
import securityPolicyRanking from './securityPolicyRanking'
import intelligenceTest from './intelligenceTest'
import accountCandy from './accountCandy'
import accountHealth from './accountHealth'
import radarEmail from './radarEmail'
import authorization from './authorization'
import admin from './admin'
import misc from './misc'
import autoCredit from './autoCredit'
import accountPerformance from './accountPerformance'
import apiKeyModelPolicy from './apiKeyModelPolicy'
import lottery from './lottery'
import accountTestTiming from './accountTestTiming'
import openaiAuth from './openaiAuth'
import upstreamEntities from './upstreamEntities'
import profitOps from './profitOps'
import upstreamOps from './upstreamOps'
import channelOps from './channelOps'
import ipAudit from './ipAudit'
import traffic from './traffic'
import connections from './connections'
import securityRecords from './securityRecords'
import siwc from './siwc'
import priorityScheduling from './priorityScheduling'
import qualityOps from './qualityOps'
import controlledExperiments from './controlledExperiments'
import accountOps from './accountOps'
import aiOps from './aiOps'
import aiOpsControl from './aiOpsControl'
import tokenGuard from './tokenGuard'
import pelicanTests from './pelicanTests'
import modelTrace from './modelTrace'
import tokenGuardV2 from './tokenGuardV2'
import channelMonitorV3 from './channelMonitorV3'
import supportTickets from './supportTickets'
import requestTiming from './requestTiming'
import latencyObservability from './latencyObservability'
import autoConfig from './autoConfig'

export default {
  adminAccessKeys,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  ...binanceRecharge,
  ...securityPolicyRanking,
  ...intelligenceTest,
  ...accountCandy,
  ...accountHealth,
  ...radarEmail,
  ...authorization,
  admin,
  ...misc,
  apiKeyModelPolicy,
  autoCredit,
  accountPerformance,
  lottery,
  accountTestTiming,
  openaiAuth,
  upstreamEntities,
  traffic,
  ipAudit,
  connections,
  securityRecords,
  siwc,
  autoConfig,
  priorityScheduling,
  qualityOps,
  channelOps,
  upstreamOps,
  profitOps,
  controlledExperiments,
  accountOps,
  aiOps,
  aiOpsControl,
  tokenGuard,
  pelicanTests,
  modelTrace,
  tokenGuardV2,
  requestTiming,
  latencyObservability,
  ...channelMonitorV3,
  ...supportTickets
}
