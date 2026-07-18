CREATE UNIQUE INDEX asset_objects_one_waveform_per_asset
    ON asset_objects (asset_id)
    WHERE kind = 'waveform';

CREATE OR REPLACE FUNCTION reject_asset_object_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.kind IN ('original', 'provider_raw_response', 'waveform') THEN
        RAISE EXCEPTION 'immutable asset object % cannot be changed', OLD.id;
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

INSERT INTO jobs (
    id, workspace_id, asset_id, created_by, kind, state, payload, max_attempts
)
SELECT
    (
        substr(md5('waveform:' || original.id::text), 1, 8) || '-' ||
        substr(md5('waveform:' || original.id::text), 9, 4) || '-' ||
        substr(md5('waveform:' || original.id::text), 13, 4) || '-' ||
        substr(md5('waveform:' || original.id::text), 17, 4) || '-' ||
        substr(md5('waveform:' || original.id::text), 21, 12)
    )::uuid,
    asset.workspace_id,
    asset.id,
    owner.user_id,
    'generate_waveform',
    'queued',
    jsonb_build_object('asset_id', asset.id::text),
    3
FROM asset_objects original
JOIN assets asset ON asset.id = original.asset_id
JOIN LATERAL (
    SELECT membership.user_id
    FROM memberships membership
    WHERE membership.workspace_id = asset.workspace_id
    ORDER BY (membership.user_id = asset.created_by) DESC,
             membership.created_at,
             membership.user_id
    LIMIT 1
) owner ON true
WHERE original.kind = 'original'
  AND NOT EXISTS (
      SELECT 1
      FROM asset_objects waveform
      WHERE waveform.asset_id = asset.id AND waveform.kind = 'waveform'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM jobs existing
      WHERE existing.asset_id = asset.id
        AND existing.kind = 'generate_waveform'
  )
ON CONFLICT DO NOTHING;
