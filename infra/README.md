# Self-Hosted Infrastructure for Researcher

This directory contains the Docker Compose setup to run the local "brain" of the Researcher agent and the agent itself.

## Services

1.  **Ollama**: Hosts the LLMs locally (e.g. Gemma, Qwen). Exposes an OpenAI-compatible API at `http://localhost:11434/v1` (configured via `LLM_BASE_URL`).
2.  **Researcher**: Runs the researcher agent in a container with headless Chrome (Chromium) for "Deep Research" capabilities.

## Setup

1.  **Configure Environment**:
    The system loads settings from `config/base.env` first (contains all defaults), then `.env` (your overrides).
    Create a `.env` file in the `researcher` root directory with only the values you want to override:
    ```bash
    # Create .env with your overrides (e.g., GitHub credentials)
    # Only include the variables you want to change from config/base.env
    cat > ../.env <<EOF
    GITHUB_TOKEN=your_token_here
    # Add any other overrides
    EOF
    ```

2.  **Start the stack**:
    ```bash
    cd researcher/infra
    docker compose up -d
    ```
    This will build the researcher image and start both Ollama and the Researcher.

3.  **Pull one or more models** (if not already done):
    ```bash
    # Example "fast" model
    docker exec -it ollama ollama pull gemma3:12b

    # Example "detailed" model
    docker exec -it ollama ollama pull qwen3:14b
    ```

4.  **Run the Researcher**:
    The researcher container will start and run the agent. You can view logs:
    ```bash
    docker compose logs -f researcher
    ```
    Note: The researcher is configured to run immediately on container start.

## Context Size Configuration

Ollama's default context size is 4096 tokens, which may cause truncation errors when processing long research content. The docker-compose.yml sets this to 40960 tokens by default to match `qwen3:14b`'s maximum.

**Model context size limits:**
- **gemma3:12b**: Up to 131,072 tokens
- **qwen3:14b**: Up to 40,960 tokens

### Configure Context Size

The context size is configured via the `OLLAMA_NUM_CTX` environment variable in `docker-compose.yml`. The default is set to 40960 tokens (qwen3:14b's maximum).

**To change the context size:**

1. **Option 1: Set in docker-compose.yml** (recommended for Docker setup):
   Edit `docker-compose.yml` and modify the `OLLAMA_NUM_CTX` value:
   ```yaml
   environment:
     - OLLAMA_NUM_CTX=${OLLAMA_NUM_CTX:-40960}  # Change to your desired size
   ```

2. **Option 2: Set via environment variable**:
   ```bash
   export OLLAMA_NUM_CTX=40960
   cd researcher/infra
   docker compose up -d
   ```

## Model Keep-Alive Configuration

By default, Ollama keeps models loaded in memory for 5 minutes after the last request. This can cause GPU/CPU usage to continue even after your application stops. The docker-compose.yml sets `OLLAMA_KEEP_ALIVE=30s` to unload models after 30 seconds of inactivity.

**To change the keep-alive duration:**

1. **Option 1: Set in docker-compose.yml** (recommended):
   Edit `docker-compose.yml` and modify the `OLLAMA_KEEP_ALIVE` value:
   ```yaml
   environment:
     - OLLAMA_KEEP_ALIVE=${OLLAMA_KEEP_ALIVE:-30s}  # Options: "0" (immediate), "30s", "1m", "5m", etc.
   ```

2. **Option 2: Set via environment variable**:
   ```bash
   export OLLAMA_KEEP_ALIVE=30s
   cd researcher/infra
   docker compose up -d ollama
   ```

**Keep-alive options:**
- `"0"` - Unload models immediately after each request (frees resources fastest)
- `"30s"` - Unload after 30 seconds of inactivity (default in this setup)
- `"1m"` - Unload after 1 minute
- `"5m"` - Unload after 5 minutes (Ollama's default)
- `"-1"` - Keep models loaded indefinitely (uses most resources)

**Recommended values:**
- **40960**: Matches qwen3:14b's maximum (default)
- **65536**: Good middle ground for both models
- **131072**: Matches gemma3:12b's maximum (requires more GPU/CPU memory)

**After changing context size, restart Ollama:**
```bash
cd researcher/infra
docker compose down
docker compose up -d
```

**Note:** Larger context sizes require more GPU/CPU memory. If you encounter out-of-memory errors, reduce the context size.

## GPU Support

GPU support is enabled in `docker-compose.yml` by default. To use your GPU for Ollama, you need:

### 1. Install NVIDIA Drivers

Install NVIDIA drivers for your Linux distribution:

**Ubuntu/Debian:**
```bash
sudo apt update
sudo apt install nvidia-driver nvidia-utils
sudo reboot  # Reboot to load the drivers
```

**Fedora/RHEL:**
```bash
sudo dnf install akmod-nvidia xorg-x11-drv-nvidia
sudo reboot  # Reboot to load the drivers
```

**Arch Linux:**
```bash
sudo pacman -S nvidia nvidia-utils
sudo reboot  # Reboot to load the drivers
```

**Other distributions:**
Refer to your distribution's documentation for NVIDIA driver installation.

After reboot, verify drivers are working:
```bash
nvidia-smi  # Should show your GPU
```

### 2. Install NVIDIA Container Toolkit

Install the NVIDIA Container Toolkit for your distribution:

**Ubuntu/Debian:**
```bash
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -s -L https://nvidia.github.io/nvidia-docker/gpgkey | sudo apt-key add -
curl -s -L https://nvidia.github.io/nvidia-docker/$distribution/nvidia-docker.list | sudo tee /etc/apt/sources.list.d/nvidia-docker.list
sudo apt update
sudo apt install -y nvidia-container-toolkit
sudo systemctl restart docker
```

**Fedora/RHEL:**
```bash
distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
curl -s -L https://nvidia.github.io/nvidia-docker/$distribution/nvidia-docker.repo | sudo tee /etc/yum.repos.d/nvidia-docker.repo
sudo dnf install -y nvidia-container-toolkit
sudo systemctl restart docker
```

**Arch Linux:**
```bash
# Install from AUR
yay -S nvidia-container-toolkit
# Or use paru
paru -S nvidia-container-toolkit
sudo systemctl restart docker
```

**Other distributions:**
See the [NVIDIA Container Toolkit documentation](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html) for installation instructions.

Configure Docker to use the NVIDIA runtime:
```bash
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

### 3. Verify GPU Access in Docker

```bash
docker run --rm --gpus all nvidia/cuda:12.0.0-base-ubuntu22.04 nvidia-smi
```

If this works, your GPU is accessible to Docker containers.

### 4. Restart Ollama with GPU

```bash
cd researcher/infra
docker compose down
docker compose up -d
```

Check that Ollama is using GPU:
```bash
docker exec ollama ollama ps
# Should show GPU usage instead of "100% CPU"
```

**Note:** If you don't have a GPU or want to disable GPU support, comment out the `deploy` section in `docker-compose.yml`.
