# Skill Registry — MCM

Generated: 2026-05-24

## Project Context

- **Stack**: Go 1.24 (backend) + React 19 / Vite 8 / TypeScript 6 / Tailwind CSS 4 (frontend)
- **Test runner**: `go test ./... -race` (34 test files, integration via httptest + mochi-mqtt)
- **Frontend tests**: None (no vitest/jest/playwright configured)
- **Linter**: `go vet`, ESLint 10 (frontend)
- **Type checker**: TypeScript 6 (frontend)
- **Formatter**: None configured explicitly
- **CI**: GitHub Actions (go test -race, cross-OS builds, frontend lint+build, OpenAPI lint)

## User Skills

| Skill | Trigger | Path |
|-------|---------|------|
| go-testing | Go tests, coverage, Bubbletea teatest, golden files | `~/.claude/skills/go-testing/SKILL.md` |
| branch-pr | Creating/opening PRs | `~/.claude/skills/branch-pr/SKILL.md` |
| chained-pr | PRs >400 lines, stacked PRs, review slices | `~/.claude/skills/chained-pr/SKILL.md` |
| issue-creation | GitHub issues, bug reports, feature requests | `~/.claude/skills/issue-creation/SKILL.md` |
| work-unit-commits | Commit splitting, chained PRs, implementation | `~/.claude/skills/work-unit-commits/SKILL.md` |
| comment-writer | PR feedback, issue replies, reviews, messages | `~/.claude/skills/comment-writer/SKILL.md` |

## Compact Rules

### go-testing
- Table-driven tests with `t.Run(tt.name, ...)`
- Test behavior/state transitions, not implementation trivia
- Use `t.TempDir()` for filesystem tests
- Integration tests skippable with `testing.Short()`
- For TUI: test `Model.Update()` directly for state; `teatest` for interactive flows

### branch-pr
- Verify issue exists before creating PR
- Link PR to issue in description
- Follow project PR template conventions

### chained-pr
- Split PRs over 400 changed lines unless maintainer accepts `size:exception`
- Each PR reviewable in ~60 minutes
- One deliverable work unit per PR; keep tests/docs with the unit
- Include dependency diagram in chained PRs

### work-unit-commits
- Each commit = one reviewable work unit
- Keep tests and docs with the code they verify
- Plan commits to avoid PRs above 400 changed lines

### issue-creation
- Check for duplicate issues before creating
- Include context, scope, and acceptance criteria
- Follow project issue templates

### comment-writer
- Warm, direct tone
- Lead with the actionable point
- Keep comments concise and constructive
