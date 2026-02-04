export default function StatusCard({ title, children, status, action }) {
  const statusColors = {
    running: 'var(--accent-green)',
    stopped: 'var(--accent-red)',
    warning: 'var(--accent-yellow)',
    idle: 'var(--text-muted)',
  };

  return (
    <div style={{
      backgroundColor: 'var(--bg-card)',
      border: '1px solid var(--border-color)',
      borderRadius: 12,
      padding: 20,
    }}>
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        marginBottom: 16,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          {status && (
            <span style={{
              width: 10,
              height: 10,
              borderRadius: '50%',
              backgroundColor: statusColors[status] || 'var(--text-muted)',
              boxShadow: status === 'running' ? `0 0 8px ${statusColors[status]}` : 'none',
            }} />
          )}
          <h3 style={{
            fontSize: '0.95rem',
            fontWeight: 600,
            color: 'var(--text-primary)',
          }}>
            {title}
          </h3>
        </div>
        {action}
      </div>
      {children}
    </div>
  );
}
