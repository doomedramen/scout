package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scout.local/scout/internal/audit"
	"scout.local/scout/internal/auth"
	"scout.local/scout/internal/identity"
	"scout.local/scout/internal/jobs"
	"scout.local/scout/internal/policy"
	"scout.local/scout/internal/secrets"
	"scout.local/scout/internal/store"
	"scout.local/scout/internal/telemetry"
	"scout.local/scout/internal/updates"
)

type Database interface{ PingContext(context.Context) error }

type Config struct {
	Production         bool
	AllowedOrigin      string
	SetupToken         string
	SetupTokenFile     string
	SecretKeyFile      string
	AgentCAFile        string
	AgentCAKeyFile     string
	StartRecovery      bool
	ReleaseTrustFile   string
	ArtifactDir        string
	MaxBodyBytes       int64
	RateLimitPerMinute int
	AgentRequireMTLS   bool
}

type App struct {
	Store      *store.Store
	Auth       *auth.Service
	Identity   *identity.Service
	Authority  *identity.Authority
	Secrets    *secrets.KeyRing
	Audit      *audit.Logger
	Policy     *policy.Engine
	Jobs       jobs.Queue
	Telemetry  *telemetry.Service
	Updates    *updates.ReleaseService
	Database   Database
	Config     Config
	setupToken string
	limiter    *rateLimiter
}

func NewApp(repository *store.Store, database Database, config Config) (*App, error) {
	if repository == nil {
		repository = store.NewMemory()
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 1 << 20
	}
	if config.RateLimitPerMinute <= 0 {
		config.RateLimitPerMinute = 120
	}
	setupToken := config.SetupToken
	if setupToken == "" && config.SetupTokenFile != "" {
		if data, readErr := os.ReadFile(config.SetupTokenFile); readErr == nil {
			setupToken = strings.TrimSpace(string(data))
		} else if !os.IsNotExist(readErr) {
			return nil, fmt.Errorf("read setup token file: %w", readErr)
		}
	}
	if setupToken == "" {
		setupToken = randomToken(32)
		if config.SetupTokenFile != "" {
			if err := os.MkdirAll(filepath.Dir(config.SetupTokenFile), 0o700); err != nil {
				return nil, fmt.Errorf("prepare setup token directory: %w", err)
			}
			if err := os.WriteFile(config.SetupTokenFile, []byte(setupToken+"\n"), 0o600); err != nil {
				return nil, fmt.Errorf("provision setup token: %w", err)
			}
		}
	}
	keyRing, err := loadKeyRing(config)
	if err != nil {
		return nil, err
	}
	var authority *identity.Authority
	if config.AgentCAFile != "" || config.AgentCAKeyFile != "" {
		authority, err = identity.LoadAuthority(config.AgentCAFile, config.AgentCAKeyFile)
	} else if config.Production {
		return nil, fmt.Errorf("production agent CA certificate and key are required")
	} else {
		authority, err = identity.NewAuthority()
	}
	if err != nil {
		return nil, err
	}
	app := &App{Store: repository, Database: database, Config: config, Secrets: keyRing, Authority: authority, setupToken: setupToken, limiter: newRateLimiter(config.RateLimitPerMinute)}
	app.Auth = auth.NewService(repository, setupToken, keyRing)
	app.Identity = &identity.Service{Store: repository, Authority: authority}
	app.Audit = audit.NewLogger(repository)
	app.Policy = &policy.Engine{Store: repository}
	app.Jobs = jobs.Queue{Store: repository}
	app.Telemetry = &telemetry.Service{Store: repository}
	trust := updates.NewTrustStore()
	if config.ReleaseTrustFile != "" {
		trust, err = updates.LoadTrustFile(config.ReleaseTrustFile)
		if err != nil {
			return nil, err
		}
	}
	app.Updates = updates.NewReleaseService(repository, trust, config.ArtifactDir)
	if config.StartRecovery {
		_, _ = repository.SetWorkspace(context.Background(), func(state *store.WorkspaceState) error {
			state.RecoveryMode = true
			state.EnrollmentPaused = true
			state.UpdatesPaused = true
			return nil
		})
	}
	return app, nil
}

func loadKeyRing(config Config) (*secrets.KeyRing, error) {
	if config.SecretKeyFile != "" {
		return secrets.LoadKeyFile(config.SecretKeyFile)
	}
	if config.Production {
		return nil, fmt.Errorf("production secret key file is required")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return secrets.NewKeyRing(key)
}

func randomToken(size int) string {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}

// Handler keeps the scaffold's public constructor while selecting durable SQL
// state when the caller supplies a *sql.DB and isolated memory state otherwise.
func Handler(db Database) http.Handler {
	var repository *store.Store
	if sqlDB, ok := db.(*sql.DB); ok {
		repository = store.NewSQL(sqlDB)
	} else {
		repository = store.NewMemory()
	}
	app, err := NewApp(repository, db, Config{Production: false, SetupToken: os.Getenv("SCOUT_SETUP_TOKEN"), SetupTokenFile: os.Getenv("SCOUT_SETUP_TOKEN_FILE")})
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, r, http.StatusInternalServerError, "startup_error", "Server configuration is invalid", false)
		})
	}
	return app.Handler()
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("POST /api/v1/setup", a.setup)
	mux.HandleFunc("POST /api/v1/sessions", a.signIn)
	mux.HandleFunc("DELETE /api/v1/sessions/current", a.signOut)
	mux.HandleFunc("POST /api/v1/sessions/reauth", a.reauth)
	mux.HandleFunc("GET /api/v1/owner", a.owner)
	mux.HandleFunc("POST /api/v1/owner/mfa/setup", a.mfaSetup)
	mux.HandleFunc("POST /api/v1/owner/mfa/confirm", a.mfaConfirm)
	mux.HandleFunc("POST /api/v1/owner/recovery-codes", a.recoveryCodes)
	a.registerInventoryRoutes(mux)
	a.registerAccessRoutes(mux)
	a.registerAgentRoutes(mux)
	a.registerOperationsRoutes(mux)
	a.registerRecoveryRoutes(mux)
	a.registerSettingsRoutes(mux)
	a.registerUpdateRoutes(mux)
	return a.middleware(mux)
}

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	state := "not configured"
	if a.Database != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		state = "connected"
		if err := a.Database.PingContext(ctx); err != nil {
			state = "unavailable"
		}
	}
	mode := "development"
	if a.Config.Production {
		mode = "production"
	}
	recovery := false
	telemetryStatus := store.TelemetryStatus{}
	if workspace, err := a.Store.Workspace(r.Context()); err == nil {
		recovery = workspace.RecoveryMode
	}
	if value, err := a.Store.TelemetryStatus(r.Context()); err == nil {
		telemetryStatus = value
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "database": state, "enrollmentAvailable": !recovery, "recoveryMode": recovery, "telemetry": telemetryStatus})
}

func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" && r.URL.Path != "/api/status" {
			if !a.limiter.allow(clientKey(r)) {
				writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests", true)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func clientKey(r *http.Request) string {
	if value := r.Header.Get("X-Forwarded-For"); value != "" {
		return strings.Split(value, ",")[0]
	}
	return r.RemoteAddr
}

type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Time
	counts map[string]int
}

func newRateLimiter(limit int) *rateLimiter {
	return &rateLimiter{limit: limit, counts: map[string]int{}}
}
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	if l.window.IsZero() || now.Sub(l.window) >= time.Minute {
		l.window = now
		l.counts = map[string]int{}
	}
	l.counts[key]++
	return l.counts[key] <= l.limit
}

func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SetupToken string `json:"setupToken"`
		Password   string `json:"password"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	owner, err := a.Auth.Setup(r.Context(), request.SetupToken, request.Password)
	if err != nil {
		writeError(w, r, http.StatusConflict, "setup_unavailable", "Owner setup could not be completed", false)
		return
	}
	_ = a.Audit.Record(r.Context(), audit.Event{ActorKind: "anonymous", Action: "owner.setup", Target: owner.ID, Outcome: map[string]any{"success": true}, RequestID: r.Header.Get("X-Request-ID")})
	writeJSON(w, http.StatusCreated, safeOwner(owner))
}

func (a *App) signIn(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password     string `json:"password"`
		TOTPCode     string `json:"totpCode"`
		RecoveryCode string `json:"recoveryCode"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Request failed validation", false)
		return
	}
	result, err := a.Auth.SignIn(r.Context(), request.Password, request.TOTPCode, request.RecoveryCode)
	if err != nil {
		_ = a.Audit.Record(r.Context(), audit.Event{ActorKind: "anonymous", Action: "owner.signin", Target: "owner", Outcome: map[string]any{"success": false}, RequestID: r.Header.Get("X-Request-ID")})
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication failed", false)
		return
	}
	secure := a.Config.Production
	http.SetCookie(w, &http.Cookie{Name: a.Auth.SessionCookieName(), Value: result.SessionToken, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: int((8 * time.Hour) / time.Second)})
	http.SetCookie(w, &http.Cookie{Name: "scout_csrf", Value: result.CSRFToken, Path: "/", HttpOnly: false, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: int((8 * time.Hour) / time.Second)})
	writeJSON(w, http.StatusOK, map[string]any{"owner": safeOwner(result.Owner), "csrfToken": result.CSRFToken})
}

func (a *App) signOut(w http.ResponseWriter, r *http.Request) {
	token := cookieValue(r, a.Auth.SessionCookieName())
	if token != "" {
		if err := a.Auth.RevokeSession(r.Context(), token); err != nil {
			writeMappedError(w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: a.Auth.SessionCookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.Config.Production, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: "scout_csrf", Value: "", Path: "/", MaxAge: -1, Secure: a.Config.Production, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) reauth(w http.ResponseWriter, r *http.Request) {
	session, ok := a.requireOwner(w, r, true)
	if !ok {
		return
	}
	var request struct {
		Password string `json:"password"`
		TOTPCode string `json:"totpCode"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	if err := a.Auth.Reauth(r.Context(), cookieValue(r, a.Auth.SessionCookieName()), request.Password, request.TOTPCode); err != nil {
		writeMappedError(w, r, err)
		return
	}
	_ = session
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) owner(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireOwner(w, r, false)
	if !ok {
		return
	}
	owner, err := a.Store.Owner(r.Context())
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, safeOwner(owner))
}

func (a *App) mfaSetup(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireOwner(w, r, true)
	if !ok {
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	secret, err := a.Auth.BeginMFASetup(r.Context(), cookieValue(r, a.Auth.SessionCookieName()), request.Password)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "otpauth": "otpauth://totp/Scout?secret=" + secret})
}

func (a *App) mfaConfirm(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireOwner(w, r, true)
	if !ok {
		return
	}
	var request struct {
		Code string `json:"totpCode"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	codes, err := a.Auth.ConfirmMFA(r.Context(), cookieValue(r, a.Auth.SessionCookieName()), request.Code)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

func (a *App) recoveryCodes(w http.ResponseWriter, r *http.Request) {
	_, ok := a.requireOwner(w, r, true)
	if !ok {
		return
	}
	var request struct {
		Password string `json:"password"`
		TOTPCode string `json:"totpCode"`
	}
	if err := decodeJSON(r, &request, 32<<10); err != nil {
		writeMappedError(w, r, store.ErrInvalid)
		return
	}
	codes, err := a.Auth.RecoveryCodes(r.Context(), cookieValue(r, a.Auth.SessionCookieName()), request.Password, request.TOTPCode)
	if err != nil {
		writeMappedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

func safeOwner(owner store.Owner) map[string]any {
	return map[string]any{"id": owner.ID, "mfaEnabled": owner.MFAEnabled, "createdAt": owner.CreatedAt}
}

func (a *App) requireOwner(w http.ResponseWriter, r *http.Request, mutation bool) (store.Session, bool) {
	token := cookieValue(r, a.Auth.SessionCookieName())
	session, err := a.Auth.ValidateSession(r.Context(), token)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required", false)
		return store.Session{}, false
	}
	if mutation {
		if err := a.checkMutation(r, session); err != nil {
			writeMappedError(w, r, err)
			return store.Session{}, false
		}
	}
	return session, true
}
func (a *App) requireSensitive(w http.ResponseWriter, r *http.Request) (store.Session, bool) {
	session, ok := a.requireOwner(w, r, true)
	if !ok {
		return store.Session{}, false
	}
	if _, err := a.Auth.RequireRecentMFA(r.Context(), cookieValue(r, a.Auth.SessionCookieName())); err != nil {
		writeMappedError(w, r, err)
		return store.Session{}, false
	}
	return session, true
}
func (a *App) checkMutation(r *http.Request, session store.Session) error {
	if origin := r.Header.Get("Origin"); origin != "" && a.Config.AllowedOrigin != "" && origin != a.Config.AllowedOrigin {
		return store.ErrForbidden
	}
	csrf := r.Header.Get(a.Auth.CSRFHeaderName())
	if csrf == "" || store.HashToken(csrf) != session.CSRFHash {
		return store.ErrForbidden
	}
	return nil
}
func cookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func decodeJSON(r *http.Request, target any, max int64) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil || int64(len(data)) > max {
		return store.ErrInvalid
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return store.ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return store.ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return store.ErrInvalid
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
