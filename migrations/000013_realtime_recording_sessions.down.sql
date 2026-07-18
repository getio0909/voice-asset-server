DROP TABLE IF EXISTS recording_sessions;

ALTER TABLE provider_profiles
    DROP CONSTRAINT IF EXISTS provider_profiles_id_workspace_unique;
