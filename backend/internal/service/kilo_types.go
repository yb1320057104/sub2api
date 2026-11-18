package service

import (
	"context"
	"time"
)

const (
	KiloManagedExtraKey = "kilo_managed"
	KiloGatewayBaseURL  = "https://ai-gateway.kilo.ai/api/v1"
)

// KiloPrepared exposes operational state only. Credential plaintext is accepted
// on import and kept in encrypted storage; no list/poll response can contain it.
type KiloPrepared struct {
	IdentityStatus          string             `json:"identity_status"`
	IdentityMode            string             `json:"identity_mode"`
	IdentitySourceAccountID *int64             `json:"identity_source_account_id"`
	IdentityCheckedAt       *time.Time         `json:"identity_checked_at"`
	BindingState            string             `json:"binding_state"`
	BindingRevision         int64              `json:"binding_revision"`
	RecoveryTaskID          *int64             `json:"recovery_task_id,omitempty"`
	BindingError            string             `json:"binding_error"`
	Reusable                bool               `json:"reusable"`
	AccountView             *KiloAccountView   `json:"account_view,omitempty"`
	ID                      int64              `json:"id"`
	Name                    string             `json:"name"`
	Email                   string             `json:"email"`
	AuthMethod              string             `json:"auth_method"`
	OrganizationID          string             `json:"organization_id"`
	Status                  string             `json:"status"`
	AccountID               *int64             `json:"account_id"`
	SourceAccountID         *int64             `json:"source_account_id"`
	HasCredential           bool               `json:"has_credential"`
	LastError               string             `json:"last_error"`
	CreatedAt               time.Time          `json:"created_at"`
	LoginProxyID            *int64             `json:"login_proxy_id"`
	LoginProxyName          string             `json:"login_proxy_name"`
	SourceName              string             `json:"source_name"`
	GatewayName             string             `json:"gateway_name"`
	RoutingOptions          KiloRoutingOptions `json:"routing_options"`
}

type KiloPreparedImportItem struct {
	Name           string `json:"name"`
	APIKey         string `json:"api_key"`
	Email          string `json:"email,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
}
type KiloPreparedImportInput struct {
	RequestID string                   `json:"request_id"`
	Items     []KiloPreparedImportItem `json:"items"`
}
type KiloImportItemResult struct {
	Line    int    `json:"line"`
	ID      *int64 `json:"id,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
type KiloPreparedImportResult struct {
	Items      []KiloImportItemResult `json:"items"`
	Imported   int                    `json:"imported"`
	Duplicates int                    `json:"duplicates"`
	Failed     int                    `json:"failed"`
}

// Login routing is fixed per Kilo identity; request exits are allocated from
// the configured pool and become reusable only after all in-flight work drains.
type KiloRoutingOptions struct {
	ProxyGroupID        int64 `json:"proxy_group_id"`
	ProxiesPerAccount   int   `json:"proxies_per_account"`
	ProxyConcurrency    int   `json:"proxy_concurrency"`
	AccountConcurrency  int   `json:"account_concurrency"`
	Priority            int   `json:"priority"`
	AutoReleaseStatuses []int `json:"auto_release_statuses"`
}

type KiloRoutingInput struct {
	LoginProxyID *int64 `json:"login_proxy_id"`
	KiloRoutingOptions
}

type KiloBatchInput struct {
	RequestID            string          `json:"request_id"`
	OnboardingName       string          `json:"onboarding_name,omitempty"`
	WorkspaceMode        string          `json:"workspace_mode,omitempty"`
	PreparedIDs          []int64         `json:"prepared_ids"`
	SourceAccountIDs     []int64         `json:"source_account_ids"`
	GroupIDs             []int64         `json:"group_ids"`
	ProxyGroupID         int64           `json:"proxy_group_id"`
	ProxiesPerAccount    int             `json:"proxies_per_account"`
	ProxyConcurrency     int             `json:"proxy_concurrency"`
	AccountConcurrency   int             `json:"account_concurrency"`
	Priority             int             `json:"priority"`
	LoginProxyIDs        map[int64]int64 `json:"login_proxy_ids,omitempty"`
	BYOKEnabledConfirmed bool            `json:"byok_enabled_confirmed"`
	AutoReleaseStatuses  []int           `json:"auto_release_statuses"`
}

type KiloJob struct {
	Revision              int64      `json:"revision"`
	RecoveryActions       []string   `json:"recovery_actions"`
	ID                    int64      `json:"id"`
	BatchID               string     `json:"batch_id"`
	PreparedID            *int64     `json:"prepared_id"`
	JobKind               string     `json:"job_kind"`
	OrganizationID        string     `json:"organization_id"`
	LoginProxyID          *int64     `json:"login_proxy_id"`
	ErrorCode             string     `json:"error_code"`
	FailedStage           string     `json:"failed_stage"`
	NextAction            string     `json:"next_action"`
	NextAttemptAt         time.Time  `json:"next_attempt_at"`
	CredentialReady       bool       `json:"credential_ready"`
	SubscriptionConnected bool       `json:"subscription_connected"`
	QuotaOAuthReady       bool       `json:"quota_oauth_ready"`
	PreparedName          string     `json:"prepared_name"`
	SourceAccountID       *int64     `json:"source_account_id"`
	SourceName            string     `json:"source_name"`
	AccountID             *int64     `json:"account_id"`
	Stage                 string     `json:"stage"`
	Status                string     `json:"status"`
	LastError             string     `json:"last_error"`
	VerificationURL       string     `json:"verification_url,omitempty"`
	UserCode              string     `json:"user_code,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	FinishedAt            *time.Time `json:"finished_at"`
}
type KiloOverview struct {
	Prepared        []KiloPrepared `json:"prepared"`
	Jobs            []KiloJob      `json:"jobs"`
	EncryptionReady bool           `json:"encryption_ready"`
}

// KiloProxyOwnership describes current cross-protocol occupancy until release.
type KiloProxyOwnership struct {
	Owned            bool   `json:"owned"`
	OwnerAccountID   *int64 `json:"owner_account_id"`
	OwnerAccountName string `json:"owner_account_name"`
	OwnerDeleted     bool   `json:"owner_deleted"`
	Purpose          string `json:"purpose,omitempty"`
}

type KiloProxyOption struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	KiloProxyOwnership
}

type KiloProxyOwnershipReader interface {
	KiloProxyOwnership(context.Context, []int64) (map[int64]KiloProxyOwnership, error)
}

type KiloDeviceInput struct {
	Name    string `json:"name"`
	ProxyID *int64 `json:"proxy_id,omitempty"`
}
type KiloDeviceStatus struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	VerificationURL string    `json:"verification_url,omitempty"`
	UserCode        string    `json:"user_code,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
	PreparedID      *int64    `json:"prepared_id,omitempty"`
	Email           string    `json:"email,omitempty"`
}
type KiloOrganization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type KiloRemoteProfile struct {
	ID                 string             `json:"id"`
	Email              string             `json:"email"`
	HasPersonalAccount bool               `json:"has_personal_account"`
	Organizations      []KiloOrganization `json:"organizations"`
}

// KiloRemote isolates the authenticated external boundary. Tests replace this
// interface rather than contacting a real account or running a model request.
type KiloRemote interface {
	Profile(context.Context, string, string) (*KiloRemoteProfile, error)
	Connection(context.Context, string, string, string) (*KiloConnectionStatus, error)
	StartDevice(context.Context, string) (*KiloRemoteDevice, error)
	PollDevice(context.Context, string, string) (*KiloRemoteDevice, error)
}

// KiloConnectionStatus is the stored remote BYOK state, not an inference probe.
// The official status API exposes an email but no Codex account ID or plan.
// AccountID and PlanType remain empty; callers must require a matching email.
type KiloConnectionStatus struct {
	Connected         bool
	Email             string
	AccountID         string
	PlanType          string
	State             string
	UsageLimitReached bool
	UsageResetsAt     *time.Time
}

type KiloRemoteDevice struct {
	Code            string
	VerificationURL string
	UserCode        string
	ExpiresIn       time.Duration
	Status          string
	Token           string
	Email           string
}
