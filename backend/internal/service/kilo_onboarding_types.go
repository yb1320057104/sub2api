package service

import (
	"context"
	"time"
)

const (
	KiloJobActivatePrepared = "activate_prepared"
	KiloJobOnboardSource    = "onboard_source"
	KiloJobUnbindSource     = "unbind_source"
)

// Rows keep source, proxy, workspace and model selection together. Secrets are
// read from the existing encrypted re-login configuration only at execution.
type KiloOnboardingItem struct {
	PreparedID       int64    `json:"prepared_id,omitempty"`
	SourceAccountID  int64    `json:"source_account_id"`
	Name             string   `json:"name"`
	LoginProxyID     int64    `json:"login_proxy_id"`
	OrganizationID   string   `json:"organization_id"`
	WorkspaceMode    string   `json:"workspace_mode,omitempty"`
	VerifiedModelIDs []string `json:"verified_model_ids"`
}
type KiloOnboardingBatchInput struct {
	BindingStrategy   string               `json:"binding_strategy,omitempty"`
	RequestID         string               `json:"request_id"`
	Items             []KiloOnboardingItem `json:"items"`
	GroupIDs          []int64              `json:"group_ids"`
	Routing           KiloRoutingOptions   `json:"routing"`
	ActivateWhenReady bool                 `json:"activate_when_ready"`
}
type KiloOnboardingItemResult struct {
	Line            int    `json:"line"`
	SourceAccountID int64  `json:"source_account_id"`
	JobID           int64  `json:"job_id"`
	Status          string `json:"status"`
}
type KiloOnboardingBatchResult struct {
	BatchID string                     `json:"batch_id"`
	Items   []KiloOnboardingItemResult `json:"items"`
}
type KiloOnboardingBatchView struct {
	BatchID string    `json:"batch_id"`
	Items   []KiloJob `json:"items"`
}

type KiloExecutorHealth struct {
	OK                bool     `json:"ok"`
	ProtocolVersion   int      `json:"protocol_version"`
	InstanceID        string   `json:"instance_id"`
	Capabilities      []string `json:"capabilities"`
	AvailableCapacity int      `json:"available_capacity"`
}
type KiloExecutorIdentity struct {
	KiloUserID  string `json:"kilo_user_id"`
	OpenAIEmail string `json:"openai_email"`
}
type KiloExecutorCredential struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	TOTPSecret string `json:"totp_secret"`
}
type KiloExecutorProxy struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
}
type KiloExecutorDevice struct {
	VerificationURL string `json:"verification_url"`
	UserCode        string `json:"user_code"`
}

// KiloExecutorIdentityCredential is sent only over the authenticated executor
// boundary; it is never part of task options or administrator responses.
type KiloExecutorIdentityCredential struct {
	APIKey          string                  `json:"api_key"`
	UserID          string                  `json:"user_id"`
	Email           string                  `json:"email"`
	SessionMaterial string                  `json:"session_material,omitempty"`
	Credential      *KiloExecutorCredential `json:"credential,omitempty"`
}

// KiloCredentialIdentityEvidence binds two password/TOTP authentications to the
// immutable source snapshot without equating subjects issued to different clients.
// It is accepted only across the authenticated executor boundary and stored encrypted.
type KiloCredentialIdentityEvidence struct {
	Version                         int       `json:"version"`
	Method                          string    `json:"method"`
	KiloUserID                      string    `json:"kilo_user_id"`
	KiloClientID                    string    `json:"kilo_client_id"`
	SourceClientID                  string    `json:"source_client_id"`
	SourceIssuer                    string    `json:"source_issuer"`
	SourceSubject                   string    `json:"source_subject"`
	SourceWorkspaceID               string    `json:"source_workspace_id"`
	SourcePlanType                  string    `json:"source_plan_type"`
	ConnectionSubject               string    `json:"connection_subject"`
	ProviderCreatedAt               time.Time `json:"provider_created_at"`
	ProviderFingerprint             string    `json:"provider_fingerprint"`
	ObservedAt                      time.Time `json:"observed_at"`
	KiloPasswordTOTPVerified        bool      `json:"kilo_password_totp_verified"`
	SourcePasswordTOTPVerified      bool      `json:"source_password_totp_verified"`
	SourceSnapshotSignatureVerified bool      `json:"source_snapshot_signature_verified"`
	SourceSignatureVerified         bool      `json:"source_signature_verified"`
	NonceVerified                   bool      `json:"nonce_verified"`
	ProviderUnchanged               bool      `json:"provider_unchanged"`
}

// KiloCredentialIdentityAssociation binds independent password/TOTP authentications
// to the signed source snapshot without equating different clients' subjects.
// It crosses only the authenticated executor boundary and is stored encrypted.
type KiloCredentialIdentityAssociation struct {
	Version           int       `json:"version"`
	Method            string    `json:"method"`
	SourceIssuer      string    `json:"source_issuer"`
	SourceSubject     string    `json:"source_subject"`
	SourceWorkspaceID string    `json:"source_workspace_id"`
	SourcePlanType    string    `json:"source_plan_type"`
	KiloUserID        string    `json:"kilo_user_id"`
	KiloClientID      string    `json:"kilo_client_id"`
	KiloSubject       string    `json:"kilo_subject"`
	VerifiedAt        time.Time `json:"verified_at"`
}

// KiloConnectionObservation describes an unchanged existing connection. Neither
// this observation nor its identity evidence proves that BYOK has been enabled.
type KiloConnectionObservation struct {
	Version             int       `json:"version"`
	Method              string    `json:"method"`
	KiloUserID          string    `json:"kilo_user_id"`
	OrganizationID      string    `json:"organization_id"`
	OpenAIEmail         string    `json:"openai_email"`
	OpenAISubject       string    `json:"openai_subject"`
	ConnectedAt         time.Time `json:"connected_at"`
	ObservedAt          time.Time `json:"observed_at"`
	ProviderCreatedAt   time.Time `json:"provider_created_at"`
	ProviderFingerprint string    `json:"provider_fingerprint"`
	ConnectionUnchanged bool      `json:"connection_unchanged"`
	ProviderUnchanged   bool      `json:"provider_unchanged"`
	EnabledEvidence     string    `json:"enabled_evidence"`
}

type KiloExecutorTaskInput struct {
	// SourceIDToken is private input, never task options, receipts or admin output.
	SourceIDToken    string                          `json:"source_id_token,omitempty"`
	KiloIdentity     *KiloExecutorIdentityCredential `json:"kilo_identity,omitempty"`
	ProtocolVersion  int                             `json:"protocol_version"`
	JobID            int64                           `json:"job_id"`
	Revision         int64                           `json:"revision"`
	OperationID      string                          `json:"operation_id"`
	Phase            string                          `json:"phase"`
	DeadlineAt       time.Time                       `json:"deadline_at"`
	ExpectedIdentity struct {
		SourceEmail       string `json:"source_email"`
		SourceWorkspaceID string `json:"source_workspace_id"`
		SourcePlanType    string `json:"source_plan_type"`
		SourceSubject     string `json:"source_subject,omitempty"`
		SourceIssuer      string `json:"source_issuer,omitempty"`
	} `json:"expected_identity"`
	LoginProxy     KiloExecutorProxy       `json:"login_proxy"`
	Credential     *KiloExecutorCredential `json:"credential,omitempty"`
	SessionHandle  string                  `json:"session_handle,omitempty"`
	OrganizationID string                  `json:"organization_id"`
	Device         *KiloExecutorDevice     `json:"device,omitempty"`
}
type KiloExecutorTask struct {
	IdentityAssociation        *KiloCredentialIdentityAssociation `json:"identity_association,omitempty"`
	CredentialIdentityEvidence *KiloCredentialIdentityEvidence    `json:"credential_identity_evidence,omitempty"`
	ConnectionObservation      *KiloConnectionObservation         `json:"connection_observation,omitempty"`
	IdentitySessionMaterial    string                             `json:"identity_session_material,omitempty"`
	TaskID                     string                             `json:"task_id"`
	InstanceID                 string                             `json:"instance_id"`
	JobID                      int64                              `json:"job_id"`
	Revision                   int64                              `json:"revision"`
	Phase                      string                             `json:"phase"`
	Status                     string                             `json:"status"`
	SessionHandle              string                             `json:"session_handle,omitempty"`
	VerifiedIdentity           KiloExecutorIdentity               `json:"verified_identity"`
	ExpiresAt                  time.Time                          `json:"expires_at"`
	ErrorCode                  string                             `json:"error_code"`
	Retryable                  bool                               `json:"retryable"`
	NextAction                 string                             `json:"next_action"`
	BYOKEnabled                bool                               `json:"byok_enabled"`
	ConnectionEmail            string                             `json:"connection_email"`
	ConnectionWorkspaceID      string                             `json:"connection_workspace_id,omitempty"`
	ConnectionIdentityEvidence string                             `json:"connection_identity_evidence,omitempty"`
	BYOKEnableEvidence         string                             `json:"byok_enable_evidence"`
	ConnectionSubject          string                             `json:"connection_subject"`
	ConnectionAt               time.Time                          `json:"connection_at"`
	OrganizationID             string                             `json:"organization_id"`
}

// The Go queue owns job/revision fencing. An executor owns only its short-lived
// remote sessions; changing instances makes their handles explicitly unusable.
type KiloOnboardingExecutor interface {
	Health(context.Context) (*KiloExecutorHealth, error)
	Submit(context.Context, KiloExecutorTaskInput) (*KiloExecutorTask, error)
	Task(context.Context, string) (*KiloExecutorTask, error)
	Cancel(context.Context, string) error
	Ack(context.Context, string) error
	CloseSession(context.Context, string) error
}

type kiloOnboardingState struct {
	InstanceID      string    `json:"instance_id"`
	SessionHandle   string    `json:"session_handle"`
	ExpiresAt       time.Time `json:"expires_at"`
	TaskID          string    `json:"task_id"`
	OperationID     string    `json:"operation_id"`
	Phase           string    `json:"phase"`
	RemoteUserID    string    `json:"remote_user_id"`
	DeviceSessionID string    `json:"device_session_id"`
	BYOKEnabled     bool      `json:"byok_enabled"`
}
