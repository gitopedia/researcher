# LLM Prompts

This directory contains the prompt templates used by the Researcher agent for LLM interactions.

## Files

- **`generate_article_system.txt`** - System message for article generation
- **`generate_article_user.txt`** - User prompt template for article generation (uses `{{.Topic}}` and `{{.ContextData}}`)
- **`extract_entities_system.txt`** - System message for entity extraction
- **`extract_entities_user.txt`** - User prompt template for entity extraction (uses `{{.Content}}`)
- **`suggest_topics_system.txt`** - System message for topic suggestion
- **`suggest_topics_user.txt`** - User prompt template for topic suggestion (uses `{{.Category}}` and `{{.ExistingTopics}}`)

## Template Variables

The user prompt templates use Go template syntax with the following variables:

### generate_article_user.txt
- `{{.Topic}}` - The article topic
- `{{.ContextData}}` - The research context/sources

### extract_entities_user.txt
- `{{.Content}}` - The article content to extract entities from

### suggest_topics_user.txt
- `{{.Category}}` - The category to suggest topics for
- `{{.ExistingTopics}}` - Comma-separated list of existing topics

## Editing Prompts

You can edit these files directly to improve the prompts. After making changes:

1. Rebuild the researcher container:
   ```bash
   cd researcher/infra
   docker compose build researcher
   docker compose up -d researcher
   ```

2. The changes will take effect on the next run.

## Best Practices

- Keep system messages concise and focused on the role
- Make user prompts explicit about requirements
- Test prompt changes with a few runs before committing
- Document any significant changes in commit messages

