package securityaudit

import (
	"context"
	"log/slog"
	"strings"
)

const (
	EventConfigUpdated        = "prompt_audit.config_updated"
	EventConfigLoaded         = "prompt_guard.config_loaded"
	EventConfigReloadDegraded = "prompt_guard.config_reload_degraded"
	EventConfigTokenInvalid   = "prompt_guard.config_token_invalid"
	EventProbeStarted         = "prompt_audit.endpoint_probe_started"
	EventProbeFinished        = "prompt_audit.endpoint_probe_finished"
	EventProbeFailed          = "prompt_audit.endpoint_probe_failed"
	EventJobEnqueued          = "prompt_audit.job_enqueued"
	EventEnqueueSkipped       = "prompt_audit.enqueue_skipped"
	EventEnqueueDropped       = "prompt_audit.enqueue_dropped"
	EventAuditStarted         = "prompt_audit.started"
	EventProcessingReclaimed  = "prompt_audit.processing_reclaimed"
	EventProcessed            = "prompt_audit.processed"
	EventProcessFailed        = "prompt_audit.process_failed"
	EventFindingRecorded      = "prompt_audit.finding_recorded"
	EventChunkStarted         = "prompt_audit.scan_chunk_started"
	EventChunkCompleted       = "prompt_audit.scan_chunk_completed"
	EventChunkFailed          = "prompt_audit.scan_chunk_failed"
	EventChunksAggregated     = "prompt_audit.scan_chunks_aggregated"
	EventEvaluationStarted    = "prompt_guard.evaluation_started"
	EventGuardAllowed         = "prompt_guard.allowed"
	EventGuardBlocked         = "prompt_guard.blocked"
	EventGuardFailed          = "prompt_guard.failed"
	EventResultRecordFailed   = "prompt_guard.result_record_failed"
	EventEventDeleted         = "prompt_audit.event_deleted"
	EventEventsDeleted        = "prompt_audit.events_deleted"
	EventDeletePreviewed      = "prompt_audit.events_delete_previewed"
	EventEventsFilterDeleted  = "prompt_audit.events_filter_deleted"
)

var knownLogEvents = map[string]struct{}{
	EventConfigUpdated: {}, EventConfigLoaded: {}, EventConfigReloadDegraded: {}, EventConfigTokenInvalid: {},
	EventProbeStarted: {}, EventProbeFinished: {}, EventProbeFailed: {},
	EventJobEnqueued: {}, EventEnqueueSkipped: {}, EventEnqueueDropped: {},
	EventAuditStarted: {}, EventProcessingReclaimed: {}, EventProcessed: {}, EventProcessFailed: {}, EventFindingRecorded: {},
	EventChunkStarted: {}, EventChunkCompleted: {}, EventChunkFailed: {}, EventChunksAggregated: {},
	EventEvaluationStarted: {}, EventGuardAllowed: {}, EventGuardBlocked: {}, EventGuardFailed: {}, EventResultRecordFailed: {},
	EventEventDeleted: {}, EventEventsDeleted: {}, EventDeletePreviewed: {}, EventEventsFilterDeleted: {},
}

var allowedLogFields = map[string]struct{}{
	"request_id": {}, "user_id": {}, "api_key_id": {}, "group_id": {}, "provider": {},
	"protocol": {}, "endpoint": {}, "model": {}, "job_id": {}, "event_id": {},
	"config_version": {}, "guard_endpoint_id": {}, "decision": {}, "risk_level": {},
	"action": {}, "chunk_index": {}, "chunk_total": {}, "chunk_chars": {}, "input_chars": {},
	"input_limit": {}, "latency_ms": {}, "status": {}, "error_code": {}, "error_kind": {},
	"queue_length": {}, "queue_capacity": {}, "stage": {}, "upstream_dispatched": {},
	"billing_preconsumed": {}, "worker_id": {}, "reclaimed_total": {}, "attempts": {},
	"max_attempts": {}, "claim_version": {}, "http_status": {}, "retryable": {},
}

func LogInfo(event string, fields map[string]any) {
	if _, ok := knownLogEvents[event]; !ok {
		return
	}
	slog.LogAttrs(context.Background(), slog.LevelInfo, event, safeAttrs(fields)...)
}
func LogWarn(event string, fields map[string]any) {
	if _, ok := knownLogEvents[event]; !ok {
		return
	}
	slog.LogAttrs(context.Background(), slog.LevelWarn, event, safeAttrs(fields)...)
}
func LogError(event string, fields map[string]any) {
	if _, ok := knownLogEvents[event]; !ok {
		return
	}
	slog.LogAttrs(context.Background(), slog.LevelError, event, safeAttrs(fields)...)
}

func safeAttrs(fields map[string]any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(fields))
	for key, value := range fields {
		key = strings.TrimSpace(key)
		if _, allowed := allowedLogFields[key]; !allowed {
			continue
		}
		if text, ok := value.(string); ok {
			if key == "error_kind" || key == "error_code" {
				value = stableErrorCode(text)
			} else {
				value = TrimRunes(strings.TrimSpace(text), 256)
			}
		}
		attrs = append(attrs, slog.Any(key, value))
	}
	return attrs
}

func mergeLogFields(base map[string]any, extra map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func requestLogFields(req Request) map[string]any {
	return map[string]any{
		"request_id": req.RequestID, "user_id": req.UserID, "api_key_id": req.APIKeyID,
		"group_id": pointerLogID(req.GroupID), "provider": req.Provider, "protocol": req.Protocol,
		"endpoint": req.Endpoint, "model": req.Model, "stage": req.Stage,
	}
}

func snapshotLogFields(snapshot PromptSnapshot) map[string]any {
	return map[string]any{
		"request_id": snapshot.RequestID, "user_id": snapshot.UserID, "api_key_id": snapshot.APIKeyID,
		"group_id": pointerLogID(snapshot.GroupID), "provider": snapshot.Provider, "protocol": snapshot.Protocol,
		"endpoint": snapshot.Endpoint, "model": snapshot.Model, "stage": snapshot.Stage,
	}
}

func jobLogFields(job *Job) map[string]any {
	if job == nil {
		return map[string]any{}
	}
	fields := snapshotLogFields(job.Snapshot)
	fields["job_id"] = job.ID
	fields["config_version"] = job.ConfigVersion
	fields["claim_version"] = job.ClaimVersion
	return fields
}

func stableErrorCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return "unknown_error"
	}
	for _, char := range code {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return "redacted_error"
	}
	return TrimRunes(code, 64)
}

func stableErrorMessage(code string) string {
	switch stableErrorCode(code) {
	case "audit_node_timeout":
		return "审计节点请求超时，请检查节点响应时间或使用快速模式"
	case "audit_node_connection_failed":
		return "无法连接审计节点，请检查地址、网络和节点服务"
	case "audit_node_authentication_failed":
		return "审计节点拒绝认证（HTTP 401/403），请核对节点密钥和权限"
	case "audit_node_rate_limited":
		return "审计节点限流（HTTP 429），请降低并发或配置备用节点"
	case "audit_node_server_error":
		return "审计节点返回服务错误（HTTP 5xx）"
	case "audit_node_http_error":
		return "审计节点返回非成功状态，请核对地址、模型和接口配置"
	case "no_enabled_endpoint":
		return "没有可用审计节点，请启用节点并检查凭据状态"
	case "audit_token_invalid":
		return "审计节点密钥无法解密，请重新保存节点密钥"
	case "audit_concurrency_full":
		return "审计并发已满，请稍后重试或调整审计模式"
	case ErrorCodeConfigUnavailable:
		return "审计配置尚未加载，请检查配置状态"
	case ErrorCodeBlocked:
		return "Prompt Guard blocked the request"
	case ErrorCodeUnavailable, "payload_store_unavailable", "payload_missing":
		return "Prompt Audit dependency is unavailable"
	case ErrorCodeInvalidResponse:
		return "Prompt Guard returned an invalid response"
	case "queue_full", "queue_admission_busy":
		return "Prompt Audit queue is unavailable"
	case "worker_panic":
		return "Prompt Audit worker failed"
	case "audit_scope_removed":
		return "Prompt Audit skipped after its group scope changed"
	case "config_load_failed", "config_ttl_reload_failed", "config_invalidation_reload_failed":
		return "Prompt Audit configuration could not be loaded"
	default:
		return "Prompt Audit operation failed"
	}
}

func sanitizeStoredError(code string) (string, string) {
	stableCode := stableErrorCode(code)
	return stableCode, TrimRunes(stableErrorMessage(stableCode), 160)
}
