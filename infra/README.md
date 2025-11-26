# Self-Hosted Infrastructure for Researcher

This directory contains the Docker Compose setup to run the local "brain" of the Researcher agent and the agent itself.

## Services

1.  **Ollama**: Hosts the LLMs locally (e.g. Gemma, Qwen). Exposes an OpenAI-compatible API at `http://localhost:11434/v1` (configured via `LLM_BASE_URL`).
2.  **Researcher**: Runs the researcher agent in a container with headless Chrome (Chromium) for "Deep Research" capabilities.

## Setup

1.  **Configure Environment**:
    Copy `env.example` to `.env` in the `researcher` root directory and fill in your GitHub credentials and LLM configuration.
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

Ollama's default context size is 4096 tokens, which may cause truncation errors when processing long research content. You can increase the context size to handle more content.

### Configure Context Size

The context size is configured via the `OLLAMA_NUM_CTX` environment variable in `docker-compose.yml`. The default is set to 8192 tokens.

**To change the context size:**

1. **Option 1: Set in docker-compose.yml** (recommended for Docker setup):
   Edit `docker-compose.yml` and modify the `OLLAMA_NUM_CTX` value:
   ```yaml
   environment:
     - OLLAMA_NUM_CTX=${OLLAMA_NUM_CTX:-16384}  # Change 16384 to your desired size
   ```

2. **Option 2: Set via environment variable**:
   ```bash
   export OLLAMA_NUM_CTX=16384
   cd researcher/infra
   docker compose up -d
   ```

**Recommended values:**
- **8192**: Good balance for most research tasks (default)
- **16384**: Better for longer articles with multiple sources
- **32768**: Maximum for DeepSeek models (requires more GPU/CPU memory)

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
