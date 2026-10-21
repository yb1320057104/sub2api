// Load from a fixed reviewed private pipeline branch, never from SOURCE_REF.
// The controller runs no builds. pk-builder uses a dedicated build Docker daemon.
pipeline {
    agent { label 'pk-builder' }
    options {
        skipDefaultCheckout(true)
        disableConcurrentBuilds()
        timestamps()
        timeout(time: 90, unit: 'MINUTES')
        buildDiscarder(logRotator(numToKeepStr: '30', artifactNumToKeepStr: '10'))
    }
    parameters {
        choice(name: 'ACTION', choices: ['release', 'package', 'publish'], description: 'release：正式标签打包、制品运行时验收并发布，无需源码测试报告或 notes；package：仅打包；publish：发布已有合格制品，不重新编译。')
        gitParameter(name: 'SOURCE_REF', type: 'PT_BRANCH_TAG', defaultValue: '',
            branchFilter: '^origin/.*$', tagFilter: 'v*-pk.*', selectedValue: 'NONE',
            sortMode: 'DESCENDING_SMART', quickFilterEnabled: true,
            useRepository: '^git@github[.]com:pengkang0322/sub2api[.]git$',
            description: 'release 请选择尚未发布的正式 vX.Y.Z-pk.N 标签（不带 origin/）；origin/ 开头的私库分支仅供 package 候选打包。publish 忽略此项。')
        string(name: 'COMMIT_SHA', defaultValue: '', description: '高级选项，通常留空。填写完整的 40 位小写提交 SHA 时，覆盖上方选择，仅打候选包。')
        string(name: 'PACKAGE_ID', defaultValue: '', description: 'release/package 留空；仅 publish 填写已封存且验收合格、尚未发布的产物 ID。')
    }
    environment {
        PK_REPOSITORY = 'pengkang0322/sub2api'
        PK_PACKAGE_ROOT = '/srv/jenkins-packages/sub2api'
        PK_EVIDENCE_ROOT = '/srv/jenkins-evidence/sub2api'
        PK_IMAGE = 'ghcr.io/pengkang0322/sub2api'
    }
    stages {
        stage('Load reviewed pipeline') {
            steps {
                deleteDir()
                dir('pipeline') { checkout scm }
                script {
                    if (params.ACTION in ['release', 'package']) {
                        def commit = params.COMMIT_SHA?.trim() ?: ''
                        def selected = params.SOURCE_REF?.trim() ?: ''
                        if (commit) {
                            if (params.ACTION == 'release') {
                                error('release 必须选择正式标签，COMMIT_SHA 留空')
                            }
                            if (!(commit ==~ /[0-9a-f]{40}/)) {
                                error('COMMIT_SHA 必须是完整的 40 位小写提交 SHA')
                            }
                            env.SOURCE_REF = commit
                        } else if (selected.startsWith('origin/')) {
                            env.SOURCE_REF = 'refs/heads/' + selected.substring('origin/'.length())
                        } else if (selected ==~ /v[0-9]+\.[0-9]+\.[0-9]+-pk\.[1-9][0-9]*/) {
                            env.SOURCE_REF = 'refs/tags/' + selected
                        } else if (selected.startsWith('refs/heads/') || selected.startsWith('refs/tags/') || selected ==~ /[0-9a-f]{40}/) {
                            // Preserve already queued builds from the previous text parameter.
                            env.SOURCE_REF = selected
                        } else {
                            error('请选择有效的私库分支或正式版本标签')
                        }
                    }
                }
                sh 'python3 pipeline/deploy/jenkins/control.py parameters'
            }
        }
        stage('Checkout private source') {
            when { expression { params.ACTION in ['release', 'package'] } }
            steps {
                dir('source') {
                    script {
                        def refs = '+refs/heads/*:refs/remotes/origin/* +refs/tags/*:refs/tags/*'
                        checkout([$class: 'GitSCM', branches: [[name: env.SOURCE_REF]],
                            userRemoteConfigs: [[url: 'git@github.com:pengkang0322/sub2api.git', credentialsId: 'pk-github-read',
                                refspec: refs]],
                            extensions: [[$class: 'CloneOption', shallow: false, noTags: false, honorRefspec: true, timeout: 20]]])
                    }
                }
                sh 'python3 pipeline/deploy/jenkins/control.py prepare'
            }
        }
        stage('Build Linux amd64 package') {
            when { expression { params.ACTION in ['release', 'package'] } }
            steps {
                sh 'bash pipeline/deploy/jenkins/package.sh'
                sh 'python3 pipeline/deploy/jenkins/control.py inventory'
            }
        }
        stage('Validate exact release artifacts') {
            when { expression { params.ACTION == 'release' } }
            steps {
                // Trusted script, dedicated daemon; no publishing credentials or production mounts.
                sh 'python3 pipeline/deploy/jenkins/verify-runtime.py --artifact-dir output --output output/artifact-validation.json'
            }
        }
        stage('Seal package and evidence') {
            when { expression { params.ACTION in ['release', 'package'] } }
            steps {
                sh 'python3 pipeline/deploy/jenkins/control.py seal'
            }
        }
        stage('Restore existing package') {
            when { expression { params.ACTION == 'publish' } }
            steps {
                sh 'python3 pipeline/deploy/jenkins/control.py restore'
            }
        }
        stage('Check private release gates') {
            when { expression { params.ACTION in ['release', 'publish'] } }
            steps {
                withCredentials([string(credentialsId: 'pk-github-publish', variable: 'PK_API_TOKEN')]) {
                    sh 'python3 pipeline/deploy/jenkins/control.py ready'
                }
            }
        }
        stage('Publish reviewed private release') {
            when { expression { params.ACTION in ['release', 'publish'] } }
            steps {
                withCredentials([
                    string(credentialsId: 'pk-github-publish', variable: 'PK_API_TOKEN'),
                    usernamePassword(credentialsId: 'pk-ghcr-publish', usernameVariable: 'PK_REGISTRY_USER', passwordVariable: 'PK_REGISTRY_TOKEN')
                ]) {
                    sh 'python3 pipeline/deploy/jenkins/control.py publish'
                }
            }
        }
    }
    post {
        always {
            archiveArtifacts artifacts: 'output/*', allowEmptyArchive: true, fingerprint: true
        }
        cleanup {
            // Only this build's workspace is removed; sealed packages and evidence remain.
            deleteDir()
        }
    }
}
