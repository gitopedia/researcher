// API client for communicating with the researcher backend

const API_BASE = process.env.NEXT_PUBLIC_API_URL || '';

async function fetchAPI(endpoint, options = {}) {
  const url = `${API_BASE}/api${endpoint}`;
  
  const response = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
  });

  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: 'Unknown error' }));
    throw new Error(error.error || `HTTP ${response.status}`);
  }

  return response.json();
}

// Status endpoints
export const getStatus = () => fetchAPI('/status');
export const getServicesStatus = () => fetchAPI('/services/status');

// Researcher control
export const getResearcherStatus = () => fetchAPI('/researcher/status');
export const startResearcher = (config) => fetchAPI('/researcher/start', {
  method: 'POST',
  body: JSON.stringify(config),
});
export const pauseResearcher = () => fetchAPI('/researcher/pause', { method: 'POST' });
export const resumeResearcher = () => fetchAPI('/researcher/resume', { method: 'POST' });
export const stopResearcher = () => fetchAPI('/researcher/stop', { method: 'POST' });
export const forceStopResearcher = () => fetchAPI('/researcher/stop', {
  method: 'POST',
  body: JSON.stringify({ force: true }),
});

// Git operations
export const getGitBranch = () => fetchAPI('/git/branch');
export const cleanBranch = (options) => fetchAPI('/git/clean', {
  method: 'POST',
  body: JSON.stringify(options),
});

// Configuration
export const getConfig = () => fetchAPI('/config');
export const updateConfig = (config) => fetchAPI('/config', {
  method: 'PUT',
  body: JSON.stringify(config),
});

// Topics
export const listTopics = () => fetchAPI('/topics');
export const getTopic = (slug) => fetchAPI(`/topics/${slug}`);

// Images
export const listImages = () => fetchAPI('/images');
export const getImageUrl = (path) => `${API_BASE}/api/images/${path}`;
export const deleteImageGroup = (type, name) => fetchAPI(`/images/group/${type}/${encodeURIComponent(name)}`, { 
  method: 'DELETE' 
});
export const deleteImage = (path) => fetchAPI('/images/single', { 
  method: 'DELETE', 
  body: JSON.stringify({ path }) 
});

// Image selections
export const getImageSelections = () => fetchAPI('/images/selections');
export const updateImageSelection = (type, name, filename) => fetchAPI('/images/selections', {
  method: 'PUT',
  body: JSON.stringify({ type, name, filename }),
});

// Finalization
export const finalizeImages = () => fetchAPI('/finalize', { method: 'POST' });

// Organization
export const organizeArticles = () => fetchAPI('/organize', { method: 'POST' });

// Logs
export const getResearcherLogs = (lines = 300) => fetchAPI(`/logs/researcher?lines=${encodeURIComponent(lines)}`);
export const getLogSources = () => fetchAPI('/logs/sources');
export const getLogsBySource = (source) => fetchAPI(`/logs/${encodeURIComponent(source)}`);

// Workers
export const listWorkers = () => fetchAPI('/workers');
export const getWorker = (id) => fetchAPI(`/workers/${encodeURIComponent(id)}`);
export const createWorker = (config) => fetchAPI('/workers', {
  method: 'POST',
  body: JSON.stringify(config),
});
export const deleteWorker = (id) => fetchAPI(`/workers/${encodeURIComponent(id)}`, { method: 'DELETE' });
export const startWorker = (id) => fetchAPI(`/workers/${encodeURIComponent(id)}/start`, { method: 'POST' });
export const stopWorker = (id) => fetchAPI(`/workers/${encodeURIComponent(id)}/stop`, { method: 'POST' });
export const pauseWorker = (id) => fetchAPI(`/workers/${encodeURIComponent(id)}/pause`, { method: 'POST' });
export const resumeWorker = (id) => fetchAPI(`/workers/${encodeURIComponent(id)}/resume`, { method: 'POST' });
export const configureWorker = (id, config) => fetchAPI(`/workers/${encodeURIComponent(id)}/configure`, {
  method: 'PUT',
  body: JSON.stringify(config),
});

// Queue
export const getQueueStatus = () => fetchAPI('/queue/status');

// GitHub Issues & Branch Management
export const listTopicIssues = () => fetchAPI('/issues/topics');
export const getIssue = (number) => fetchAPI(`/issues/${number}`);
export const getBranchIssue = () => fetchAPI('/branch/issue');
export const listBranches = () => fetchAPI('/branches');
export const deleteBranch = (revertCheckboxes = true) => fetchAPI('/branch/delete', {
  method: 'POST',
  body: JSON.stringify({ revertCheckboxes }),
});
export const switchBranch = (branch) => fetchAPI('/branch/switch', {
  method: 'POST',
  body: JSON.stringify({ branch }),
});
export const createBranch = (issueNumber) => fetchAPI('/branch/create', {
  method: 'POST',
  body: JSON.stringify({ issueNumber }),
});

// WebSocket connection
export function createWebSocket(onMessage) {
  const wsUrl = API_BASE.replace('http', 'ws') || `ws://${window.location.hostname}:3001`;
  const ws = new WebSocket(`${wsUrl}/api/ws`);
  
  ws.onmessage = (event) => {
    try {
      const data = JSON.parse(event.data);
      onMessage(data);
    } catch (e) {
      console.error('WebSocket parse error:', e);
    }
  };
  
  ws.onerror = (error) => {
    console.error('WebSocket error:', error);
  };
  
  return ws;
}
