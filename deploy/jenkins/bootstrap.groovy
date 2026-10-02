// Install once in JENKINS_HOME/init.groovy.d; secrets are provisioned out of Git.
import jenkins.model.Jenkins
import hudson.security.HudsonPrivateSecurityRealm
import hudson.security.FullControlOnceLoggedInAuthorizationStrategy
import hudson.slaves.DumbSlave
import hudson.slaves.JNLPLauncher
import hudson.slaves.RetentionStrategy
import hudson.model.Node
import com.cloudbees.plugins.credentials.SystemCredentialsProvider
import com.cloudbees.plugins.credentials.CredentialsScope
import com.cloudbees.jenkins.plugins.sshcredentials.impl.BasicSSHUserPrivateKey
import org.jenkinsci.plugins.workflow.job.WorkflowJob
import org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition
import hudson.plugins.git.GitSCM
import hudson.plugins.git.UserRemoteConfig
import hudson.plugins.git.BranchSpec

def j = Jenkins.get()
def marker = new File(j.rootDir, '.pk-bootstrap-complete')
if (marker.exists()) return
j.setNumExecutors(0)
j.setSlaveAgentPort(-1)
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
  'Read-only deploy key for pengkang0322/sub2api')
SystemCredentialsProvider.getInstance().getCredentials().add(key)
SystemCredentialsProvider.getInstance().save()
def node = new DumbSlave('pk-builder', 'Isolated Sub2API build agent', '/home/jenkins/agent', '1',
  Node.Mode.EXCLUSIVE, 'pk-builder', new JNLPLauncher(), new RetentionStrategy.Always(), [])
j.addNode(node)
new File(j.rootDir, '.pk-agent-secret').text = j.getComputer('pk-builder').getJnlpMac()
def scm = new GitSCM([new UserRemoteConfig('git@github.com:pengkang0322/sub2api.git', null, null, 'pk-github-read')],
  [new BranchSpec('*/codex/jenkins-build')], false, [], null, null, [])
def job = j.createProject(WorkflowJob, 'sub2api-package')
job.setDefinition(new CpsScmFlowDefinition(scm, 'Jenkinsfile'))
job.setDescription('Jenkins builds only. Local test evidence is required before publishing the exact package to the private GitHub Release and GHCR. No production deployment.')
job.save()
j.save()
marker.text = 'Provisioned with anonymous access disabled and zero controller executors.\n'
