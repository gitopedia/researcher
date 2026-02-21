import { useEffect, useRef, useState, useCallback, useMemo } from 'react';
import * as api from '../lib/api';

const SOURCE_LABELS = {
  researcher: 'Researcher',
  api: 'API Server',
  queue: 'Queue',
  workers: 'Workers',
};

// Log-line colour rules. Each pattern is tested; first match wins.
const LOG_PATTERNS = [
  { regex: /\b(ERROR|FATAL|CRIT|panic)\b/i,        color: '#ef4444' },  // red
  { regex: /\b(WARN|WARNING)\b/i,                   color: '#eab308' },  // yellow
  { regex: /\b(DEBUG|TRACE)\b/i,                     color: '#68687a' },  // muted
  { regex: /\b(INFO)\b/i,                            color: '#22c55e' },  // green
  { regex: /^\s*\d{4}[/-]\d{2}[/-]\d{2}/,           color: '#9898a8' },  // timestamps → secondary
];

function colourLine(line) {
  for (const { regex, color } of LOG_PATTERNS) {
    if (regex.test(line)) return color;
  }
  return null; // default
}

function ColouredLog({ text }) {
  const lines = useMemo(() => (text || '').split('\n'), [text]);
  return (
    <>
      {lines.map((line, i) => {
        const c = colourLine(line);
        // Highlight level keywords inline
        const parts = line.split(/(\b(?:ERROR|FATAL|CRIT|WARN|WARNING|INFO|DEBUG|TRACE|panic)\b)/i);
        return (
          <div key={i} style={{ color: c || 'var(--text-secondary)', minHeight: '1.3em' }}>
            {parts.map((p, j) => {
              if (/^(ERROR|FATAL|CRIT|panic)$/i.test(p)) {
                return <span key={j} style={{ color: '#ef4444', fontWeight: 600 }}>{p}</span>;
              }
              if (/^(WARN|WARNING)$/i.test(p)) {
                return <span key={j} style={{ color: '#eab308', fontWeight: 600 }}>{p}</span>;
              }
              if (/^(INFO)$/i.test(p)) {
                return <span key={j} style={{ color: '#22c55e', fontWeight: 600 }}>{p}</span>;
              }
              if (/^(DEBUG|TRACE)$/i.test(p)) {
                return <span key={j} style={{ color: '#68687a' }}>{p}</span>;
              }
              return <span key={j}>{p}</span>;
            })}
          </div>
        );
      })}
    </>
  );
}

export default function LogsPage() {
  const [lines, setLines] = useState(400);
  const [text, setText] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [follow, setFollow] = useState(true);
  const [sources, setSources] = useState([]);
  const [activeSource, setActiveSource] = useState('researcher');
  const boxRef = useRef(null);

  useEffect(() => {
    async function loadSources() {
      try {
        const data = await api.getLogSources();
        if (Array.isArray(data) && data.length > 0) {
          setSources(data);
        } else {
          setSources(['researcher', 'api', 'queue', 'workers']);
        }
      } catch {
        setSources(['researcher', 'api', 'queue', 'workers']);
      }
    }
    loadSources();
  }, []);

  const fetchLogs = useCallback(async () => {
    setLoading(true);
    try {
      let res;
      if (activeSource === 'researcher') {
        res = await api.getResearcherLogs(lines);
      } else {
        res = await api.getLogsBySource(activeSource);
      }
      setText(res.text || '');
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, [activeSource, lines]);

  useEffect(() => { fetchLogs(); }, [fetchLogs]);

  useEffect(() => {
    if (!autoRefresh) return;
    const t = setInterval(fetchLogs, 2000);
    return () => clearInterval(t);
  }, [autoRefresh, fetchLogs]);

  useEffect(() => {
    if (!follow) return;
    const el = boxRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, [text, follow]);

  // The page is a flex column that fills the remaining viewport.
  // Toolbar at top, log pane fills the rest.
  return (
    <div style={{
      display: 'flex',
      flexDirection: 'column',
      height: 'calc(100vh - 48px - 44px)', // 48px page padding (24*2), 44px footer
      overflow: 'hidden',
    }}>
      {/* Toolbar: source tabs + controls */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        flexWrap: 'wrap',
        marginBottom: 10,
        flexShrink: 0,
      }}>
        {/* Source tabs */}
        <div style={{
          display: 'flex',
          gap: 3,
          backgroundColor: 'var(--bg-card)',
          padding: 3,
          borderRadius: 8,
          border: '1px solid var(--border-color)',
        }}>
          {sources.map((src) => {
            const isActive = activeSource === src;
            return (
              <button
                key={src}
                onClick={() => setActiveSource(src)}
                style={{
                  padding: '6px 14px',
                  fontSize: '0.8rem',
                  fontWeight: isActive ? 600 : 400,
                  backgroundColor: isActive ? 'var(--accent-blue)' : 'transparent',
                  border: 'none',
                  borderRadius: 6,
                  color: isActive ? '#fff' : 'var(--text-secondary)',
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                }}
              >
                {SOURCE_LABELS[src] || src}
              </button>
            );
          })}
        </div>

        {/* Spacer */}
        <div style={{ flex: 1 }} />

        {/* Controls */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: '0.8rem' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
            <span style={{ color: 'var(--text-muted)' }}>Lines</span>
            <input
              type="number"
              min={50}
              max={5000}
              value={lines}
              onChange={(e) => setLines(Math.max(50, Math.min(5000, parseInt(e.target.value || '0', 10))))}
              style={{
                width: 72,
                padding: '4px 8px',
                fontSize: '0.8rem',
                backgroundColor: 'var(--bg-secondary)',
                border: '1px solid var(--border-color)',
                borderRadius: 4,
                color: 'var(--text-primary)',
                fontFamily: 'JetBrains Mono, monospace',
                textAlign: 'right',
              }}
            />
          </div>

          <label style={{ display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={autoRefresh}
              onChange={(e) => setAutoRefresh(e.target.checked)}
              style={{ width: 14, height: 14 }}
            />
            <span style={{ color: 'var(--text-secondary)' }}>Auto</span>
          </label>

          <label style={{ display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={follow}
              onChange={(e) => setFollow(e.target.checked)}
              style={{ width: 14, height: 14 }}
            />
            <span style={{ color: 'var(--text-secondary)' }}>Follow</span>
          </label>

          <button
            onClick={fetchLogs}
            disabled={loading}
            style={{
              padding: '5px 10px',
              fontSize: '0.8rem',
              backgroundColor: 'var(--accent-blue)',
              border: 'none',
              borderRadius: 4,
              color: '#fff',
              fontWeight: 500,
              opacity: loading ? 0.6 : 1,
              cursor: 'pointer',
            }}
          >
            {loading ? '...' : 'Refresh'}
          </button>
        </div>
      </div>

      {error && (
        <div style={{
          padding: '8px 12px',
          backgroundColor: 'rgba(239, 68, 68, 0.1)',
          border: '1px solid var(--accent-red)',
          borderRadius: 6,
          marginBottom: 8,
          color: 'var(--accent-red)',
          fontSize: '0.82rem',
          flexShrink: 0,
        }}>
          {error}
        </div>
      )}

      {/* Log pane — fills remaining height */}
      <div
        ref={boxRef}
        onScroll={() => {
          const el = boxRef.current;
          if (!el) return;
          const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 10;
          if (!atBottom && follow) setFollow(false);
        }}
        style={{
          flex: 1,
          minHeight: 0,
          backgroundColor: '#0d0d14',
          border: '1px solid var(--border-color)',
          borderRadius: 8,
          padding: '10px 14px',
          overflow: 'auto',
          fontFamily: 'JetBrains Mono, monospace',
          fontSize: '0.72rem',
          lineHeight: 1.55,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
        }}
      >
        {text ? <ColouredLog text={text} /> : (loading ? <span style={{ color: 'var(--text-muted)' }}>Loading…</span> : <span style={{ color: 'var(--text-muted)' }}>No logs yet.</span>)}
      </div>
    </div>
  );
}
