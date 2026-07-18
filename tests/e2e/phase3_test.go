package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/correction"
	"github.com/getio0909/voice-asset-server/internal/glossary"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/llmprofile"
	"github.com/getio0909/voice-asset-server/internal/platform/httpapi"
	"github.com/getio0909/voice-asset-server/internal/review"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

func TestPhase3MockCorrectionReviewAndApproval(t *testing.T) {
	pool := migratedE2EPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	authRepository := auth.NewPostgresRepository(pool)
	const (
		email        = "phase3-owner@example.com"
		password     = "correct-horse-battery-staple"
		assetID      = "31000000-0000-4000-8000-000000000003"
		transcriptID = "32000000-0000-4000-8000-000000000003"
		revisionID   = "33000000-0000-4000-8000-000000000003"
		segmentID    = "34000000-0000-4000-8000-000000000003"
	)
	if _, err := auth.NewBootstrapService(authRepository, auth.PasswordHasher{}).CreateOwner(ctx, auth.OwnerInput{
		Email: email, Password: password, WorkspaceName: "Phase 3 E2E",
	}); err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	var userID, workspaceID string
	if err := pool.QueryRow(ctx, `
		SELECT user_account.id::text, membership.workspace_id::text
		FROM users user_account
		JOIN memberships membership ON membership.user_id = user_account.id
		WHERE user_account.email = $1`, email).Scan(&userID, &workspaceID); err != nil {
		t.Fatalf("load owner identity: %v", err)
	}
	text := "今天讨论容易云调度平台的版本 2.0，并确认不会改数字。"
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO assets (id, workspace_id, title, language, status, created_by)
		  VALUES ($1, $2, 'Correction fixture', 'zh-CN', 'ready', $3)`, []any{assetID, workspaceID, userID}},
		{`INSERT INTO transcripts (id, asset_id, language) VALUES ($1, $2, 'zh-CN')`, []any{transcriptID, assetID}},
		{`INSERT INTO transcript_revisions (
			id, transcript_id, kind, text_content, provider_snapshot,
			hotword_snapshot, glossary_snapshot, diff, validation_result, created_by
		  ) VALUES ($1, $2, 'raw_asr', $3, '{"provider_id":"mock_asr"}', '{}', '{}', '{}', '{}', $4)`,
			[]any{revisionID, transcriptID, text, userID}},
		{`INSERT INTO transcript_segments (
			id, revision_id, ordinal, start_ms, end_ms, text_content, confidence, words
		  ) VALUES ($1, $2, 0, 0, 3000, $3, 0.9, '[]')`, []any{segmentID, revisionID, text}},
	}
	for _, statement := range seed {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed correction fixture: %v", err)
		}
	}
	localStorage, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("initialize local storage: %v", err)
	}
	jobRepository := job.NewPostgresRepository(pool)
	jobService := job.NewService(jobRepository)
	transcriptRepository := transcript.NewPostgresRepository(pool)
	transcriptService := transcript.NewService(transcriptRepository)
	glossaryService := glossary.NewService(glossary.NewPostgresRepository(pool))
	profileRepository := llmprofile.NewPostgresRepository(pool)
	profileService := llmprofile.NewService(profileRepository, nil)
	reviewService := review.NewService(review.NewPostgresRepository(pool), transcriptRepository)

	server := httptest.NewUnstartedServer(nil)
	publicOrigin := "http://" + server.Listener.Addr().String()
	server.Config.Handler = httpapi.NewApplicationHandler(httpapi.Options{
		BrandName: "VoiceAsset", AuthService: auth.NewService(authRepository, auth.PasswordHasher{}),
		JobService: jobService, CorrectionService: jobService, TranscriptService: transcriptService,
		GlossaryService: glossaryService, LLMProfileService: profileService, ReviewService: reviewService,
		PublicOrigin: publicOrigin,
	})
	server.Start()
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: server.Client().Transport, Jar: jar, Timeout: 30 * time.Second}
	request(t, client, http.MethodPost, server.URL+"/api/v1/auth/sessions",
		map[string]string{"Content-Type": "application/json", "Origin": publicOrigin},
		stringsReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)), http.StatusCreated)

	glossaryBody := `{
		"display_name":"Platform corrections","scope_type":"workspace","entries":[{
			"canonical_form":"容器云","aliases":["容易云"],"language":"zh-CN",
			"context_terms":["调度"],"forbidden_contexts":[],"regex":false,
			"case_sensitive":false,"priority":100,"description":"E2E domain term"
		}]}`
	glossaryResponse := request(t, client, http.MethodPost, server.URL+"/api/v1/glossary-sets",
		map[string]string{"Content-Type": "application/json", "Origin": publicOrigin},
		stringsReader(glossaryBody), http.StatusCreated)
	var createdGlossary glossary.Set
	decode(t, glossaryResponse.body, &createdGlossary)
	if createdGlossary.CurrentVersion != 1 {
		t.Fatalf("created glossary = %+v", createdGlossary)
	}

	profileBody := `{
		"provider_id":"mock_llm","display_name":"Mock correction","state":"enabled","priority":1,
		"config":{"model":"deterministic_glossary_v1","timeout":"30s","concurrency":32,
		"temperature":0,"context_limit":64000,"structured_output":true,
		"prompt_template":"correction.v1","auto_approval_policy":"never"}
	}`
	profileResponse := request(t, client, http.MethodPost, server.URL+"/api/v1/llm-profiles",
		map[string]string{"Content-Type": "application/json", "Origin": publicOrigin},
		stringsReader(profileBody), http.StatusCreated)
	var createdProfile llmprofile.Profile
	decode(t, profileResponse.body, &createdProfile)
	if createdProfile.ProviderID != llm.MockProviderID || createdProfile.SecretConfigured {
		t.Fatalf("created LLM profile = %+v", createdProfile)
	}

	jobResponse := request(t, client, http.MethodPost,
		server.URL+"/api/v1/transcript-revisions/"+revisionID+"/corrections",
		map[string]string{"Origin": publicOrigin, "Idempotency-Key": "phase3-correction"}, nil, http.StatusAccepted)
	var correctionJob job.Job
	decode(t, jobResponse.body, &correctionJob)
	processor := correction.NewProcessor(
		jobRepository, transcriptRepository, llmprofile.NewResolver(profileRepository, nil, nil),
		glossaryService, localStorage, correction.NewPostgresCommitter(pool), "phase3-worker",
	)
	processed, err := processor.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("correction RunOnce() = (%t, %v)", processed, err)
	}
	finishedResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/transcription-jobs/"+correctionJob.ID, nil, nil, http.StatusOK)
	var finished job.Job
	decode(t, finishedResponse.body, &finished)
	if finished.State != job.StateSucceeded || finished.ResultRevisionID == nil {
		t.Fatalf("finished correction job = %+v", finished)
	}
	correctedResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/transcript-revisions/"+*finished.ResultRevisionID, nil, nil, http.StatusOK)
	var corrected transcript.Revision
	decode(t, correctedResponse.body, &corrected)
	if corrected.Kind != transcript.KindLLMCorrected || corrected.ParentRevisionID != revisionID ||
		corrected.Text != "今天讨论容器云调度平台的版本 2.0，并确认不会改数字。" ||
		corrected.Model != "deterministic_glossary_v1" || len(corrected.Segments) != 1 {
		t.Fatalf("corrected revision = %+v", corrected)
	}
	var diff struct {
		Changes []llm.Change `json:"changes"`
	}
	if err := json.Unmarshal(corrected.Diff, &diff); err != nil || len(diff.Changes) != 1 {
		t.Fatalf("corrected diff = %s, %v", corrected.Diff, err)
	}

	request(t, client, http.MethodPost,
		server.URL+"/api/v1/transcript-revisions/"+corrected.ID+"/reviews",
		map[string]string{"Content-Type": "application/json", "Origin": publicOrigin},
		stringsReader(`{"action":"accept_change","change_index":0}`), http.StatusCreated)
	approvalResponse := request(t, client, http.MethodPost,
		server.URL+"/api/v1/transcript-revisions/"+corrected.ID+"/approve",
		map[string]string{"Content-Type": "application/json", "Origin": publicOrigin},
		stringsReader(`{}`), http.StatusCreated)
	var approval review.ApprovalResult
	decode(t, approvalResponse.body, &approval)
	if approval.HumanRevision.Kind != transcript.KindHumanEdited ||
		approval.ApprovedRevision.Kind != transcript.KindApproved ||
		approval.ApprovedRevision.Text != corrected.Text ||
		approval.ApprovedRevision.ParentRevisionID != approval.HumanRevision.ID {
		t.Fatalf("approval result = %+v", approval)
	}
	request(t, client, http.MethodPost,
		server.URL+"/api/v1/transcript-revisions/"+corrected.ID+"/approve",
		map[string]string{"Content-Type": "application/json", "Origin": publicOrigin},
		bytes.NewReader([]byte(`{}`)), http.StatusConflict)

	var revisionCount, rawResponseCount, reviewCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_revisions WHERE transcript_id = $1`, transcriptID).Scan(&revisionCount); err != nil || revisionCount != 4 {
		t.Fatalf("revision count = %d, %v", revisionCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM asset_objects WHERE asset_id = $1 AND kind = 'provider_raw_response'`, assetID).Scan(&rawResponseCount); err != nil || rawResponseCount != 1 {
		t.Fatalf("raw response count = %d, %v", rawResponseCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_revision_reviews WHERE revision_id = $1`, corrected.ID).Scan(&reviewCount); err != nil || reviewCount != 2 {
		t.Fatalf("review count = %d, %v", reviewCount, err)
	}

	autoConfig := createdProfile.Config
	autoConfig.AutoApprovalPolicy = llm.AutoApprovalGlossaryOnly
	if _, err := profileService.Update(ctx, auth.Principal{
		UserID: userID, WorkspaceID: workspaceID, Scopes: []string{auth.ScopeAdminWrite},
	}, createdProfile.ID, createdProfile.Version, llmprofile.UpdateInput{Config: &autoConfig}); err != nil {
		t.Fatalf("enable glossary-only auto approval: %v", err)
	}
	const (
		autoAssetID      = "41000000-0000-4000-8000-000000000003"
		autoTranscriptID = "42000000-0000-4000-8000-000000000003"
		autoRevisionID   = "43000000-0000-4000-8000-000000000003"
		autoSegmentID    = "44000000-0000-4000-8000-000000000003"
	)
	autoText := "再次讨论容易云调度平台的版本 3.0。"
	autoSeed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO assets (id, workspace_id, title, language, status, created_by)
		  VALUES ($1, $2, 'Auto approval fixture', 'zh-CN', 'ready', $3)`, []any{autoAssetID, workspaceID, userID}},
		{`INSERT INTO transcripts (id, asset_id, language) VALUES ($1, $2, 'zh-CN')`, []any{autoTranscriptID, autoAssetID}},
		{`INSERT INTO transcript_revisions (
			id, transcript_id, kind, text_content, provider_snapshot,
			hotword_snapshot, glossary_snapshot, diff, validation_result, created_by
		  ) VALUES ($1, $2, 'normalized', $3, '{"provider_id":"mock_asr"}', '{}', '{}', '{}', '{}', $4)`,
			[]any{autoRevisionID, autoTranscriptID, autoText, userID}},
		{`INSERT INTO transcript_segments (
			id, revision_id, ordinal, start_ms, end_ms, text_content, confidence, words
		  ) VALUES ($1, $2, 0, 0, 2400, $3, 0.9, '[]')`, []any{autoSegmentID, autoRevisionID, autoText}},
	}
	for _, statement := range autoSeed {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed auto-approval fixture: %v", err)
		}
	}
	autoJobResponse := request(t, client, http.MethodPost,
		server.URL+"/api/v1/transcript-revisions/"+autoRevisionID+"/corrections",
		map[string]string{"Origin": publicOrigin, "Idempotency-Key": "phase3-auto-correction"}, nil, http.StatusAccepted)
	var autoJob job.Job
	decode(t, autoJobResponse.body, &autoJob)
	processed, err = processor.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("auto correction RunOnce() = (%t, %v)", processed, err)
	}
	autoFinishedResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/transcription-jobs/"+autoJob.ID, nil, nil, http.StatusOK)
	var autoFinished job.Job
	decode(t, autoFinishedResponse.body, &autoFinished)
	if autoFinished.State != job.StateSucceeded || autoFinished.ResultRevisionID == nil {
		t.Fatalf("finished auto correction job = %+v", autoFinished)
	}
	autoApprovedResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/transcript-revisions/"+*autoFinished.ResultRevisionID, nil, nil, http.StatusOK)
	var autoApproved transcript.Revision
	decode(t, autoApprovedResponse.body, &autoApproved)
	if autoApproved.Kind != transcript.KindApproved || autoApproved.CreatedByType != "system" ||
		autoApproved.Text != "再次讨论容器云调度平台的版本 3.0。" || autoApproved.ParentRevisionID == "" {
		t.Fatalf("auto-approved revision = %+v", autoApproved)
	}
	autoHumanResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/transcript-revisions/"+autoApproved.ParentRevisionID, nil, nil, http.StatusOK)
	var autoHuman transcript.Revision
	decode(t, autoHumanResponse.body, &autoHuman)
	if autoHuman.Kind != transcript.KindHumanEdited || autoHuman.CreatedByType != "system" ||
		autoHuman.ParentRevisionID == "" {
		t.Fatalf("auto-reviewed revision = %+v", autoHuman)
	}
	autoCorrectedResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/transcript-revisions/"+autoHuman.ParentRevisionID, nil, nil, http.StatusOK)
	var autoCorrected transcript.Revision
	decode(t, autoCorrectedResponse.body, &autoCorrected)
	if autoCorrected.Kind != transcript.KindLLMCorrected || autoCorrected.ReviewStatus != "approved" ||
		autoCorrected.ParentRevisionID != autoRevisionID {
		t.Fatalf("auto correction source = %+v", autoCorrected)
	}
	request(t, client, http.MethodPost,
		server.URL+"/api/v1/transcript-revisions/"+autoCorrected.ID+"/approve",
		map[string]string{"Content-Type": "application/json", "Origin": publicOrigin},
		bytes.NewReader([]byte(`{}`)), http.StatusConflict)
	var autoReviewCount, autoAuditCount, autoRevisionCount int
	var autoReviewMetadata []byte
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transcript_revision_reviews
		WHERE revision_id = $1 AND action = 'approve'`, autoCorrected.ID).Scan(&autoReviewCount); err != nil {
		t.Fatalf("load auto review evidence: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT metadata FROM transcript_revision_reviews
		WHERE revision_id = $1 AND action = 'approve'`, autoCorrected.ID).Scan(&autoReviewMetadata); err != nil {
		t.Fatalf("load auto review metadata: %v", err)
	}
	var autoMetadata struct {
		Automated          bool   `json:"automated"`
		AutoApprovalPolicy string `json:"auto_approval_policy"`
	}
	if err := json.Unmarshal(autoReviewMetadata, &autoMetadata); err != nil || autoReviewCount != 1 ||
		!autoMetadata.Automated || autoMetadata.AutoApprovalPolicy != llm.AutoApprovalGlossaryOnly {
		t.Fatalf("auto review evidence = %d, %s, %v", autoReviewCount, autoReviewMetadata, err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE action = 'transcript.auto_approved' AND target_id = $1 AND actor_type = 'system'`,
		autoApproved.ID).Scan(&autoAuditCount); err != nil || autoAuditCount != 1 {
		t.Fatalf("auto approval audit count = %d, %v", autoAuditCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_revisions WHERE transcript_id = $1`,
		autoTranscriptID).Scan(&autoRevisionCount); err != nil || autoRevisionCount != 4 {
		t.Fatalf("auto approval revision count = %d, %v", autoRevisionCount, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE transcript_revisions SET text_content = 'mutated' WHERE id = $1`, revisionID); err == nil {
		t.Fatal("immutable source revision accepted an update")
	}
}
