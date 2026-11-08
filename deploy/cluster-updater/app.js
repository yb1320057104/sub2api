const el = id => document.getElementById(id)
let current
const labels = {starting:'正在初始化',in_sync:'已跟上最新正式版本',pending_confirmation:'发现新版本，等待确认',newer_than_latest:'运行版本高于最新发布，已禁止降级',check_failed:'检查未完成',building:'正在构建统一版本',deploying:'正在依次更新集群',completed:'集群更新完成',failed:'构建或预检查失败',interrupted:'更新中断，需要检查后恢复'}
const phases = {approved:'已确认',resolving_source:'核对源代码',downloading_source:'下载源代码',building_frontend:'构建页面',building_binary:'构建程序',artifact_ready:'统一制品已就绪',staging_primary:'准备主节点实例',staging_secondary:'准备副节点实例',draining_primary:'等待主节点现有请求完成',draining_secondary:'等待副节点现有请求完成',updating_primary:'更新主节点 API',updating_secondary:'更新副节点 API',updating_workers:'更新后台任务实例',finished:'已结束'}
Object.assign(phases,{merging_cluster:'合并集群兼容代码',installing_frontend_dependencies:'准备页面构建依赖',installing_go_dependencies:'准备程序构建依赖',verifying_go_dependencies:'校验程序依赖',updating_worker_primary:'更新主节点后台任务实例',updating_worker_secondary:'更新副节点后台任务实例',verifying_primary:'复核主节点健康',verifying_secondary:'复核副节点健康'})
const errors={cluster_migrations_require_compatibility_review:'新版本修改了数据库迁移，需先核对滚动更新兼容性；当前应用保持运行。',cluster_overlay_conflict:'新版本与集群兼容代码存在冲突，需处理后再更新。',updater_restarted_review_required:'更新过程中控制器重启，请先检查任务记录再恢复。'}
const errorText=code=>errors[code]||code
async function call(path,payload){
  const token=localStorage.getItem('auth_token')
  if(!token){el('login').hidden=false;throw new Error('请先登录管理员账号')}
  const r=await fetch('/cluster-update/api/'+path,{method:payload?'POST':'GET',headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:payload?JSON.stringify(payload):undefined})
  const data=await r.json()
  if(!r.ok){if(r.status===401||r.status===403)el('login').hidden=false;throw new Error(data.error||'请求失败')}
  el('login').hidden=true;return data
}
function render(s){
  current=s
  const busy=['building','deploying','interrupted'].includes(s.job?.status)
  for(const node of ['primary','secondary']){
    const row=s.nodes?.[node]
    el(node).textContent=row?.version||'未知'
    el(node+'Ready').textContent=row?row.ready?'API 就绪':'API 未就绪':'状态未知'
    el(node+'Worker').textContent=row?`${row.worker_version} · ${row.worker_running?'运行中':'待命'}`:'未知'
  }
  el('status').textContent=labels[busy?s.job.status:s.status]||s.status
  el('latest').textContent=s.latest?.tag||'尚未确认'
  el('checked').textContent=s.checked_at?'上次检查：'+new Date(s.checked_at*1000).toLocaleString():''
  el('target').textContent=busy?`已确认更新到 ${s.job.target.baseline}；${phases[s.job.phase]||s.job.phase}`:s.pending?`统一更新到 ${s.pending.baseline}，包含两台 API 和两台后台任务实例。`:s.status==='in_sync'?'两台 API 和后台任务实例的正式版本基线均为最新。HA 补丁后缀不同不代表需要降级。':'当前没有可确认更新，请查看检查状态。'
  el('approve').hidden=!s.pending&&!busy
  el('approve').disabled=busy||!s.updates_enabled||!s.pending
  el('approve').textContent=busy?'更新任务处理中':s.updates_enabled?'确认统一更新到 '+(s.pending?.baseline||''):'更新功能未启用'
  el('check').disabled=busy
  el('job').textContent=s.job?`${labels[s.job.status]||s.job.status}\n目标：${s.job.target.baseline}\n范围：主、副 API 及后台任务实例\n阶段：${phases[s.job.phase]||s.job.phase}${s.job.error?'\n原因：'+errorText(s.job.error):''}`:'暂无任务'
  el('error').textContent=errorText(s.error)||''
}
async function refresh(){try{render(await call('status'))}catch(e){current=undefined;el('approve').disabled=true;el('status').textContent=e.message;el('error').textContent=e.message}}
el('check').onclick=async()=>{el('check').disabled=true;try{render(await call('check',{}))}catch(e){el('error').textContent=e.message}finally{el('check').disabled=['building','deploying','interrupted'].includes(current?.job?.status)}}
el('approve').onclick=async()=>{const target=current?.pending;if(!target||!current.updates_enabled||el('approve').disabled||!confirm(`确认统一更新到 ${target.baseline}？\n将构建一次，依次更新副、主 API，然后更新两台后台任务实例。`))return;el('approve').disabled=true;try{render(await call('approve',{baseline:target.baseline,tag:target.tag,commit:target.commit,confirmation_id:target.confirmation_id}))}catch(e){el('error').textContent=e.message;await refresh()}}
refresh();setInterval(refresh,5000)
