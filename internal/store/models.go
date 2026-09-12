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

type ScanEntryPoint struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Transport    string `json:"transport"`
	Port         int    `json:"port"`
	AccessMethod string `json:"accessMethod,omitempty"`
	Enabled      bool   `json:"enabled"`
}

type ScanLimits struct {
	ProbesPerSecond     int `json:"probesPerSecond"`
	Concurrency         int `json:"concurrency"`
	TargetBudget        int `json:"targetBudget"`
	AttemptBudget       int `json:"attemptBudget"`
	TimeoutMilliseconds int `json:"timeoutMilliseconds"`
	RunDeadlineSeconds  int `json:"runDeadlineSeconds"`
	ResultPageSize      int `json:"resultPageSize,omitempty"`
}

type ScanPolicy struct {
	ScopeID         string           `json:"scopeId"`
	Revision        int64            `json:"revision"`
	Enabled         bool             `json:"enabled"`
	ServerEnabled   bool             `json:"serverEnabled"`
	AgentIDs        []string         `json:"agentIds"`
	ScheduleSeconds int              `json:"scheduleSeconds"`
	EntryPoints     []ScanEntryPoint `json:"entryPoints"`
	Limits          ScanLimits       `json:"limits"`
	UpdatedAt       time.Time        `json:"updatedAt"`
}

type ScanPolicySnapshot struct {
	Ranges      []string         `json:"ranges"`
	Exclusions  []string         `json:"exclusions"`
	EntryPoints []ScanEntryPoint `json:"entryPoints"`
	Limits      ScanLimits       `json:"limits"`
}

type ScanVantageAssignment struct {
	ScopeID      string     `json:"scopeId"`
	ScannerKind  string     `json:"scannerKind"`
	ScannerID    string     `json:"scannerId"`
	DeviceID     string     `json:"deviceId,omitempty"`
	Capabilities []string   `json:"capabilities"`
	State        string     `json:"state"`
	LastSeen     *time.Time `json:"lastSeen,omitempty"`
	Revision     int64      `json:"revision"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type ScanOutcomeCounts struct {
	Open         int `json:"open"`
	Closed       int `json:"closed"`
	Filtered     int `json:"filtered"`
	Unreachable  int `json:"unreachable"`
	Skipped      int `json:"skipped"`
	ScannerError int `json:"scannerError"`
}

type ScanRun struct {
	ID                    string             `json:"id"`
	ScopeID               string             `json:"scopeId"`
	ScopeRevision         int64              `json:"scopeRevision"`
	ScannerKind           string             `json:"scannerKind"`
	ScannerID             string             `json:"scannerId"`
	Trigger               string             `json:"trigger"`
	State                 string             `json:"state"`
	ScheduledAt           time.Time          `json:"scheduledAt"`
	StartedAt             *time.Time         `json:"startedAt,omitempty"`
	FinishedAt            *time.Time         `json:"finishedAt,omitempty"`
	AssignmentExpiresAt   time.Time          `json:"assignmentExpiresAt"`
	LeaseOwner            string             `json:"leaseOwner,omitempty"`
	LeaseEpoch            int64              `json:"leaseEpoch"`
	LeaseExpiresAt        *time.Time         `json:"leaseExpiresAt,omitempty"`
	PolicySnapshot        ScanPolicySnapshot `json:"policySnapshot"`
	TargetsPlanned        int                `json:"targetsPlanned"`
	AttemptsPlanned       int                `json:"attemptsPlanned"`
	AttemptsCompleted     int                `json:"attemptsCompleted"`
	OutcomeCounts         ScanOutcomeCounts  `json:"outcomeCounts"`
	PartialReason         string             `json:"partialReason,omitempty"`
	ErrorCode             string             `json:"errorCode,omitempty"`
	PageCount             int                `json:"pageCount"`
	FinalPageOrdinal      *int               `json:"finalPageOrdinal,omitempty"`
	IdempotencyKey        string             `json:"idempotencyKey,omitempty"`
	CancellationRequested bool               `json:"cancellationRequested"`
	CreatedAt             time.Time          `json:"createdAt"`
	UpdatedAt             time.Time          `json:"updatedAt"`
}

type ScanRunLease struct {
	RunID      string    `json:"runId"`
	LeaseOwner string    `json:"leaseOwner"`
	LeaseEpoch int64     `json:"leaseEpoch"`
	LeaseUntil time.Time `json:"leaseUntil"`
	State      string    `json:"state"`
}

type ScanResultReceipt struct {
	RunID       string    `json:"runId"`
	PageOrdinal int       `json:"pageOrdinal"`
	ContentHash string    `json:"contentHash"`
	AcceptedAt  time.Time `json:"acceptedAt"`
	ResultCount int       `json:"resultCount"`
	IsFinal     bool      `json:"isFinal"`
}

type EntryPointObservation struct {
	ID                  string    `json:"id"`
	RunID               string    `json:"runId"`
	PageOrdinal         int       `json:"pageOrdinal"`
	ScopeID             string    `json:"scopeId"`
	ScopeRevision       int64     `json:"scopeRevision"`
	ScannerKind         string    `json:"scannerKind"`
	ScannerID           string    `json:"scannerId"`
	Address             string    `json:"address"`
	Transport           string    `json:"transport"`
	Port                int       `json:"port"`
	EntryPointID        string    `json:"entryPointId"`
	Outcome             string    `json:"outcome"`
	ReasonCode          string    `json:"reasonCode,omitempty"`
	LatencyMilliseconds *float64  `json:"latencyMilliseconds,omitempty"`
	ObservedAt          time.Time `json:"observedAt"`
	ReceivedAt          time.Time `json:"receivedAt"`
	ExpiresAt           time.Time `json:"expiresAt"`
	Actionable          bool      `json:"actionable"`
}

type EntryPointCurrent struct {
	Key           string    `json:"key"`
	ScopeID       string    `json:"scopeId"`
	Address       string    `json:"address"`
	Transport     string    `json:"transport"`
	Port          int       `json:"port"`
	ScannerKind   string    `json:"scannerKind"`
	ScannerID     string    `json:"scannerId"`
	ObservationID string    `json:"observationId"`
	Outcome       string    `json:"outcome"`
	Freshness     string    `json:"freshness"`
	Contradicted  bool      `json:"contradicted"`
	ObservedAt    time.Time `json:"observedAt"`
	ReceivedAt    time.Time `json:"receivedAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type ScanCandidateExtension struct {
	CandidateID           string                `json:"candidateId"`
	CoverageState         string                `json:"coverageState"`
	EntryPointIDs         []string              `json:"entryPointIds"`
	PreferredAccessMethod string                `json:"preferredAccessMethod,omitempty"`
	LastScannedAt         *time.Time            `json:"lastScannedAt,omitempty"`
	Provenance            []ScanVantageEvidence `json:"provenance"`
	UpdatedAt             time.Time             `json:"updatedAt"`
}

type ScanVantageEvidence struct {
	ScannerKind    string    `json:"scannerKind"`
	ScannerID      string    `json:"scannerId"`
	LastObservedAt time.Time `json:"lastObservedAt"`
	Outcome        string    `json:"outcome"`
}

type ScanAccessRequestKey struct {
	DedupeKey       string    `json:"dedupeKey"`
	CandidateID     string    `json:"candidateId"`
	AccessMethod    string    `json:"accessMethod"`
	Endpoint        string    `json:"endpoint"`
	AccessRequestID string    `json:"accessRequestId"`
	State           string    `json:"state"`
	UpdatedAt       time.Time `json:"updatedAt"`
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
	ID              string            `json:"id"`
	DeviceID        string            `json:"deviceId"`
	AgentID         string            `json:"agentId"`
	CollectorID     string            `json:"collectorId"`
	EntityID        string            `json:"entityId"`
	Metric          string            `json:"metric"`
	Labels          map[string]string `json:"labels,omitempty"`
	Value           *float64          `json:"value"`
	Availability    Freshness         `json:"availability"`
	Unit            string            `json:"unit"`
	IntervalSeconds int               `json:"intervalSeconds,omitempty"`
	ObservedAt      time.Time         `json:"observedAt"`
	ReceivedAt      time.Time         `json:"receivedAt"`
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
	ServicePattern            string     `json:"servicePattern,omitempty"`
	CollectorID               string     `json:"collectorId,omitempty"`
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
	ServicePattern            string    `json:"servicePattern,omitempty"`
	CollectorID               string    `json:"collectorId,omitempty"`
	TriggerSeconds            int       `json:"triggerSeconds"`
	ClearSeconds              int       `json:"clearSeconds"`
	MinimumConsecutiveSamples int       `json:"minimumConsecutiveSamples"`
	Severity                  string    `json:"severity"`
	Enabled                   bool      `json:"enabled"`
	Revision                  int64     `json:"revision"`
	CreatedAt                 time.Time `json:"createdAt"`
	UpdatedAt                 time.Time `json:"updatedAt"`
}

type NotificationDestination struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	BaseURL              string     `json:"baseUrl"`
	MaskedTopic          string     `json:"maskedTopic"`
	HasToken             bool       `json:"hasToken"`
	AllowPlainHTTP       bool       `json:"allowPlainHttp"`
	Enabled              bool       `json:"enabled"`
	Revision             int64      `json:"revision"`
	LastTestAt           *time.Time `json:"lastTestAt,omitempty"`
	RetiredAt            *time.Time `json:"retiredAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	SecretCiphertext     []byte     `json:"secretCiphertext,omitempty"`
	SecretNonce          []byte     `json:"secretNonce,omitempty"`
	SecretWrappedDataKey []byte     `json:"secretWrappedDataKey,omitempty"`
	SecretKeyVersion     int        `json:"secretKeyVersion,omitempty"`
}

const (
	NotificationDeliveryQueued     = "queued"
	NotificationDeliverySending    = "sending"
	NotificationDeliveryRetry      = "retry"
	NotificationDeliveryAccepted   = "accepted"
	NotificationDeliveryFailed     = "failed"
	NotificationDeliveryCancelled  = "cancelled"
	NotificationDeliverySuppressed = "suppressed"
	NotificationDeliveryExpired    = "expired"
)

type NotificationDelivery struct {
	ID                  string     `json:"id"`
	DestinationID       string     `json:"destinationId"`
	DestinationRevision int64      `json:"destinationRevision"`
	IncidentID          string     `json:"incidentId,omitempty"`
	TransitionID        string     `json:"transitionId,omitempty"`
	SummaryKey          string     `json:"summaryKey,omitempty"`
	Status              string     `json:"status"`
	Attempts            int        `json:"attempts"`
	NextAttemptAt       *time.Time `json:"nextAttemptAt,omitempty"`
	ExpiresAt           time.Time  `json:"expiresAt"`
	LeaseEpoch          int64      `json:"leaseEpoch"`
	LeaseOwner          string     `json:"leaseOwner,omitempty"`
	LeaseUntil          *time.Time `json:"leaseUntil,omitempty"`
	AcceptedAt          *time.Time `json:"acceptedAt,omitempty"`
	RemoteID            string     `json:"remoteId,omitempty"`
	SafeError           string     `json:"safeError,omitempty"`
	Payload             []byte     `json:"payload,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	SuppressionReasons  []string   `json:"-"`
}

type NotificationDeliveryQuery struct {
	DestinationID string
	IncidentID    string
	Status        string
	Cursor        string
	Limit         int
}

type NotificationDeliveryPage struct {
	Items      []NotificationDelivery
	NextCursor string
}

type NotificationDeliveryEnqueueResult struct {
	Enqueued int
	Dropped  int
}

type NotificationDeliveryOutcome struct {
	Accepted           bool
	Cancelled          bool
	Suppressed         bool
	SuppressionReasons []string
	Retryable          bool
	RemoteID           string
	SafeError          string
	RetryAfter         time.Duration
	Now                time.Time
}

const (
	SuppressionWindowRecurring = "recurring"
	SuppressionWindowOneTime   = "oneTime"
)

type SuppressionWindow struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	TargetKind string     `json:"targetKind"`
	TargetID   string     `json:"targetId,omitempty"`
	Enabled    bool       `json:"enabled"`
	Mode       string     `json:"mode"`
	Timezone   string     `json:"timezone,omitempty"`
	Weekdays   []int      `json:"weekdays,omitempty"`
	StartLocal string     `json:"startLocal,omitempty"`
	EndLocal   string     `json:"endLocal,omitempty"`
	StartsAt   *time.Time `json:"startsAt,omitempty"`
	EndsAt     *time.Time `json:"endsAt,omitempty"`
	Revision   int64      `json:"revision"`
	RetiredAt  *time.Time `json:"retiredAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type SuppressionWindowQuery struct {
	Cursor         string
	Limit          int
	IncludeRetired bool
}

type SuppressionWindowPage struct {
	Items      []SuppressionWindow
	NextCursor string
}

type SuppressionEpisode struct {
	DestinationID  string     `json:"destinationId"`
	EntityID       string     `json:"entityId"`
	DeviceID       string     `json:"deviceId,omitempty"`
	Epoch          int64      `json:"epoch"`
	StartedAt      time.Time  `json:"startedAt"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
	ReasonBits     []string   `json:"reasonBits"`
	SummaryBatchID string     `json:"summaryBatchId,omitempty"`
}

type NotificationDeliveryStats struct {
	Queued        int64 `json:"queued"`
	Sending       int64 `json:"sending"`
	Retry         int64 `json:"retry"`
	Accepted      int64 `json:"accepted"`
	Failed        int64 `json:"failed"`
	Cancelled     int64 `json:"cancelled"`
	Suppressed    int64 `json:"suppressed"`
	Expired       int64 `json:"expired"`
	QueueOverflow int64 `json:"queueOverflow"`
}

type AlertEvaluation struct {
	LineageID           string     `json:"lineageId"`
	EntityID            string     `json:"entityId"`
	EffectiveRevision   int64      `json:"effectiveRevision"`
	EvidenceState       string     `json:"evidenceState"`
	LastObservedAt      *time.Time `json:"lastObservedAt,omitempty"`
	LastReceivedAt      *time.Time `json:"lastReceivedAt,omitempty"`
	PendingSince        *time.Time `json:"pendingSince,omitempty"`
	RecoverySince       *time.Time `json:"recoverySince,omitempty"`
	LastValidAt         *time.Time `json:"lastValidAt,omitempty"`
	TriggerConsecutive  int        `json:"triggerConsecutive"`
	RecoveryConsecutive int        `json:"recoveryConsecutive"`
	IncidentID          string     `json:"incidentId,omitempty"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

type Incident struct {
	ID             string         `json:"id"`
	LineageID      string         `json:"lineageId"`
	EntityID       string         `json:"entityId"`
	DeviceID       string         `json:"deviceId,omitempty"`
	RuleRevision   int64          `json:"ruleRevision"`
	RuleSnapshot   map[string]any `json:"ruleSnapshot"`
	Severity       string         `json:"severity"`
	Status         string         `json:"status"`
	EvidenceState  string         `json:"evidenceState"`
	Value          *float64       `json:"value,omitempty"`
	Unit           string         `json:"unit,omitempty"`
	Source         string         `json:"source,omitempty"`
	OpenedAt       time.Time      `json:"openedAt"`
	ObservedAt     time.Time      `json:"observedAt"`
	EvaluatedAt    time.Time      `json:"evaluatedAt"`
	AcknowledgedAt *time.Time     `json:"acknowledgedAt,omitempty"`
	AcknowledgedBy string         `json:"acknowledgedBy,omitempty"`
	ClosedAt       *time.Time     `json:"closedAt,omitempty"`
	CloseReason    string         `json:"closeReason,omitempty"`
	Revision       int64          `json:"revision"`
}

type IncidentTransition struct {
	ID            string     `json:"id"`
	IncidentID    string     `json:"incidentId"`
	Sequence      int64      `json:"sequence"`
	Kind          string     `json:"kind"`
	Actor         string     `json:"actor,omitempty"`
	EvidenceState string     `json:"evidenceState"`
	Reason        string     `json:"reason,omitempty"`
	Value         *float64   `json:"value,omitempty"`
	ObservedAt    *time.Time `json:"observedAt,omitempty"`
	OccurredAt    time.Time  `json:"occurredAt"`
	RuleRevision  int64      `json:"ruleRevision"`
}

type AlertWorkItem struct {
	LineageID       string     `json:"lineageId"`
	EntityID        string     `json:"entityId"`
	DirtyGeneration int64      `json:"dirtyGeneration"`
	LeaseEpoch      int64      `json:"leaseEpoch"`
	LeaseOwner      string     `json:"leaseOwner,omitempty"`
	LeaseUntil      *time.Time `json:"leaseUntil,omitempty"`
	Attempts        int        `json:"attempts"`
	LastError       string     `json:"lastError,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
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
	ID           string            `json:"id"`
	DeviceID     string            `json:"deviceId"`
	ScopeID      string            `json:"scopeId,omitempty"`
	CandidateID  string            `json:"candidateId,omitempty"`
	AccessMethod string            `json:"accessMethod,omitempty"`
	Endpoint     string            `json:"endpoint,omitempty"`
	ReasonCode   string            `json:"reasonCode"`
	SafeDetails  map[string]string `json:"safeDetails"`
	State        string            `json:"state"`
	LastAttempt  time.Time         `json:"lastAttempt"`
}

type Candidate struct {
	ID                    string                `json:"id"`
	SiteID                string                `json:"siteId"`
	ScopeID               string                `json:"scopeId"`
	Address               string                `json:"address"`
	Hostname              string                `json:"hostname,omitempty"`
	Source                string                `json:"source"`
	State                 string                `json:"state"`
	ScopeRevision         int64                 `json:"scopeRevision"`
	FirstSeen             time.Time             `json:"firstSeen"`
	LastSeen              time.Time             `json:"lastSeen"`
	ExpiresAt             time.Time             `json:"expiresAt"`
	Excluded              bool                  `json:"excluded"`
	EvidenceIDs           []string              `json:"evidenceIds,omitempty"`
	CoverageState         string                `json:"coverageState,omitempty"`
	EntryPointIDs         []string              `json:"entryPointIds,omitempty"`
	PreferredAccessMethod string                `json:"preferredAccessMethod,omitempty"`
	LastScannedAt         *time.Time            `json:"lastScannedAt,omitempty"`
	Provenance            []ScanVantageEvidence `json:"provenance,omitempty"`
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
	RecoveryMode               bool       `json:"recoveryMode"`
	DiscoveryPaused            bool       `json:"discoveryPaused"`
	EnrollmentPaused           bool       `json:"enrollmentPaused"`
	UpdatesPaused              bool       `json:"updatesPaused"`
	NotificationsPaused        bool       `json:"notificationsPaused"`
	MonitoringRevision         int64      `json:"monitoringRevision"`
	MonitoringDefaultsVersion  string     `json:"monitoringDefaultsVersion"`
	RetentionRawDays           int        `json:"retentionRawDays"`
	RetentionFiveMinuteDays    int        `json:"retentionFiveMinuteDays"`
	RetentionHourlyDays        int        `json:"retentionHourlyDays"`
	RetentionHours             int        `json:"retentionHours"`
	MaxSamples                 int        `json:"maxSamples"`
	TelemetryBudgetBytes       int64      `json:"telemetryBudgetBytes"`
	DroppedSamples             int64      `json:"droppedSamples"`
	TelemetryTruncated         int64      `json:"telemetryTruncated"`
	TelemetryBackpressureCount int64      `json:"telemetryBackpressureCount"`
	ActiveAdmissionFailures    int64      `json:"activeAdmissionFailures"`
	TelemetryBackpressure      bool       `json:"telemetryBackpressure"`
	LastRetentionAt            *time.Time `json:"lastRetentionAt,omitempty"`
	LastEvaluationAt           *time.Time `json:"lastEvaluationAt,omitempty"`
	LastRollupAt               *time.Time `json:"lastRollupAt,omitempty"`
	PauseRequested             bool       `json:"pauseRequested"`
	PausePending               bool       `json:"pausePending"`
	ExecutionHolders           []string   `json:"executionHolders,omitempty"`
	PolicyRevision             int64      `json:"policyRevision"`
	NotificationQueueOverflows int64      `json:"notificationQueueOverflows"`
	SchemaVersion              int        `json:"schemaVersion"`
}

type State struct {
	Version                  int                                 `json:"version"`
	Owner                    *Owner                              `json:"owner,omitempty"`
	Sessions                 map[string]Session                  `json:"sessions"`
	Sites                    map[string]Site                     `json:"sites"`
	Scopes                   map[string]Scope                    `json:"scopes"`
	Devices                  map[string]Device                   `json:"devices"`
	Invitations              map[string]BootstrapInvitation      `json:"invitations"`
	Agents                   map[string]AgentIdentity            `json:"agents"`
	Credentials              map[string]CredentialRef            `json:"credentials"`
	Trust                    map[string]TrustRecord              `json:"trust"`
	AccessRequests           map[string]AccessRequest            `json:"accessRequests"`
	Candidates               map[string]Candidate                `json:"candidates"`
	ScanPolicies             map[string]ScanPolicy               `json:"scanPolicies"`
	ScanVantageAssignments   map[string]ScanVantageAssignment    `json:"scanVantageAssignments"`
	ScanRuns                 map[string]ScanRun                  `json:"scanRuns"`
	ScanRunLeases            map[string]ScanRunLease             `json:"scanRunLeases"`
	ScanResultReceipts       map[string]ScanResultReceipt        `json:"scanResultReceipts"`
	EntryPointObservations   map[string]EntryPointObservation    `json:"entryPointObservations"`
	EntryPointCurrent        map[string]EntryPointCurrent        `json:"entryPointCurrent"`
	ScanCandidateExtensions  map[string]ScanCandidateExtension   `json:"scanCandidateExtensions"`
	ScanAccessRequestKeys    map[string]ScanAccessRequestKey     `json:"scanAccessRequestKeys"`
	Workers                  map[string]WorkerIdentity           `json:"workers"`
	Jobs                     map[string]Job                      `json:"jobs"`
	BatchReceipts            map[string]string                   `json:"batchReceipts"`
	Samples                  []MetricSample                      `json:"samples"`
	Observations             map[string]Observation              `json:"observations"`
	Relationships            map[string]Relationship             `json:"relationships"`
	Collectors               map[string]CollectorDescriptorState `json:"collectors"`
	AlertRules               map[string]AlertRule                `json:"alertRules"`
	AlertOverrides           map[string]AlertOverride            `json:"alertOverrides"`
	NotificationDestinations map[string]NotificationDestination  `json:"notificationDestinations"`
	NotificationDeliveries   map[string]NotificationDelivery     `json:"notificationDeliveries"`
	SuppressionWindows       map[string]SuppressionWindow        `json:"suppressionWindows"`
	SuppressionEpisodes      map[string]SuppressionEpisode       `json:"suppressionEpisodes"`
	AlertEvaluations         map[string]AlertEvaluation          `json:"alertEvaluations"`
	Incidents                map[string]Incident                 `json:"incidents"`
	IncidentTransitions      map[string]IncidentTransition       `json:"incidentTransitions"`
	AlertWork                map[string]AlertWorkItem            `json:"alertWork"`
	Releases                 map[string]Release                  `json:"releases"`
	Assignments              map[string]Assignment               `json:"assignments"`
	UpdatePolicies           map[string]DeviceUpdatePolicy       `json:"updatePolicies"`
	CollectorConfigs         map[string]CollectorConfig          `json:"collectorConfigs"`
	ServiceEntities          map[string]ServiceEntity            `json:"serviceEntities"`
	Rollouts                 map[string]Rollout                  `json:"rollouts"`
	RetentionPreviews        map[string]RetentionPreview         `json:"retentionPreviews"`
	AuditEvents              []AuditEvent                        `json:"auditEvents"`
	Workspace                WorkspaceState                      `json:"workspace"`
}
