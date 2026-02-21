import { useState, useEffect, useCallback } from 'react';
import { useBranch } from '../hooks/useBranch';
import StatusCard from '../components/StatusCard';
import * as api from '../lib/api';

export default function BranchesPage() {
  const { branch: currentBranch, branchMeta, refreshBranch, openBranchPicker } = useBranch();
  const [branches, setBranches] = useState([]);
  const [topicIssues, setTopicIssues] = useState([]);
  const [showIssues, setShowIssues] = useState(false);
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState(null);
  const [error, setError] = useState(null);

  const loadBranches = useCallback(async () => {
    try {
      const data = await api.listBranches();
      setBranches(data || []);
    } catch (e) {
      console.error('Failed to load branches:', e);
    }
  }, []);

  useEffect(() => { loadBranches(); }, [loadBranches]);

  const handleRefresh = useCallback(() => {
    loadBranches();
    refreshBranch();
  }, [loadBranches, refreshBranch]);

  const handleOpenIssues = async () => {
    setShowIssues(true);
    setError(null);
    try {
      const data = await api.listTopicIssues();
      setTopicIssues(data || []);
    } catch (e) {
      setError(e.message);
    }
  };

  const handleCreateBranch = async (issueNumber) => {
    setCreating(true);
    setError(null);
    try {
      await api.createBranch(issueNumber);
      setShowIssues(false);
      handleRefresh();
    } catch (e) {
      setError(e.message);
    } finally {
      setCreating(false);
    }
  };

  const handleDeleteBranch = async (branchName) => {
    if (!window.confirm(`Delete branch "${branchName}"? This cannot be undone.`)) return;
    setDeleting(branchName);
    setError(null);
    try {
      // Switch to branch first, then delete
      await api.switchBranch(branchName);
      await api.deleteBranch(true);
      handleRefresh();
    } catch (e) {
      setError(e.message);
    } finally {
      setDeleting(null);
    }
  };

  const researchBranches = (branches || []).filter(
    (b) => typeof b === 'object' ? b.isResearch : false
  );
  const otherBranches = (branches || []).filter(
    (b) => typeof b === 'object' ? !b.isResearch : true
  );

  return (
    <div>
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        marginBottom: 24,
      }}>
        <div>
          <h1 style={{ fontSize: '1.75rem', fontWeight: 700, letterSpacing: '-0.02em' }}>
            Branches
          </h1>
          <p style={{ color: 'var(--text-secondary)', fontSize: '0.9rem', marginTop: 4 }}>
            Manage research branches and create new ones from issues
          </p>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button
            onClick={handleRefresh}
            style={{
              padding: '8px 14px',
              fontSize: '0.85rem',
              backgroundColor: 'var(--bg-secondary)',
              border: '1px solid var(--border-color)',
              borderRadius: 8,
              color: 'var(--text-primary)',
              cursor: 'pointer',
            }}
          >
            Refresh
          </button>
          <button
            onClick={handleOpenIssues}
            style={{
              padding: '8px 16px',
              fontSize: '0.85rem',
              backgroundColor: 'var(--accent-green)',
              border: 'none',
              borderRadius: 8,
              color: '#fff',
              fontWeight: 500,
              cursor: 'pointer',
            }}
          >
            + New Branch from Issue
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

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 320px', gap: 20, alignItems: 'start' }}>
        {/* Main list */}
        <div>
          {/* Current dashboard branch */}
          <StatusCard title="Current Dashboard Branch">
            <div
              onClick={openBranchPicker}
              style={{ cursor: 'pointer' }}
            >
              {branchMeta?.topic ? (
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                    {branchMeta.issueNumber && (
                      <span style={{
                        fontSize: '0.68rem',
                        fontWeight: 600,
                        backgroundColor: 'var(--accent-purple)',
                        color: '#fff',
                        padding: '2px 6px',
                        borderRadius: 3,
                      }}>
                        #{branchMeta.issueNumber}
                      </span>
                    )}
                    <span style={{ fontSize: '1rem', fontWeight: 600 }}>{branchMeta.topic}</span>
                  </div>
                  {(branchMeta.domain || branchMeta.category) && (
                    <div style={{ fontSize: '0.78rem', color: 'var(--text-muted)', paddingLeft: branchMeta.issueNumber ? 34 : 0 }}>
                      {[branchMeta.domain, branchMeta.category].filter(Boolean).join(' › ')}
                    </div>
                  )}
                  <div style={{
                    fontSize: '0.72rem',
                    fontFamily: 'JetBrains Mono, monospace',
                    color: 'var(--text-muted)',
                    marginTop: 6,
                    paddingLeft: branchMeta.issueNumber ? 34 : 0,
                  }}>
                    {currentBranch?.name}
                  </div>
                </div>
              ) : (
                <div style={{
                  fontFamily: 'JetBrains Mono, monospace',
                  fontSize: '0.9rem',
                  color: currentBranch?.isMain ? 'var(--accent-green)' : 'var(--accent-purple)',
                }}>
                  {currentBranch?.name || '...'}
                </div>
              )}
              <div style={{ fontSize: '0.72rem', color: 'var(--text-muted)', marginTop: 8 }}>
                Click to switch dashboard branch
              </div>
            </div>
          </StatusCard>

          {/* Research branches */}
          <div style={{ marginTop: 20 }}>
            <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 12 }}>
              Research Branches ({researchBranches.length})
            </h2>
            {researchBranches.length === 0 ? (
              <div style={{
                padding: '24px 16px',
                textAlign: 'center',
                color: 'var(--text-muted)',
                fontSize: '0.9rem',
                backgroundColor: 'var(--bg-card)',
                borderRadius: 12,
                border: '1px solid var(--border-color)',
              }}>
                No research branches. Create one from a topic issue.
              </div>
            ) : (
              researchBranches.map((b) => {
                const isCurrent = b.name === currentBranch?.name;
                return (
                  <div
                    key={b.name}
                    style={{
                      padding: '14px 16px',
                      backgroundColor: 'var(--bg-card)',
                      borderRadius: 12,
                      border: isCurrent ? '1px solid var(--accent-blue)' : '1px solid var(--border-color)',
                      marginBottom: 10,
                    }}
                  >
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                      <div style={{ minWidth: 0, flex: 1 }}>
                        {/* Topic + issue badge */}
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                          {b.issueNumber && (
                            <span style={{
                              fontSize: '0.65rem',
                              fontWeight: 600,
                              backgroundColor: 'var(--accent-purple)',
                              color: '#fff',
                              padding: '2px 6px',
                              borderRadius: 3,
                              flexShrink: 0,
                            }}>
                              #{b.issueNumber}
                            </span>
                          )}
                          <span style={{
                            fontSize: '0.95rem',
                            fontWeight: 600,
                            color: 'var(--text-primary)',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                          }}>
                            {b.topic || b.name}
                          </span>
                          {isCurrent && (
                            <span style={{
                              fontSize: '0.65rem',
                              backgroundColor: 'var(--accent-blue)',
                              color: '#fff',
                              padding: '2px 6px',
                              borderRadius: 3,
                              flexShrink: 0,
                            }}>
                              active
                            </span>
                          )}
                        </div>
                        {/* Domain › Category */}
                        {(b.domain || b.category) && (
                          <div style={{
                            fontSize: '0.75rem',
                            color: 'var(--text-muted)',
                            paddingLeft: b.issueNumber ? 34 : 0,
                            marginBottom: 4,
                          }}>
                            {[b.domain, b.category].filter(Boolean).join(' › ')}
                          </div>
                        )}
                        {/* Branch name */}
                        <div style={{
                          fontSize: '0.7rem',
                          fontFamily: 'JetBrains Mono, monospace',
                          color: 'var(--text-muted)',
                          paddingLeft: b.issueNumber ? 34 : 0,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}>
                          {b.name}
                        </div>
                      </div>
                      {/* Actions */}
                      <div style={{ display: 'flex', gap: 6, flexShrink: 0, marginLeft: 12 }}>
                        {!isCurrent && (
                          <button
                            onClick={() => {
                              api.switchBranch(b.name).then(() => handleRefresh());
                            }}
                            style={{
                              padding: '4px 10px',
                              fontSize: '0.72rem',
                              backgroundColor: 'var(--accent-blue)',
                              border: 'none',
                              borderRadius: 4,
                              color: '#fff',
                              cursor: 'pointer',
                            }}
                          >
                            Switch
                          </button>
                        )}
                        <button
                          onClick={() => handleDeleteBranch(b.name)}
                          disabled={deleting === b.name}
                          style={{
                            padding: '4px 10px',
                            fontSize: '0.72rem',
                            backgroundColor: 'transparent',
                            border: '1px solid var(--border-color)',
                            borderRadius: 4,
                            color: 'var(--accent-red)',
                            cursor: deleting === b.name ? 'not-allowed' : 'pointer',
                            opacity: deleting === b.name ? 0.5 : 1,
                          }}
                        >
                          {deleting === b.name ? '...' : 'Delete'}
                        </button>
                      </div>
                    </div>
                  </div>
                );
              })
            )}
          </div>

          {/* Other branches */}
          {otherBranches.length > 0 && (
            <div style={{ marginTop: 20 }}>
              <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 12, color: 'var(--text-secondary)' }}>
                Other Branches ({otherBranches.length})
              </h2>
              {otherBranches.map((b) => {
                const name = typeof b === 'object' ? b.name : b;
                const isCurrent = name === currentBranch?.name;
                return (
                  <div
                    key={name}
                    style={{
                      padding: '10px 16px',
                      backgroundColor: 'var(--bg-card)',
                      borderRadius: 8,
                      border: isCurrent ? '1px solid var(--accent-blue)' : '1px solid var(--border-color)',
                      marginBottom: 8,
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                    }}
                  >
                    <span style={{
                      fontFamily: 'JetBrains Mono, monospace',
                      fontSize: '0.82rem',
                      color: name === 'main' ? 'var(--accent-green)' : 'var(--text-primary)',
                    }}>
                      {name}
                    </span>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      {isCurrent && (
                        <span style={{
                          fontSize: '0.65rem',
                          backgroundColor: 'var(--accent-blue)',
                          color: '#fff',
                          padding: '2px 6px',
                          borderRadius: 3,
                        }}>
                          active
                        </span>
                      )}
                      {!isCurrent && (
                        <button
                          onClick={() => {
                            api.switchBranch(name).then(() => handleRefresh());
                          }}
                          style={{
                            padding: '4px 10px',
                            fontSize: '0.72rem',
                            backgroundColor: 'var(--accent-blue)',
                            border: 'none',
                            borderRadius: 4,
                            color: '#fff',
                            cursor: 'pointer',
                          }}
                        >
                          Switch
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Sidebar: Quick Info */}
        <div>
          <StatusCard title="Summary">
            <div style={{ fontSize: '0.85rem', lineHeight: 2, color: 'var(--text-secondary)' }}>
              <div>Total branches: <strong style={{ color: 'var(--text-primary)' }}>{branches.length}</strong></div>
              <div>Research: <strong style={{ color: 'var(--accent-purple)' }}>{researchBranches.length}</strong></div>
              <div>Other: <strong style={{ color: 'var(--text-primary)' }}>{otherBranches.length}</strong></div>
            </div>
          </StatusCard>
        </div>
      </div>

      {/* Issue picker overlay */}
      {showIssues && (
        <div style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0,0,0,0.6)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 1000,
        }}>
          <div style={{
            backgroundColor: 'var(--bg-card)',
            borderRadius: 12,
            padding: 24,
            maxWidth: 600,
            width: '90%',
            maxHeight: '80vh',
            overflow: 'auto',
            border: '1px solid var(--border-color)',
          }}>
            <h3 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 12 }}>
              Create Branch from Issue
            </h3>
            <p style={{ color: 'var(--text-secondary)', fontSize: '0.85rem', marginBottom: 16 }}>
              Select a Research Topic issue to create a new branch.
            </p>

            <div style={{ maxHeight: 400, overflow: 'auto', marginBottom: 16 }}>
              {topicIssues.length === 0 ? (
                <p style={{ color: 'var(--text-muted)', fontSize: '0.85rem' }}>Loading topic issues...</p>
              ) : (
                topicIssues.map((issue) => {
                  const titleParts = (issue.title || '').split(' > ').map(s => s.trim());
                  const domain = titleParts.length >= 3 ? titleParts[0] : (titleParts.length === 2 ? titleParts[0] : null);
                  const category = titleParts.length >= 3 ? titleParts[1] : null;
                  const topic = titleParts.length >= 3 ? titleParts[2] : (titleParts.length === 2 ? titleParts[1] : titleParts[0]);
                  const existingBranch = researchBranches.find(b => b.issueNumber === issue.number);
                  return (
                    <button
                      key={issue.number}
                      onClick={() => !existingBranch && handleCreateBranch(issue.number)}
                      disabled={creating || !!existingBranch}
                      style={{
                        width: '100%',
                        padding: '12px 14px',
                        marginBottom: 8,
                        fontSize: '0.85rem',
                        backgroundColor: existingBranch ? 'var(--bg-card)' : 'var(--bg-secondary)',
                        border: existingBranch ? '1px solid var(--accent-green)' : '1px solid var(--border-color)',
                        borderRadius: 8,
                        color: 'var(--text-primary)',
                        cursor: (creating || existingBranch) ? 'not-allowed' : 'pointer',
                        textAlign: 'left',
                        opacity: creating && !existingBranch ? 0.5 : 1,
                      }}
                    >
                      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 4 }}>
                        <span style={{
                          fontSize: '0.75rem',
                          backgroundColor: existingBranch ? 'var(--text-muted)' : 'var(--accent-green)',
                          color: '#fff',
                          padding: '2px 8px',
                          borderRadius: 4,
                          fontWeight: 500,
                          flexShrink: 0,
                        }}>
                          #{issue.number}
                        </span>
                        <span style={{ fontWeight: 600 }}>{topic}</span>
                        {existingBranch && (
                          <span style={{ fontSize: '0.7rem', color: 'var(--accent-green)', marginLeft: 'auto', flexShrink: 0 }}>
                            ✓ branch exists
                          </span>
                        )}
                      </div>
                      {(domain || category) && (
                        <div style={{ fontSize: '0.72rem', color: 'var(--text-muted)', marginTop: 2, paddingLeft: 2 }}>
                          {[domain, category].filter(Boolean).join(' › ')}
                        </div>
                      )}
                    </button>
                  );
                })
              )}
            </div>

            <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
              <button
                onClick={() => setShowIssues(false)}
                disabled={creating}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--bg-secondary)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 6,
                  color: 'var(--text-primary)',
                  cursor: creating ? 'not-allowed' : 'pointer',
                  opacity: creating ? 0.5 : 1,
                }}
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
