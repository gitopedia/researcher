import { useState, useEffect, useCallback } from 'react';
import { useStatus } from '../hooks/useStatus';
import { useBranch } from '../hooks/useBranch';
import StatusCard from '../components/StatusCard';
import * as api from '../lib/api';

const WORKER_TYPE_LABELS = {
  research: 'Research',
  image_prompt: 'Image Prompt',
  image_gen: 'Image Generation',
};

const STATE_COLORS = {
  stopped: 'var(--text-muted)',
  starting: 'var(--accent-yellow)',
  running: 'var(--accent-green)',
  paused: 'var(--accent-yellow)',
  stopping: 'var(--accent-yellow)',
  error: 'var(--accent-red)',
};

// ── BranchSelect: dropdown of available branches ────────────────────────────

// Helper: format a branch object into a readable label for dropdowns
function branchLabel(b, showInUse) {
  const name = typeof b === 'string' ? b : b.name;
  const domain = typeof b === 'object' ? b.domain : null;
  const category = typeof b === 'object' ? b.category : null;
  const topic = typeof b === 'object' ? b.topic : null;
  const issueNum = typeof b === 'object' ? b.issueNumber : null;

  // Prefer readable "Domain › Category › Topic (#N)" when available
  if (topic) {
    const parts = [domain, category, topic].filter(Boolean);
    let label = parts.join(' › ');
    if (issueNum) label += `  (#${issueNum})`;
    if (showInUse) label += '  [in use]';
    return label;
  }

  // Fallback to raw branch name
  let label = name;
  if (issueNum) label += `  (#${issueNum})`;
  if (showInUse) label += '  [in use]';
  return label;
}

function BranchSelect({ value, onChange, branches, workers, disabled }) {
  // Build a set of branches already claimed by running/paused workers
  const claimedSet = new Set();
  (workers || []).forEach((w) => {
    if (w.branch && (w.state === 'running' || w.state === 'paused' || w.state === 'starting')) {
      claimedSet.add(w.branch);
    }
  });

  // Only show research branches in the picker (non-research like "main" aren't useful)
  const researchBranches = (branches || []).filter(
    (b) => (typeof b === 'object' ? b.isResearch : (typeof b === 'string' && b.startsWith('research/')))
  );

  return (
    <select
      value={value || ''}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      style={{
        flex: 1,
        padding: '6px 10px',
        fontSize: '0.8rem',
        fontFamily: 'JetBrains Mono, monospace',
        backgroundColor: 'var(--bg-secondary)',
        border: '1px solid var(--border-color)',
        borderRadius: 6,
        color: 'var(--text-primary)',
        opacity: disabled ? 0.6 : 1,
        minWidth: 0,
      }}
    >
      <option value="">— select a branch —</option>
      {researchBranches.map((b) => {
        const name = typeof b === 'string' ? b : b.name;
        const isClaimed = claimedSet.has(name);
        return (
          <option key={name} value={name} disabled={isClaimed && name !== value}>
            {branchLabel(b, isClaimed && name !== value)}
          </option>
        );
      })}
    </select>
  );
}

// ── WorkerCard ──────────────────────────────────────────────────────────────

function WorkerCard({ worker, branches, allWorkers, onRefresh }) {
  const [configBranch, setConfigBranch] = useState(worker.branch || '');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  // Keep local selection in sync when worker prop changes (e.g. after configure)
  useEffect(() => {
    setConfigBranch(worker.branch || '');
  }, [worker.branch]);

  const handleAction = useCallback(async (action) => {
    setLoading(true);
    setError(null);
    try {
      switch (action) {
        case 'start':
          await api.startWorker(worker.id);
          break;
        case 'stop':
          await api.stopWorker(worker.id);
          break;
        case 'pause':
          await api.pauseWorker(worker.id);
          break;
        case 'resume':
          await api.resumeWorker(worker.id);
          break;
      }
      onRefresh();
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, [worker.id, onRefresh]);

  const handleConfigure = useCallback(async (branch) => {
    setLoading(true);
    setError(null);
    try {
      await api.configureWorker(worker.id, {
        branch,
        repoPath: '',
        enabled: true,
      });
      onRefresh();
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, [worker.id, onRefresh]);

  const handleBranchChange = useCallback((newBranch) => {
    setConfigBranch(newBranch);
    // Auto-apply when the user picks from the dropdown
    if (newBranch && newBranch !== worker.branch) {
      handleConfigure(newBranch);
    }
  }, [worker.branch, handleConfigure]);

  const handleDelete = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      await api.deleteWorker(worker.id);
      onRefresh();
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, [worker.id, onRefresh]);

  const isStopped = worker.state === 'stopped' || worker.state === 'error';
  const isRunning = worker.state === 'running';
  const isPaused = worker.state === 'paused';

  return (
    <div style={{
      padding: 16,
      backgroundColor: 'var(--bg-card)',
      borderRadius: 12,
      border: '1px solid var(--border-color)',
      marginBottom: 16,
    }}>
      {/* Header row */}
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        marginBottom: 12,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 6,
            fontSize: '0.75rem',
            padding: '3px 8px',
            borderRadius: 4,
            backgroundColor: 'var(--bg-secondary)',
            color: 'var(--text-muted)',
            fontFamily: 'JetBrains Mono, monospace',
          }}>
            {WORKER_TYPE_LABELS[worker.type] || worker.type}
          </span>
          <span style={{
            fontSize: '0.9rem',
            fontWeight: 600,
            color: 'var(--text-primary)',
          }}>
            {worker.id}
          </span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 6,
            fontSize: '0.8rem',
            color: STATE_COLORS[worker.state] || 'var(--text-muted)',
          }}>
            <span style={{
              width: 8,
              height: 8,
              borderRadius: '50%',
              backgroundColor: 'currentColor',
            }} />
            {worker.state}
          </span>
          {isStopped && (
            <button
              onClick={handleDelete}
              disabled={loading}
              title="Remove worker"
              style={{
                padding: '2px 6px',
                fontSize: '0.75rem',
                backgroundColor: 'transparent',
                border: '1px solid var(--border-color)',
                borderRadius: 4,
                color: 'var(--text-muted)',
                cursor: 'pointer',
              }}
            >
              ✕
            </button>
          )}
        </div>
      </div>

      {/* Branch selector */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        marginBottom: 12,
      }}>
        <label style={{ fontSize: '0.8rem', color: 'var(--text-secondary)', minWidth: 50 }}>
          Branch
        </label>
        <BranchSelect
          value={configBranch}
          onChange={handleBranchChange}
          branches={branches}
          workers={allWorkers}
          disabled={!isStopped}
        />
      </div>

      {/* Status info */}
      {worker.currentStep && (
        <div style={{
          padding: '6px 10px',
          backgroundColor: 'var(--bg-secondary)',
          borderRadius: 6,
          marginBottom: 12,
          fontSize: '0.8rem',
          fontFamily: 'JetBrains Mono, monospace',
          color: 'var(--text-muted)',
        }}>
          {worker.currentStep}
        </div>
      )}

      <div style={{
        display: 'flex',
        gap: 16,
        fontSize: '0.8rem',
        color: 'var(--text-secondary)',
        marginBottom: 12,
      }}>
        <span>Iterations: <strong>{worker.iterations || 0}</strong></span>
        <span>Errors: <strong style={{ color: worker.errors > 0 ? 'var(--accent-red)' : 'inherit' }}>{worker.errors || 0}</strong></span>
        {worker.startedAt && worker.state !== 'stopped' && (
          <span>Started: {new Date(worker.startedAt).toLocaleTimeString()}</span>
        )}
      </div>

      {worker.lastError && (
        <div style={{
          padding: '6px 10px',
          backgroundColor: 'rgba(239, 68, 68, 0.1)',
          borderRadius: 6,
          marginBottom: 12,
          fontSize: '0.8rem',
          color: 'var(--accent-red)',
        }}>
          {worker.lastError}
        </div>
      )}

      {error && (
        <div style={{
          padding: '6px 10px',
          backgroundColor: 'rgba(239, 68, 68, 0.1)',
          borderRadius: 6,
          marginBottom: 12,
          fontSize: '0.8rem',
          color: 'var(--accent-red)',
        }}>
          {error}
        </div>
      )}

      {/* Controls */}
      <div style={{ display: 'flex', gap: 8 }}>
        {isStopped && (
          <button
            onClick={() => handleAction('start')}
            disabled={loading || !configBranch}
            style={{
              padding: '8px 16px',
              fontSize: '0.85rem',
              backgroundColor: !configBranch ? 'var(--bg-secondary)' : 'var(--accent-green)',
              border: 'none',
              borderRadius: 6,
              color: !configBranch ? 'var(--text-muted)' : '#fff',
              fontWeight: 500,
              cursor: !configBranch ? 'not-allowed' : 'pointer',
            }}
          >
            Start
          </button>
        )}
        {isRunning && (
          <>
            <button
              onClick={() => handleAction('pause')}
              disabled={loading}
              style={{
                padding: '8px 16px',
                fontSize: '0.85rem',
                backgroundColor: 'var(--accent-yellow)',
                border: 'none',
                borderRadius: 6,
                color: '#000',
                fontWeight: 500,
                cursor: 'pointer',
              }}
            >
              Pause
            </button>
            <button
              onClick={() => handleAction('stop')}
              disabled={loading}
              style={{
                padding: '8px 16px',
                fontSize: '0.85rem',
                backgroundColor: 'var(--accent-red)',
                border: 'none',
                borderRadius: 6,
                color: '#fff',
                fontWeight: 500,
                cursor: 'pointer',
              }}
            >
              Stop
            </button>
          </>
        )}
        {isPaused && (
          <>
            <button
              onClick={() => handleAction('resume')}
              disabled={loading}
              style={{
                padding: '8px 16px',
                fontSize: '0.85rem',
                backgroundColor: 'var(--accent-green)',
                border: 'none',
                borderRadius: 6,
                color: '#fff',
                fontWeight: 500,
                cursor: 'pointer',
              }}
            >
              Resume
            </button>
            <button
              onClick={() => handleAction('stop')}
              disabled={loading}
              style={{
                padding: '8px 16px',
                fontSize: '0.85rem',
                backgroundColor: 'var(--accent-red)',
                border: 'none',
                borderRadius: 6,
                color: '#fff',
                fontWeight: 500,
                cursor: 'pointer',
              }}
            >
              Stop
            </button>
          </>
        )}
      </div>
    </div>
  );
}

// ── QueuePanel ──────────────────────────────────────────────────────────────

function QueuePanel({ queueStatus }) {
  if (!queueStatus) return null;

  const queues = [
    { label: 'LLM Queue', data: queueStatus.llm },
    { label: 'ComfyUI Queue', data: queueStatus.comfyui },
  ];

  return (
    <StatusCard title="Queue Status">
      {queues.map((q) => (
        <div key={q.label} style={{
          padding: '10px 0',
          borderBottom: '1px solid var(--border-color)',
        }}>
          <div style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            marginBottom: 6,
          }}>
            <span style={{ fontSize: '0.85rem', fontWeight: 500 }}>{q.label}</span>
            <span style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              fontSize: '0.8rem',
              color: q.data?.serviceUp ? 'var(--accent-green)' : 'var(--text-muted)',
            }}>
              <span style={{
                width: 8,
                height: 8,
                borderRadius: '50%',
                backgroundColor: q.data?.serviceUp ? 'var(--accent-green)' : 'var(--text-muted)',
              }} />
              {q.data?.serviceUp ? 'Service Up' : 'Service Down'}
            </span>
          </div>
          <div style={{
            display: 'flex',
            gap: 16,
            fontSize: '0.8rem',
            color: 'var(--text-secondary)',
          }}>
            <span>Pending: <strong>{q.data?.pending || 0}</strong></span>
            <span>Processing: <strong>{q.data?.processing ? 'Yes' : 'No'}</strong></span>
            <span>Total: <strong>{q.data?.totalJobs || 0}</strong></span>
            <span>Errors: <strong style={{ color: (q.data?.totalErrors || 0) > 0 ? 'var(--accent-red)' : 'inherit' }}>{q.data?.totalErrors || 0}</strong></span>
          </div>
          {q.data?.currentJob && (
            <div style={{
              marginTop: 6,
              padding: '4px 8px',
              backgroundColor: 'var(--bg-secondary)',
              borderRadius: 4,
              fontSize: '0.75rem',
              fontFamily: 'JetBrains Mono, monospace',
              color: 'var(--text-muted)',
            }}>
              {q.data.currentJob}
            </div>
          )}
          {q.data?.lastError && (
            <div style={{
              marginTop: 6,
              fontSize: '0.75rem',
              color: 'var(--accent-red)',
            }}>
              Last error: {q.data.lastError}
            </div>
          )}
        </div>
      ))}
    </StatusCard>
  );
}

// ── CreateWorkerModal ───────────────────────────────────────────────────────

function CreateWorkerModal({ branches, workers, onClose, onCreated }) {
  const [id, setId] = useState('');
  const [type, setType] = useState('research');
  const [branch, setBranch] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  // Look up selected branch metadata
  const selectedBranchObj = branch
    ? (branches || []).find((b) => (typeof b === 'object' ? b.name : b) === branch)
    : null;
  const selMeta = selectedBranchObj && typeof selectedBranchObj === 'object' ? selectedBranchObj : null;

  const handleCreate = async () => {
    if (!id) {
      setError('Worker ID is required');
      return;
    }
    setLoading(true);
    setError(null);
    try {
      await api.createWorker({ id, type, branch });
      onCreated();
      onClose();
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{
      position: 'fixed',
      top: 0,
      left: 0,
      right: 0,
      bottom: 0,
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
        width: 420,
        border: '1px solid var(--border-color)',
      }}>
        <h3 style={{ marginBottom: 16, fontSize: '1.1rem' }}>Create Worker</h3>

        {error && (
          <div style={{
            padding: '8px 12px',
            backgroundColor: 'rgba(239, 68, 68, 0.1)',
            borderRadius: 6,
            marginBottom: 12,
            color: 'var(--accent-red)',
            fontSize: '0.85rem',
          }}>
            {error}
          </div>
        )}

        <div style={{ marginBottom: 12 }}>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'var(--text-secondary)', marginBottom: 4 }}>
            Worker ID
          </label>
          <input
            type="text"
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="e.g. research-topic-42"
            style={{
              width: '100%',
              padding: '8px 12px',
              fontSize: '0.85rem',
              backgroundColor: 'var(--bg-secondary)',
              border: '1px solid var(--border-color)',
              borderRadius: 6,
              color: 'var(--text-primary)',
              boxSizing: 'border-box',
            }}
          />
        </div>

        <div style={{ marginBottom: 12 }}>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'var(--text-secondary)', marginBottom: 4 }}>
            Type
          </label>
          <select
            value={type}
            onChange={(e) => setType(e.target.value)}
            style={{
              width: '100%',
              padding: '8px 12px',
              fontSize: '0.85rem',
              backgroundColor: 'var(--bg-secondary)',
              border: '1px solid var(--border-color)',
              borderRadius: 6,
              color: 'var(--text-primary)',
              boxSizing: 'border-box',
            }}
          >
            <option value="research">Research</option>
            <option value="image_prompt">Image Prompt</option>
            <option value="image_gen">Image Generation</option>
          </select>
        </div>

        <div style={{ marginBottom: 20 }}>
          <label style={{ display: 'block', fontSize: '0.8rem', color: 'var(--text-secondary)', marginBottom: 4 }}>
            Branch (optional — can assign later)
          </label>
          <BranchSelect
            value={branch}
            onChange={setBranch}
            branches={branches}
            workers={workers}
            disabled={false}
          />

          {/* Selected branch issue info */}
          {selMeta && selMeta.topic && (
            <div style={{
              marginTop: 8,
              padding: '10px 12px',
              backgroundColor: 'var(--bg-primary)',
              borderRadius: 6,
              border: '1px solid var(--border-color)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 3 }}>
                {selMeta.issueNumber && (
                  <span style={{
                    fontSize: '0.62rem',
                    fontWeight: 600,
                    backgroundColor: 'var(--accent-purple)',
                    color: '#fff',
                    padding: '1px 5px',
                    borderRadius: 3,
                    flexShrink: 0,
                  }}>
                    #{selMeta.issueNumber}
                  </span>
                )}
                <span style={{
                  fontSize: '0.88rem',
                  fontWeight: 600,
                  color: 'var(--text-primary)',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}>
                  {selMeta.topic}
                </span>
              </div>
              {(selMeta.domain || selMeta.category) && (
                <div style={{
                  fontSize: '0.72rem',
                  color: 'var(--text-muted)',
                  paddingLeft: selMeta.issueNumber ? 28 : 0,
                }}>
                  {[selMeta.domain, selMeta.category].filter(Boolean).join(' › ')}
                </div>
              )}
            </div>
          )}
        </div>

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button
            onClick={onClose}
            style={{
              padding: '8px 16px',
              fontSize: '0.85rem',
              backgroundColor: 'var(--bg-secondary)',
              border: '1px solid var(--border-color)',
              borderRadius: 6,
              color: 'var(--text-primary)',
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
          <button
            onClick={handleCreate}
            disabled={loading}
            style={{
              padding: '8px 16px',
              fontSize: '0.85rem',
              backgroundColor: 'var(--accent-blue)',
              border: 'none',
              borderRadius: 6,
              color: '#fff',
              fontWeight: 500,
              cursor: 'pointer',
            }}
          >
            {loading ? 'Creating...' : 'Create'}
          </button>
        </div>
      </div>
    </div>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────────

export default function WorkersPage() {
  const { workers, queueStatus, refresh } = useStatus();
  const { branches: globalBranches, refreshBranch } = useBranch();
  const [showCreate, setShowCreate] = useState(false);
  const [branches, setBranches] = useState([]);

  // Load branches on mount and when we need to refresh
  const loadBranches = useCallback(async () => {
    try {
      const data = await api.listBranches();
      setBranches(data || []);
    } catch (e) {
      // non-critical, will show empty list
      console.error('Failed to load branches:', e);
    }
  }, []);

  useEffect(() => {
    loadBranches();
  }, [loadBranches]);

  // Sync with global branch list when it updates
  useEffect(() => {
    if (globalBranches && globalBranches.length > 0) {
      setBranches(globalBranches);
    }
  }, [globalBranches]);

  // Refresh both workers + branches
  const handleRefreshAll = useCallback(() => {
    refresh();
    loadBranches();
    refreshBranch();
  }, [refresh, loadBranches, refreshBranch]);

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
            Workers
          </h1>
          <p style={{
            color: 'var(--text-secondary)',
            fontSize: '0.9rem',
            marginTop: 4,
          }}>
            Manage research and image generation workers
          </p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          style={{
            padding: '8px 16px',
            fontSize: '0.85rem',
            backgroundColor: 'var(--accent-blue)',
            border: 'none',
            borderRadius: 8,
            color: '#fff',
            fontWeight: 500,
            cursor: 'pointer',
          }}
        >
          + New Worker
        </button>
      </div>

      {/* Queue mini-status */}
      <QueuePanel queueStatus={queueStatus} />

      {/* Workers list */}
      <div style={{ marginTop: 20 }}>
        {(!workers || workers.length === 0) ? (
          <div style={{
            padding: '32px 16px',
            textAlign: 'center',
            color: 'var(--text-muted)',
            fontSize: '0.9rem',
            backgroundColor: 'var(--bg-card)',
            borderRadius: 12,
            border: '1px solid var(--border-color)',
          }}>
            No workers configured yet. Click "+ New Worker" to create one.
          </div>
        ) : (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(420px, 1fr))', gap: 16 }}>
            {workers.map((w) => (
              <WorkerCard
                key={w.id}
                worker={w}
                branches={branches}
                allWorkers={workers}
                onRefresh={handleRefreshAll}
              />
            ))}
          </div>
        )}
      </div>

      {showCreate && (
        <CreateWorkerModal
          branches={branches}
          workers={workers}
          onClose={() => setShowCreate(false)}
          onCreated={handleRefreshAll}
        />
      )}
    </div>
  );
}
