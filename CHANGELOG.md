# Changelog

All notable changes to the Gitopedia Researcher agent will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Two-step article generation: content first, then citations added separately to prevent hallucination
- New `AddReferences` LLM method for accurate citation placement
- Researcher version now displayed in website article list

### Changed
- Simplified version auto-increment: patch version bumps on each commit (0.3.1 → 0.3.2 → 0.3.3)
- Removed commit hash suffix in favor of clean semver numbers
- Thinking mode now also applies to entity extraction and source summarization (not just article generation)
- Article generation prompts now target 2500-4000 words with detailed section requirements
- Prompts explicitly instruct LLM not to include citations (handled in separate step)

## [0.3.0] - 2025-12-03

### Added
- Multi-model LLM configuration (`LLM_MODEL_FAST`, `LLM_MODEL_ENTITY`, `LLM_MODEL_ARTICLE`)
- LLM thinking mode support for Ollama models (qwen3, deepseek-r1, gpt-oss)
- Thinking traces saved to debug directory when enabled
- `--merge-only` CLI flag for running only PR merge logic
- `--once` CLI flag for single iteration mode
- CI failure log fetching and diagnostics
- Entity ID sanitization to prevent YAML parsing errors
- UTC datetime with time in article frontmatter
- Model name tracking in article frontmatter
- Version tracking in article frontmatter

### Changed
- Improved merge conflict resolution using Git Data API
- Better logging throughout merge process
- Article count now excludes `_incoming` and `_debug` directories

### Fixed
- YAML parsing errors from entity names with quotes/special characters
- Incorrect article count including source summaries and debug files
- Merge commits now properly have two parents

## [0.2.0] - 2025-12-01

### Added
- Two-phase source summarization (plain text → structured JSON)
- Source summary word count validation
- Debug output for source processing pipeline
- Pagination for GitHub issue fetching

### Changed
- Improved entity extraction prompts
- Better frontmatter generation with code fence stripping

### Fixed
- Issues being incorrectly closed before PR merge
- PRs stuck in conflict resolution loop

## [0.1.0] - 2025-11-28

### Added
- Initial researcher agent implementation
- Topic suggestion via LLM
- Web search and content fetching
- Article generation with frontmatter
- Entity extraction and authority management
- GitHub integration (issues, PRs, branches)
- Article organization into categories

[0.3.0]: https://github.com/gitopedia/researcher/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/gitopedia/researcher/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/gitopedia/researcher/releases/tag/v0.1.0

