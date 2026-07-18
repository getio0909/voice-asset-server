DROP TABLE IF EXISTS annotations;
DROP TABLE IF EXISTS asset_tags;
DROP TABLE IF EXISTS tags;

ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_collection_workspace_fk;
DROP INDEX IF EXISTS assets_workspace_collection_created_idx;
ALTER TABLE assets DROP COLUMN IF EXISTS collection_id;

DROP TABLE IF EXISTS collections;
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_workspace_id_unique;
