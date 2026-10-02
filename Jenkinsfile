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
        choice(name: 'ACTION', choices: ['package', 'publish'], description: 'package only builds artifacts; publish promotes an existing, locally tested package without rebuilding.')
        string(name: 'SOURCE_REF', defaultValue: 'refs/heads/main', description: 'Private branch, complete commit SHA, or reviewed refs/tags/vX.Y.Z-pk.N. Used only by package.')
        string(name: 'PACKAGE_ID', defaultValue: '', description: 'Existing package ID from a successful package build. Required by publish.')
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
                sh 'python3 pipeline/deploy/jenkins/control.py parameters'
            }
        }
        stage('Checkout private source') {
            when { expression { params.ACTION == 'package' } }
            steps {
                dir('source') {
                    checkout([$class: 'GitSCM', branches: [[name: params.SOURCE_REF]],
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
