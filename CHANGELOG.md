# Changelog

All notable changes to the beads project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Fixed unchecked error in import.go when unmarshaling JSON
- Fixed unchecked error returns in test cleanup code
- Removed duplicate test code in dependencies_test.go
- Fixed Go version in go.mod (was incorrectly set to 1.25.2)

### Added
- Added `bd version` command to display version information
- Added CHANGELOG.md to track project changes

## [0.9.0] - 2025-10-12

### Added
- **Collision Resolution System**: Automatic ID remapping for import collisions
  - Reference scoring algorithm to minimize updates during remapping
  - Word-boundary regex matching to prevent false replacements
  - Automatic updating of text references and dependencies
  - `--resolve-collisions` flag for safe branch merging
  - `--dry-run` flag to preview collision detection
- **Export/Import with JSONL**: Git-friendly text format
  - Dependencies embedded in JSONL for complete portability
  - Idempotent import (exact matches detected)
  - Collision detection (same ID, different content)
- **Ready Work Algorithm**: Find issues with no open blockers
  - `bd ready` command shows unblocked work
  - `bd blocked` command shows what's waiting
- **Dependency Management**: Four dependency types
  - `blocks`: Hard blocker (affects ready work)
  - `related`: Soft relationship
  - `parent-child`: Epic/subtask hierarchy
  - `discovered-from`: Track issues discovered during work
- **Database Discovery**: Auto-find database in project hierarchy
  - Walks up directory tree like git
  - Supports `$BEADS_DB` environment variable
  - Falls back to `~/.beads/default.db`
- **Comprehensive Documentation**:
  - README.md with 900+ lines of examples and FAQs
  - CLAUDE.md for AI agent integration patterns
  - SECURITY.md with security policy and best practices
  - TEXT_FORMATS.md analyzing JSONL approach
  - EXTENDING.md for database extension patterns
  - GIT_WORKFLOW.md for git integration
- **Examples**: Real-world integration patterns
  - Python agent implementation
  - Bash agent script
  - Git hooks for automatic export/import
  - Branch merge workflow with collision resolution
  - Claude Desktop MCP integration (coming soon)

### Changed
- Switched to JSONL as source of truth (from binary SQLite)
- SQLite database now acts as ephemeral cache
- Issue IDs generated with numerical max (not alphabetical)
- Export sorts issues by ID for consistent git diffs

### Security
- SQL injection protection via allowlisted field names
- Input validation for all issue fields
- File path validation for database operations
- Warnings about not storing secrets in issues

## [0.1.0] - Initial Development

### Added
- Core issue tracking (create, update, list, show, close)
- SQLite storage backend
- Dependency tracking with cycle detection
- Label support
- Event audit trail
- Full-text search
- Statistics and reporting
- `bd init` for project initialization
- `bd quickstart` interactive tutorial

---

## Version History

- **0.9.0** (2025-10-12): Pre-release polish and collision resolution
- **0.1.0**: Initial development version

## Upgrade Guide

### Upgrading to 0.9.0

No breaking changes. The JSONL export format is backward compatible.

If you have issues in your database:
1. Run `bd export -o .beads/issues.jsonl` to create the text file
2. Commit `.beads/issues.jsonl` to git
3. Add `.beads/*.db` to `.gitignore`

New collaborators can clone the repo and run:
```bash
bd import -i .beads/issues.jsonl
```

The SQLite database will be automatically populated from the JSONL file.

## Future Releases

See open issues tagged with milestone markers for planned features in upcoming releases.

For version 1.0, see: `bd dep tree bd-8` (the 1.0 milestone epic)
