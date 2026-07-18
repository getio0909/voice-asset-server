package asset_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/audit"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryCreateIsIdempotentAndWorkspaceScoped(t *testing.T) {
	pool := migratedAssetPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID       = "10000000-0000-4000-8000-000000000011"
		otherSpace        = "10000000-0000-4000-8000-000000000012"
		userID            = "20000000-0000-4000-8000-000000000011"
		assetID           = "30000000-0000-4000-8000-000000000011"
		auditID           = "40000000-0000-4000-8000-000000000011"
		collectionID      = "50000000-0000-4000-8000-000000000011"
		otherCollectionID = "50000000-0000-4000-8000-000000000012"
		apiKeyID          = "60000000-0000-4000-8000-000000000011"
		tagID             = "70000000-0000-4000-8000-000000000011"
		requestHash       = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')", workspaceID, otherSpace); err != nil {
		t.Fatalf("seed workspaces: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, 'owner@example.com', 'encoded', 'active')`, userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`, workspaceID, otherSpace, userID); err != nil {
		t.Fatalf("seed memberships: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO collections (id, workspace_id, name, created_by)
		VALUES ($1, $3, 'Primary collection', $5), ($2, $4, 'Other collection', $5)`,
		collectionID, otherCollectionID, workspaceID, otherSpace, userID,
	); err != nil {
		t.Fatalf("seed collections: %v", err)
	}

	repository := asset.NewPostgresRepository(pool)
	params := asset.CreateParams{
		AssetID: assetID, AuditID: auditID, WorkspaceID: workspaceID, CreatedBy: userID,
		Title: "First recording", Language: "en", IdempotencyKey: "asset-key", RequestHash: requestHash,
	}
	created, replayed, err := repository.Create(ctx, params)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if replayed || created.ID != assetID || created.Status != "draft" || created.Version != 1 {
		t.Fatalf("Create() = (%+v, %t), want new draft", created, replayed)
	}
	params.AssetID = "30000000-0000-4000-8000-000000000099"
	params.AuditID = "40000000-0000-4000-8000-000000000099"
	replayedAsset, replayed, err := repository.Create(ctx, params)
	if err != nil || !replayed || replayedAsset.ID != assetID {
		t.Fatalf("replayed Create() = (%+v, %t, %v)", replayedAsset, replayed, err)
	}
	params.RequestHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, _, err := repository.Create(ctx, params); !errors.Is(err, asset.ErrIdempotencyConflict) {
		t.Fatalf("conflicting Create() error = %v, want ErrIdempotencyConflict", err)
	}
	if _, err := repository.Get(ctx, otherSpace, assetID); !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("cross-workspace Get() error = %v, want ErrNotFound", err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action = 'asset.created'").Scan(&auditCount); err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}

	assetService := asset.NewService(repository)
	collection := collectionID
	metadataPrincipal := auth.Principal{
		UserID: userID, WorkspaceID: workspaceID, Role: "agent",
		CredentialType: "api_key", CredentialID: apiKeyID,
		Scopes: []string{auth.ScopeMetadataWrite},
	}
	updated, err := assetService.UpdateMetadata(ctx, metadataPrincipal, assetID, 1, asset.UpdateMetadataInput{
		Title: "Updated recording", Language: "zh-cn", CollectionID: &collection,
	}, "asset-metadata-update")
	if err != nil || updated.Version != 2 || updated.CollectionID == nil || *updated.CollectionID != collectionID ||
		updated.Title != "Updated recording" || updated.Language != "zh-CN" {
		t.Fatalf("UpdateMetadata() = (%+v, %v)", updated, err)
	}
	if _, err := assetService.UpdateMetadata(ctx, metadataPrincipal, assetID, 1, asset.UpdateMetadataInput{
		Title: "Stale recording", Language: "en", CollectionID: nil,
	}, "asset-metadata-stale"); !errors.Is(err, asset.ErrConflict) {
		t.Fatalf("UpdateMetadata(stale) error = %v", err)
	}
	otherCollection := otherCollectionID
	if _, err := assetService.UpdateMetadata(ctx, metadataPrincipal, assetID, 2, asset.UpdateMetadataInput{
		Title: "Cross workspace", Language: "en", CollectionID: &otherCollection,
	}, "asset-metadata-cross-collection"); !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("UpdateMetadata(cross workspace collection) error = %v", err)
	}
	otherPrincipal := metadataPrincipal
	otherPrincipal.WorkspaceID = otherSpace
	if _, err := assetService.UpdateMetadata(ctx, otherPrincipal, assetID, 2, asset.UpdateMetadataInput{
		Title: "Cross workspace", Language: "en", CollectionID: nil,
	}, "asset-metadata-cross-asset"); !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("UpdateMetadata(cross workspace asset) error = %v", err)
	}
	var metadataActor, metadataAPIKey, metadataVersion string
	if err := pool.QueryRow(ctx, `
		SELECT actor_type, metadata->>'api_key_id', metadata->>'version'
		FROM audit_logs WHERE request_id = 'asset-metadata-update'`,
	).Scan(&metadataActor, &metadataAPIKey, &metadataVersion); err != nil ||
		metadataActor != "agent" || metadataAPIKey != apiKeyID || metadataVersion != "2" {
		t.Fatalf("metadata audit = actor=%q api_key=%q version=%q error=%v", metadataActor, metadataAPIKey, metadataVersion, err)
	}
	var rejectedAuditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE request_id IN ('asset-metadata-stale', 'asset-metadata-cross-collection', 'asset-metadata-cross-asset')`,
	).Scan(&rejectedAuditCount); err != nil || rejectedAuditCount != 0 {
		t.Fatalf("rejected metadata audit count = (%d, %v)", rejectedAuditCount, err)
	}
	auditService := audit.NewService(audit.NewPostgresRepository(pool))
	if err := auditService.Record(ctx, audit.RecordInput{
		Principal: auth.Principal{UserID: userID, WorkspaceID: workspaceID, Role: "agent"},
		Action:    "asset.read", TargetType: "asset", TargetID: assetID, RequestID: "integration-request-1",
	}); err != nil {
		t.Fatalf("record read audit: %v", err)
	}
	var actorType string
	if err := pool.QueryRow(ctx, `
		SELECT actor_type FROM audit_logs
		WHERE action = 'asset.read' AND target_id = $1 AND request_id = 'integration-request-1'`, assetID,
	).Scan(&actorType); err != nil || actorType != "agent" {
		t.Fatalf("read audit actor = (%q, %v)", actorType, err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO assets (id, workspace_id, title, language, status, created_by, created_at, updated_at, deleted_at)
		VALUES
			('30000000-0000-4000-8000-000000000012', $1, 'Budget 100%', 'en', 'ready', $3, clock_timestamp() + interval '3 minutes', clock_timestamp(), NULL),
			('30000000-0000-4000-8000-000000000013', $1, 'Budget 1000', 'en', 'ready', $3, clock_timestamp() + interval '2 minutes', clock_timestamp(), NULL),
			('30000000-0000-4000-8000-000000000014', $2, 'Budget 100%', 'en', 'ready', $3, clock_timestamp() + interval '4 minutes', clock_timestamp(), NULL),
			('30000000-0000-4000-8000-000000000015', $1, 'Budget 100%', 'en', 'trashed', $3, clock_timestamp() + interval '5 minutes', clock_timestamp(), clock_timestamp())`,
		workspaceID, otherSpace, userID,
	); err != nil {
		t.Fatalf("seed list assets: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tags (id, workspace_id, name, created_by)
		VALUES ($1, $2, 'Budget review', $3)`,
		tagID, workspaceID, userID,
	); err != nil {
		t.Fatalf("seed asset list tag definition: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_tags (workspace_id, asset_id, tag_id, created_by)
		VALUES ($1, '30000000-0000-4000-8000-000000000012', $2, $3)`,
		workspaceID, tagID, userID,
	); err != nil {
		t.Fatalf("seed asset list tag assignment: %v", err)
	}
	literalMatches, err := repository.List(ctx, asset.ListParams{WorkspaceID: workspaceID, Query: "%", Limit: 10})
	if err != nil {
		t.Fatalf("List(literal query) error = %v", err)
	}
	if len(literalMatches) != 1 || literalMatches[0].ID != "30000000-0000-4000-8000-000000000012" {
		t.Fatalf("List(literal query) = %+v", literalMatches)
	}
	firstPage, err := repository.List(ctx, asset.ListParams{WorkspaceID: workspaceID, Limit: 2})
	if err != nil || len(firstPage) != 2 {
		t.Fatalf("List(first page) = (%+v, %v)", firstPage, err)
	}
	secondPage, err := repository.List(ctx, asset.ListParams{
		WorkspaceID: workspaceID, Limit: 2, BeforeCreatedAt: &firstPage[1].CreatedAt, BeforeID: firstPage[1].ID,
	})
	if err != nil || len(secondPage) != 1 || secondPage[0].ID != assetID {
		t.Fatalf("List(second page) = (%+v, %v)", secondPage, err)
	}
	collectionMatches, err := repository.List(ctx, asset.ListParams{
		WorkspaceID: workspaceID, CollectionID: collectionID, Limit: 10,
	})
	if err != nil || len(collectionMatches) != 1 || collectionMatches[0].ID != assetID {
		t.Fatalf("List(collection) = (%+v, %v)", collectionMatches, err)
	}
	tagMatches, err := repository.List(ctx, asset.ListParams{
		WorkspaceID: workspaceID, TagID: tagID, Limit: 10,
	})
	if err != nil || len(tagMatches) != 1 || tagMatches[0].ID != "30000000-0000-4000-8000-000000000012" {
		t.Fatalf("List(tag) = (%+v, %v)", tagMatches, err)
	}
	trashedMatches, err := repository.List(ctx, asset.ListParams{
		WorkspaceID: workspaceID, Status: "trashed", Limit: 10,
	})
	if err != nil || len(trashedMatches) != 1 || trashedMatches[0].ID != "30000000-0000-4000-8000-000000000015" {
		t.Fatalf("List(trashed) = (%+v, %v)", trashedMatches, err)
	}
	createdFrom := firstPage[1].CreatedAt
	createdBefore := firstPage[0].CreatedAt.Add(time.Microsecond)
	timeMatches, err := repository.List(ctx, asset.ListParams{
		WorkspaceID: workspaceID, CreatedFrom: &createdFrom, CreatedBefore: &createdBefore, Limit: 10,
	})
	if err != nil || len(timeMatches) != 2 {
		t.Fatalf("List(created range) = (%+v, %v)", timeMatches, err)
	}

	lifecyclePrincipal := auth.Principal{
		UserID: userID, WorkspaceID: workspaceID, Role: "agent",
		CredentialType: "api_key", CredentialID: apiKeyID,
		Scopes: []string{auth.ScopeAssetsWrite},
	}
	lifecycleAssetID := "30000000-0000-4000-8000-000000000012"
	trashed, err := assetService.Trash(ctx, lifecyclePrincipal, lifecycleAssetID, 1, "asset-trash")
	if err != nil || trashed.Status != "trashed" || trashed.Version != 2 {
		t.Fatalf("Trash() = (%+v, %v)", trashed, err)
	}
	if _, err := repository.Get(ctx, workspaceID, lifecycleAssetID); !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("Get(trashed) error = %v, want ErrNotFound", err)
	}
	restored, err := assetService.Restore(ctx, lifecyclePrincipal, lifecycleAssetID, 2, "asset-restore")
	if err != nil || restored.Status != "ready" || restored.Version != 3 {
		t.Fatalf("Restore() = (%+v, %v)", restored, err)
	}
	if visible, err := repository.Get(ctx, workspaceID, lifecycleAssetID); err != nil || visible.Status != "ready" {
		t.Fatalf("Get(restored) = (%+v, %v)", visible, err)
	}
	if _, err := assetService.Trash(ctx, lifecyclePrincipal, lifecycleAssetID, 1, "asset-trash-stale"); !errors.Is(err, asset.ErrConflict) {
		t.Fatalf("Trash(stale) error = %v, want ErrConflict", err)
	}
	otherLifecyclePrincipal := lifecyclePrincipal
	otherLifecyclePrincipal.WorkspaceID = otherSpace
	if _, err := assetService.Trash(ctx, otherLifecyclePrincipal, lifecycleAssetID, 3, "asset-trash-cross"); !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("Trash(cross workspace) error = %v, want ErrNotFound", err)
	}
	var lifecycleAction, lifecycleActor, lifecycleAPIKey, lifecycleStatus, lifecycleVersion string
	if err := pool.QueryRow(ctx, `
		SELECT action, actor_type, metadata->>'api_key_id', metadata->>'status', metadata->>'version'
		FROM audit_logs WHERE request_id = 'asset-trash'`,
	).Scan(&lifecycleAction, &lifecycleActor, &lifecycleAPIKey, &lifecycleStatus, &lifecycleVersion); err != nil ||
		lifecycleAction != "asset.trashed" || lifecycleActor != "agent" || lifecycleAPIKey != apiKeyID ||
		lifecycleStatus != "ready" || lifecycleVersion != "2" {
		t.Fatalf("trash audit = action=%q actor=%q api_key=%q status=%q version=%q error=%v",
			lifecycleAction, lifecycleActor, lifecycleAPIKey, lifecycleStatus, lifecycleVersion, err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT action, metadata->>'status', metadata->>'version'
		FROM audit_logs WHERE request_id = 'asset-restore'`,
	).Scan(&lifecycleAction, &lifecycleStatus, &lifecycleVersion); err != nil ||
		lifecycleAction != "asset.restored" || lifecycleStatus != "ready" || lifecycleVersion != "3" {
		t.Fatalf("restore audit = action=%q status=%q version=%q error=%v",
			lifecycleAction, lifecycleStatus, lifecycleVersion, err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs WHERE request_id IN ('asset-trash-stale', 'asset-trash-cross')`,
	).Scan(&rejectedAuditCount); err != nil || rejectedAuditCount != 0 {
		t.Fatalf("rejected lifecycle audit count = (%d, %v)", rejectedAuditCount, err)
	}
}

func TestPostgresRepositorySearchesLatestTranscriptSegmentsWithProviderAndSpeakerFilters(t *testing.T) {
	pool := migratedAssetPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID = "10000000-0000-4000-8000-000000000021"
		otherSpace  = "10000000-0000-4000-8000-000000000022"
		userID      = "20000000-0000-4000-8000-000000000021"
		assetID     = "30000000-0000-4000-8000-000000000021"
		otherAsset  = "30000000-0000-4000-8000-000000000022"
		transcript  = "40000000-0000-4000-8000-000000000021"
		otherText   = "40000000-0000-4000-8000-000000000022"
		rawRevision = "50000000-0000-4000-8000-000000000021"
		latest      = "50000000-0000-4000-8000-000000000022"
		otherLatest = "50000000-0000-4000-8000-000000000023"
		latestHit   = "60000000-0000-4000-8000-000000000021"
	)
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Search'), ($2, 'Other')", workspaceID, otherSpace); err != nil {
		t.Fatalf("seed search workspaces: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, 'search@example.com', 'encoded', 'active')`, userID); err != nil {
		t.Fatalf("seed search user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`, workspaceID, otherSpace, userID); err != nil {
		t.Fatalf("seed search memberships: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets (id, workspace_id, title, language, status, created_by)
		VALUES ($1, $3, 'Planning archive', 'zh-CN', 'ready', $5),
		       ($2, $4, 'Other planning archive', 'en', 'ready', $5)`,
		assetID, otherAsset, workspaceID, otherSpace, userID,
	); err != nil {
		t.Fatalf("seed search assets: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO transcripts (id, asset_id, language)
		VALUES ($1, $3, 'zh-CN'), ($2, $4, 'en')`, transcript, otherText, assetID, otherAsset); err != nil {
		t.Fatalf("seed search transcripts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO transcript_revisions (
			id, transcript_id, kind, text_content, provider_snapshot, created_at
		) VALUES
			($1, $4, 'raw_asr', 'obsolete revenue wording', '{"provider_id":"mock_asr"}', '2026-07-17T01:00:00Z'),
			($2, $4, 'approved', '本次季度收入增长。', '{"provider_id":"mock_asr"}', '2026-07-17T02:00:00Z'),
			($3, $5, 'raw_asr', 'quarterly revenue outside workspace', '{"provider_id":"mock_asr"}', '2026-07-17T02:00:00Z')`,
		rawRevision, latest, otherLatest, transcript, otherText,
	); err != nil {
		t.Fatalf("seed search revisions: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO transcript_segments (
			id, revision_id, ordinal, start_ms, end_ms, speaker, text_content
		) VALUES
			('60000000-0000-4000-8000-000000000020', $1, 0, 0, 900, 'Alice', 'obsolete revenue wording'),
			($4, $2, 0, 1000, 2400, 'Alice', '本次季度收入增长。'),
			('60000000-0000-4000-8000-000000000023', $2, 1, 2500, 3600, 'Alice', 'revenue quarterly result'),
			('60000000-0000-4000-8000-000000000024', $3, 0, 0, 1000, 'Alice', 'quarterly revenue outside workspace')`,
		rawRevision, latest, otherLatest, latestHit,
	); err != nil {
		t.Fatalf("seed search segments: %v", err)
	}

	repository := asset.NewPostgresRepository(pool)
	matches, err := repository.List(ctx, asset.ListParams{
		WorkspaceID: workspaceID, Query: "季度", ProviderID: "mock_asr", Speaker: "alice", Limit: 10,
	})
	if err != nil {
		t.Fatalf("List(search) error = %v", err)
	}
	if len(matches) != 1 || matches[0].ID != assetID || matches[0].Search == nil {
		t.Fatalf("List(search) = %+v", matches)
	}
	search := matches[0].Search
	if search.Title || len(search.ProviderIDs) != 1 || search.ProviderIDs[0] != "mock_asr" || len(search.Segments) != 1 {
		t.Fatalf("search metadata = %+v", search)
	}
	hit := search.Segments[0]
	if hit.TranscriptID != transcript || hit.RevisionID != latest || hit.SegmentID != latestHit ||
		hit.StartMS != 1000 || hit.EndMS != 2400 || hit.Speaker == nil || *hit.Speaker != "Alice" {
		t.Fatalf("segment hit = %+v", hit)
	}
	fullTextMatches, err := repository.List(ctx, asset.ListParams{
		WorkspaceID: workspaceID, Query: "quarterly revenue", Speaker: "ALICE", Limit: 10,
	})
	if err != nil || len(fullTextMatches) != 1 || fullTextMatches[0].Search == nil ||
		len(fullTextMatches[0].Search.Segments) != 1 ||
		fullTextMatches[0].Search.Segments[0].SegmentID != "60000000-0000-4000-8000-000000000023" {
		t.Fatalf("List(full-text segment terms) = (%+v, %v)", fullTextMatches, err)
	}
	titleMatches, err := repository.List(ctx, asset.ListParams{
		WorkspaceID: workspaceID, Query: "archive planning", Limit: 10,
	})
	if err != nil || len(titleMatches) != 1 || titleMatches[0].Search == nil ||
		!titleMatches[0].Search.Title || len(titleMatches[0].Search.Segments) != 0 {
		t.Fatalf("List(full-text title terms) = (%+v, %v)", titleMatches, err)
	}

	for name, params := range map[string]asset.ListParams{
		"old revision": {WorkspaceID: workspaceID, Query: "obsolete", Limit: 10},
		"provider":     {WorkspaceID: workspaceID, ProviderID: "tencent_asr", Limit: 10},
		"speaker":      {WorkspaceID: workspaceID, Speaker: "Bob", Limit: 10},
	} {
		t.Run(name, func(t *testing.T) {
			items, listErr := repository.List(ctx, params)
			if listErr != nil || len(items) != 0 {
				t.Fatalf("List(%s) = (%+v, %v), want no matches", name, items, listErr)
			}
		})
	}

	plain, err := repository.List(ctx, asset.ListParams{WorkspaceID: workspaceID, Limit: 10})
	if err != nil || len(plain) != 1 || plain[0].Search != nil {
		t.Fatalf("List(plain) = (%+v, %v)", plain, err)
	}
}

func migratedAssetPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("asset_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create pool")
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire migration connection")
	}
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err != nil {
		connection.Release()
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := migration.Apply(ctx, connection.Conn(), files); err != nil {
		connection.Release()
		t.Fatalf("apply migrations: %v", err)
	}
	connection.Release()
	return pool
}
