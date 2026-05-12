REPLACE INTO dolt_ignore VALUES ('__temp_wisp_dependencies', true);
-- Rename only when the legacy wisp_dependencies table is present (see migration 0040).
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_dependencies') > 0,
    'ALTER TABLE wisp_dependencies RENAME TO __temp_wisp_dependencies',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
CREATE TABLE wisp_dependencies (
    issue_id VARCHAR(255) NOT NULL,
    depends_on_id VARCHAR(255) NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'blocks',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_by VARCHAR(255) DEFAULT '',
    metadata JSON DEFAULT (JSON_OBJECT()),
    thread_id VARCHAR(255) DEFAULT '',
    PRIMARY KEY (issue_id, depends_on_id),
    INDEX idx_wisp_dep_depends (depends_on_id),
    INDEX idx_wisp_dep_type (type),
    INDEX idx_wisp_dep_type_depends (type, depends_on_id)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_dependencies', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_dependencies');
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '__temp_wisp_dependencies') > 0,
    'INSERT INTO wisp_dependencies SELECT * FROM __temp_wisp_dependencies',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DROP TABLE IF EXISTS __temp_wisp_dependencies;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisp_dependencies';
