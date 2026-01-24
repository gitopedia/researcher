# Fresh Research Run

## Configuration (Edit These Before Running)

```
ITERATIONS = 100
MIN_IMPROVEMENTS = 10
MAX_ATTEMPTS = 20
ISSUE_NUMBER = 121
ARTICLE_COUNT = 30
```

---

## Instructions for Cursor AI

When I reference this file, execute the following steps in order:

### Step 1: Clean the Gitopedia Repository

Run these commands in the gitopedia directory to reset it to a clean state:

```powershell
cd C:\Solus\Gitopedia\gitopedia
git checkout main
git fetch origin
git reset --hard origin/main
git clean -fd Compendium/_incoming/
git clean -fd Compendium/_debug/
```

Delete any local research branches:
```powershell
git branch | Select-String "research/" | ForEach-Object { git branch -D $_.ToString().Trim() }
```

### Step 2: Reset the GitHub Issue

Unassign the researcher bot and remove blocking labels from issue #{ISSUE_NUMBER}:

```powershell
gh issue edit {ISSUE_NUMBER} --repo gitopedia/gitopedia --remove-assignee gitopedia-researcher
gh issue edit {ISSUE_NUMBER} --repo gitopedia/gitopedia --remove-label "pending review"
```

### Step 3: Fetch and Update the Article List

Fetch the current issue body:
```powershell
gh issue view {ISSUE_NUMBER} --repo gitopedia/gitopedia --json body -q .body
```

Then update the issue body with these changes:
1. **Uncheck all articles** - Replace all `- [x]` with `- [ ]`
2. **Adjust article count to {ARTICLE_COUNT}**:
   - If there are MORE than {ARTICLE_COUNT} articles, remove extras from the end of the list
   - If there are FEWER than {ARTICLE_COUNT} articles, suggest relevant new articles to add based on the topic title
3. Keep the rest of the issue body (header, category link, etc.) unchanged

Use `gh issue edit {ISSUE_NUMBER} --repo gitopedia/gitopedia --body "..."` to update the issue with the modified body.

### Step 4: Build and Run the Researcher

First, ensure the app is built with the latest code changes:

```powershell
cd C:\Solus\Gitopedia\researcher
go build ./...
```

If the build fails, stop and report the error.

Then run the researcher with the configured parameters:

```powershell
$env:TOPIC_PROCESSING_ITERATIONS = "{ITERATIONS}"
$env:IMPROVEMENTS_PER_NEW_ARTICLE = "{MIN_IMPROVEMENTS}"
$env:MAX_IMPROVEMENT_ATTEMPTS = "{MAX_ATTEMPTS}"

go run . --once --repo-path "../gitopedia" --no-commit
```

Run this command in the background.

### Step 5: Verify the Run Started Successfully

Wait 2 minutes, then check the log file to verify the researcher started without errors:

```powershell
Start-Sleep -Seconds 120
Get-Content "researcher.log" -Tail 30
```

Check for:
- **Success indicators**: "Starting iterative processing", "Processing NEW article", "Creating branch"
- **Failure indicators**: "Failed to initialize", "API error", "exit status 1"

If the run failed to start, report the error from the log. Otherwise, confirm the run is in progress and end your response.

---

## Notes

- The `--no-commit` flag means changes are only written locally, not pushed to GitHub
- When adding new articles, choose topics that fit the issue's category/subcategory context
- `MIN_IMPROVEMENTS` is the minimum successful improvements per new article
- `MAX_ATTEMPTS` is the maximum attempts before giving up (should be >= MIN_IMPROVEMENTS)
