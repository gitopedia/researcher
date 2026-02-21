import { useState, useEffect, useCallback } from 'react';
import { useStatus } from '../hooks/useStatus';
import StatusCard from '../components/StatusCard';
import * as api from '../lib/api';
import { parseIssueChecklist, parseRunProgress } from '../lib/runProgress';
import ProgressBar from '../components/ProgressBar';

export default function ResearcherPage() {
  const { researcherStatus, refresh } = useStatus();
  const [config, setConfig] = useState(null);
  const [branch, setBranch] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [finalizeConfirm, setFinalizeConfirm] = useState(false);
  const [finalizing, setFinalizing] = useState(false);
  const [finalizeResult, setFinalizeResult] = useState(null);
  const [organizeConfirm, setOrganizeConfirm] = useState(false);
  const [organizing, setOrganizing] = useState(false);
  const [organizeResult, setOrganizeResult] = useState(null);

  // Branch & Issue Management state
  const [branchIssue, setBranchIssue] = useState(null);
  const [branches, setBranches] = useState([]);
  const [topicIssues, setTopicIssues] = useState([]);
  const [deleteConfirm, setDeleteConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteResult, setDeleteResult] = useState(null);
  const [switchModal, setSwitchModal] = useState(false);
  const [createModal, setCreateModal] = useState(false);
  const [switching, setSwitching] = useState(false);
  const [creating, setCreating] = useState(false);

  // Run progress (derived from logs + issue checklist)
  const [runProgress, setRunProgress] = useState(null);
  const [runProgressError, setRunProgressError] = useState(null);

  // Load branch and config data
  const loadBranchData = useCallback(async () => {
    try {
      const [configData, branchData, branchIssueData] = await Promise.all([
        api.getConfig(),
        api.getGitBranch(),
        api.getBranchIssue().catch(() => null),
      ]);
      setConfig(configData);
      setBranch(branchData);
      setBranchIssue(branchIssueData);
    } catch (e) {
      setError(e.message);
    }
  }, []);

  // Fetch config and branch info
  useEffect(() => {
    loadBranchData();
  }, [loadBranchData]);

  const handleConfigChange = (key, value) => {
    setConfig(prev => ({ ...prev, [key]: value }));
  };

  const handleSaveConfig = async () => {
    setLoading(true);
    try {
      await api.updateConfig(config);
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  const handleStartRun = async (mode) => {
    setLoading(true);
    try {
      await api.startResearcher({
        mode,
        iterations: config?.iterations || 10,
        minImprovements: config?.minImprovements || 10,
        maxAttempts: config?.maxAttempts || 20,
      });
      refresh();
      // Branch may be created/switched automatically at run start (e.g. backfill on main).
      // Refresh branch info so the UI reflects the active branch.
      await loadBranchData();
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  const handleClean = async (cleanImages, cleanArticles) => {
    if (!confirm(`Are you sure you want to clean ${cleanImages ? 'images' : ''}${cleanImages && cleanArticles ? ' and ' : ''}${cleanArticles ? 'articles' : ''}?`)) {
      return;
    }
    setLoading(true);
    try {
      await api.cleanBranch({ cleanImages, cleanArticles });
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };

  const handleFinalize = async () => {
    setFinalizing(true);
    setFinalizeResult(null);
    try {
      const result = await api.finalizeImages();
      setFinalizeResult(result);
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setFinalizing(false);
      setFinalizeConfirm(false);
    }
  };

  const handleOrganize = async () => {
    setOrganizing(true);
    setOrganizeResult(null);
    try {
      const result = await api.organizeArticles();
      setOrganizeResult(result);
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setOrganizing(false);
      setOrganizeConfirm(false);
    }
  };

  // Load branches list when switch modal opens
  const handleOpenSwitchModal = async () => {
    setSwitchModal(true);
    try {
      const data = await api.listBranches();
      setBranches(data || []);
    } catch (e) {
      setError(e.message);
    }
  };

  // Load topic issues when create modal opens
  const handleOpenCreateModal = async () => {
    setCreateModal(true);
    try {
      const data = await api.listTopicIssues();
      setTopicIssues(data || []);
    } catch (e) {
      setError(e.message);
    }
  };

  // Delete current branch
  const handleDeleteBranch = async () => {
    setDeleting(true);
    setDeleteResult(null);
    try {
      const result = await api.deleteBranch(true);
      setDeleteResult(result);
      // Reload branch data after deletion
      await loadBranchData();
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setDeleting(false);
      setDeleteConfirm(false);
    }
  };

  // Switch to a different branch
  const handleSwitchBranch = async (branchName) => {
    setSwitching(true);
    try {
      await api.switchBranch(branchName);
      setSwitchModal(false);
      // Reload branch data after switch
      await loadBranchData();
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setSwitching(false);
    }
  };

  // Create new branch from issue
  const handleCreateBranch = async (issueNumber) => {
    setCreating(true);
    try {
      await api.createBranch(issueNumber);
      setCreateModal(false);
      // Reload branch data after creation
      await loadBranchData();
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setCreating(false);
    }
  };

  const isIdle = researcherStatus?.state === 'idle';
  const isOnMain = branch?.isMain;
  const isRunActive = researcherStatus?.state === 'running' || researcherStatus?.state === 'paused';

  // Keep branch info fresh during active runs, since branch can change server-side.
  useEffect(() => {
    if (!isRunActive) {
      return undefined;
    }
    loadBranchData();
    const timer = setInterval(() => {
      loadBranchData();
    }, 5000);
    return () => clearInterval(timer);
  }, [isRunActive, loadBranchData]);

  // Poll researcher logs while a run is active, then derive progress counters.
  useEffect(() => {
    if (!isRunActive) {
      setRunProgress(null);
      setRunProgressError(null);
      return undefined;
    }

    let cancelled = false;

    const fetchProgress = async () => {
      try {
        const res = await api.getResearcherLogs(1200);
        if (cancelled) return;

        const parsed = parseRunProgress(res?.text || '');
        const checklist = parseIssueChecklist(branchIssue?.issue?.body || '');
        setRunProgress({ parsed, checklist, logPath: res?.path || '' });
        setRunProgressError(null);
      } catch (e) {
        if (cancelled) return;
        setRunProgressError(e.message);
      }
    };

    // Fetch immediately and then every 2s (same cadence as Logs page)
    fetchProgress();
    const t = setInterval(fetchProgress, 2000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [isRunActive, branchIssue?.issue?.body]);

  return (
    <div>
      <div style={{ marginBottom: 24 }}>
        <h1 style={{
          fontSize: '1.75rem',
          fontWeight: 700,
          letterSpacing: '-0.02em',
        }}>
          Researcher
        </h1>
        <p style={{
          color: 'var(--text-secondary)',
          fontSize: '0.9rem',
          marginTop: 4,
        }}>
          Configure and control research runs
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

      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(380px, 1fr))',
        gap: 20,
      }}>
        {/* Git Branch Info */}
        <StatusCard 
          title="Git Branch"
          status={branch?.isDirty ? 'warning' : (isOnMain ? 'running' : 'idle')}
        >
          {branch ? (
            <div style={{ fontSize: '0.85rem' }}>
              <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                marginBottom: 8,
              }}>
                <span style={{ color: 'var(--text-secondary)' }}>Branch</span>
                <span style={{ 
                  fontFamily: 'JetBrains Mono, monospace',
                  color: isOnMain ? 'var(--accent-green)' : 'var(--accent-purple)',
                  fontSize: '0.8rem',
                  maxWidth: 200,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}>
                  {branch.name}
                </span>
              </div>
              
              <div style={{
                display: 'flex',
                justifyContent: 'space-between',
                marginBottom: 8,
              }}>
                <span style={{ color: 'var(--text-secondary)' }}>Status</span>
                <span style={{ 
                  color: branch.isDirty ? 'var(--accent-yellow)' : 'var(--accent-green)',
                }}>
                  {branch.isDirty ? 'Uncommitted changes' : 'Clean'}
                </span>
              </div>

              {/* Branch Management Buttons */}
              <div style={{ marginTop: 16, display: 'flex', gap: 8 }}>
                <button
                  onClick={handleOpenSwitchModal}
                  disabled={loading}
                  style={{
                    flex: 1,
                    padding: '8px 12px',
                    fontSize: '0.8rem',
                    backgroundColor: 'var(--accent-blue)',
                    border: 'none',
                    borderRadius: 6,
                    color: '#fff',
                    fontWeight: 500,
                    opacity: loading ? 0.5 : 1,
                    cursor: 'pointer',
                  }}
                >
                  Switch Branch
                </button>
                {isOnMain ? (
                  <button
                    onClick={handleOpenCreateModal}
                    disabled={loading}
                    style={{
                      flex: 1,
                      padding: '8px 12px',
                      fontSize: '0.8rem',
                      backgroundColor: 'var(--accent-green)',
                      border: 'none',
                      borderRadius: 6,
                      color: '#fff',
                      fontWeight: 500,
                      opacity: loading ? 0.5 : 1,
                      cursor: 'pointer',
                    }}
                  >
                    New Branch
                  </button>
                ) : (
                  <button
                    onClick={() => setDeleteConfirm(true)}
                    disabled={loading}
                    style={{
                      flex: 1,
                      padding: '8px 12px',
                      fontSize: '0.8rem',
                      backgroundColor: 'var(--accent-red)',
                      border: 'none',
                      borderRadius: 6,
                      color: '#fff',
                      fontWeight: 500,
                      opacity: loading ? 0.5 : 1,
                      cursor: 'pointer',
                    }}
                  >
                    Delete Branch
                  </button>
                )}
              </div>

              {!isOnMain && isIdle && (
                <div style={{ marginTop: 8, display: 'flex', gap: 8 }}>
                  <button
                    onClick={() => handleClean(true, false)}
                    disabled={loading}
                    style={{
                      flex: 1,
                      padding: '8px 12px',
                      fontSize: '0.8rem',
                      backgroundColor: 'var(--accent-yellow)',
                      border: 'none',
                      borderRadius: 6,
                      color: '#000',
                      fontWeight: 500,
                      opacity: loading ? 0.5 : 1,
                      cursor: 'pointer',
                    }}
                  >
                    Clean Images
                  </button>
                  <button
                    onClick={() => handleClean(true, true)}
                    disabled={loading}
                    style={{
                      flex: 1,
                      padding: '8px 12px',
                      fontSize: '0.8rem',
                      backgroundColor: 'var(--accent-orange)',
                      border: 'none',
                      borderRadius: 6,
                      color: '#fff',
                      fontWeight: 500,
                      opacity: loading ? 0.5 : 1,
                      cursor: 'pointer',
                    }}
                  >
                    Clean All
                  </button>
                </div>
              )}
            </div>
          ) : (
            <div style={{ color: 'var(--text-muted)' }}>Loading...</div>
          )}
        </StatusCard>

        {/* Issue Info Card - shows when on a research branch */}
        {branchIssue?.issue && (
          <StatusCard 
            title={`Issue #${branchIssue.issue.number}`}
            status="running"
          >
            <div style={{ fontSize: '0.85rem' }}>
              <h4 style={{
                margin: '0 0 12px 0',
                fontSize: '0.95rem',
                fontWeight: 600,
                color: 'var(--text-primary)',
              }}>
                {branchIssue.issue.title}
              </h4>
              
              <div style={{
                backgroundColor: 'var(--bg-secondary)',
                borderRadius: 8,
                padding: 12,
                maxHeight: 300,
                overflow: 'auto',
                fontSize: '0.8rem',
                lineHeight: 1.7,
              }}>
                <IssueBodyRenderer body={branchIssue.issue.body} />
              </div>
            </div>
          </StatusCard>
        )}

        {/* Research Configuration */}
        <StatusCard title="Configuration">
          {config ? (
            <div>
              <ConfigField
                label="Iterations"
                value={config.iterations}
                onChange={(v) => handleConfigChange('iterations', parseInt(v) || 0)}
                description="Number of article iterations per topic"
              />
              <ConfigField
                label="Min Improvements"
                value={config.minImprovements}
                onChange={(v) => handleConfigChange('minImprovements', parseInt(v) || 0)}
                description="Minimum successful improvements per new article"
              />
              <ConfigField
                label="Max Attempts"
                value={config.maxAttempts}
                onChange={(v) => handleConfigChange('maxAttempts', parseInt(v) || 0)}
                description="Maximum improvement attempts before giving up"
              />
              
              <div style={{ marginTop: 16 }}>
                <label style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  fontSize: '0.85rem',
                  cursor: 'pointer',
                }}>
                  <input
                    type="checkbox"
                    checked={config.generateImagesAfterRun}
                    onChange={(e) => handleConfigChange('generateImagesAfterRun', e.target.checked)}
                    style={{ width: 16, height: 16 }}
                  />
                  <span style={{ color: 'var(--text-primary)' }}>
                    Generate images after run
                  </span>
                </label>
              </div>

              <button
                onClick={handleSaveConfig}
                disabled={loading}
                style={{
                  width: '100%',
                  marginTop: 16,
                  padding: '10px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--accent-blue)',
                  border: 'none',
                  borderRadius: 6,
                  color: '#fff',
                  fontWeight: 500,
                  opacity: loading ? 0.5 : 1,
                }}
              >
                Save Configuration
              </button>
            </div>
          ) : (
            <div style={{ color: 'var(--text-muted)' }}>Loading...</div>
          )}
        </StatusCard>

        {/* Run Controls */}
        <StatusCard 
          title="Run Controls"
          status={researcherStatus?.state === 'running' ? 'running' : 
                  researcherStatus?.state === 'paused' ? 'warning' : 'idle'}
        >
          {researcherStatus?.state === 'running' || researcherStatus?.state === 'paused' ? (
            <div>
              {/* Run Progress */}
              <div style={{
                padding: '12px 16px',
                backgroundColor: 'var(--bg-secondary)',
                borderRadius: 8,
                marginBottom: 16,
                border: '1px solid var(--border-color)',
              }}>
                <div style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'baseline',
                  marginBottom: 10,
                }}>
                  <div style={{
                    fontSize: '0.85rem',
                    fontWeight: 600,
                    color: 'var(--text-primary)',
                  }}>
                    Run Progress
                  </div>
                  {runProgress?.logPath && (
                    <div style={{
                      fontSize: '0.72rem',
                      color: 'var(--text-muted)',
                      fontFamily: 'JetBrains Mono, monospace',
                      maxWidth: 220,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                      textAlign: 'right',
                    }}>
                      {runProgress.logPath}
                    </div>
                  )}
                </div>

                {runProgressError && (
                  <div style={{
                    fontSize: '0.8rem',
                    color: 'var(--accent-red)',
                    marginBottom: 10,
                  }}>
                    Failed to read logs: {runProgressError}
                  </div>
                )}

                {(() => {
                  const parsed = runProgress?.parsed;
                  const checklist = runProgress?.checklist;
                  const imageGen = parsed?.imageGen;
                  const improvement = parsed?.improvement;
                  const topicIteration = parsed?.topicIteration;

                  const showImageProgress = Boolean(imageGen);

                  const currentArticleName =
                    (improvement?.article) ||
                    (parsed?.currentArticle?.name) ||
                    null;

                  const articleDone = checklist?.done ?? null;
                  const articleTotal = checklist?.total ?? null;

                  return (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                      {/* Image processing indicators (Phase 2 only) */}
                      {showImageProgress ? (
                        <>
                          <div style={{
                            display: 'flex',
                            justifyContent: 'space-between',
                            fontSize: '0.8rem',
                          }}>
                            <span style={{ color: 'var(--text-secondary)' }}>
                              Images generated
                            </span>
                            <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                              {imageGen?.current != null && imageGen?.total != null
                                ? `${imageGen.current} / ${imageGen.total}`
                                : 'In progress…'}
                            </span>
                          </div>
                          {imageGen?.current != null && imageGen?.total != null && imageGen.total > 0 && (
                            <ProgressBar
                              value={imageGen.current}
                              max={imageGen.total}
                              color="var(--accent-purple)"
                            />
                          )}

                          {imageGen?.topic && (
                            <div style={{ fontSize: '0.78rem', color: 'var(--text-muted)' }}>
                              {imageGen.topic}
                            </div>
                          )}
                        </>
                      ) : (
                        <>
                          {/* Topic iteration loop (when available) */}
                          {topicIteration && topicIteration.total > 0 && (
                            <>
                              <div style={{
                                display: 'flex',
                                justifyContent: 'space-between',
                                fontSize: '0.8rem',
                              }}>
                                <span style={{ color: 'var(--text-secondary)' }}>
                                  Topic iterations
                                </span>
                                <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                                  {topicIteration.current} / {topicIteration.total}
                                </span>
                              </div>
                              <ProgressBar
                                value={topicIteration.current}
                                max={topicIteration.total}
                                color="var(--accent-blue)"
                              />
                            </>
                          )}

                          {/* Article checklist progress (best-effort from issue body) */}
                          {articleTotal && articleTotal > 0 && (
                            <>
                              <div style={{
                                display: 'flex',
                                justifyContent: 'space-between',
                                fontSize: '0.8rem',
                              }}>
                                <span style={{ color: 'var(--text-secondary)' }}>
                                  Articles complete
                                </span>
                                <span style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                                  {articleDone} / {articleTotal}
                                </span>
                              </div>
                              <ProgressBar
                                value={articleDone}
                                max={articleTotal}
                                color="var(--accent-green)"
                              />
                            </>
                          )}

                          {/* Current improvement attempts/successes */}
                          {improvement && (
                            <div style={{
                              display: 'grid',
                              gridTemplateColumns: '1fr auto',
                              gap: 8,
                              alignItems: 'center',
                              fontSize: '0.8rem',
                            }}>
                              <div style={{ color: 'var(--text-secondary)' }}>
                                Improvement attempts
                              </div>
                              <div style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                                {improvement.attempt} / {improvement.maxAttempts}
                              </div>
                              <div style={{ gridColumn: '1 / -1' }}>
                                <ProgressBar
                                  value={improvement.attempt}
                                  max={improvement.maxAttempts}
                                  color="var(--accent-yellow)"
                                />
                              </div>

                              <div style={{ color: 'var(--text-secondary)' }}>
                                Improvement successes
                              </div>
                              <div style={{ fontFamily: 'JetBrains Mono, monospace' }}>
                                {improvement.successes} / {improvement.minSuccesses}
                              </div>
                              <div style={{ gridColumn: '1 / -1' }}>
                                <ProgressBar
                                  value={Math.min(improvement.successes, improvement.minSuccesses)}
                                  max={improvement.minSuccesses}
                                  color="var(--accent-green)"
                                />
                              </div>
                            </div>
                          )}

                          {currentArticleName && (
                            <div style={{ fontSize: '0.78rem', color: 'var(--text-muted)' }}>
                              Current: {currentArticleName}
                            </div>
                          )}
                        </>
                      )}

                      <div style={{ fontSize: '0.78rem', color: 'var(--text-muted)' }}>
                        {parsed?.lastEvent || researcherStatus.currentStep || 'In progress…'}
                      </div>
                    </div>
                  );
                })()}
              </div>

              <div style={{
                padding: '12px 16px',
                backgroundColor: 'var(--bg-secondary)',
                borderRadius: 8,
                marginBottom: 16,
              }}>
                <div style={{ fontSize: '0.85rem', marginBottom: 8 }}>
                  <span style={{ color: 'var(--text-secondary)' }}>Mode: </span>
                  <span style={{ 
                    fontFamily: 'JetBrains Mono, monospace',
                    color: 'var(--text-primary)',
                  }}>
                    {researcherStatus.mode || 'full'}
                  </span>
                </div>
                {researcherStatus.currentStep && (
                  <div style={{ fontSize: '0.8rem', color: 'var(--text-secondary)' }}>
                    {researcherStatus.currentStep}
                  </div>
                )}
              </div>

              <div style={{ display: 'flex', gap: 8 }}>
                {researcherStatus.state === 'running' ? (
                  <>
                    <button
                      onClick={() => api.pauseResearcher()}
                      style={{
                        flex: 1,
                        padding: '10px 16px',
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
                        padding: '10px 16px',
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
                  </>
                ) : (
                  <>
                    <button
                      onClick={() => api.resumeResearcher()}
                      style={{
                        flex: 1,
                        padding: '10px 16px',
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
                        padding: '10px 16px',
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
                  </>
                )}
              </div>
            </div>
          ) : (
            <div>
              <p style={{ 
                fontSize: '0.85rem', 
                color: 'var(--text-secondary)',
                marginBottom: 16,
              }}>
                {isOnMain 
                  ? 'Create a new branch first, then run research'
                  : 'Run research tasks on this branch'}
              </p>

              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {/* Force stop (useful if backend restarted and run is orphaned) */}
                <button
                  onClick={async () => {
                    const ok = confirm(
                      'Force stop will try to terminate any running researcher subprocesses (including orphaned runs). Continue?'
                    );
                    if (!ok) return;
                    setLoading(true);
                    try {
                      await api.forceStopResearcher();
                      await refresh();
                      setError(null);
                    } catch (e) {
                      setError(e.message);
                    } finally {
                      setLoading(false);
                    }
                  }}
                  disabled={loading}
                  style={{
                    padding: '10px 16px',
                    fontSize: '0.85rem',
                    backgroundColor: 'var(--accent-red)',
                    border: 'none',
                    borderRadius: 6,
                    color: '#fff',
                    fontWeight: 600,
                    opacity: loading ? 0.5 : 1,
                    cursor: loading ? 'not-allowed' : 'pointer',
                    marginBottom: 8,
                  }}
                >
                  Force Stop Run
                </button>

                {/* Full Research Run - available on research branches */}
                {!isOnMain && (
                  <button
                    onClick={() => handleStartRun('full')}
                    disabled={loading}
                    style={{
                      padding: '12px 16px',
                      fontSize: '0.9rem',
                      backgroundColor: 'var(--accent-green)',
                      border: 'none',
                      borderRadius: 8,
                      color: '#fff',
                      fontWeight: 600,
                      opacity: loading ? 0.5 : 1,
                      cursor: loading ? 'not-allowed' : 'pointer',
                    }}
                  >
                    🚀 Run Article Research
                  </button>
                )}
                
                {!isOnMain && (
                  <p style={{ 
                    fontSize: '0.75rem', 
                    color: 'var(--text-muted)',
                    marginTop: -4,
                    marginBottom: 8,
                  }}>
                    Creates articles, image prompts, and generates images
                  </p>
                )}

                <button
                  onClick={() => handleStartRun('backfill-images')}
                  disabled={loading}
                  style={{
                    padding: '10px 16px',
                    fontSize: '0.85rem',
                    backgroundColor: 'var(--accent-purple)',
                    border: 'none',
                    borderRadius: 6,
                    color: '#fff',
                    fontWeight: 500,
                    opacity: loading ? 0.5 : 1,
                    cursor: loading ? 'not-allowed' : 'pointer',
                  }}
                >
                  Backfill Images (prompts + generate)
                </button>

                <button
                  onClick={() => handleStartRun('generate-images')}
                  disabled={loading}
                  style={{
                    padding: '10px 16px',
                    fontSize: '0.85rem',
                    backgroundColor: 'var(--accent-blue)',
                    border: 'none',
                    borderRadius: 6,
                    color: '#fff',
                    fontWeight: 500,
                    opacity: loading ? 0.5 : 1,
                    cursor: loading ? 'not-allowed' : 'pointer',
                  }}
                >
                  Generate Images Only
                </button>

                {!isOnMain && (
                  <>
                    <div style={{ 
                      borderTop: '1px solid var(--border-color)', 
                      marginTop: 12, 
                      paddingTop: 12 
                    }}>
                      <p style={{ 
                        fontSize: '0.75rem', 
                        color: 'var(--text-muted)',
                        marginBottom: 8,
                      }}>
                        Post-processing (after research is complete)
                      </p>
                    </div>
                    <button
                      onClick={() => setFinalizeConfirm(true)}
                      disabled={loading || finalizing || organizing}
                      style={{
                        padding: '10px 16px',
                        fontSize: '0.85rem',
                        backgroundColor: 'var(--bg-secondary)',
                        border: '1px solid var(--accent-green)',
                        borderRadius: 6,
                        color: 'var(--accent-green)',
                        fontWeight: 500,
                        opacity: loading || finalizing || organizing ? 0.5 : 1,
                        cursor: (loading || finalizing || organizing) ? 'not-allowed' : 'pointer',
                      }}
                    >
                      Finalize Images
                    </button>
                    <button
                      onClick={() => setOrganizeConfirm(true)}
                      disabled={loading || finalizing || organizing}
                      style={{
                        padding: '10px 16px',
                        fontSize: '0.85rem',
                        backgroundColor: 'var(--bg-secondary)',
                        border: '1px solid var(--accent-yellow)',
                        borderRadius: 6,
                        color: 'var(--accent-yellow)',
                        fontWeight: 500,
                        opacity: loading || finalizing || organizing ? 0.5 : 1,
                        cursor: (loading || finalizing || organizing) ? 'not-allowed' : 'pointer',
                      }}
                    >
                      Organize Articles
                    </button>
                  </>
                )}
              </div>
            </div>
          )}
        </StatusCard>
      </div>

      {/* Finalize Confirmation Modal */}
      {finalizeConfirm && (
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
            maxWidth: 450,
            width: '90%',
            border: '1px solid var(--border-color)',
          }}>
            <h3 style={{
              fontSize: '1.1rem',
              fontWeight: 600,
              marginBottom: 12,
              color: 'var(--text-primary)',
            }}>
              Finalize Images
            </h3>
            <p style={{
              color: 'var(--text-secondary)',
              fontSize: '0.9rem',
              marginBottom: 20,
              lineHeight: 1.5,
            }}>
              This will process your image selections:
            </p>
            <ul style={{
              color: 'var(--text-secondary)',
              fontSize: '0.85rem',
              marginBottom: 20,
              paddingLeft: 20,
              lineHeight: 1.6,
            }}>
              <li>Selected images will be renamed to their canonical names (removing _1, _2, etc.)</li>
              <li>Non-selected image variants will be deleted</li>
              <li>Groups without selections will be left unchanged</li>
            </ul>
            <div style={{
              display: 'flex',
              gap: 12,
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setFinalizeConfirm(false)}
                disabled={finalizing}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--bg-secondary)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 6,
                  color: 'var(--text-primary)',
                  cursor: finalizing ? 'not-allowed' : 'pointer',
                  opacity: finalizing ? 0.5 : 1,
                }}
              >
                Cancel
              </button>
              <button
                onClick={handleFinalize}
                disabled={finalizing}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--accent-green)',
                  border: 'none',
                  borderRadius: 6,
                  color: 'white',
                  cursor: finalizing ? 'not-allowed' : 'pointer',
                  opacity: finalizing ? 0.5 : 1,
                }}
              >
                {finalizing ? 'Finalizing...' : 'Finalize'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Finalize Result Modal */}
      {finalizeResult && (
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
            maxWidth: 500,
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
              Finalization Complete
            </h3>
            
            {finalizeResult.renamed?.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <h4 style={{
                  fontSize: '0.9rem',
                  fontWeight: 500,
                  color: 'var(--accent-blue)',
                  marginBottom: 8,
                }}>
                  Renamed ({finalizeResult.renamed.length})
                </h4>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  padding: 12,
                  fontSize: '0.8rem',
                  fontFamily: 'JetBrains Mono, monospace',
                  color: 'var(--text-secondary)',
                  maxHeight: 150,
                  overflow: 'auto',
                }}>
                  {finalizeResult.renamed.map((item, i) => (
                    <div key={i}>{item}</div>
                  ))}
                </div>
              </div>
            )}
            
            {finalizeResult.converted?.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <h4 style={{
                  fontSize: '0.9rem',
                  fontWeight: 500,
                  color: 'var(--accent-green)',
                  marginBottom: 8,
                }}>
                  Converted to AVIF ({finalizeResult.converted.length})
                </h4>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  padding: 12,
                  fontSize: '0.8rem',
                  fontFamily: 'JetBrains Mono, monospace',
                  color: 'var(--text-secondary)',
                  maxHeight: 150,
                  overflow: 'auto',
                }}>
                  {finalizeResult.converted.map((item, i) => (
                    <div key={i}>{item}</div>
                  ))}
                </div>
              </div>
            )}
            
            {finalizeResult.deleted?.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <h4 style={{
                  fontSize: '0.9rem',
                  fontWeight: 500,
                  color: 'var(--accent-red)',
                  marginBottom: 8,
                }}>
                  Deleted ({finalizeResult.deleted.length})
                </h4>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  padding: 12,
                  fontSize: '0.8rem',
                  fontFamily: 'JetBrains Mono, monospace',
                  color: 'var(--text-secondary)',
                  maxHeight: 150,
                  overflow: 'auto',
                }}>
                  {finalizeResult.deleted.map((item, i) => (
                    <div key={i}>{item}</div>
                  ))}
                </div>
              </div>
            )}
            
            {finalizeResult.errors?.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <h4 style={{
                  fontSize: '0.9rem',
                  fontWeight: 500,
                  color: 'var(--accent-yellow)',
                  marginBottom: 8,
                }}>
                  Errors ({finalizeResult.errors.length})
                </h4>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  padding: 12,
                  fontSize: '0.8rem',
                  fontFamily: 'JetBrains Mono, monospace',
                  color: 'var(--accent-red)',
                  maxHeight: 100,
                  overflow: 'auto',
                }}>
                  {finalizeResult.errors.map((item, i) => (
                    <div key={i}>{item}</div>
                  ))}
                </div>
              </div>
            )}
            
            {finalizeResult.renamed?.length === 0 && finalizeResult.deleted?.length === 0 && finalizeResult.converted?.length === 0 && (
              <p style={{
                color: 'var(--text-secondary)',
                fontSize: '0.9rem',
                marginBottom: 16,
              }}>
                No changes were made. Make sure to select images on the Images page before finalizing.
              </p>
            )}
            
            <div style={{
              display: 'flex',
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setFinalizeResult(null)}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--accent-blue)',
                  border: 'none',
                  borderRadius: 6,
                  color: 'white',
                  cursor: 'pointer',
                }}
              >
                Close
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Organize Confirmation Modal */}
      {organizeConfirm && (
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
            maxWidth: 500,
            width: '90%',
            border: '1px solid var(--border-color)',
          }}>
            <h3 style={{
              fontSize: '1.1rem',
              fontWeight: 600,
              marginBottom: 12,
              color: 'var(--text-primary)',
            }}>
              Organize Articles
            </h3>
            <p style={{
              color: 'var(--text-secondary)',
              fontSize: '0.9rem',
              marginBottom: 20,
              lineHeight: 1.5,
            }}>
              This will reorganize articles from _incoming into the Compendium structure:
            </p>
            <ul style={{
              color: 'var(--text-secondary)',
              fontSize: '0.85rem',
              marginBottom: 20,
              paddingLeft: 20,
              lineHeight: 1.6,
            }}>
              <li>Move articles to their domain/category/topic folders</li>
              <li>Move images to _img/ folders</li>
              <li>Update image references in markdown to .avif format</li>
              <li>Create/update index.md files at each level</li>
              <li>Clean up _incoming, _debug, and _config folders</li>
            </ul>
            <p style={{
              color: 'var(--accent-yellow)',
              fontSize: '0.85rem',
              marginBottom: 20,
            }}>
              Make sure you have finalized images before organizing!
            </p>
            <div style={{
              display: 'flex',
              gap: 12,
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setOrganizeConfirm(false)}
                disabled={organizing}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--bg-secondary)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 6,
                  color: 'var(--text-primary)',
                  cursor: organizing ? 'not-allowed' : 'pointer',
                  opacity: organizing ? 0.5 : 1,
                }}
              >
                Cancel
              </button>
              <button
                onClick={handleOrganize}
                disabled={organizing}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--accent-yellow)',
                  border: 'none',
                  borderRadius: 6,
                  color: '#000',
                  cursor: organizing ? 'not-allowed' : 'pointer',
                  opacity: organizing ? 0.5 : 1,
                }}
              >
                {organizing ? 'Organizing...' : 'Organize'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Organize Result Modal */}
      {organizeResult && (
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
            maxWidth: 600,
            width: '90%',
            maxHeight: '80vh',
            overflow: 'auto',
            border: '1px solid var(--border-color)',
          }}>
            <h3 style={{
              fontSize: '1.1rem',
              fontWeight: 600,
              marginBottom: 16,
              color: organizeResult.success ? 'var(--accent-green)' : 'var(--accent-red)',
            }}>
              {organizeResult.success ? 'Organization Complete' : 'Organization Failed'}
            </h3>
            
            <p style={{
              color: 'var(--text-secondary)',
              fontSize: '0.9rem',
              marginBottom: 16,
            }}>
              {organizeResult.message}
            </p>
            
            {organizeResult.articlesMoved?.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <h4 style={{
                  fontSize: '0.9rem',
                  fontWeight: 500,
                  color: 'var(--accent-green)',
                  marginBottom: 8,
                }}>
                  Articles Moved ({organizeResult.articlesMoved.length})
                </h4>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  padding: 12,
                  fontSize: '0.8rem',
                  fontFamily: 'JetBrains Mono, monospace',
                  color: 'var(--text-secondary)',
                  maxHeight: 150,
                  overflow: 'auto',
                }}>
                  {organizeResult.articlesMoved.map((item, i) => (
                    <div key={i}>{item}</div>
                  ))}
                </div>
              </div>
            )}
            
            {organizeResult.imagesMoved?.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <h4 style={{
                  fontSize: '0.9rem',
                  fontWeight: 500,
                  color: 'var(--accent-blue)',
                  marginBottom: 8,
                }}>
                  Images Moved ({organizeResult.imagesMoved.length})
                </h4>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  padding: 12,
                  fontSize: '0.8rem',
                  fontFamily: 'JetBrains Mono, monospace',
                  color: 'var(--text-secondary)',
                  maxHeight: 150,
                  overflow: 'auto',
                }}>
                  {organizeResult.imagesMoved.map((item, i) => (
                    <div key={i}>{item}</div>
                  ))}
                </div>
              </div>
            )}
            
            {organizeResult.errors?.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <h4 style={{
                  fontSize: '0.9rem',
                  fontWeight: 500,
                  color: 'var(--accent-red)',
                  marginBottom: 8,
                }}>
                  Errors ({organizeResult.errors.length})
                </h4>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  padding: 12,
                  fontSize: '0.8rem',
                  fontFamily: 'JetBrains Mono, monospace',
                  color: 'var(--accent-red)',
                  maxHeight: 100,
                  overflow: 'auto',
                }}>
                  {organizeResult.errors.map((item, i) => (
                    <div key={i}>{item}</div>
                  ))}
                </div>
              </div>
            )}
            
            <div style={{
              display: 'flex',
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setOrganizeResult(null)}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--accent-blue)',
                  border: 'none',
                  borderRadius: 6,
                  color: 'white',
                  cursor: 'pointer',
                }}
              >
                Close
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete Branch Confirmation Modal */}
      {deleteConfirm && (
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
            maxWidth: 450,
            width: '90%',
            border: '1px solid var(--border-color)',
          }}>
            <h3 style={{
              fontSize: '1.1rem',
              fontWeight: 600,
              marginBottom: 12,
              color: 'var(--accent-red)',
            }}>
              Delete Branch
            </h3>
            <p style={{
              color: 'var(--text-secondary)',
              fontSize: '0.9rem',
              marginBottom: 16,
              lineHeight: 1.5,
            }}>
              Are you sure you want to delete branch <code style={{
                backgroundColor: 'var(--bg-secondary)',
                padding: '2px 6px',
                borderRadius: 4,
                fontSize: '0.85rem',
              }}>{branch?.name}</code>?
            </p>
            <ul style={{
              color: 'var(--text-secondary)',
              fontSize: '0.85rem',
              marginBottom: 20,
              paddingLeft: 20,
              lineHeight: 1.6,
            }}>
              <li>All checked article checkboxes in the associated issue will be unchecked</li>
              <li>Local and remote branches will be deleted</li>
              <li>You will be switched to the main branch</li>
            </ul>
            <div style={{
              display: 'flex',
              gap: 12,
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setDeleteConfirm(false)}
                disabled={deleting}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--bg-secondary)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 6,
                  color: 'var(--text-primary)',
                  cursor: deleting ? 'not-allowed' : 'pointer',
                  opacity: deleting ? 0.5 : 1,
                }}
              >
                Cancel
              </button>
              <button
                onClick={handleDeleteBranch}
                disabled={deleting}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--accent-red)',
                  border: 'none',
                  borderRadius: 6,
                  color: 'white',
                  cursor: deleting ? 'not-allowed' : 'pointer',
                  opacity: deleting ? 0.5 : 1,
                }}
              >
                {deleting ? 'Deleting...' : 'Delete Branch'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete Result Modal */}
      {deleteResult && (
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
            maxWidth: 500,
            width: '90%',
            maxHeight: '80vh',
            overflow: 'auto',
            border: '1px solid var(--border-color)',
          }}>
            <h3 style={{
              fontSize: '1.1rem',
              fontWeight: 600,
              marginBottom: 16,
              color: deleteResult.success ? 'var(--accent-green)' : 'var(--accent-red)',
            }}>
              {deleteResult.success ? 'Branch Deleted' : 'Delete Failed'}
            </h3>
            
            <p style={{
              color: 'var(--text-secondary)',
              fontSize: '0.9rem',
              marginBottom: 16,
            }}>
              {deleteResult.message}
            </p>
            
            {deleteResult.revertedArticles?.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <h4 style={{
                  fontSize: '0.9rem',
                  fontWeight: 500,
                  color: 'var(--accent-yellow)',
                  marginBottom: 8,
                }}>
                  Unchecked Articles ({deleteResult.revertedArticles.length})
                </h4>
                <div style={{
                  backgroundColor: 'var(--bg-secondary)',
                  borderRadius: 6,
                  padding: 12,
                  fontSize: '0.8rem',
                  fontFamily: 'JetBrains Mono, monospace',
                  color: 'var(--text-secondary)',
                  maxHeight: 150,
                  overflow: 'auto',
                }}>
                  {deleteResult.revertedArticles.map((item, i) => (
                    <div key={i}>☐ {item}</div>
                  ))}
                </div>
              </div>
            )}
            
            <div style={{
              display: 'flex',
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setDeleteResult(null)}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--accent-blue)',
                  border: 'none',
                  borderRadius: 6,
                  color: 'white',
                  cursor: 'pointer',
                }}
              >
                Close
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Switch Branch Modal */}
      {switchModal && (
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
            maxWidth: 500,
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
              Select a branch to switch to. Any uncommitted changes will be discarded.
            </p>
            
            <div style={{
              maxHeight: 300,
              overflow: 'auto',
              marginBottom: 16,
            }}>
              {branches.length === 0 ? (
                <p style={{ color: 'var(--text-muted)', fontSize: '0.85rem' }}>
                  Loading branches...
                </p>
              ) : (
                branches.map((b) => (
                  <button
                    key={b.name}
                    onClick={() => handleSwitchBranch(b.name)}
                    disabled={switching || b.name === branch?.name}
                    style={{
                      width: '100%',
                      padding: '10px 14px',
                      marginBottom: 8,
                      fontSize: '0.85rem',
                      backgroundColor: b.name === branch?.name ? 'var(--accent-blue)' : 'var(--bg-secondary)',
                      border: '1px solid var(--border-color)',
                      borderRadius: 8,
                      color: b.name === branch?.name ? '#fff' : 'var(--text-primary)',
                      cursor: (switching || b.name === branch?.name) ? 'not-allowed' : 'pointer',
                      textAlign: 'left',
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      opacity: switching ? 0.5 : 1,
                    }}
                  >
                    <span style={{ 
                      fontFamily: 'JetBrains Mono, monospace',
                      fontSize: '0.8rem',
                    }}>
                      {b.name}
                    </span>
                    {b.isResearch && (
                      <span style={{
                        fontSize: '0.7rem',
                        backgroundColor: 'var(--accent-purple)',
                        color: '#fff',
                        padding: '2px 6px',
                        borderRadius: 4,
                      }}>
                        #{b.issueNumber}
                      </span>
                    )}
                  </button>
                ))
              )}
            </div>
            
            <div style={{
              display: 'flex',
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setSwitchModal(false)}
                disabled={switching}
                style={{
                  padding: '8px 16px',
                  fontSize: '0.85rem',
                  backgroundColor: 'var(--bg-secondary)',
                  border: '1px solid var(--border-color)',
                  borderRadius: 6,
                  color: 'var(--text-primary)',
                  cursor: switching ? 'not-allowed' : 'pointer',
                  opacity: switching ? 0.5 : 1,
                }}
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Create Branch Modal */}
      {createModal && (
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
            maxWidth: 600,
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
              Create New Branch
            </h3>
            
            <p style={{
              color: 'var(--text-secondary)',
              fontSize: '0.85rem',
              marginBottom: 16,
            }}>
              Select a Research Topic issue to create a new branch for.
            </p>
            
            <div style={{
              maxHeight: 400,
              overflow: 'auto',
              marginBottom: 16,
            }}>
              {topicIssues.length === 0 ? (
                <p style={{ color: 'var(--text-muted)', fontSize: '0.85rem' }}>
                  Loading topic issues...
                </p>
              ) : (
                topicIssues.map((issue) => (
                  <button
                    key={issue.number}
                    onClick={() => handleCreateBranch(issue.number)}
                    disabled={creating}
                    style={{
                      width: '100%',
                      padding: '12px 14px',
                      marginBottom: 8,
                      fontSize: '0.85rem',
                      backgroundColor: 'var(--bg-secondary)',
                      border: '1px solid var(--border-color)',
                      borderRadius: 8,
                      color: 'var(--text-primary)',
                      cursor: creating ? 'not-allowed' : 'pointer',
                      textAlign: 'left',
                      opacity: creating ? 0.5 : 1,
                    }}
                  >
                    <div style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 10,
                      marginBottom: 4,
                    }}>
                      <span style={{
                        fontSize: '0.75rem',
                        backgroundColor: 'var(--accent-green)',
                        color: '#fff',
                        padding: '2px 8px',
                        borderRadius: 4,
                        fontWeight: 500,
                      }}>
                        #{issue.number}
                      </span>
                      <span style={{ fontWeight: 500 }}>
                        {issue.title}
                      </span>
                    </div>
                    {issue.body && (
                      <div style={{
                        fontSize: '0.75rem',
                        color: 'var(--text-muted)',
                        whiteSpace: 'nowrap',
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        marginTop: 4,
                      }}>
                        {issue.body.split('\n')[0]}
                      </div>
                    )}
                  </button>
                ))
              )}
            </div>
            
            <div style={{
              display: 'flex',
              justifyContent: 'flex-end',
            }}>
              <button
                onClick={() => setCreateModal(false)}
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

function ConfigField({ label, value, onChange, description }) {
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        marginBottom: 4,
      }}>
        <label style={{ 
          fontSize: '0.85rem', 
          color: 'var(--text-secondary)',
        }}>
          {label}
        </label>
        <input
          type="number"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          style={{
            width: 80,
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
      {description && (
        <p style={{ 
          fontSize: '0.75rem', 
          color: 'var(--text-muted)',
        }}>
          {description}
        </p>
      )}
    </div>
  );
}

// Renders issue body with checkboxes displayed as read-only
function IssueBodyRenderer({ body }) {
  if (!body) return null;

  // Parse markdown-style checkboxes and render them
  const lines = body.split('\n');
  
  return (
    <div>
      {lines.map((line, i) => {
        const trimmed = line.trim();
        
        // Check for checked checkbox: - [x] or - [X]
        if (trimmed.startsWith('- [x] ') || trimmed.startsWith('- [X] ')) {
          const content = trimmed.substring(6);
          return (
            <div key={i} style={{ 
              display: 'flex', 
              alignItems: 'flex-start', 
              gap: 8,
              marginBottom: 4,
              color: 'var(--accent-green)',
            }}>
              <span style={{ fontSize: '1rem' }}>☑</span>
              <span style={{ textDecoration: 'line-through', opacity: 0.8 }}>{content}</span>
            </div>
          );
        }
        
        // Check for unchecked checkbox: - [ ]
        if (trimmed.startsWith('- [ ] ')) {
          const content = trimmed.substring(6);
          return (
            <div key={i} style={{ 
              display: 'flex', 
              alignItems: 'flex-start', 
              gap: 8,
              marginBottom: 4,
              color: 'var(--text-secondary)',
            }}>
              <span style={{ fontSize: '1rem' }}>☐</span>
              <span>{content}</span>
            </div>
          );
        }
        
        // Regular text or other formatting
        if (trimmed === '') {
          return <div key={i} style={{ height: 8 }} />;
        }
        
        // Headers (### style)
        if (trimmed.startsWith('### ')) {
          return (
            <h4 key={i} style={{ 
              fontSize: '0.9rem', 
              fontWeight: 600, 
              margin: '12px 0 8px 0',
              color: 'var(--text-primary)',
            }}>
              {trimmed.substring(4)}
            </h4>
          );
        }
        
        if (trimmed.startsWith('## ')) {
          return (
            <h3 key={i} style={{ 
              fontSize: '0.95rem', 
              fontWeight: 600, 
              margin: '14px 0 8px 0',
              color: 'var(--text-primary)',
            }}>
              {trimmed.substring(3)}
            </h3>
          );
        }
        
        return (
          <div key={i} style={{ 
            marginBottom: 4,
            color: 'var(--text-secondary)',
          }}>
            {line}
          </div>
        );
      })}
    </div>
  );
}
