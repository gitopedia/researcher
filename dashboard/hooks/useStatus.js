import { useState, useEffect, useCallback, useRef } from 'react';
import { getStatus, getResearcherStatus, createWebSocket } from '../lib/api';

export function useStatus() {
  const [status, setStatus] = useState(null);
  const [researcherStatus, setResearcherStatus] = useState(null);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState(null);
  const wsRef = useRef(null);
  const reconnectTimeoutRef = useRef(null);

  const connect = useCallback(() => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      return;
    }

    try {
      wsRef.current = createWebSocket((data) => {
        if (data.type === 'status') {
          setStatus(data.payload);
        } else if (data.type === 'researcher') {
          setResearcherStatus(data.payload);
        }
      });

      wsRef.current.onopen = () => {
        setConnected(true);
        setError(null);
      };

      wsRef.current.onclose = () => {
        setConnected(false);
        // Attempt to reconnect after 3 seconds
        reconnectTimeoutRef.current = setTimeout(connect, 3000);
      };

      wsRef.current.onerror = () => {
        setError('WebSocket connection failed');
        setConnected(false);
      };
    } catch (e) {
      setError(e.message);
      setConnected(false);
      // Attempt to reconnect after 3 seconds
      reconnectTimeoutRef.current = setTimeout(connect, 3000);
    }
  }, []);

  // Initial data fetch (fallback if WebSocket isn't available)
  const refresh = useCallback(async () => {
    try {
      const [statusData, researcherData] = await Promise.all([
        getStatus(),
        getResearcherStatus(),
      ]);
      setStatus(statusData);
      setResearcherStatus(researcherData);
      setError(null);
    } catch (e) {
      setError(e.message);
    }
  }, []);

  useEffect(() => {
    // Initial fetch
    refresh();
    
    // Connect WebSocket
    connect();

    return () => {
      if (wsRef.current) {
        wsRef.current.close();
      }
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
    };
  }, [connect, refresh]);

  return {
    status,
    researcherStatus,
    connected,
    error,
    refresh,
  };
}
