import { useState } from 'react';

export default function ServiceToggle({ 
  name, 
  running, 
  version, 
  extra,
  onStart, 
  onStop,
  disabled = false,
}) {
  const [loading, setLoading] = useState(false);

  const handleToggle = async () => {
    setLoading(true);
    try {
      if (running) {
        await onStop();
      } else {
        await onStart();
      }
    } catch (e) {
      console.error(`Failed to toggle ${name}:`, e);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{
      display: 'flex',
      justifyContent: 'space-between',
      alignItems: 'center',
      padding: '12px 16px',
      backgroundColor: 'var(--bg-secondary)',
      borderRadius: 8,
      marginBottom: 8,
    }}>
      <div>
        <div style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
        }}>
          <span style={{
            width: 8,
            height: 8,
            borderRadius: '50%',
            backgroundColor: running ? 'var(--accent-green)' : 'var(--accent-red)',
          }} />
          <span style={{
            fontSize: '0.9rem',
            fontWeight: 500,
            color: 'var(--text-primary)',
          }}>
            {name}
          </span>
          {version && (
            <span style={{
              fontSize: '0.75rem',
              color: 'var(--text-muted)',
              fontFamily: 'JetBrains Mono, monospace',
            }}>
              v{version}
            </span>
          )}
        </div>
        {extra && (
          <div style={{
            fontSize: '0.8rem',
            color: 'var(--text-secondary)',
            marginTop: 4,
            marginLeft: 16,
          }}>
            {extra}
          </div>
        )}
      </div>

      <button
        onClick={handleToggle}
        disabled={disabled || loading}
        style={{
          padding: '6px 14px',
          fontSize: '0.8rem',
          fontWeight: 500,
          borderRadius: 6,
          border: 'none',
          backgroundColor: running ? 'var(--accent-red)' : 'var(--accent-green)',
          color: '#fff',
          opacity: disabled || loading ? 0.5 : 1,
          cursor: disabled || loading ? 'not-allowed' : 'pointer',
          minWidth: 70,
        }}
      >
        {loading ? '...' : (running ? 'Stop' : 'Start')}
      </button>
    </div>
  );
}
