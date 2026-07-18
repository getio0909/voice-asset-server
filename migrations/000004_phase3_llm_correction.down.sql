DROP TRIGGER IF EXISTS transcript_review_immutable_guard ON transcript_revision_reviews;
DROP FUNCTION IF EXISTS reject_transcript_review_mutation();
DROP TABLE IF EXISTS transcript_revision_reviews;

DROP INDEX IF EXISTS transcript_revisions_correction_job_unique;
ALTER TABLE transcript_revisions
    DROP COLUMN IF EXISTS review_status,
    DROP COLUMN IF EXISTS prompt_version,
    DROP COLUMN IF EXISTS model,
    DROP COLUMN IF EXISTS created_by_type;

DROP TABLE IF EXISTS glossary_set_versions;
DROP TABLE IF EXISTS glossary_sets;
DROP INDEX IF EXISTS provider_profiles_enabled_llm_route_idx;
