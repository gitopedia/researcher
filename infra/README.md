# Self-Hosted Infrastructure for Researcher

This directory contains the Docker Compose setup to run the local "brain" of the Researcher agent.

## Services

1.  **Ollama**: Hosts the DeepSeek (or other) LLM locally. Exposes an OpenAI-compatible API at `http://localhost:11434/v1`.

## Setup

1.  **Start the stack**:
    ```bash
    cd researcher/infra
    docker compose up -d
    ```

2.  **Pull the DeepSeek model** (once Ollama is running):
    ```bash
    docker exec -it ollama ollama pull deepseek-coder:6.7b
    # Or deepseek-llm:67b if you have the VRAM!
    ```

3.  **Configure the Researcher**:
    Set these environment variables when running the Researcher script:
    ```bash
    export OPENAI_BASE_URL="http://localhost:11434/v1"
    export OPENAI_API_KEY="ollama"  # Value doesn't matter for Ollama
    export OPENAI_MODEL="deepseek-coder:6.7b"
    ```

## GPU Support

To use your GPU for Ollama, ensure you have the **NVIDIA Container Toolkit** installed on your host. Then uncomment the `deploy` section in `docker-compose.yml`.
