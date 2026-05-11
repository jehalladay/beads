REPLACE INTO dolt_ignore VALUES ('__temp_wisp_dependencies', true);
ALTER TABLE wisp_dependencies RENAME TO __temp_wisp_dependencies;
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
INSERT INTO wisp_dependencies SELECT * FROM __temp_wisp_dependencies;
DROP TABLE __temp_wisp_dependencies;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisp_dependencies';
