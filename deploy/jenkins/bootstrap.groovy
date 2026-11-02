// Install once in JENKINS_HOME/init.groovy.d; secrets are provisioned out of Git.
import jenkins.model.Jenkins
import hudson.security.HudsonPrivateSecurityRealm
import hudson.security.FullControlOnceLoggedInAuthorizationStrategy
import hudson.slaves.DumbSlave
import hudson.slaves.JNLPLauncher
import hudson.slaves.RetentionStrategy
import hudson.model.Node
import hudson.model.ChoiceParameterDefinition
import hudson.model.ParametersDefinitionProperty
import com.cloudbees.plugins.credentials.SystemCredentialsProvider
import com.cloudbees.plugins.credentials.CredentialsScope
import com.cloudbees.jenkins.plugins.sshcredentials.impl.BasicSSHUserPrivateKey
import org.jenkinsci.plugins.workflow.job.WorkflowJob
import org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition
import hudson.plugins.git.GitSCM
import hudson.plugins.git.UserRemoteConfig
import hudson.plugins.git.BranchSpec
import hudson.plugins.locale.PluginImpl

def j = Jenkins.get()
def marker = new File(j.rootDir, '.pk-bootstrap-complete')
if (marker.exists()) return
def locale = PluginImpl.get()
locale.setSystemLocale('zh_CN')
locale.setIgnoreAcceptLanguage(true)
locale.setAllowUserPreferences(false)
locale.save()
j.setNumExecutors(0)
j.setSlaveAgentPort(-1)
jenkins.model.JenkinsLocationConfiguration.get().setUrl('http://127.0.0.1:18081/')
def realm = new HudsonPrivateSecurityRealm(false)
def admin = realm.createAccount('admin', new File('/run/bootstrap/admin-password').text.trim())
j.setSecurityRealm(realm)
def auth = new FullControlOnceLoggedInAuthorizationStrategy()
auth.setAllowAnonymousRead(false)
j.setAuthorizationStrategy(auth)
def apiToken = admin.getProperty(jenkins.security.ApiTokenProperty).tokenStore.generateNewToken('local-provisioning')
new File(j.rootDir, '.pk-provisioning-token').text = apiToken.plainValue
admin.save()
def key = new BasicSSHUserPrivateKey(CredentialsScope.GLOBAL, 'pk-github-read', 'git',
  new BasicSSHUserPrivateKey.DirectEntryPrivateKeySource(new File('/run/bootstrap/github-read').text), '',
  'Read-only deploy key for yb1320057104/sub2api')
SystemCredentialsProvider.getInstance().getCredentials().add(key)
SystemCredentialsProvider.getInstance().save()
def node = new DumbSlave('pk-builder', 'Isolated Sub2API build agent', '/home/jenkins/agent', '1',
  Node.Mode.EXCLUSIVE, 'pk-builder', new JNLPLauncher(), new RetentionStrategy.Always(), [])
j.addNode(node)
new File(j.rootDir, '.pk-agent-secret').text = j.getComputer('pk-builder').getJnlpMac()
def scm = new GitSCM([new UserRemoteConfig('git@github.com:yb1320057104/sub2api.git', null, null, 'pk-github-read')],
  [new BranchSpec('*/main')], false, [], null, null, [])
def job = j.createProject(WorkflowJob, 'sub2api-package')
job.setDefinition(new CpsScmFlowDefinition(scm, 'Jenkinsfile'))
job.addProperty(new ParametersDefinitionProperty(new ChoiceParameterDefinition(
  'REINSTALL_NODE_MODULES', ['否', '是'] as String[],
  '是否重新安装 node_modules：默认否；是会删除本次构建的 node_modules 与 pnpm-lock.yaml，重新解析、审计并封存实际依赖。publish 必须选否。')))
job.setDescription('release builds a formal private tag once, validates the exact artifacts and publishes to private GitHub Release/GHCR. Source-test notes are optional. Dependency reinstallation is opt-in and audited. No production deployment.')
job.save()
j.save()
marker.text = 'Provisioned with anonymous access disabled and zero controller executors.\n'
