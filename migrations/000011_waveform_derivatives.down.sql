CREATE OR REPLACE FUNCTION reject_asset_object_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.kind IN ('original', 'provider_raw_response') THEN
        RAISE EXCEPTION 'immutable asset object % cannot be changed', OLD.id;
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

DELETE FROM asset_objects WHERE kind = 'waveform';
DROP INDEX asset_objects_one_waveform_per_asset;
DELETE FROM jobs WHERE kind = 'generate_waveform';
