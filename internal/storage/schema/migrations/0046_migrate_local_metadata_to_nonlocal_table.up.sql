REPLACE INTO dolt_ignore VALUES ('__temp_local_metadata', true);
ALTER TABLE local_metadata RENAME TO __temp_local_metadata;
DELETE FROM dolt_ignore WHERE pattern = 'local_metadata';
CREATE TABLE local_metadata (
    `key` VARCHAR(255) PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('local_metadata', 'main', 'immediate');
REPLACE INTO dolt_ignore VALUES ('local_metadata', true);
CALL DOLT_COMMIT('-Am', 'create nonlocal table local_metadata');
INSERT INTO local_metadata SELECT * FROM __temp_local_metadata;
DROP TABLE __temp_local_metadata;
DELETE FROM dolt_ignore WHERE pattern = '__temp_local_metadata';
