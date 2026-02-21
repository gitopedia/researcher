import { useState, useEffect, useCallback, useMemo } from 'react';
import Head from 'next/head';
import Link from 'next/link';
import { useRouter } from 'next/router';
import * as api from '../lib/api';
import { BranchContext } from '../hooks/useBranch';

// Parse "Domain > Category > Topic" from an issue title
function parseIssueTitle(title) {
  if (!title) return {};
  const parts = title.split(' > ').map(s => s.trim());
  if (parts.length >= 3) return { domain: parts[0], category: parts[1], topic: parts[2] };
  if (parts.length === 2) return { domain: parts[0], topic: parts[1] };
  return { topic: parts[0] };
}

export default function App({ Component, pageProps }) {
  const router = useRouter();
  const [mounted, setMounted] = useState(false);
  const [branch, setBranch] = useState(null);
  const [branchIssue, setBranchIssue] = useState(null);
  const [branches, setBranches] = useState([]);
  const [branchLoading, setBranchLoading] = useState(false);
  const [showBranchPicker, setShowBranchPicker] = useState(false);

  const refreshBranch = useCallback(async () => {
    try {
      const [branchData, branchIssueData, branchList] = await Promise.all([
        api.getGitBranch(),
        api.getBranchIssue().catch(() => null),
        api.listBranches().catch(() => []),
      ]);
      setBranch(branchData);
      setBranchIssue(branchIssueData);
      setBranches(branchList || []);
    } catch (e) {
      // Silently ignore – server may not be ready
    }
  }, []);

  const handleSwitchBranch = useCallback(async (branchName) => {
    setBranchLoading(true);
    try {
      await api.switchBranch(branchName);
      await refreshBranch();
      setShowBranchPicker(false);
    } catch (e) {
      console.error('Failed to switch branch:', e);
    } finally {
      setBranchLoading(false);
    }
  }, [refreshBranch]);

  const handleOpenBranchPicker = useCallback(() => {
    setShowBranchPicker(true);
    // Also refresh branches when opening the picker
    api.listBranches().then(d => setBranches(d || [])).catch(() => {});
  }, []);

  useEffect(() => {
    setMounted(true);
    refreshBranch();
    const interval = setInterval(refreshBranch, 10000);
    return () => clearInterval(interval);
  }, [refreshBranch]);

  // Parse issue context for the current branch
  const branchMeta = useMemo(() => {
    if (branchIssue?.issue?.title) {
      return {
        ...parseIssueTitle(branchIssue.issue.title),
        issueNumber: branchIssue.issue.number,
        title: branchIssue.issue.title,
      };
    }
    return {};
  }, [branchIssue]);

  const branchCtx = {
    branch,
    branches,
    branchIssue,
    branchMeta,
    loading: branchLoading,
    switchBranch: handleSwitchBranch,
    refreshBranch,
    openBranchPicker: handleOpenBranchPicker,
  };

  const navItems = [
    { href: '/', label: 'Dashboard', icon: '📊' },
    { href: '/workers', label: 'Workers', icon: '⚙️' },
    { href: '/branches', label: 'Branches', icon: '🌿' },
    { href: '/queue', label: 'Queue', icon: '📬' },
    { href: '/researcher', label: 'Researcher', icon: '🔬' },
    { href: '/topics', label: 'Topics', icon: '📝' },
    { href: '/images', label: 'Images', icon: '🖼️' },
    { href: '/logs', label: 'Logs', icon: '📜' },
  ];

  return (
    <>
      <Head>
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <title>Researcher Dashboard</title>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        <link 
          href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600&family=Inter:wght@400;500;600;700&display=swap" 
          rel="stylesheet" 
        />
      </Head>
      
      <style jsx global>{`
        :root {
          --bg-primary: #0a0a0f;
          --bg-secondary: #12121a;
          --bg-card: #1a1a24;
          --bg-hover: #22222e;
          --border-color: #2a2a38;
          --text-primary: #e8e8ed;
          --text-secondary: #9898a8;
          --text-muted: #68687a;
          --accent-green: #22c55e;
          --accent-red: #ef4444;
          --accent-yellow: #eab308;
          --accent-blue: #3b82f6;
          --accent-purple: #8b5cf6;
          --footer-height: 44px;
        }

        * {
          margin: 0;
          padding: 0;
          box-sizing: border-box;
        }

        html, body {
          background-color: var(--bg-primary);
          color: var(--text-primary);
          font-family: 'Inter', system-ui, -apple-system, sans-serif;
          line-height: 1.5;
          min-height: 100vh;
        }

        a {
          color: var(--accent-blue);
          text-decoration: none;
        }

        a:hover {
          text-decoration: underline;
        }

        button {
          font-family: inherit;
          cursor: pointer;
        }

        code, pre {
          font-family: 'JetBrains Mono', monospace;
        }

        /* Scrollbar styling */
        ::-webkit-scrollbar {
          width: 8px;
          height: 8px;
        }
        ::-webkit-scrollbar-track {
          background: var(--bg-secondary);
        }
        ::-webkit-scrollbar-thumb {
          background: var(--border-color);
          border-radius: 4px;
        }
        ::-webkit-scrollbar-thumb:hover {
          background: var(--text-muted);
        }
      `}</style>

      <div style={{ display: 'flex', minHeight: '100vh' }}>
        {/* Sidebar Navigation */}
        <nav style={{
          width: 220,
          backgroundColor: 'var(--bg-secondary)',
          borderRight: '1px solid var(--border-color)',
          padding: '20px 0',
          position: 'fixed',
          top: 0,
          bottom: 'var(--footer-height)',
          overflowY: 'auto',
        }}>
          <div style={{
            padding: '0 20px 24px',
            borderBottom: '1px solid var(--border-color)',
            marginBottom: 16,
          }}>
            <h1 style={{
              fontSize: '1.1rem',
              fontWeight: 700,
              color: 'var(--text-primary)',
              letterSpacing: '-0.02em',
            }}>
              🔬 Researcher
            </h1>
            <p style={{
              fontSize: '0.75rem',
              color: 'var(--text-muted)',
              marginTop: 4,
            }}>
              Dashboard
            </p>
          </div>

          <ul style={{ listStyle: 'none' }}>
            {navItems.map(item => {
              const isActive = router.pathname === item.href;
              return (
                <li key={item.href}>
                  <Link href={item.href} style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 10,
                    padding: '10px 20px',
                    color: isActive ? 'var(--text-primary)' : 'var(--text-secondary)',
                    backgroundColor: isActive ? 'var(--bg-hover)' : 'transparent',
                    borderLeft: isActive ? '3px solid var(--accent-blue)' : '3px solid transparent',
                    fontSize: '0.9rem',
                    fontWeight: isActive ? 500 : 400,
                    textDecoration: 'none',
                    transition: 'all 0.15s ease',
                  }}>
                    <span>{item.icon}</span>
                    <span>{item.label}</span>
                  </Link>
                </li>
              );
            })}
          </ul>
        </nav>

        {/* Main Content */}
        <main style={{
          flex: 1,
          marginLeft: 220,
          padding: 24,
          paddingBottom: 'calc(24px + var(--footer-height))',
          minHeight: '100vh',
        }}>
          {mounted && (
            <BranchContext.Provider value={branchCtx}>
              <Component {...pageProps} />
            </BranchContext.Provider>
          )}
        </main>
      </div>

      {/* ── Global Branch Footer ──────────────────────────────────────── */}
      <footer
        onClick={handleOpenBranchPicker}
        style={{
          position: 'fixed',
          bottom: 0,
          left: 0,
          right: 0,
          height: 'var(--footer-height)',
          backgroundColor: 'var(--bg-secondary)',
          borderTop: '1px solid var(--border-color)',
          display: 'flex',
          alignItems: 'center',
          padding: '0 20px',
          gap: 16,
          cursor: 'pointer',
          zIndex: 900,
          transition: 'background-color 0.15s ease',
        }}
      >
        {/* Git branch icon + name */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0 }}>
          <span style={{ fontSize: '0.8rem', opacity: 0.7 }}>🌿</span>
          <span style={{
            fontSize: '0.78rem',
            fontFamily: 'JetBrains Mono, monospace',
            color: branch?.isMain ? 'var(--accent-green)' : 'var(--accent-purple)',
            maxWidth: 260,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }} title={branch?.name || 'Loading...'}>
            {branch?.name || '...'}
          </span>
        </div>

        {/* Divider */}
        {branchMeta.topic && (
          <div style={{
            width: 1,
            height: 20,
            backgroundColor: 'var(--border-color)',
            flexShrink: 0,
          }} />
        )}

        {/* Issue badge + topic / breadcrumb */}
        {branchMeta.topic && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
            {branchMeta.issueNumber && (
              <span style={{
                fontSize: '0.65rem',
                fontWeight: 600,
                backgroundColor: 'var(--accent-purple)',
                color: '#fff',
                padding: '1px 6px',
                borderRadius: 3,
                flexShrink: 0,
              }}>
                #{branchMeta.issueNumber}
              </span>
            )}
            <span style={{
              fontSize: '0.82rem',
              fontWeight: 500,
              color: 'var(--text-primary)',
              whiteSpace: 'nowrap',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
            }}>
              {branchMeta.topic}
            </span>
            {(branchMeta.domain || branchMeta.category) && (
              <span style={{
                fontSize: '0.72rem',
                color: 'var(--text-muted)',
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
              }}>
                {[branchMeta.domain, branchMeta.category].filter(Boolean).join(' › ')}
              </span>
            )}
          </div>
        )}

        {/* Spacer */}
        <div style={{ flex: 1 }} />

        {/* Switch prompt */}
        <span style={{
          fontSize: '0.72rem',
          color: 'var(--text-muted)',
          flexShrink: 0,
        }}>
          Click to switch branch
        </span>
      </footer>

      {/* ── Branch Picker Modal ───────────────────────────────────────── */}
      {showBranchPicker && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0, 0, 0, 0.6)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 1000,
        }}>
          <div style={{
            backgroundColor: 'var(--bg-card)',
            borderRadius: 12,
            padding: 24,
            maxWidth: 560,
            width: '90%',
            maxHeight: '80vh',
            overflow: 'auto',
            border: '1px solid var(--border-color)',
          }}>
            <h3 style={{
              fontSize: '1.1rem',
              fontWeight: 600,
              marginBottom: 16,
              color: 'var(--text-primary)',
            }}>
              Switch Branch
            </h3>
            <p style={{
              color: 'var(--text-secondary)',
              fontSize: '0.85rem',
              marginBottom: 16,
            }}>
              Select a branch to view in the dashboard. Workers maintain their own working directories.
            </p>
            <div style={{ maxHeight: 400, overflow: 'auto', marginBottom: 16 }}>
              {branches.length === 0 ? (
                <p style={{ color: 'var(--text-muted)', fontSize: '0.85rem' }}>Loading branches...</p>
              ) : (
                branches.map((b) => {
                  const isCurrent = b.name === branch?.name;
                  const meta = b.topic ? { domain: b.domain, category: b.category, topic: b.topic } : null;
                  return (
                    <button
                      key={b.name}
                      onClick={() => handleSwitchBranch(b.name)}
                      disabled={branchLoading || isCurrent}
                      style={{
                        width: '100%',
                        padding: '12px 14px',
                        marginBottom: 8,
                        fontSize: '0.85rem',
                        backgroundColor: isCurrent ? 'var(--accent-blue)' : 'var(--bg-secondary)',
                        border: '1px solid var(--border-color)',
                        borderRadius: 8,
                        color: isCurrent ? '#fff' : 'var(--text-primary)',
                        cursor: (branchLoading || isCurrent) ? 'not-allowed' : 'pointer',
                        textAlign: 'left',
                        opacity: branchLoading ? 0.5 : 1,
                      }}
                    >
                      {/* Top row: topic name or branch name + badge */}
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: meta ? 4 : 0 }}>
                        {b.isResearch && b.issueNumber && (
                          <span style={{
                            fontSize: '0.65rem',
                            fontWeight: 600,
                            backgroundColor: isCurrent ? 'rgba(255,255,255,0.25)' : 'var(--accent-purple)',
                            color: '#fff',
                            padding: '1px 6px',
                            borderRadius: 3,
                            flexShrink: 0,
                          }}>
                            #{b.issueNumber}
                          </span>
                        )}
                        <span style={{
                          fontWeight: meta ? 600 : 400,
                          fontFamily: meta ? 'inherit' : 'JetBrains Mono, monospace',
                          fontSize: meta ? '0.88rem' : '0.8rem',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}>
                          {meta ? meta.topic : b.name}
                        </span>
                        {isCurrent && (
                          <span style={{ fontSize: '0.7rem', marginLeft: 'auto', opacity: 0.8, flexShrink: 0 }}>
                            current
                          </span>
                        )}
                      </div>
                      {/* Breadcrumb line */}
                      {meta && (meta.domain || meta.category) && (
                        <div style={{
                          fontSize: '0.72rem',
                          color: isCurrent ? 'rgba(255,255,255,0.7)' : 'var(--text-muted)',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                          paddingLeft: b.issueNumber ? 32 : 0,
                        }}>
                          {[meta.domain, meta.category].filter(Boolean).join(' › ')}
                        </div>
                      )}
                      {/* Branch name if we showed the topic */}
                      {meta && (
                        <div style={{
                          fontSize: '0.68rem',
                          fontFamily: 'JetBrains Mono, monospace',
                          color: isCurrent ? 'rgba(255,255,255,0.5)' : 'var(--text-muted)',
                          marginTop: 3,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                          paddingLeft: b.issueNumber ? 32 : 0,
                        }}>
                          {b.name}
                        </div>
                      )}
                    </button>
                  );
                })
              )}
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
              <button
                onClick={() => setShowBranchPicker(false)}
                disabled={branchLoading}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--bg-secondary)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 6,
                  color: 'var(--text-primary)',
                  cursor: branchLoading ? 'not-allowed' : 'pointer',
                  opacity: branchLoading ? 0.5 : 1,
                }}
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
