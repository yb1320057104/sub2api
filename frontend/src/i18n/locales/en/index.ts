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
import authorization from './authorization'
import admin from './admin'
import misc from './misc'

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
  ...authorization,
  admin,
  ...misc,
}
