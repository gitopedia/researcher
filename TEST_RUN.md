# Running Test Research

## Quick Test Run (Step-by-Step, No Push)

To run a test research step-by-step without pushing to GitHub:

```bash
cd C:\Solus\Gitopedia\researcher

# Run with local git mode (no pushing) and step-by-step
go run . --once --step --repo-path "C:\Solus\Gitopedia\gitopedia" --no-commit
```

## Step-by-Step Mode

The researcher supports step-by-step execution with these steps:
- `discovery` - Search for sources
- `summarization` - Summarize the first source
- `drafting` - Generate the article
- `finalize` - Create PR (skipped in local mode)

### Run a Specific Step

```bash
# Run only the discovery step
go run . --once --step --step-name discovery --repo-path "C:\Solus\Gitopedia\gitopedia" --no-commit

# Run only the summarization step
go run . --once --step --step-name summarization --repo-path "C:\Solus\Gitopedia\gitopedia" --no-commit

# Run only the drafting step
go run . --once --step --step-name drafting --repo-path "C:\Solus\Gitopedia\gitopedia" --no-commit
```

## Configuration

The `.env` file is set to:
- `RUN_PROFILE=test` - Fast iteration mode (1 topic, fewer sources)
- `MAX_TOPICS_PER_RUN=1` - Process only 1 topic per run

## Prerequisites

1. **GitHub Issue**: Create a GitHub issue with the `research-request` label
2. **Topic**: The issue title will be used as the research topic
3. **Local Git Repo**: The `--repo-path` flag points to your local gitopedia repository

## What Happens in Local Mode

- Files are written to the local repository
- No commits are made (with `--no-commit` flag)
- No PRs are created
- Debug files are saved to `Compendium/_debug/articles/{slug}/`

## Full Run (All Steps)

To run all steps automatically without pausing:

```bash
go run . --once --repo-path "C:\Solus\Gitopedia\gitopedia" --no-commit
```

