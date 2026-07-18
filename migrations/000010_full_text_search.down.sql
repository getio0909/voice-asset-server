DROP INDEX IF EXISTS transcript_revisions_asr_provider_idx;
DROP INDEX IF EXISTS transcripts_asset_id_idx;
DROP INDEX IF EXISTS transcript_segments_speaker_revision_idx;
DROP INDEX IF EXISTS transcript_segments_search_vector_idx;
ALTER TABLE transcript_segments DROP COLUMN IF EXISTS search_vector;
DROP INDEX IF EXISTS assets_search_vector_idx;
ALTER TABLE assets DROP COLUMN IF EXISTS search_vector;
