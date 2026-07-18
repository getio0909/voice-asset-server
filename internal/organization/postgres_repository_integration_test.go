package organization_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/organization"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	organizationWorkspaceID = "10000000-0000-4000-8000-000000000061"
	organizationOtherSpace  = "10000000-0000-4000-8000-000000000062"
	organizationUserID      = "20000000-0000-4000-8000-000000000061"
	organizationAssetID     = "30000000-0000-4000-8000-000000000061"
	organizationReadyAsset  = "30000000-0000-4000-8000-000000000062"
	organizationDeleted     = "30000000-0000-4000-8000-000000000063"
	organizationOtherAsset  = "30000000-0000-4000-8000-000000000064"
	organizationCollection  = "40000000-0000-4000-8000-000000000061"
	organizationOldCollect  = "40000000-0000-4000-8000-000000000062"
	organizationTag         = "50000000-0000-4000-8000-000000000061"
	organizationOldTag      = "50000000-0000-4000-8000-000000000062"
	organizationOtherTag    = "50000000-0000-4000-8000-000000000063"
	organizationAnnotation  = "60000000-0000-4000-8000-000000000061"
	organizationOldNote     = "60000000-0000-4000-8000-000000000062"
	organizationAPIKeyID    = "80000000-0000-4000-8000-000000000061"
)

func TestPostgresRepositoryOrganizationReadsAreStableAndWorkspaceScoped(t *testing.T) {
	pool := migratedOrganizationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	seedOrganizationReadFixtures(t, ctx, pool)

	service := organization.NewService(organization.NewPostgresRepository(pool))
	principal := auth.Principal{
		WorkspaceID: organizationWorkspaceID,
		Scopes:      []string{auth.ScopeAssetsRead},
	}

	collections, err := service.ListCollections(ctx, principal, organization.ListInput{Limit: 1})
	if err != nil || len(collections.Items) != 1 || collections.NextCursor == nil {
		t.Fatalf("ListCollections(first) = (%+v, %v)", collections, err)
	}
	if item := collections.Items[0]; item.ID != organizationCollection || item.AssetCount != 1 {
		t.Fatalf("first collection = %+v", item)
	}
	collection, err := service.GetCollection(ctx, principal, organizationCollection)
	if err != nil || collection.ID != organizationCollection || collection.AssetCount != 1 {
		t.Fatalf("GetCollection() = (%+v, %v)", collection, err)
	}
	otherPrincipal := principal
	otherPrincipal.WorkspaceID = organizationOtherSpace
	if _, err := service.GetCollection(ctx, otherPrincipal, organizationCollection); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("GetCollection(cross workspace) error = %v", err)
	}
	collections, err = service.ListCollections(ctx, principal, organization.ListInput{
		Limit: 1, Cursor: *collections.NextCursor,
	})
	if err != nil || len(collections.Items) != 1 || collections.Items[0].ID != organizationOldCollect || collections.NextCursor != nil {
		t.Fatalf("ListCollections(second) = (%+v, %v)", collections, err)
	}

	tags, err := service.ListTags(ctx, principal, organization.ListInput{Limit: 1})
	if err != nil || len(tags.Items) != 1 || tags.NextCursor == nil {
		t.Fatalf("ListTags(first) = (%+v, %v)", tags, err)
	}
	if item := tags.Items[0]; item.ID != organizationTag || item.AssetCount != 1 {
		t.Fatalf("first tag = %+v", item)
	}
	tags, err = service.ListTags(ctx, principal, organization.ListInput{
		Limit: 1, Cursor: *tags.NextCursor,
	})
	if err != nil || len(tags.Items) != 1 || tags.Items[0].ID != organizationOldTag || tags.NextCursor != nil {
		t.Fatalf("ListTags(second) = (%+v, %v)", tags, err)
	}
	assetTags, err := service.ListAssetTags(ctx, principal, organization.AssetTagListInput{
		AssetID: organizationAssetID,
	})
	if err != nil || len(assetTags.Items) != 1 || assetTags.Items[0].ID != organizationTag ||
		assetTags.Items[0].AssetCount != 1 || assetTags.NextCursor != nil {
		t.Fatalf("ListAssetTags() = (%+v, %v)", assetTags, err)
	}
	if _, err := service.ListAssetTags(ctx, principal, organization.AssetTagListInput{
		AssetID: organizationDeleted,
	}); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("ListAssetTags(deleted asset) error = %v", err)
	}
	if _, err := service.ListAssetTags(ctx, principal, organization.AssetTagListInput{
		AssetID: organizationOtherAsset,
	}); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("ListAssetTags(cross workspace) error = %v", err)
	}

	annotations, err := service.ListAnnotations(ctx, principal, organization.AnnotationListInput{
		AssetID: organizationAssetID, Limit: 1,
	})
	if err != nil || len(annotations.Items) != 1 || annotations.NextCursor == nil {
		t.Fatalf("ListAnnotations(first) = (%+v, %v)", annotations, err)
	}
	if item := annotations.Items[0]; item.ID != organizationAnnotation || item.StartMS != 1250 || item.EndMS == nil || *item.EndMS != 2500 {
		t.Fatalf("first annotation = %+v", item)
	}
	annotations, err = service.ListAnnotations(ctx, principal, organization.AnnotationListInput{
		AssetID: organizationAssetID, Limit: 1, Cursor: *annotations.NextCursor,
	})
	if err != nil || len(annotations.Items) != 1 || annotations.Items[0].ID != organizationOldNote || annotations.NextCursor != nil {
		t.Fatalf("ListAnnotations(second) = (%+v, %v)", annotations, err)
	}
	if _, err := service.ListAnnotations(ctx, principal, organization.AnnotationListInput{
		AssetID: organizationOtherAsset,
	}); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("ListAnnotations(cross workspace) error = %v", err)
	}

	status, err := service.GetProcessingStatus(ctx, principal, organizationAssetID)
	if err != nil {
		t.Fatalf("GetProcessingStatus() error = %v", err)
	}
	if status.AssetStatus != "processing" || !status.Active || len(status.Jobs) != 20 || status.Jobs[0].State != "queued" {
		t.Fatalf("processing status = %+v", status)
	}
	for _, item := range status.Jobs {
		if item.ID == "70000000-0000-4000-8000-000000000020" {
			t.Fatalf("oldest job was not bounded: %+v", item)
		}
	}
	ready, err := service.GetProcessingStatus(ctx, principal, organizationReadyAsset)
	if err != nil || ready.Active || len(ready.Jobs) != 0 {
		t.Fatalf("ready processing status = (%+v, %v)", ready, err)
	}
	if _, err := service.GetProcessingStatus(ctx, principal, organizationOtherAsset); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("GetProcessingStatus(cross workspace) error = %v", err)
	}
}

func TestPostgresRepositoryOrganizationMutationsAreAtomicAuditedAndWorkspaceScoped(t *testing.T) {
	pool := migratedOrganizationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	seedOrganizationReadFixtures(t, ctx, pool)

	service := organization.NewService(organization.NewPostgresRepository(pool))
	principal := auth.Principal{
		UserID: organizationUserID, WorkspaceID: organizationWorkspaceID, Role: "agent",
		CredentialType: "api_key", CredentialID: organizationAPIKeyID,
		Scopes: []string{auth.ScopeMetadataWrite},
	}

	added, err := service.AddTags(ctx, principal, organizationAssetID, organization.TagMutationInput{
		TagIDs: []string{organizationOldTag},
	}, "organization-add-tags")
	if err != nil || added.ChangedCount != 1 || len(added.TagIDs) != 1 || added.TagIDs[0] != organizationOldTag {
		t.Fatalf("AddTags() = (%+v, %v)", added, err)
	}
	replayed, err := service.AddTags(ctx, principal, organizationAssetID, organization.TagMutationInput{
		TagIDs: []string{organizationOldTag},
	}, "organization-replay-tags")
	if err != nil || replayed.ChangedCount != 0 {
		t.Fatalf("AddTags(replay) = (%+v, %v)", replayed, err)
	}
	removed, err := service.RemoveTags(ctx, principal, organizationAssetID, organization.TagMutationInput{
		TagIDs: []string{organizationOldTag},
	}, "organization-remove-tags")
	if err != nil || removed.ChangedCount != 1 {
		t.Fatalf("RemoveTags() = (%+v, %v)", removed, err)
	}
	if _, err := service.AddTags(ctx, principal, organizationAssetID, organization.TagMutationInput{
		TagIDs: []string{organizationOtherTag},
	}, "organization-cross-tag"); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("AddTags(cross workspace tag) error = %v", err)
	}
	if _, err := service.AddTags(ctx, principal, organizationOtherAsset, organization.TagMutationInput{
		TagIDs: []string{organizationTag},
	}, "organization-cross-asset"); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("AddTags(cross workspace asset) error = %v", err)
	}

	endMS := int64(3500)
	created, err := service.CreateAnnotation(ctx, principal, organizationAssetID, organization.AnnotationCreateInput{
		Kind: "note", StartMS: 3000, EndMS: &endMS, Body: "Review this decision",
	}, "organization-create-annotation")
	if err != nil || created.AssetID != organizationAssetID || created.Kind != "note" || created.Body != "Review this decision" {
		t.Fatalf("CreateAnnotation() = (%+v, %v)", created, err)
	}
	if _, err := service.CreateAnnotation(ctx, principal, organizationOtherAsset, organization.AnnotationCreateInput{
		Kind: "bookmark", StartMS: 0,
	}, "organization-cross-annotation"); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("CreateAnnotation(cross workspace) error = %v", err)
	}

	assertOrganizationAudit(t, ctx, pool, "organization-add-tags", "asset.tags_added", "1")
	assertOrganizationAudit(t, ctx, pool, "organization-replay-tags", "asset.tags_added", "0")
	assertOrganizationAudit(t, ctx, pool, "organization-remove-tags", "asset.tags_removed", "1")
	assertOrganizationAudit(t, ctx, pool, "organization-create-annotation", "annotation.created", "")

	var crossWorkspaceAuditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE request_id IN ('organization-cross-tag', 'organization-cross-asset', 'organization-cross-annotation')`,
	).Scan(&crossWorkspaceAuditCount); err != nil || crossWorkspaceAuditCount != 0 {
		t.Fatalf("cross-workspace audit count = (%d, %v)", crossWorkspaceAuditCount, err)
	}
}

func assertOrganizationAudit(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	requestID,
	wantAction,
	wantChangedCount string,
) {
	t.Helper()
	var actorType, action, apiKeyID string
	var changedCount *string
	if err := pool.QueryRow(ctx, `
		SELECT actor_type, action, metadata->>'api_key_id', metadata->>'changed_count'
		FROM audit_logs WHERE request_id = $1`, requestID,
	).Scan(&actorType, &action, &apiKeyID, &changedCount); err != nil {
		t.Fatalf("query audit %q: %v", requestID, err)
	}
	if actorType != "agent" || action != wantAction || apiKeyID != organizationAPIKeyID {
		t.Fatalf("audit %q = actor=%q action=%q api_key=%q", requestID, actorType, action, apiKeyID)
	}
	if wantChangedCount == "" {
		if changedCount != nil {
			t.Fatalf("audit %q changed_count = %q, want null", requestID, *changedCount)
		}
	} else if changedCount == nil || *changedCount != wantChangedCount {
		t.Fatalf("audit %q changed_count = %v, want %q", requestID, changedCount, wantChangedCount)
	}
}

func seedOrganizationReadFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name)
		VALUES ($1, 'Organization primary'), ($2, 'Organization other');
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($3, 'organization-owner@example.com', 'encoded', 'active');
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`,
		organizationWorkspaceID, organizationOtherSpace, organizationUserID,
	); err != nil {
		t.Fatalf("seed organization accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO collections (id, workspace_id, name, description, created_by, created_at, updated_at)
		VALUES
			($1, $4, 'Current collection', 'Primary assets', $6, '2026-07-16T12:03:00Z', '2026-07-16T12:03:00Z'),
			($2, $4, 'Older collection', '', $6, '2026-07-16T12:02:00Z', '2026-07-16T12:02:00Z'),
			($3, $5, 'Other collection', '', $6, '2026-07-16T12:04:00Z', '2026-07-16T12:04:00Z');
		INSERT INTO tags (id, workspace_id, name, color, created_by, created_at)
		VALUES
			($7, $4, 'Important', '#FF8800', $6, '2026-07-16T12:03:00Z'),
			($8, $4, 'Follow up', NULL, $6, '2026-07-16T12:02:00Z'),
			($9, $5, 'Other tag', NULL, $6, '2026-07-16T12:04:00Z')`,
		organizationCollection, organizationOldCollect, "40000000-0000-4000-8000-000000000063",
		organizationWorkspaceID, organizationOtherSpace, organizationUserID,
		organizationTag, organizationOldTag, organizationOtherTag,
	); err != nil {
		t.Fatalf("seed organization containers: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets (
			id, workspace_id, collection_id, title, language, status, created_by,
			created_at, updated_at, deleted_at
		) VALUES
			($1, $5, $7, 'Processing asset', 'en', 'processing', $6, '2026-07-16T12:10:00Z', '2026-07-16T12:30:00Z', NULL),
			($2, $5, $8, 'Ready asset', 'en', 'ready', $6, '2026-07-16T12:09:00Z', '2026-07-16T12:20:00Z', NULL),
			($3, $5, $7, 'Deleted asset', 'en', 'trashed', $6, '2026-07-16T12:11:00Z', '2026-07-16T12:11:00Z', '2026-07-16T12:12:00Z'),
			($4, $9, $10, 'Other asset', 'en', 'ready', $6, '2026-07-16T12:12:00Z', '2026-07-16T12:12:00Z', NULL)`,
		organizationAssetID, organizationReadyAsset, organizationDeleted, organizationOtherAsset,
		organizationWorkspaceID, organizationUserID, organizationCollection, organizationOldCollect,
		organizationOtherSpace, "40000000-0000-4000-8000-000000000063",
	); err != nil {
		t.Fatalf("seed organization assets: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_tags (workspace_id, asset_id, tag_id, created_by)
		VALUES
			($1, $3, $6, $2), ($1, $4, $6, $2), ($1, $5, $7, $2),
			($8, $9, $10, $2);
		INSERT INTO annotations (
			id, workspace_id, asset_id, kind, start_ms, end_ms, body,
			created_by, created_at, updated_at, deleted_at
		) VALUES
			($11, $1, $3, 'bookmark', 1250, 2500, 'Key segment', $2, '2026-07-16T13:03:00Z', '2026-07-16T13:03:00Z', NULL),
			($12, $1, $3, 'note', 250, NULL, 'Earlier note', $2, '2026-07-16T13:02:00Z', '2026-07-16T13:02:00Z', NULL),
			($13, $1, $3, 'note', 0, NULL, 'Deleted note', $2, '2026-07-16T13:04:00Z', '2026-07-16T13:04:00Z', '2026-07-16T13:05:00Z'),
			($14, $8, $9, 'note', 0, NULL, 'Other note', $2, '2026-07-16T13:06:00Z', '2026-07-16T13:06:00Z', NULL)`,
		organizationWorkspaceID, organizationUserID, organizationAssetID, organizationDeleted,
		organizationReadyAsset, organizationTag, organizationOldTag,
		organizationOtherSpace, organizationOtherAsset, organizationOtherTag,
		organizationAnnotation, organizationOldNote,
		"60000000-0000-4000-8000-000000000063", "60000000-0000-4000-8000-000000000064",
	); err != nil {
		t.Fatalf("seed organization assignments: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO jobs (
			id, workspace_id, asset_id, kind, state, attempts, max_attempts,
			created_by, payload, created_at, updated_at
		)
		SELECT
			('70000000-0000-4000-8000-' || lpad(sequence::text, 12, '0'))::uuid,
			$1, $2, 'mock_transcribe', 'succeeded', 1, 3, $3, '{}'::jsonb,
			'2026-07-16T13:00:00Z'::timestamptz - sequence * interval '1 minute',
			'2026-07-16T13:00:30Z'::timestamptz - sequence * interval '1 minute'
		FROM generate_series(1, 20) AS sequence;
		INSERT INTO jobs (
			id, workspace_id, asset_id, kind, state, attempts, max_attempts,
			created_by, payload, created_at, updated_at
		) VALUES (
			'70000000-0000-4000-8000-000000000099', $1, $2, 'mock_transcribe',
			'queued', 0, 3, $3, '{}'::jsonb, '2026-07-16T13:01:00Z', '2026-07-16T13:02:00Z'
		)`, organizationWorkspaceID, organizationAssetID, organizationUserID,
	); err != nil {
		t.Fatalf("seed organization jobs: %v", err)
	}
}

func migratedOrganizationPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("organization_test_%d", time.Now().UnixNano())
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
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
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
