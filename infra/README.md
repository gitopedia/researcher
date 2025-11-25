# Self-Hosted Infrastructure for Researcher

This directory contains the Docker Compose setup to run the local "brain" of the Researcher agent and the agent itself.

## Services

1.  **Ollama**: Hosts the DeepSeek (or other) LLM locally. Exposes an OpenAI-compatible API at `http://localhost:11434/v1`.
2.  **Researcher**: Runs the researcher agent in a container with headless Chrome (Chromium) for "Deep Research" capabilities.

## Setup

1.  **Configure Environment**:
    Copy `env.example` to `.env` in the `researcher` root directory and fill in your GitHub credentials.
    ```bash
    cp ../env.example ../.env
    # Edit ../.env
    ```

2.  **Start the stack**:
    ```bash
    cd researcher/infra
    docker compose up -d
    ```
    This will build the researcher image and start both Ollama and the Researcher.

3.  **Pull the DeepSeek model** (if not already done):
    ```bash
    docker exec -it ollama ollama pull deepseek-llm:7b
    ```

4.  **Run the Researcher**:
    The researcher container will start and run the agent. You can view logs:
    ```bash
    docker compose logs -f researcher
    ```
    Note: The researcher is configured to run immediately on container start.

## GPU Support

To use your GPU for Ollama, ensure you have the **NVIDIA Container Toolkit** installed on your host. Then uncomment the `deploy` section in `docker-compose.yml`.
