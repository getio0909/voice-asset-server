DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM assets WHERE status = 'purging') THEN
        RAISE EXCEPTION 'cannot downgrade while an asset purge is incomplete';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION reject_asset_object_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.kind IN ('original', 'provider_raw_response', 'waveform') THEN
        RAISE EXCEPTION 'immutable asset object % cannot be changed', OLD.id;
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE OR REPLACE FUNCTION reject_transcript_revision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable transcript revision % cannot be changed', OLD.id;
END;
$$;

CREATE OR REPLACE FUNCTION reject_transcript_segment_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable transcript segment % cannot be changed', OLD.id;
END;
$$;

CREATE OR REPLACE FUNCTION reject_transcript_review_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable transcript review % cannot be changed', OLD.id;
END;
$$;

ALTER TABLE assets DROP CONSTRAINT assets_status_check;
ALTER TABLE assets
    ADD CONSTRAINT assets_status_check
    CHECK (status IN ('draft', 'uploading', 'processing', 'ready', 'failed', 'trashed'));
