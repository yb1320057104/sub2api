export default {
  authorization: {
    encryptionMissing: '请先配置并持久保存 TOTP_ENCRYPTION_KEY，才能保存账号凭据和授权代理。未固定密钥时，重启会导致历史无法解密。',
    title: '账号授权', description: '批量提交邮箱、密码和2FA，使用本项目授权链接完成入库；历史凭据供账号巡检后的统一重授权使用。',
    tabs: { workbench: '工作台', proxies: '授权代理', history: '凭据历史' },
    schedule: '授权巡检调度', scheduleHint: '服务端统一 cron 调度，不依赖页面打开；正在执行的任务不会重复启动，逾期轮次在空闲后补跑。',
    inspectionEnabled: '启用账号巡检', reauthEnabled: '启用统一重授权', inspectionCron: '每3分钟巡检', reauthCron: '每10分钟重授权',
    inspectionConcurrency: '巡检并发', reauthConcurrency: '重授权并发', authorizationConcurrency: '本批授权并发', accountConcurrency: '账号并发', priority: '账号优先级', groups: '入库分组',
    statusCodes: '触发重授权的 HTTP 状态码', statusCodesHint: '默认401，可填写多个400～599错误码，以逗号分隔。账号管理已有匹配错误时跳过探测；网络失败不等于授权失效。', invalidCodes: '请填写不重复的400～599整数状态码。',
    next: '下次计划', off: '未启用', latestAccountsHint: '每轮开始重新读取账号管理。只巡检真实 OpenAI OAuth 账号；手动新增账号在下一轮按条件加入，已删除账号不参与。',
    browserMissing: '未找到外置授权浏览器。请在应用运行环境安装 Chromium，Docker 部署可执行 deploy/install-browser.sh，也可设置 PK_AUTH_BROWSER_PATH。浏览器不再随镜像或更新包交付；重建容器后需重新安装，普通重启和网页更新不受影响。',
    openWorkbench: '打开授权工作台', openInspection: '查看授权巡检', accountInput: '账号密码与2FA', accountPlaceholder: '邮箱----密码----2FA密钥\n每行一个账号，也支持制表符分隔',
    accountInputHint: '每批最多1000条。2FA填写验证器密钥，不是六位验证码；自动重授权要求完整密码与2FA。提交后加密保存到历史，授权任务按配置并发执行。',
    noGroups: '暂无可选 OpenAI 分组，可先在分组管理添加。', importHint: '新账号负载因子与账号并发一致。重授权原账号时保留原分组、优先级、并发、代理和二开配置。',
    submit: '保存凭据并提交任务', submitting: '正在提交…', submitted: '凭据已保存，授权任务已提交', jobs: '授权任务', refreshHint: '仅刷新状态，不触发授权',
    proxyInput: '授权代理（每行一条）', proxyHint: '导入仅保存代理，不会自动批量预检。可点击每条的 GET预检；实际授权前也会使用与浏览器版本一致的身份经该出口检查 chatgpt.com。失败时展示具体原因，绝不直连兜底。账号巡检仍用账号原代理。', saveProxies: '加密保存代理',
    historyHint: '保留每次提交与补全的历史版本。自动重授权按邮箱读取最新版本，密码或2FA不全时跳过。列表默认遮蔽密码和2FA；查看内容沿用系统敏感操作验证。',
    searchEmail: '搜索邮箱或代理地址', filter: '状态筛选', search: '搜索', refresh: '刷新', proxyEndpoint: '授权出口', account: '账号', status: '状态', credentials: '凭据', details: '说明', time: '时间', actions: '操作',
    password: '账号密码', twofa: '2FA密钥', email: '账号邮箱', incomplete: '未填写', missingHint: '账号凭据不够，已跳过授权。请补齐密码与2FA。', supplemented: '已补齐，等待下次重授权任务', historyReady: '完整凭据已保存', historyMissing: '账号凭据不够', authorizedAt: '最近授权',
    proxyUnchecked: '尚未预检',
    checking: '预检中…', checkProxy: 'GET预检', disable: '停用', cancel: '取消任务', viewCredentials: '查看凭据', supplement: '补全凭证', supplementHint: '已有邮箱保持绑定；缺少邮箱时可补填。保存后记入凭据历史，不立即授权；下一次重授权任务会读取这份凭据。', saveCredentials: '保存补全凭据', hide: '隐藏内容', show: '显示内容',
    total: '共 {count} 条', pagination: '记录分页', saved: '已保存', loadFailed: '读取失败，请刷新重试', saveFailed: '保存失败，请检查输入后重试', operationFailed: '操作未完成，请重试',
    source: { submission: '工作台提交', supplement: '巡检补全', manual: '手动授权', reauthorization: '定时重授权' },
    bannedHint: '官方已确认账号停用、删除或封禁，自动重授权已跳过。',
    states: { banned: '账号已封禁', all: '全部状态', unknown: '待巡检', authorized: '授权正常', expired: '授权已掉，等待重授权', missing_credentials: '账号凭据不够', credentials_completed: '已补齐，等待下次重授权任务', queued: '等待授权', authorizing: '正在授权', running: '正在执行', error: '探测异常', unsupported: '无需密码授权巡检', passed: '预检通过', failed: '失败', needs_input: '需要人工处理', cancelled: '已取消', completed: '授权成功', saved: '已保存', cache_pending: '凭据已保存，等待缓存刷新' },
    empty: { jobs: '暂无任务。提交账号后可查看授权进度。', proxies: '尚未配置授权代理，请在上方添加。', history: '暂无历史。工作台提交和巡检补全的凭据会保存在这里。', accounts: '当前账号管理中没有匹配的 OpenAI 账号。' }
  }
}
