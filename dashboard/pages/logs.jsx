import { useEffect, useRef, useState } from 'react';
import StatusCard from '../components/StatusCard';
import * as api from '../lib/api';

export default function LogsPage() {
  const [lines, setLines] = useState(400);
  const [text, setText] = useState('');
  const [path, setPath] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [follow, setFollow] = useState(true);
  const boxRef = useRef(null);

  const fetchLogs = async () => {
    setLoading(true);
    try {
      const res = await api.getResearcherLogs(lines);
      setText(res.text || '');
      setPath(res.path || '');
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchLogs();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lines]);

  useEffect(() => {
    if (!autoRefresh) return;
    const t = setInterval(fetchLogs, 2000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoRefresh, lines]);

  useEffect(() => {
    if (!follow) return;
    const el = boxRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, [text, follow]);

  return (
    <div>
      <div style={{ marginBottom: 24 }}>
        <h1 style={{
          fontSize: '1.75rem',
          fontWeight: 700,
          letterSpacing: '-0.02em',
        }}>
          Logs
        </h1>
        <p style={{
          color: 'var(--text-secondary)',
          fontSize: '0.9rem',
          marginTop: 4,
        }}>
          Live tail of <code>researcher.log</code>
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

      <StatusCard title="Researcher Log">
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: '0.8rem', color: 'var(--text-secondary)' }}>Lines</span>
            <input
              type="number"
              min={50}
              max={5000}
              value={lines}
              onChange={(e) => setLines(Math.max(50, Math.min(5000, parseInt(e.target.value || '0', 10))))}
              style={{
                width: 90,
                padding: '6px 10px',
                fontSize: '0.85rem',
                backgroundColor: 'var(--bg-secondary)',
                border: '1px solid var(--border-color)',
                borderRadius: 6,
                color: 'var(--text-primary)',
                fontFamily: 'JetBrains Mono, monospace',
                textAlign: 'right',
              }}
            />
          </div>

          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: '0.85rem', cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={autoRefresh}
              onChange={(e) => setAutoRefresh(e.target.checked)}
              style={{ width: 16, height: 16 }}
            />
            <span style={{ color: 'var(--text-primary)' }}>Auto refresh (2s)</span>
          </label>

          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: '0.85rem', cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={follow}
              onChange={(e) => setFollow(e.target.checked)}
              style={{ width: 16, height: 16 }}
            />
            <span style={{ color: 'var(--text-primary)' }}>Follow</span>
          </label>

          <button
            onClick={fetchLogs}
            disabled={loading}
            style={{
              padding: '8px 12px',
              fontSize: '0.85rem',
              backgroundColor: 'var(--accent-blue)',
              border: 'none',
              borderRadius: 6,
              color: '#fff',
              fontWeight: 600,
              opacity: loading ? 0.6 : 1,
            }}
          >
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {path && (
          <div style={{
            fontSize: '0.75rem',
            color: 'var(--text-muted)',
            marginBottom: 10,
          }}>
            Source: <code>{path}</code>
          </div>
        )}

        <div
          ref={boxRef}
          onScroll={() => {
            // If user scrolls up, disable follow automatically.
            const el = boxRef.current;
            if (!el) return;
            const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 10;
            if (!atBottom && follow) setFollow(false);
          }}
          style={{
            backgroundColor: 'var(--bg-secondary)',
            border: '1px solid var(--border-color)',
            borderRadius: 10,
            padding: 14,
            height: '70vh',
            overflow: 'auto',
            fontFamily: 'JetBrains Mono, monospace',
            fontSize: '0.78rem',
            lineHeight: 1.6,
            whiteSpace: 'pre-wrap',
            color: 'var(--text-secondary)',
          }}
        >
          {text || (loading ? 'Loading…' : 'No logs yet.')}
        </div>
      </StatusCard>
    </div>
  );
}

