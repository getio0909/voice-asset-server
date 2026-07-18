// Package httpapi implements the public HTTP boundary.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/account"
	"github.com/getio0909/voice-asset-server/internal/apikey"
	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/audit"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/clip"
	"github.com/getio0909/voice-asset-server/internal/glossary"
	"github.com/getio0909/voice-asset-server/internal/hotword"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/llmprofile"
	"github.com/getio0909/voice-asset-server/internal/membership"
	"github.com/getio0909/voice-asset-server/internal/notification"
	"github.com/getio0909/voice-asset-server/internal/operations"
	"github.com/getio0909/voice-asset-server/internal/organization"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
	"github.com/getio0909/voice-asset-server/internal/providerprofile"
	"github.com/getio0909/voice-asset-server/internal/review"
	"github.com/getio0909/voice-asset-server/internal/syncchange"
	"github.com/getio0909/voice-asset-server/internal/systemsetting"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/getio0909/voice-asset-server/internal/transcriptexport"
	"github.com/getio0909/voice-asset-server/internal/upload"
	"github.com/getio0909/voice-asset-server/internal/waveform"
	"github.com/getio0909/voice-asset-server/internal/webhook"
	"github.com/getio0909/voice-asset-server/internal/workspace"
)

// Handler serves the Phase 0 public endpoints.
type Handler struct {
	brandName            string
	logger               *slog.Logger
	now                  func() time.Time
	authService          AuthService
	accountService       AccountService
	apiKeyService        APIKeyService
	auditService         AuditService
	assetService         AssetService
	syncChangeService    SyncChangeService
	notificationService  NotificationService
	organizationService  OrganizationService
	operationsService    OperationsService
	systemSettingService SystemSettingService
	membershipService    MembershipService
	workspaceService     WorkspaceService
	audioService         AudioService
	waveformService      WaveformService
	clipService          ClipService
	jobService           JobService
	correctionService    CorrectionJobService
	reviewService        ReviewService
	uploadService        UploadService
	transcriptService    TranscriptService
	exportService        TranscriptExportService
	providerService      ProviderProfileService
	hotwordService       HotwordService
	llmProfileService    LLMProfileService
	glossaryService      GlossaryService
	webhookService       WebhookService
	realtimeEndpoint     RealtimeEndpoint
	readinessCheck       func(context.Context) error
	publicOrigin         string
	cookieSecure         bool
	loginLimiter         *loginLimiter
	pairingLimiter       *loginLimiter
	passwordLimiter      *loginLimiter
	metrics              *httpMetrics
}

const maxRequestIDLength = 200

type AuthService interface {
	Login(ctx context.Context, email, password string) (auth.LoginResult, error)
	Authenticate(ctx context.Context, token string) (auth.Principal, error)
	Logout(ctx context.Context, token string) error
}

type AccountService interface {
	ChangePassword(context.Context, auth.Principal, account.ChangePasswordInput, string) (account.ChangePasswordResult, error)
}

type AuditService interface {
	Record(context.Context, audit.RecordInput) error
}

type APIKeyService interface {
	Create(context.Context, auth.Principal, apikey.CreateInput, string) (apikey.CreateResult, error)
	List(context.Context, auth.Principal) ([]apikey.APIKey, error)
	Revoke(context.Context, auth.Principal, string, string) (apikey.APIKey, error)
}

type AssetService interface {
	Create(ctx context.Context, principal auth.Principal, input asset.CreateInput, idempotencyKey string) (asset.Asset, bool, error)
	Get(ctx context.Context, principal auth.Principal, assetID string) (asset.Asset, error)
	List(ctx context.Context, principal auth.Principal, input asset.ListInput) (asset.ListResult, error)
	UpdateMetadata(context.Context, auth.Principal, string, int64, asset.UpdateMetadataInput, string) (asset.Asset, error)
	Trash(context.Context, auth.Principal, string, int64, string) (asset.Asset, error)
	Restore(context.Context, auth.Principal, string, int64, string) (asset.Asset, error)
	RequestPurge(context.Context, auth.Principal, string, int64, asset.PurgeInput, string, string) (asset.PurgeRequest, bool, error)
	GetPurge(context.Context, auth.Principal, string) (asset.PurgeRequest, error)
}

type SyncChangeService interface {
	List(context.Context, auth.Principal, syncchange.ListInput) (syncchange.ListResult, error)
}

type NotificationService interface {
	List(context.Context, auth.Principal, notification.ListInput) (notification.ListResult, error)
}

type OrganizationService interface {
	GetCollection(context.Context, auth.Principal, string) (organization.Collection, error)
	ListCollections(context.Context, auth.Principal, organization.ListInput) (organization.CollectionList, error)
	ListTags(context.Context, auth.Principal, organization.ListInput) (organization.TagList, error)
	ListAssetTags(context.Context, auth.Principal, organization.AssetTagListInput) (organization.TagList, error)
	ListAnnotations(context.Context, auth.Principal, organization.AnnotationListInput) (organization.AnnotationList, error)
	GetProcessingStatus(context.Context, auth.Principal, string) (organization.ProcessingStatus, error)
	AddTags(context.Context, auth.Principal, string, organization.TagMutationInput, string) (organization.TagMutationResult, error)
	RemoveTags(context.Context, auth.Principal, string, organization.TagMutationInput, string) (organization.TagMutationResult, error)
	CreateAnnotation(context.Context, auth.Principal, string, organization.AnnotationCreateInput, string) (organization.Annotation, error)
}

type OperationsService interface {
	ListJobs(context.Context, auth.Principal, operations.JobListInput) (operations.JobList, error)
	RetryJob(context.Context, auth.Principal, string, string) (operations.JobSummary, error)
	ListAuditLogs(context.Context, auth.Principal, operations.AuditListInput) (operations.AuditList, error)
	GetSystemStatus(context.Context, auth.Principal) (operations.SystemStatus, error)
}

type SystemSettingService interface {
	Get(context.Context, auth.Principal) (systemsetting.Snapshot, error)
}

type MembershipService interface {
	Create(context.Context, auth.Principal, membership.CreateInput, string) (membership.Member, error)
	List(context.Context, auth.Principal, membership.ListInput) (membership.List, error)
	Update(context.Context, auth.Principal, string, int64, membership.UpdateInput, string) (membership.Member, error)
}

type WorkspaceService interface {
	Get(context.Context, auth.Principal) (workspace.Workspace, error)
	Update(context.Context, auth.Principal, int64, workspace.UpdateInput, string) (workspace.Workspace, error)
}

type AudioService interface {
	Open(ctx context.Context, principal auth.Principal, assetID string) (audio.Media, error)
}

type WaveformService interface {
	Open(ctx context.Context, principal auth.Principal, assetID string) (waveform.Media, error)
}

type ClipService interface {
	Create(context.Context, auth.Principal, string, clip.CreateInput, string) (clip.Clip, error)
	Open(context.Context, auth.Principal, string) (clip.Media, error)
}

type JobService interface {
	CreateTranscription(
		ctx context.Context,
		principal auth.Principal,
		assetID,
		idempotencyKey string,
	) (job.Job, bool, error)
	Get(ctx context.Context, principal auth.Principal, jobID string) (job.Job, error)
}

type CorrectionJobService interface {
	CreateCorrection(context.Context, auth.Principal, string, string) (job.Job, bool, error)
}

type ReviewService interface {
	AddDecision(context.Context, auth.Principal, string, review.DecisionInput) (review.Record, error)
	Approve(context.Context, auth.Principal, string, review.ApprovalInput) (review.ApprovalResult, error)
}

type UploadService interface {
	Create(ctx context.Context, principal auth.Principal, input upload.CreateInput, idempotencyKey string) (upload.Session, bool, error)
	Get(ctx context.Context, principal auth.Principal, uploadID string) (upload.Session, error)
	PutPart(
		ctx context.Context,
		principal auth.Principal,
		uploadID string,
		partNumber int,
		expectedSHA256 string,
		source io.Reader,
	) (upload.Part, bool, error)
	Complete(ctx context.Context, principal auth.Principal, uploadID string) (upload.Session, bool, error)
}

type TranscriptService interface {
	List(ctx context.Context, principal auth.Principal, assetID string) ([]transcript.Summary, error)
	GetRevision(ctx context.Context, principal auth.Principal, revisionID string) (transcript.Revision, error)
}

type TranscriptExportService interface {
	Create(context.Context, auth.Principal, string, transcriptexport.CreateInput, string) (transcriptexport.Export, error)
	Open(context.Context, auth.Principal, string) (transcriptexport.Media, error)
}

type ProviderProfileService interface {
	Create(ctx context.Context, principal auth.Principal, input providerprofile.CreateInput) (providerprofile.Profile, error)
	List(ctx context.Context, principal auth.Principal) ([]providerprofile.Profile, error)
	Update(
		ctx context.Context,
		principal auth.Principal,
		profileID string,
		expectedVersion int64,
		input providerprofile.UpdateInput,
	) (providerprofile.Profile, error)
	Health(ctx context.Context, principal auth.Principal, profileID string) (providerprofile.Health, error)
	Capabilities(principal auth.Principal) ([]asr.Capabilities, error)
}

type HotwordService interface {
	Create(ctx context.Context, principal auth.Principal, input hotword.CreateInput) (hotword.Set, error)
	List(ctx context.Context, principal auth.Principal) ([]hotword.Set, error)
	AddVersion(
		ctx context.Context,
		principal auth.Principal,
		setID string,
		expectedResourceVersion int64,
		input hotword.AddVersionInput,
	) (hotword.Set, error)
	Update(
		ctx context.Context,
		principal auth.Principal,
		setID string,
		expectedResourceVersion int64,
		input hotword.UpdateInput,
	) (hotword.Set, error)
}

type LLMProfileService interface {
	Create(context.Context, auth.Principal, llmprofile.CreateInput) (llmprofile.Profile, error)
	List(context.Context, auth.Principal) ([]llmprofile.Profile, error)
	Update(context.Context, auth.Principal, string, int64, llmprofile.UpdateInput) (llmprofile.Profile, error)
	Health(context.Context, auth.Principal, string) (llmprofile.Health, error)
	Capabilities(auth.Principal) ([]llm.Capabilities, error)
}

type GlossaryService interface {
	Create(context.Context, auth.Principal, glossary.CreateInput) (glossary.Set, error)
	List(context.Context, auth.Principal) ([]glossary.Set, error)
	AddVersion(context.Context, auth.Principal, string, int64, glossary.AddVersionInput) (glossary.Set, error)
	Update(context.Context, auth.Principal, string, int64, glossary.UpdateInput) (glossary.Set, error)
}

type WebhookService interface {
	Create(context.Context, auth.Principal, webhook.CreateInput, string) (webhook.CreateResult, error)
	List(context.Context, auth.Principal) ([]webhook.Endpoint, error)
	ListDeliveries(context.Context, auth.Principal, string, int) ([]webhook.Delivery, error)
	Update(context.Context, auth.Principal, string, int64, webhook.UpdateInput, string) (webhook.Endpoint, error)
	RotateSecret(context.Context, auth.Principal, string, int64, string) (webhook.CreateResult, error)
	EnqueueTest(context.Context, auth.Principal, string, string) (webhook.Delivery, error)
}

type RealtimeEndpoint interface {
	Serve(context.Context, auth.Principal, http.ResponseWriter, *http.Request)
}

type Options struct {
	BrandName            string
	Logger               *slog.Logger
	AuthService          AuthService
	AccountService       AccountService
	APIKeyService        APIKeyService
	AuditService         AuditService
	AssetService         AssetService
	SyncChangeService    SyncChangeService
	NotificationService  NotificationService
	OrganizationService  OrganizationService
	OperationsService    OperationsService
	SystemSettingService SystemSettingService
	MembershipService    MembershipService
	WorkspaceService     WorkspaceService
	AudioService         AudioService
	WaveformService      WaveformService
	ClipService          ClipService
	JobService           JobService
	CorrectionService    CorrectionJobService
	ReviewService        ReviewService
	UploadService        UploadService
	TranscriptService    TranscriptService
	ExportService        TranscriptExportService
	ProviderService      ProviderProfileService
	HotwordService       HotwordService
	LLMProfileService    LLMProfileService
	GlossaryService      GlossaryService
	WebhookService       WebhookService
	RealtimeEndpoint     RealtimeEndpoint
	ReadinessCheck       func(context.Context) error
	PublicOrigin         string
	CookieSecure         bool
}

// NewHandler constructs an HTTP handler with no external service dependency.
func NewHandler(brandName string, logger *slog.Logger) http.Handler {
	return NewApplicationHandler(Options{BrandName: brandName, Logger: logger})
}

func NewApplicationHandler(options Options) http.Handler {
	if options.Logger == nil {
		options.Logger = slog.New(slog.DiscardHandler)
	}
	return &Handler{
		brandName:            options.BrandName,
		logger:               options.Logger,
		now:                  time.Now,
		authService:          options.AuthService,
		accountService:       options.AccountService,
		apiKeyService:        options.APIKeyService,
		auditService:         options.AuditService,
		assetService:         options.AssetService,
		syncChangeService:    options.SyncChangeService,
		notificationService:  options.NotificationService,
		organizationService:  options.OrganizationService,
		operationsService:    options.OperationsService,
		systemSettingService: options.SystemSettingService,
		membershipService:    options.MembershipService,
		workspaceService:     options.WorkspaceService,
		audioService:         options.AudioService,
		waveformService:      options.WaveformService,
		clipService:          options.ClipService,
		jobService:           options.JobService,
		correctionService:    options.CorrectionService,
		reviewService:        options.ReviewService,
		uploadService:        options.UploadService,
		transcriptService:    options.TranscriptService,
		exportService:        options.ExportService,
		providerService:      options.ProviderService,
		hotwordService:       options.HotwordService,
		llmProfileService:    options.LLMProfileService,
		glossaryService:      options.GlossaryService,
		webhookService:       options.WebhookService,
		realtimeEndpoint:     options.RealtimeEndpoint,
		readinessCheck:       options.ReadinessCheck,
		publicOrigin:         options.PublicOrigin,
		cookieSecure:         options.CookieSecure,
		loginLimiter:         newLoginLimiter(5, time.Minute),
		pairingLimiter:       newLoginLimiter(5, time.Minute),
		passwordLimiter:      newLoginLimiter(5, time.Minute),
		metrics:              newHTTPMetrics(),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/metrics" {
		h.metrics.serveHTTP(w, r)
		return
	}
	requestID := r.Header.Get("X-Request-ID")
	if !validRequestID(requestID) {
		requestID = newRequestID()
	}
	started := time.Now()
	route := metricRoute(r.URL.Path)
	tracked := newTrackedResponseWriter(w)
	h.metrics.begin()
	defer func() {
		panicValue := recover()
		status := tracked.status
		if panicValue != nil && !tracked.wroteHeader {
			status = http.StatusInternalServerError
		}
		duration := time.Since(started)
		h.metrics.observe(r.Method, route, status, duration)
		h.logger.InfoContext(r.Context(), "request handled",
			"method", metricMethod(r.Method),
			"route", route,
			"status", status,
			"duration_ms", float64(duration.Microseconds())/1000,
			"request_id", requestID,
		)
		if panicValue != nil {
			panic(panicValue)
		}
	}()
	w = tracked

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-API-Version", product.APIVersion)
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Server-Version", product.ServerVersion)
	switch r.URL.Path {
	case "/healthz", "/livez":
		if r.Method != http.MethodGet {
			h.writeMethodNotAllowed(w, requestID, http.MethodGet)
			return
		}
		h.writeHealth(w)
	case "/readyz":
		if r.Method != http.MethodGet {
			h.writeMethodNotAllowed(w, requestID, http.MethodGet)
			return
		}
		if h.readinessCheck != nil && h.readinessCheck(r.Context()) != nil {
			h.writeError(w, http.StatusServiceUnavailable, "not_ready", "service is not ready", requestID)
			return
		}
		h.writeHealth(w)
	case "/version":
		if r.Method != http.MethodGet {
			h.writeMethodNotAllowed(w, requestID, http.MethodGet)
			return
		}
		h.writeJSON(w, http.StatusOK, product.CurrentBuildInfo())
	case "/api/v1/system/capabilities":
		if r.Method != http.MethodGet {
			h.writeMethodNotAllowed(w, requestID, http.MethodGet)
			return
		}
		h.writeJSON(w, http.StatusOK, product.CurrentCapabilities())
	case "/api/v1/auth/sessions":
		h.handleAuthSessions(w, r, requestID)
	case "/api/v1/auth/session":
		h.handleAuthSession(w, r, requestID)
	case "/api/v1/auth/session/refresh":
		h.handleAuthSessionRefresh(w, r, requestID)
	case "/api/v1/auth/password":
		h.handleAccountPassword(w, r, requestID)
	case "/api/v1/auth/device-sessions":
		h.handleDeviceSessions(w, r, requestID)
	case "/api/v1/auth/pairing-sessions":
		h.handlePairingSessions(w, r, requestID)
	case "/api/v1/realtime/transcriptions":
		h.handleRealtimeTranscription(w, r, requestID)
	case "/api/v1/api-keys":
		h.handleAPIKeys(w, r, requestID)
	case "/api/v1/admin/jobs":
		h.handleAdminJobs(w, r, requestID)
	case "/api/v1/admin/audit-logs":
		h.handleAdminAuditLogs(w, r, requestID)
	case "/api/v1/admin/system-status":
		h.handleAdminSystemStatus(w, r, requestID)
	case "/api/v1/admin/system-settings":
		h.handleAdminSystemSettings(w, r, requestID)
	case "/api/v1/admin/members":
		h.handleAdminMembers(w, r, requestID)
	case "/api/v1/admin/workspace":
		h.handleAdminWorkspace(w, r, requestID)
	case "/api/v1/assets":
		h.handleAssets(w, r, requestID)
	case "/api/v1/sync/changes":
		h.handleSyncChanges(w, r, requestID)
	case "/api/v1/events":
		h.handleNotifications(w, r, requestID)
	case "/api/v1/collections":
		h.handleCollections(w, r, requestID)
	case "/api/v1/tags":
		h.handleTags(w, r, requestID)
	case "/api/v1/uploads":
		h.handleUploads(w, r, requestID)
	case "/api/v1/provider-profiles":
		h.handleProviderProfiles(w, r, requestID)
	case "/api/v1/hotword-sets":
		h.handleHotwordSets(w, r, requestID)
	case "/api/v1/glossary-sets":
		h.handleGlossarySets(w, r, requestID)
	case "/api/v1/admin/webhooks":
		h.handleAdminWebhooks(w, r, requestID)
	case "/api/v1/llm-profiles":
		h.handleLLMProfiles(w, r, requestID)
	case "/api/v1/asr/provider-capabilities":
		h.handleProviderCapabilities(w, r, requestID)
	case "/api/v1/llm/provider-capabilities":
		h.handleLLMProviderCapabilities(w, r, requestID)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/v1/admin/jobs/") {
			h.handleAdminJobRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/admin/members/") {
			h.handleAdminMemberRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/device-sessions/") {
			h.handleDeviceSessionRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/pairing-sessions/") {
			h.handlePairingSessionRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/collections/") {
			h.handleCollection(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/api-keys/") {
			h.handleAPIKeyRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/glossary-sets/") {
			h.handleGlossarySetRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/admin/webhooks/") {
			h.handleAdminWebhookRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/llm-profiles/") {
			h.handleLLMProfileRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/hotword-sets/") {
			h.handleHotwordSetRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/provider-profiles/") {
			h.handleProviderProfileRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/uploads/") {
			h.handleUploadRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/transcript-revisions/") {
			h.handleTranscriptRevision(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/transcription-jobs/") {
			h.handleTranscriptionJob(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/audio-clips/") {
			h.handleAudioClip(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/transcript-exports/") {
			h.handleTranscriptExport(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/assets/") {
			h.handleAssetRoute(w, r, requestID)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/asset-purge-jobs/") {
			h.handleAssetPurgeJob(w, r, requestID)
			return
		}
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	}
}

func (h *Handler) writeHealth(w http.ResponseWriter) {
	h.writeJSON(w, http.StatusOK, healthResponse{
		Status:    "ok",
		Service:   h.brandName,
		Timestamp: h.now().UTC().Format(time.RFC3339),
	})
}

type healthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func (h *Handler) writeMethodNotAllowed(w http.ResponseWriter, requestID string, allow ...string) {
	w.Header().Set("Allow", strings.Join(allow, ", "))
	h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", requestID)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code, message, requestID string) {
	h.writeJSON(w, status, errorResponse{Error: apiError{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Error("encode HTTP response", "error", err)
	}
}

func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(value[:])
}

func validRequestID(value string) bool {
	if value == "" || len(value) > maxRequestIDLength {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '-', '.', ':', '_':
			continue
		default:
			return false
		}
	}
	return true
}
