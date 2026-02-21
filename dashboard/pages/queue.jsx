import { useState, useEffect, useCallback } from 'react';
import StatusCard from '../components/StatusCard';
import * as api from '../lib/api';

const STATE_COLORS = {
  up: 'var(--accent-green)',
  down: 'var(--text-muted)',
};

function ServiceIndicator({ up }) {
  return (
    <span style={{
      display: 'inline-flex',
      alignItems: 'center',
      gap: 6,
      fontSize: '0.82rem',
      color: up ? STATE_COLORS.up : STATE_COLORS.down,
    }}>
      <span style={{
        width: 9,
        height: 9,
        borderRadius: '50%',
        backgroundColor: 'currentColor',
      }} />
      {up ? 'Online' : 'Offline'}
    </span>
  );
}

function QueueCard({ title, data }) {
  if (!data) return null;

  return (
    <div style={{
      padding: 20,
      backgroundColor: 'var(--bg-card)',
      borderRadius: 12,
      border: '1px solid var(--border-color)',
    }}>
      {/* Header */}
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        marginBottom: 16,
      }}>
        <h2 style={{ fontSize: '1.1rem', fontWeight: 600 }}>{title}</h2>
        <ServiceIndicator up={data.serviceUp} />
      </div>

      {/* Stats grid */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(4, 1fr)',
        gap: 12,
        marginBottom: 16,
      }}>
        <StatBox label="Pending" value={data.pending || 0} color={data.pending > 0 ? 'var(--accent-yellow)' : 'var(--text-primary)'} />
        <StatBox label="Processing" value={data.processing ? 'Yes' : 'No'} color={data.processing ? 'var(--accent-blue)' : 'var(--text-muted)'} />
        <StatBox label="Total Jobs" value={data.totalJobs || 0} />
        <StatBox label="Errors" value={data.totalErrors || 0} color={(data.totalErrors || 0) > 0 ? 'var(--accent-red)' : 'var(--text-primary)'} />
      </div>

      {/* Current job */}
      {data.currentJob && (
        <div style={{ marginBottom: 12 }}>
          <div style={{
            fontSize: '0.72rem',
            textTransform: 'uppercase',
            letterSpacing: '0.06em',
            color: 'var(--text-muted)',
            marginBottom: 4,
          }}>
            Currently Processing
          </div>
          <div style={{
            padding: '8px 12px',
            backgroundColor: 'var(--bg-secondary)',
            borderRadius: 6,
            fontFamily: 'JetBrains Mono, monospace',
            fontSize: '0.78rem',
            color: 'var(--accent-blue)',
            border: '1px solid var(--border-color)',
          }}>
            {data.currentJob}
          </div>
        </div>
      )}

      {/* Last success */}
      {data.lastSuccess && data.lastSuccess !== '0001-01-01T00:00:00Z' && (
        <div style={{ fontSize: '0.78rem', color: 'var(--text-secondary)', marginBottom: 4 }}>
          Last success: <span style={{ color: 'var(--accent-green)' }}>
            {new Date(data.lastSuccess).toLocaleTimeString()}
          </span>
        </div>
      )}

      {/* Last error */}
      {data.lastError && (
        <div style={{
          padding: '8px 12px',
          backgroundColor: 'rgba(239, 68, 68, 0.08)',
          borderRadius: 6,
          border: '1px solid rgba(239, 68, 68, 0.2)',
          marginTop: 8,
        }}>
          <div style={{
            fontSize: '0.72rem',
            textTransform: 'uppercase',
            letterSpacing: '0.06em',
            color: 'var(--accent-red)',
            marginBottom: 4,
            fontWeight: 600,
          }}>
            Last Error
          </div>
          <div style={{
            fontFamily: 'JetBrains Mono, monospace',
            fontSize: '0.75rem',
            color: 'var(--accent-red)',
            wordBreak: 'break-all',
          }}>
            {data.lastError}
          </div>
        </div>
      )}
    </div>
  );
}

function StatBox({ label, value, color }) {
  return (
    <div style={{
      padding: '10px 12px',
      backgroundColor: 'var(--bg-secondary)',
      borderRadius: 8,
      border: '1px solid var(--border-color)',
      textAlign: 'center',
    }}>
      <div style={{
        fontSize: '0.68rem',
        textTransform: 'uppercase',
        letterSpacing: '0.05em',
        color: 'var(--text-muted)',
        marginBottom: 4,
      }}>
        {label}
      </div>
      <div style={{
        fontSize: '1.3rem',
        fontWeight: 700,
        fontFamily: 'JetBrains Mono, monospace',
        color: color || 'var(--text-primary)',
      }}>
        {value}
      </div>
    </div>
  );
}

export default function QueuePage() {
  const [status, setStatus] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const fetchStatus = useCallback(async () => {
    try {
      const data = await api.getQueueStatus();
      setStatus(data);
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchStatus();
    const t = setInterval(fetchStatus, 2000);
    return () => clearInterval(t);
  }, [fetchStatus]);

  const llm = status?.llm;
  const comfy = status?.comfyui;

  // Overall health
  const bothUp = llm?.serviceUp && comfy?.serviceUp;
  const anyProcessing = llm?.processing || comfy?.processing;
  const totalPending = (llm?.pending || 0) + (comfy?.pending || 0);
  const totalErrors = (llm?.totalErrors || 0) + (comfy?.totalErrors || 0);

  return (
    <div>
      <div style={{ marginBottom: 24 }}>
        <h1 style={{ fontSize: '1.75rem', fontWeight: 700, letterSpacing: '-0.02em' }}>
          Queue
        </h1>
        <p style={{ color: 'var(--text-secondary)', fontSize: '0.9rem', marginTop: 4 }}>
          LLM and ComfyUI job queue status
        </p>
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

      {loading && !status ? (
        <div style={{ color: 'var(--text-muted)', fontSize: '0.9rem', padding: 24 }}>Loading queue status...</div>
      ) : (
        <>
          {/* Overview strip */}
          <div style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(4, 1fr)',
            gap: 12,
            marginBottom: 24,
          }}>
            <StatBox
              label="Services"
              value={bothUp ? 'All Up' : 'Degraded'}
              color={bothUp ? 'var(--accent-green)' : 'var(--accent-yellow)'}
            />
            <StatBox
              label="Pending"
              value={totalPending}
              color={totalPending > 0 ? 'var(--accent-yellow)' : 'var(--text-primary)'}
            />
            <StatBox
              label="Processing"
              value={anyProcessing ? 'Active' : 'Idle'}
              color={anyProcessing ? 'var(--accent-blue)' : 'var(--text-muted)'}
            />
            <StatBox
              label="Total Errors"
              value={totalErrors}
              color={totalErrors > 0 ? 'var(--accent-red)' : 'var(--text-primary)'}
            />
          </div>

          {/* Queue cards */}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
            <QueueCard title="LLM Queue" data={llm} />
            <QueueCard title="ComfyUI Queue" data={comfy} />
          </div>
        </>
      )}
    </div>
  );
}
