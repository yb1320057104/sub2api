#!/bin/bash
# The upstream preparer generated a new stack and database/JWT secrets. It cannot
# safely prepare the private PK overlay for an existing production deployment.
# Use the authenticated private-repository workflow in PK_MAINTENANCE.md.
set -e

main() {
    cat >&2 <<'EOF'
此旧 Docker 初始化脚本已停用，避免覆盖“小典专属”的现有服务配置与密钥。
请登录 pengkang0322/sub2api 并按私库维护文档准备部署或恢复：
https://github.com/pengkang0322/sub2api/blob/main/PK_MAINTENANCE.md

使用已确认版本的 ghcr.io/pengkang0322/sub2api 私有镜像。
保留现有 Compose、数据库、Redis、数据卷和密钥，合并 deploy/docker-compose.pk.yml。
deploy/.env.pk.example 仅是示例；实际 UPDATE_GITHUB_TOKEN 应通过服务器私有环境配置注入。
网页更新 token 需要本私库 Contents: read；拉取私有镜像需要先登录 ghcr.io。
本脚本没有下载文件、生成新密码、改动服务或回退到官方镜像。
EOF
    return 1
}

main "$@"
