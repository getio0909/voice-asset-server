DROP TABLE IF EXISTS provider_profile_health_checks;
DROP TABLE IF EXISTS hotword_set_versions;
DROP TABLE IF EXISTS hotword_sets;

DROP INDEX IF EXISTS provider_profiles_enabled_asr_route_idx;
DROP INDEX IF EXISTS provider_profiles_workspace_name_unique;
ALTER TABLE provider_profiles
    DROP CONSTRAINT IF EXISTS provider_profiles_secret_size_check,
    DROP CONSTRAINT IF EXISTS provider_profiles_config_object_check,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS priority;
