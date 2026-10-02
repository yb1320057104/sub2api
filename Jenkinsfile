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
        choice(name: 'ACTION', choices: ['package', 'publish'], description: 'package：打包；publish：发布已打包且通过本地验证的正式版本，不重新编译。')
        gitParameter(name: 'SOURCE_REF', type: 'PT_BRANCH_TAG', defaultValue: 'origin/main',
            branchFilter: '^origin/.*$', tagFilter: 'v*-pk.*', selectedValue: 'DEFAULT',
            sortMode: 'DESCENDING_SMART', quickFilterEnabled: true,
            useRepository: '^git@github[.]com:pengkang0322/sub2api[.]git$',
            description: '选择私库分支或版本标签，可搜索。origin/ 开头为分支（候选包），v 开头为正式版本标签。仅 package 使用。')
        string(name: 'COMMIT_SHA', defaultValue: '', description: '高级选项，通常留空。填写完整的 40 位小写提交 SHA 时，覆盖上方选择，仅打候选包。')
        string(name: 'PACKAGE_ID', defaultValue: '', description: '打包时留空。仅 publish 时填写已成功打包并通过本地验证的产物 ID。')
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
                    if (params.ACTION == 'package') {
                        def commit = params.COMMIT_SHA?.trim() ?: ''
                        def selected = params.SOURCE_REF?.trim() ?: ''
                        if (commit) {
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
            when { expression { params.ACTION == 'package' } }
            steps {
                dir('source') {
                    checkout([$class: 'GitSCM', branches: [[name: env.SOURCE_REF]],
                        userRemoteConfigs: [[url: 'git@github.com:pengkang0322/sub2api.git', credentialsId: 'pk-github-read',
                            refspec: '+refs/heads/*:refs/remotes/origin/* +refs/tags/*:refs/tags/*']],
                        extensions: [[$class: 'CloneOption', shallow: false, noTags: false, timeout: 20]]])
                }
                sh 'python3 pipeline/deploy/jenkins/control.py prepare'
            }
        }
        stage('Build Linux amd64 package') {
            when { expression { params.ACTION == 'package' } }
            steps {
                sh 'bash pipeline/deploy/jenkins/package.sh'
                sh 'python3 pipeline/deploy/jenkins/control.py seal'
            }
        }
        stage('Check existing package and local evidence') {
            when { expression { params.ACTION == 'publish' } }
            steps {
                sh 'python3 pipeline/deploy/jenkins/control.py restore'
                withCredentials([string(credentialsId: 'pk-github-publish', variable: 'PK_API_TOKEN')]) {
                    sh 'python3 pipeline/deploy/jenkins/control.py ready'
                }
            }
        }
        stage('Publish reviewed private release') {
            when { expression { params.ACTION == 'publish' } }
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
