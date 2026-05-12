REPLACE INTO dolt_ignore VALUES ('__temp_repo_mtimes', true);
-- Rename only when the legacy repo_mtimes table is present. Legacy clones may
-- not have it in the working set since repo_mtimes used to be dolt-ignored and
-- didn't transfer via DOLT_CLONE.
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'repo_mtimes') > 0,
    'ALTER TABLE repo_mtimes RENAME TO __temp_repo_mtimes',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DELETE FROM dolt_ignore WHERE pattern = 'repo_mtimes';
CREATE TABLE repo_mtimes (
    repo_path VARCHAR(512) PRIMARY KEY,
    jsonl_path VARCHAR(512) NOT NULL,
    mtime_ns BIGINT NOT NULL,
    last_checked DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_repo_mtimes_checked (last_checked)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('repo_mtimes', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table repo_mtimes');
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '__temp_repo_mtimes') > 0,
    'INSERT INTO repo_mtimes SELECT * FROM __temp_repo_mtimes',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DROP TABLE IF EXISTS __temp_repo_mtimes;
DELETE FROM dolt_ignore WHERE pattern = '__temp_repo_mtimes';
