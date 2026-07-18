DROP TRIGGER IF EXISTS audit_log_immutable_guard ON audit_logs;
DROP FUNCTION IF EXISTS reject_audit_log_mutation();
DROP TRIGGER IF EXISTS transcript_segment_immutable_guard ON transcript_segments;
DROP FUNCTION IF EXISTS reject_transcript_segment_mutation();
DROP TABLE IF EXISTS transcript_segments;
DROP INDEX IF EXISTS transcript_revisions_raw_job_unique;
ALTER TABLE transcript_revisions
    DROP COLUMN IF EXISTS provider_raw_object_id,
    DROP COLUMN IF EXISTS source_job_id;
DROP TABLE IF EXISTS job_attempts;
DROP INDEX IF EXISTS jobs_expired_lease_idx;
DROP INDEX IF EXISTS jobs_asset_kind_active_unique;
DROP INDEX IF EXISTS jobs_workspace_idempotency_unique;
ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_asset_workspace_fk,
    DROP CONSTRAINT IF EXISTS jobs_last_error_code_check,
    DROP CONSTRAINT IF EXISTS jobs_attempt_limit_check,
    DROP CONSTRAINT IF EXISTS jobs_running_lease_check,
    DROP CONSTRAINT IF EXISTS jobs_lease_pair_check,
    DROP CONSTRAINT IF EXISTS jobs_idempotency_hash_check,
    DROP CONSTRAINT IF EXISTS jobs_idempotency_key_check,
    DROP CONSTRAINT IF EXISTS jobs_idempotency_pair_check,
    DROP CONSTRAINT IF EXISTS jobs_created_by_membership_fk,
    DROP COLUMN IF EXISTS result_revision_id,
    DROP COLUMN IF EXISTS last_error_code,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS lease_owner,
    DROP COLUMN IF EXISTS idempotency_request_hash,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS max_attempts,
    DROP COLUMN IF EXISTS payload,
    DROP COLUMN IF EXISTS created_by;
DROP TABLE IF EXISTS upload_parts;
DROP TABLE IF EXISTS upload_sessions;
DROP INDEX IF EXISTS asset_objects_one_original_per_asset;
DROP INDEX IF EXISTS assets_workspace_idempotency_unique;
ALTER TABLE assets
    DROP CONSTRAINT IF EXISTS assets_id_workspace_unique,
    DROP CONSTRAINT IF EXISTS assets_idempotency_pair_check,
    DROP COLUMN IF EXISTS idempotency_request_hash,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS language;
DROP TABLE IF EXISTS sessions;
