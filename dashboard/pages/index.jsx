import { useStatus } from '../hooks/useStatus';
import StatusCard from '../components/StatusCard';
import ServiceToggle from '../components/ServiceToggle';
import ProgressBar from '../components/ProgressBar';
import * as api from '../lib/api';

function formatBytes(bytes) {
  if (!bytes) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

export default function Dashboard() {
  const { status, researcherStatus, connected, error, refresh } = useStatus();

  const docker = status?.docker || {};
  const ollama = status?.ollama || {};
  const comfyui = status?.comfyui || {};
  const hardware = status?.hardware || {};
  const gpu = hardware.gpu;
  const ram = hardware.ram || {};

  return (
    <div>
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        marginBottom: 24,
      }}>
        <div>
          <h1 style={{
            fontSize: '1.75rem',
            fontWeight: 700,
            letterSpacing: '-0.02em',
          }}>
            Dashboard
          </h1>
          <p style={{
            color: 'var(--text-secondary)',
            fontSize: '0.9rem',
            marginTop: 4,
          }}>
            System status and service control
          </p>
        </div>
        
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            fontSize: '0.8rem',
            color: connected ? 'var(--accent-green)' : 'var(--accent-red)',
          }}>
            <span style={{
              width: 8,
              height: 8,
              borderRadius: '50%',
              backgroundColor: 'currentColor',
            }} />
            {connected ? 'Connected' : 'Disconnected'}
          </span>
          <button
            onClick={refresh}
            style={{
              padding: '8px 16px',
              fontSize: '0.85rem',
              backgroundColor: 'var(--bg-card)',
              border: '1px solid var(--border-color)',
              borderRadius: 8,
              color: 'var(--text-primary)',
            }}
          >
            Refresh
          </button>
        </div>
      </div>

      {error && (
        <div style={{
          padding: '12px 16px',
          backgroundColor: 'rgba(239, 68, 68, 0.1)',
          border: '1px solid var(--accent-red)',
          borderRadius: 8,
          marginBottom: 20,
          color: 'var(--accent-red)',
          fontSize: '0.9rem',
        }}>
          {error}
        </div>
      )}

      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))',
        gap: 20,
      }}>
        {/* Researcher Status */}
        <StatusCard 
          title="Researcher" 
          status={researcherStatus?.state === 'running' ? 'running' : 
                  researcherStatus?.state === 'paused' ? 'warning' : 'idle'}
        >
          <div style={{ fontSize: '0.85rem' }}>
            <div style={{
              display: 'flex',
              justifyContent: 'space-between',
              marginBottom: 8,
            }}>
              <span style={{ color: 'var(--text-secondary)' }}>State</span>
              <span style={{ 
                fontWeight: 500,
                textTransform: 'capitalize',
                color: researcherStatus?.state === 'running' ? 'var(--accent-green)' :
                       researcherStatus?.state === 'paused' ? 'var(--accent-yellow)' : 
                       'var(--text-primary)',
              }}>
                {researcherStatus?.state || 'Unknown'}
              </span>
            </div>
            
            {researcherStatus?.mode && (
              <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                marginBottom: 8,
              }}>
                <span style={{ color: 'var(--text-secondary)' }}>Mode</span>
                <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                  {researcherStatus.mode}
                </span>
              </div>
            )}

            {researcherStatus?.currentStep && (
              <div style={{ marginTop: 12 }}>
                <div style={{ 
                  color: 'var(--text-secondary)', 
                  marginBottom: 6,
                  fontSize: '0.8rem',
                }}>
                  Current Step
                </div>
                <div style={{
                  padding: '8px 12px',
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  fontFamily: 'JetBrains Mono, monospace',
                  fontSize: '0.8rem',
                  color: 'var(--text-primary)',
                }}>
                  {researcherStatus.currentStep}
                </div>
              </div>
            )}

            {researcherStatus?.duration && (
              <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                marginTop: 12,
              }}>
                <span style={{ color: 'var(--text-secondary)' }}>Duration</span>
                <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                  {researcherStatus.duration}
                </span>
              </div>
            )}

            {researcherStatus?.state === 'running' && (
              <div style={{ marginTop: 16, display: 'flex', gap: 8 }}>
                <button
                  onClick={() => api.pauseResearcher()}
                  style={{
                    flex: 1,
                    padding: '8px 16px',
                    fontSize: '0.85rem',
                    backgroundColor: 'var(--accent-yellow)',
                    border: 'none',
                    borderRadius: 6,
                    color: '#000',
                    fontWeight: 500,
                  }}
                >
                  Pause
                </button>
                <button
                  onClick={() => api.stopResearcher()}
                  style={{
                    flex: 1,
                    padding: '8px 16px',
                    fontSize: '0.85rem',
                    backgroundColor: 'var(--accent-red)',
                    border: 'none',
                    borderRadius: 6,
                    color: '#fff',
                    fontWeight: 500,
                  }}
                >
                  Stop
                </button>
              </div>
            )}

            {researcherStatus?.state === 'paused' && (
              <div style={{ marginTop: 16, display: 'flex', gap: 8 }}>
                <button
                  onClick={() => api.resumeResearcher()}
                  style={{
                    flex: 1,
                    padding: '8px 16px',
                    fontSize: '0.85rem',
                    backgroundColor: 'var(--accent-green)',
                    border: 'none',
                    borderRadius: 6,
                    color: '#fff',
                    fontWeight: 500,
                  }}
                >
                  Resume
                </button>
                <button
                  onClick={() => api.stopResearcher()}
                  style={{
                    flex: 1,
                    padding: '8px 16px',
                    fontSize: '0.85rem',
                    backgroundColor: 'var(--accent-red)',
                    border: 'none',
                    borderRadius: 6,
                    color: '#fff',
                    fontWeight: 500,
                  }}
                >
                  Stop
                </button>
              </div>
            )}
          </div>
        </StatusCard>

        {/* Services */}
        <StatusCard title="Services">
          <ServiceToggle
            name="Docker Desktop"
            running={docker.running}
            version={docker.version}
            onStart={api.startDocker}
            onStop={api.stopDocker}
          />
          <ServiceToggle
            name="Ollama"
            running={ollama.running}
            extra={ollama.loadedModel ? `Loaded: ${ollama.loadedModel}` : null}
            onStart={api.startOllama}
            onStop={api.stopOllama}
            disabled={!docker.running}
          />
          <ServiceToggle
            name="ComfyUI"
            running={comfyui.running}
            version={comfyui.version}
            onStart={api.startComfyUI}
            onStop={api.stopComfyUI}
            disabled={!docker.running}
          />
        </StatusCard>

        {/* Hardware */}
        <StatusCard title="Hardware">
          {gpu ? (
            <div style={{ marginBottom: 16 }}>
              <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                marginBottom: 8,
              }}>
                <span style={{ fontSize: '0.85rem', color: 'var(--text-secondary)' }}>
                  GPU
                </span>
                <span style={{ 
                  fontSize: '0.8rem', 
                  color: 'var(--text-muted)',
                  fontFamily: 'JetBrains Mono, monospace',
                }}>
                  {gpu.name}
                </span>
              </div>
              
              <div style={{ marginBottom: 12 }}>
                <div style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  marginBottom: 4,
                  fontSize: '0.8rem',
                }}>
                  <span style={{ color: 'var(--text-secondary)' }}>VRAM</span>
                  <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                    {formatBytes(gpu.vramUsed)} / {formatBytes(gpu.vramTotal)}
                  </span>
                </div>
                <ProgressBar 
                  value={gpu.vramUsed} 
                  max={gpu.vramTotal}
                  color={gpu.vramUsed / gpu.vramTotal > 0.9 ? 'var(--accent-red)' : 'var(--accent-purple)'}
                />
              </div>

              <div style={{
                display: 'flex',
                gap: 16,
                fontSize: '0.8rem',
              }}>
                <div>
                  <span style={{ color: 'var(--text-secondary)' }}>Utilization: </span>
                  <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                    {gpu.utilization}%
                  </span>
                </div>
                <div>
                  <span style={{ color: 'var(--text-secondary)' }}>Temp: </span>
                  <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                    {gpu.temperature}°C
                  </span>
                </div>
              </div>
            </div>
          ) : (
            <div style={{
              padding: '12px 16px',
              backgroundColor: 'var(--bg-secondary)',
              borderRadius: 8,
              marginBottom: 16,
              fontSize: '0.85rem',
              color: 'var(--text-muted)',
            }}>
              GPU info unavailable (nvidia-smi not found)
            </div>
          )}

          <div>
            <div style={{
              display: 'flex',
              justifyContent: 'space-between',
              marginBottom: 4,
              fontSize: '0.8rem',
            }}>
              <span style={{ color: 'var(--text-secondary)' }}>RAM</span>
              <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                {formatBytes(ram.used)} / {formatBytes(ram.total)}
              </span>
            </div>
            <ProgressBar 
              value={ram.used} 
              max={ram.total}
              color={ram.used / ram.total > 0.9 ? 'var(--accent-red)' : 'var(--accent-blue)'}
            />
          </div>
        </StatusCard>
      </div>
    </div>
  );
}
