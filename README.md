# Gitopedia – Researcher

This repository is part of the Gitopedia multi-repo project.

- Main Gitopedia README: [../gitopedia/README.md](../gitopedia/README.md)
- Researcher component docs: [../gitopedia/docs/build/researcher.md](../gitopedia/docs/build/researcher.md)
- Integration: [../gitopedia/docs/integration.md](../gitopedia/docs/integration.md)
- Roadmap: [../gitopedia/docs/roadmap.md](../gitopedia/docs/roadmap.md)

For full context and up-to-date guidance, always refer to the main Gitopedia README and docs.

## Prerequisites

### GitHub CLI

The Researcher uses `gh` CLI for GitHub operations and invoking the Encyclopaedist agent:

```bash
# Install gh CLI (see https://cli.github.com/)
# Then authenticate:
gh auth login

# Verify authentication:
gh auth status
```

For Docker deployments, either:
- Mount `~/.config/gh` from the host, or
- Set `GH_TOKEN` environment variable

## Running the Researcher

To run the Researcher with full "Deep Research" capabilities (web scraping via headless Chrome), it is recommended to use the Docker Compose setup.

See [infra/README.md](infra/README.md) for instructions on setting up the Docker environment with Ollama and the Researcher agent.

## Workflow

1. Researcher monitors for article requests (GitHub Issues)
2. Performs deep research using web scraping and LLM
3. Creates Draft PR with articles in `_incoming/`
4. Invokes **Encyclopaedist** agent to organize articles
5. Encyclopaedist moves files to `Compendium/`, validates, and marks PR ready
6. Automated CI merges and triggers downstream indexing

See [../gitopedia/docs/agents/](../gitopedia/docs/agents/) for agent documentation.


