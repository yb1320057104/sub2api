import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import promptAudit from './promptAudit'
import plugins from './plugins'
import auditCenter from './auditCenter'
import moderationUsers from './moderationUsers'
import groupCacheTest from './groupCacheTest'
import requestCapture from './requestCapture'
import astraGateway from './astraGateway'
import safetyPromptAudit from './safetyPromptAudit'
import harvestFlow from './harvestFlow'

export default {
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...promptAudit,
  ...plugins,
  ...auditCenter,
  ...moderationUsers,
  ...groupCacheTest,
  ...requestCapture,
  ...astraGateway,
  ...safetyPromptAudit,
  ...harvestFlow
}
