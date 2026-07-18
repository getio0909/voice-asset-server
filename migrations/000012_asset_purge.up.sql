ALTER TABLE assets DROP CONSTRAINT assets_status_check;
ALTER TABLE assets
    ADD CONSTRAINT assets_status_check
    CHECK (status IN (
        'draft', 'uploading', 'processing', 'ready', 'failed', 'trashed', 'purging'
    ));

-- Immutable records may be removed only after the owning asset has entered the
-- internal purging state. Ordinary updates and deletes remain rejected.
CREATE OR REPLACE FUNCTION reject_asset_object_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND EXISTS (
        SELECT 1 FROM assets WHERE id = OLD.asset_id AND status = 'purging'
    ) THEN
        RETURN OLD;
    END IF;
    IF OLD.kind IN ('original', 'provider_raw_response', 'waveform') THEN
        RAISE EXCEPTION 'immutable asset object % cannot be changed', OLD.id;
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE OR REPLACE FUNCTION reject_transcript_revision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND EXISTS (
        SELECT 1
        FROM transcripts transcript
        JOIN assets asset ON asset.id = transcript.asset_id
        WHERE transcript.id = OLD.transcript_id AND asset.status = 'purging'
    ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'immutable transcript revision % cannot be changed', OLD.id;
END;
$$;

CREATE OR REPLACE FUNCTION reject_transcript_segment_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND EXISTS (
        SELECT 1
        FROM transcript_revisions revision
        JOIN transcripts transcript ON transcript.id = revision.transcript_id
        JOIN assets asset ON asset.id = transcript.asset_id
        WHERE revision.id = OLD.revision_id AND asset.status = 'purging'
    ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'immutable transcript segment % cannot be changed', OLD.id;
END;
$$;

CREATE OR REPLACE FUNCTION reject_transcript_review_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND EXISTS (
        SELECT 1
        FROM transcript_revisions revision
        JOIN transcripts transcript ON transcript.id = revision.transcript_id
        JOIN assets asset ON asset.id = transcript.asset_id
        WHERE revision.id = OLD.revision_id AND asset.status = 'purging'
    ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'immutable transcript review % cannot be changed', OLD.id;
END;
$$;
