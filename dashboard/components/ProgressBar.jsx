export default function ProgressBar({ value, max, color = 'var(--accent-blue)' }) {
  const safeMax = max || 0;
  const safeValue = value || 0;
  const percent = safeMax ? (safeValue / safeMax) * 100 : 0;

  return (
    <div style={{
      height: 6,
      backgroundColor: 'var(--bg-secondary)',
      borderRadius: 3,
      overflow: 'hidden',
    }}>
      <div style={{
        height: '100%',
        width: `${Math.min(percent, 100)}%`,
        backgroundColor: color,
        borderRadius: 3,
        transition: 'width 0.3s ease',
      }} />
    </div>
  );
}

