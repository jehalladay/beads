REPLACE INTO dolt_ignore VALUES ('__temp_wisps', true);
-- Rename only when the legacy wisps table is present. Legacy clones may not
-- have it in the working set since wisps used to be dolt-ignored and didn't
-- transfer via DOLT_CLONE.
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisps') > 0,
    'ALTER TABLE wisps RENAME TO __temp_wisps',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DELETE FROM dolt_ignore WHERE pattern = 'wisps';
CREATE TABLE wisps (
    id VARCHAR(255) PRIMARY KEY,
    content_hash VARCHAR(64),
    title VARCHAR(500) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    design TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'open',
    priority INT NOT NULL DEFAULT 2,
    issue_type VARCHAR(32) NOT NULL DEFAULT 'task',
    assignee VARCHAR(255),
    estimated_minutes INT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by VARCHAR(255) DEFAULT '',
    owner VARCHAR(255) DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    closed_at DATETIME,
    closed_by_session VARCHAR(255) DEFAULT '',
    external_ref VARCHAR(255),
    spec_id VARCHAR(1024),
    compaction_level INT DEFAULT 0,
    compacted_at DATETIME,
    compacted_at_commit VARCHAR(64),
    original_size INT,
    sender VARCHAR(255) DEFAULT '',
    ephemeral TINYINT(1) DEFAULT 0,
    wisp_type VARCHAR(32) DEFAULT '',
    pinned TINYINT(1) DEFAULT 0,
    is_template TINYINT(1) DEFAULT 0,
    mol_type VARCHAR(32) DEFAULT '',
    work_type VARCHAR(32) DEFAULT 'mutex',
    source_system VARCHAR(255) DEFAULT '',
    metadata JSON DEFAULT (JSON_OBJECT()),
    source_repo VARCHAR(512) DEFAULT '',
    close_reason TEXT DEFAULT '',
    event_kind VARCHAR(32) DEFAULT '',
    actor VARCHAR(255) DEFAULT '',
    target VARCHAR(255) DEFAULT '',
    payload TEXT DEFAULT '',
    await_type VARCHAR(32) DEFAULT '',
    await_id VARCHAR(255) DEFAULT '',
    timeout_ns BIGINT DEFAULT 0,
    waiters TEXT DEFAULT '',
    hook_bead VARCHAR(255) DEFAULT '',
    role_bead VARCHAR(255) DEFAULT '',
    agent_state VARCHAR(32) DEFAULT '',
    last_activity DATETIME,
    role_type VARCHAR(32) DEFAULT '',
    rig VARCHAR(255) DEFAULT '',
    due_at DATETIME,
    defer_until DATETIME,
    no_history TINYINT(1) DEFAULT 0,
    started_at DATETIME,
    INDEX idx_wisps_status (status),
    INDEX idx_wisps_priority (priority),
    INDEX idx_wisps_issue_type (issue_type),
    INDEX idx_wisps_assignee (assignee),
    INDEX idx_wisps_created_at (created_at),
    INDEX idx_wisps_spec_id (spec_id),
    INDEX idx_wisps_external_ref (external_ref)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisps', 'main', 'immediate');
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisps_*', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisps');
-- Copy data only if the temp table was actually created above.
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '__temp_wisps') > 0,
    'INSERT INTO wisps SELECT * FROM __temp_wisps',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DROP TABLE IF EXISTS __temp_wisps;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisps';

