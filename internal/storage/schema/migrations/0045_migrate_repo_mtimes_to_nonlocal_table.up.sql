REPLACE INTO dolt_ignore VALUES ('__temp_repo_mtimes', true);
ALTER TABLE repo_mtimes RENAME TO __temp_repo_mtimes;
DELETE FROM dolt_ignore WHERE pattern = 'repo_mtimes';
CREATE TABLE repo_mtimes (
    repo_path VARCHAR(512) PRIMARY KEY,
    jsonl_path VARCHAR(512) NOT NULL,
    mtime_ns BIGINT NOT NULL,
    last_checked DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_repo_mtimes_checked (last_checked)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('repo_mtimes', 'main', 'immediate');
REPLACE INTO dolt_ignore VALUES ('temp_repo_mtimes', true);
CALL DOLT_COMMIT('-Am', 'create nonlocal table repo_mtimes');
INSERT INTO repo_mtimes SELECT * FROM __temp_repo_mtimes;
DROP TABLE __temp_repo_mtimes;
DELETE FROM dolt_ignore WHERE pattern = '__temp_repo_mtimes';
