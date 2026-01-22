# Fresh Research Run

## Configuration (Edit These Before Running)

```
ITERATIONS = 10
IMPROVEMENTS_PER_ARTICLE = 5
ISSUE_NUMBER = 121
ARTICLE_COUNT = 20
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

### Step 4: Run the Researcher

Run the researcher with the configured parameters:

```powershell
cd C:\Solus\Gitopedia\researcher

$env:TOPIC_PROCESSING_ITERATIONS = "{ITERATIONS}"
$env:IMPROVEMENTS_PER_NEW_ARTICLE = "{IMPROVEMENTS_PER_ARTICLE}"
$env:GENERATE_IMAGES_AFTER_RUN = "true"
$env:GENERATE_SECTION_IMAGES = "false"

go run . --once --repo-path "../gitopedia" --no-commit
```

Run this command in the background so we can monitor progress.

### Step 5: Summary

After the run completes, show me:
1. List of articles created in `Compendium/_incoming/`
2. Any errors from the improvement logs in `Compendium/_debug/`
3. The iteration count from each article's frontmatter

---

## Notes

- The `--no-commit` flag means changes are only written locally, not pushed to GitHub
- Set `GENERATE_IMAGES_AFTER_RUN` to `false` to skip header image generation
- Section images are disabled by default (not working well currently)
- When adding new articles, choose topics that fit the issue's category/subcategory context
