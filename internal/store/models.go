package store

import "time"

type Availability string

const (
	AvailabilityConnecting Availability = "connecting"
	AvailabilityOnline     Availability = "online"
	AvailabilityOffline    Availability = "offline"
	AvailabilityRevoked    Availability = "revoked"
)

type Freshness string

const (
	FreshnessCurrent     Freshness = "current"
	FreshnessStale       Freshness = "stale"
	FreshnessMissing     Freshness = "missing"
	FreshnessUnsupported Freshness = "unsupported"
	FreshnessUnavailable Freshness = "unavailable"
)

type CollectorState string

const (
	CollectorAbsent      CollectorState = "absent"
	CollectorDetected    CollectorState = "detected"
	CollectorNeedsAccess CollectorState = "needs_access"
	CollectorEnabled     CollectorState = "enabled"
	CollectorDegraded    CollectorState = "degraded"
	CollectorDisabled    CollectorState = "disabled"
)

type Owner struct {
	ID                   string    `json:"id"`
	PasswordHash         string    `json:"passwordHash"`
	TOTPSecretCiphertext []byte    `json:"totpSecretCiphertext,omitempty"`
	TOTPSecretNonce      []byte    `json:"totpSecretNonce,omitempty"`
	TOTPWrappedDataKey   []byte    `json:"totpWrappedDataKey,omitempty"`
	TOTPKeyVersion       int       `json:"totpKeyVersion,omitempty"`
	MFAEnabled           bool      `json:"mfaEnabled"`
	RecoveryCodeHashes   []string  `json:"recoveryCodeHashes,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
}

type Session struct {
	TokenHash      string     `json:"tokenHash"`
	CSRFHash       string     `json:"csrfHash"`
	OwnerID        string     `json:"ownerId"`
	CreatedAt      time.Time  `json:"createdAt"`
	LastSeen       time.Time  `json:"lastSeen"`
	AbsoluteExpiry time.Time  `json:"absoluteExpiry"`
	RecentMFAAt    *time.Time `json:"recentMfaAt,omitempty"`
	RevokedAt      *time.Time `json:"revokedAt,omitempty"`
}

type Site struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	AddressContext string    `json:"addressContext"`
	CreatedAt      time.Time `json:"createdAt"`
}

type ScopeLimits struct {
	ProbesPerSecond int `json:"probesPerSecond"`
	Concurrency     int `json:"concurrency"`
	TargetBudget    int `json:"targetBudget"`
}

type Scope struct {
	ID             string      `json:"id"`
	SiteID         string      `json:"siteId"`
	Ranges         []string    `json:"ranges"`
	Exclusions     []string    `json:"exclusions"`
	AllowedMethods []string    `json:"allowedMethods"`
	Ports          []int       `json:"ports"`
	CredentialRef  string      `json:"credentialRef,omitempty"`
	TrustRef       string      `json:"trustRef,omitempty"`
	Limits         ScopeLimits `json:"limits"`
	Enabled        bool        `json:"enabled"`
	Revision       int64       `json:"revision"`
	CreatedAt      time.Time   `json:"createdAt"`
	UpdatedAt      time.Time   `json:"updatedAt"`
}

type IdentifierEvidence struct {
	Kind       string    `json:"kind"`
	Namespace  string    `json:"namespace"`
	Value      string    `json:"value"`
	Source     string    `json:"source"`
	Confidence float64   `json:"confidence"`
	FirstSeen  time.Time `json:"firstSeen"`
	LastSeen   time.Time `json:"lastSeen"`
}

type MetricSample struct {
	ID           string            `json:"id"`
	DeviceID     string            `json:"deviceId"`
	AgentID      string            `json:"agentId"`
	CollectorID  string            `json:"collectorId"`
	EntityID     string            `json:"entityId"`
	Metric       string            `json:"metric"`
	Labels       map[string]string `json:"labels,omitempty"`
	Value        *float64          `json:"value"`
	Availability Freshness         `json:"availability"`
	Unit         string            `json:"unit"`
	ObservedAt   time.Time         `json:"observedAt"`
	ReceivedAt   time.Time         `json:"receivedAt"`
}

type Observation struct {
	ID          string         `json:"id"`
	ReporterID  string         `json:"reporterId"`
	CollectorID string         `json:"collectorId"`
	SubjectID   string         `json:"subjectId"`
	Kind        string         `json:"kind"`
	Payload     map[string]any `json:"payload"`
	ObservedAt  time.Time      `json:"observedAt"`
	ReceivedAt  time.Time      `json:"receivedAt"`
	ExpiresAt   time.Time      `json:"expiresAt"`
	Confidence  float64        `json:"confidence"`
}

type AgentIdentity struct {
	ID               string     `json:"id"`
	DeviceID         string     `json:"deviceId"`
	PublicKeyHash    string     `json:"publicKeyHash"`
	CertSerial       string     `json:"certSerial"`
	AuthTokenHash    string     `json:"authTokenHash,omitempty"`
	CertificatePEM   string     `json:"certificatePem"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	RevokedAt        *time.Time `json:"revokedAt,omitempty"`
	InstalledVersion string     `json:"installedVersion"`
}

type Device struct {
	ID               string                     `json:"id"`
	SiteID           string                     `json:"siteId,omitempty"`
	DisplayName      string                     `json:"displayName"`
	Platform         string                     `json:"platform"`
	Architecture     string                     `json:"architecture"`
	Hostname         string                     `json:"hostname,omitempty"`
	Lifecycle        string                     `json:"lifecycle"`
	CreatedAt        time.Time                  `json:"createdAt"`
	DecommissionedAt *time.Time                 `json:"decommissionedAt,omitempty"`
	Addresses        []string                   `json:"addresses,omitempty"`
	Identifiers      []IdentifierEvidence       `json:"identifiers,omitempty"`
	AgentID          string                     `json:"agentId,omitempty"`
	AgentVersion     string                     `json:"agentVersion,omitempty"`
	LastHeartbeat    *time.Time                 `json:"lastHeartbeat,omitempty"`
	Availability     Availability               `json:"availability"`
	MetricFreshness  map[string]Freshness       `json:"metricFreshness"`
	CurrentMetrics   map[string]MetricSample    `json:"currentMetrics,omitempty"`
	CollectorStates  []CollectorDescriptorState `json:"collectorStates,omitempty"`
	Revision         int64                      `json:"revision"`
	Excluded         bool                       `json:"excluded"`
}

type CollectorDescriptorState struct {
	ID          string         `json:"id"`
	Provider    string         `json:"provider"`
	State       CollectorState `json:"state"`
	LastSuccess *time.Time     `json:"lastSuccess,omitempty"`
	Diagnostic  string         `json:"diagnostic,omitempty"`
}

type AlertRule struct {
	ID                        string     `json:"id"`
	TemplateKey               string     `json:"templateKey,omitempty"`
	Name                      string     `json:"name"`
	Kind                      string     `json:"kind"`
	Metric                    string     `json:"metric,omitempty"`
	EntityID                  string     `json:"entityId,omitempty"`
	Operator                  string     `json:"operator,omitempty"`
	TriggerValue              *float64   `json:"triggerValue,omitempty"`
	ClearValue                *float64   `json:"clearValue,omitempty"`
	TriggerState              string     `json:"triggerState,omitempty"`
	ClearState                string     `json:"clearState,omitempty"`
	TriggerSeconds            int        `json:"triggerSeconds"`
	ClearSeconds              int        `json:"clearSeconds"`
	MinimumConsecutiveSamples int        `json:"minimumConsecutiveSamples"`
	Severity                  string     `json:"severity"`
	TargetKind                string     `json:"targetKind"`
	TargetID                  string     `json:"targetId,omitempty"`
	Enabled                   bool       `json:"enabled"`
	Revision                  int64      `json:"revision"`
	RetiredAt                 *time.Time `json:"retiredAt,omitempty"`
	CreatedAt                 time.Time  `json:"createdAt"`
	UpdatedAt                 time.Time  `json:"updatedAt"`
}

type AlertOverride struct {
	ID                        string    `json:"id"`
	LineageID                 string    `json:"lineageId"`
	TargetKind                string    `json:"targetKind"`
	TargetID                  string    `json:"targetId"`
	Kind                      string    `json:"kind"`
	Metric                    string    `json:"metric,omitempty"`
	EntityID                  string    `json:"entityId,omitempty"`
	Operator                  string    `json:"operator,omitempty"`
	TriggerValue              *float64  `json:"triggerValue,omitempty"`
	ClearValue                *float64  `json:"clearValue,omitempty"`
	TriggerState              string    `json:"triggerState,omitempty"`
	ClearState                string    `json:"clearState,omitempty"`
	TriggerSeconds            int       `json:"triggerSeconds"`
	ClearSeconds              int       `json:"clearSeconds"`
	MinimumConsecutiveSamples int       `json:"minimumConsecutiveSamples"`
	Severity                  string    `json:"severity"`
	Enabled                   bool      `json:"enabled"`
	Revision                  int64     `json:"revision"`
	CreatedAt                 time.Time `json:"createdAt"`
	UpdatedAt                 time.Time `json:"updatedAt"`
}

type BootstrapInvitation struct {
	TokenHash       string     `json:"tokenHash"`
	DeviceID        string     `json:"deviceId"`
	EnrollmentJobID string     `json:"enrollmentJobId,omitempty"`
	ExpiresAt       time.Time  `json:"expiresAt"`
	ConsumedAt      *time.Time `json:"consumedAt,omitempty"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty"`
}

type CredentialRef struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Endpoint       string            `json:"endpoint,omitempty"`
	AllowedUse     []string          `json:"allowedUse"`
	Targets        []string          `json:"targets"`
	Ciphertext     []byte            `json:"ciphertext,omitempty"`
	Nonce          []byte            `json:"nonce,omitempty"`
	WrappedDataKey []byte            `json:"wrappedDataKey,omitempty"`
	KeyVersion     int               `json:"keyVersion"`
	Metadata       map[string]string `json:"metadata"`
	Revision       int64             `json:"revision"`
	RevokedAt      *time.Time        `json:"revokedAt,omitempty"`
}

type TrustRecord struct {
	ID                 string     `json:"id"`
	ScopeID            string     `json:"scopeId,omitempty"`
	Endpoint           string     `json:"endpoint"`
	Host               string     `json:"host"`
	Fingerprint        string     `json:"fingerprint"`
	PublicKey          string     `json:"publicKey,omitempty"`
	Revision           int64      `json:"revision"`
	OwnerEstablishedAt time.Time  `json:"ownerEstablishedAt"`
	RevokedAt          *time.Time `json:"revokedAt,omitempty"`
}

type AccessRequest struct {
	ID          string            `json:"id"`
	DeviceID    string            `json:"deviceId"`
	ScopeID     string            `json:"scopeId,omitempty"`
	ReasonCode  string            `json:"reasonCode"`
	SafeDetails map[string]string `json:"safeDetails"`
	State       string            `json:"state"`
	LastAttempt time.Time         `json:"lastAttempt"`
}

type Candidate struct {
	ID            string    `json:"id"`
	SiteID        string    `json:"siteId"`
	ScopeID       string    `json:"scopeId"`
	Address       string    `json:"address"`
	Hostname      string    `json:"hostname,omitempty"`
	Source        string    `json:"source"`
	State         string    `json:"state"`
	ScopeRevision int64     `json:"scopeRevision"`
	FirstSeen     time.Time `json:"firstSeen"`
	LastSeen      time.Time `json:"lastSeen"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Excluded      bool      `json:"excluded"`
	EvidenceIDs   []string  `json:"evidenceIds,omitempty"`
}

type WorkerIdentity struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	AuthTokenHash string     `json:"authTokenHash"`
	SiteIDs       []string   `json:"siteIds"`
	CreatedAt     time.Time  `json:"createdAt"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
}

type DeviceUpdatePolicy struct {
	DeviceID         string     `json:"deviceId"`
	Mode             string     `json:"mode"`
	ReleaseID        string     `json:"releaseId,omitempty"`
	Version          string     `json:"version,omitempty"`
	WindowStart      *time.Time `json:"windowStart,omitempty"`
	WindowEnd        *time.Time `json:"windowEnd,omitempty"`
	ExpectedRevision int64      `json:"expectedRevision"`
	Revision         int64      `json:"revision"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type CollectorConfig struct {
	DeviceID      string            `json:"deviceId"`
	CollectorID   string            `json:"collectorId"`
	Provider      string            `json:"provider"`
	Enabled       bool              `json:"enabled"`
	Config        map[string]string `json:"config,omitempty"`
	CredentialRef string            `json:"credentialRef,omitempty"`
	Revision      int64             `json:"revision"`
	Health        CollectorState    `json:"health"`
	Diagnostic    string            `json:"diagnostic,omitempty"`
	LastSuccess   *time.Time        `json:"lastSuccess,omitempty"`
}

type ServiceEntity struct {
	ID         string            `json:"id"`
	Provider   string            `json:"provider"`
	ClusterID  string            `json:"clusterId,omitempty"`
	DeviceID   string            `json:"deviceId,omitempty"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Labels     map[string]string `json:"labels,omitempty"`
	ObservedAt time.Time         `json:"observedAt"`
	ExpiresAt  time.Time         `json:"expiresAt"`
}

type Job struct {
	ID                string            `json:"id"`
	Kind              string            `json:"kind"`
	DeviceID          string            `json:"deviceId"`
	ScopeID           string            `json:"scopeId,omitempty"`
	ScopeRevision     int64             `json:"scopeRevision"`
	CredentialVersion int64             `json:"credentialVersion"`
	ReleaseID         string            `json:"releaseId,omitempty"`
	State             string            `json:"state"`
	LeaseOwner        string            `json:"leaseOwner,omitempty"`
	Epoch             int64             `json:"epoch"`
	LeaseExpiry       *time.Time        `json:"leaseExpiry,omitempty"`
	Attempts          int               `json:"attempts"`
	NextAttempt       time.Time         `json:"nextAttempt"`
	Deadline          *time.Time        `json:"deadline,omitempty"`
	Destination       string            `json:"destination,omitempty"`
	TrustRef          string            `json:"trustRef,omitempty"`
	CredentialGrantID string            `json:"credentialGrantId,omitempty"`
	Result            map[string]string `json:"result,omitempty"`
}

type Release struct {
	ID            string         `json:"id"`
	ManifestHash  string         `json:"manifestHash"`
	Version       string         `json:"version"`
	Generation    int64          `json:"generation"`
	Platform      string         `json:"platform"`
	Architecture  string         `json:"architecture"`
	Digest        string         `json:"digest"`
	Bytes         int64          `json:"bytes"`
	TrustKeyID    string         `json:"trustKeyId"`
	ImmutablePath string         `json:"immutableBlobPath"`
	Manifest      map[string]any `json:"manifest"`
	RevokedAt     *time.Time     `json:"revokedAt,omitempty"`
}

type Assignment struct {
	ID             string    `json:"id"`
	RolloutID      string    `json:"rolloutId,omitempty"`
	DeviceID       string    `json:"deviceId"`
	DesiredRelease string    `json:"desiredRelease"`
	Generation     int64     `json:"generation"`
	ExpiresAt      time.Time `json:"expiresAt"`
	State          string    `json:"state"`
}

type Rollout struct {
	ID               string     `json:"id"`
	ReleaseID        string     `json:"releaseId"`
	Mode             string     `json:"mode"`
	Targets          []string   `json:"targets"`
	Concurrency      int        `json:"concurrency"`
	Canaries         int        `json:"canaries"`
	FailureThreshold int        `json:"failureThreshold"`
	WindowStart      *time.Time `json:"windowStart,omitempty"`
	WindowEnd        *time.Time `json:"windowEnd,omitempty"`
	Paused           bool       `json:"paused"`
	Revision         int64      `json:"revision"`
	FailureCount     int        `json:"failureCount"`
	CreatedAt        time.Time  `json:"createdAt"`
}

type Relationship struct {
	ID                 string    `json:"id"`
	FromEntity         string    `json:"fromEntity"`
	ToEntity           string    `json:"toEntity"`
	Type               string    `json:"type"`
	Confidence         float64   `json:"confidence"`
	ProjectionRevision int64     `json:"projectionRevision"`
	EvidenceIDs        []string  `json:"evidenceIds"`
	ObservedAt         time.Time `json:"observedAt"`
	ExpiresAt          time.Time `json:"expiresAt"`
	Source             string    `json:"source"`
}

type AuditEvent struct {
	ID              string         `json:"id"`
	ActorKind       string         `json:"actorKind"`
	ActorID         string         `json:"actorId"`
	Action          string         `json:"action"`
	Target          string         `json:"target"`
	EventTime       time.Time      `json:"eventTime"`
	RedactedOutcome map[string]any `json:"redactedOutcome"`
	RequestID       string         `json:"requestId"`
}

type WorkspaceState struct {
	RecoveryMode          bool       `json:"recoveryMode"`
	DiscoveryPaused       bool       `json:"discoveryPaused"`
	EnrollmentPaused      bool       `json:"enrollmentPaused"`
	UpdatesPaused         bool       `json:"updatesPaused"`
	RetentionHours        int        `json:"retentionHours"`
	MaxSamples            int        `json:"maxSamples"`
	TelemetryBudgetBytes  int64      `json:"telemetryBudgetBytes"`
	DroppedSamples        int64      `json:"droppedSamples"`
	TelemetryBackpressure bool       `json:"telemetryBackpressure"`
	LastRetentionAt       *time.Time `json:"lastRetentionAt,omitempty"`
	PauseRequested        bool       `json:"pauseRequested"`
	PausePending          bool       `json:"pausePending"`
	ExecutionHolders      []string   `json:"executionHolders,omitempty"`
	PolicyRevision        int64      `json:"policyRevision"`
	SchemaVersion         int        `json:"schemaVersion"`
}

type State struct {
	Version          int                                 `json:"version"`
	Owner            *Owner                              `json:"owner,omitempty"`
	Sessions         map[string]Session                  `json:"sessions"`
	Sites            map[string]Site                     `json:"sites"`
	Scopes           map[string]Scope                    `json:"scopes"`
	Devices          map[string]Device                   `json:"devices"`
	Invitations      map[string]BootstrapInvitation      `json:"invitations"`
	Agents           map[string]AgentIdentity            `json:"agents"`
	Credentials      map[string]CredentialRef            `json:"credentials"`
	Trust            map[string]TrustRecord              `json:"trust"`
	AccessRequests   map[string]AccessRequest            `json:"accessRequests"`
	Candidates       map[string]Candidate                `json:"candidates"`
	Workers          map[string]WorkerIdentity           `json:"workers"`
	Jobs             map[string]Job                      `json:"jobs"`
	BatchReceipts    map[string]string                   `json:"batchReceipts"`
	Samples          []MetricSample                      `json:"samples"`
	Observations     map[string]Observation              `json:"observations"`
	Relationships    map[string]Relationship             `json:"relationships"`
	Collectors       map[string]CollectorDescriptorState `json:"collectors"`
	AlertRules       map[string]AlertRule                `json:"alertRules"`
	AlertOverrides   map[string]AlertOverride            `json:"alertOverrides"`
	Releases         map[string]Release                  `json:"releases"`
	Assignments      map[string]Assignment               `json:"assignments"`
	UpdatePolicies   map[string]DeviceUpdatePolicy       `json:"updatePolicies"`
	CollectorConfigs map[string]CollectorConfig          `json:"collectorConfigs"`
	ServiceEntities  map[string]ServiceEntity            `json:"serviceEntities"`
	Rollouts         map[string]Rollout                  `json:"rollouts"`
	AuditEvents      []AuditEvent                        `json:"auditEvents"`
	Workspace        WorkspaceState                      `json:"workspace"`
}
