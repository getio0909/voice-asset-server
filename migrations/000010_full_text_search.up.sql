ALTER TABLE assets
    ADD COLUMN search_vector tsvector
        GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, title)) STORED;

CREATE INDEX assets_search_vector_idx
    ON assets USING GIN (search_vector);

ALTER TABLE transcript_segments
    ADD COLUMN search_vector tsvector
        GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, text_content)) STORED;

CREATE INDEX transcript_segments_search_vector_idx
    ON transcript_segments USING GIN (search_vector);

CREATE INDEX transcript_segments_speaker_revision_idx
    ON transcript_segments (lower(speaker), revision_id)
    WHERE speaker IS NOT NULL;

CREATE INDEX transcripts_asset_id_idx
    ON transcripts (asset_id);

CREATE INDEX transcript_revisions_asr_provider_idx
    ON transcript_revisions (
        (provider_snapshot ->> 'provider_id'), transcript_id, created_at DESC, id DESC
    )
    WHERE kind = 'raw_asr';
