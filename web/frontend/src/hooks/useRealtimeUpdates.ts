import { useEffect, useState, useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import toast from 'react-hot-toast';
import { webSocketService } from '@/services/websocket';
import type { UpdateNotification } from '@/types';
import { chatKeys } from './useChat';

interface UseRealtimeUpdatesOptions {
  topics?: string[];
  enabled?: boolean;
  onDocumentUpdate?: (notification: UpdateNotification) => void;
}

export function useRealtimeUpdates(options: UseRealtimeUpdatesOptions = {}) {
  const {
    topics = ['bnm', 'aaoifi'],
    enabled = true,
    onDocumentUpdate,
  } = options;

  const queryClient = useQueryClient();
  const [isConnected, setIsConnected] = useState(false);

  const handleMessage = useCallback(
    (notification: UpdateNotification) => {
      switch (notification.type) {
        case 'document_updated':
          // Show toast notification
          toast.success(notification.message, {
            icon: '📄',
            duration: 5000,
          });

          // Invalidate relevant queries
          queryClient.invalidateQueries({ queryKey: ['documents'] });

          // Call custom handler if provided
          onDocumentUpdate?.(notification);
          break;

        case 'cache_invalidated':
          // Silently invalidate caches
          queryClient.invalidateQueries({ queryKey: chatKeys.all });
          break;

        case 'system':
          // Show system notification
          toast(notification.message, {
            icon: 'ℹ️',
          });
          break;
      }
    },
    [queryClient, onDocumentUpdate]
  );

  useEffect(() => {
    if (!enabled) return;

    // Connect to WebSocket
    webSocketService.connect();

    // Set up event handlers
    const unsubMessage = webSocketService.onMessage(handleMessage);
    const unsubConnect = webSocketService.onConnect(() => {
      setIsConnected(true);
    });
    const unsubDisconnect = webSocketService.onDisconnect(() => {
      setIsConnected(false);
    });

    // Subscribe to topics
    webSocketService.subscribe(topics);

    // Cleanup - only unsubscribe handlers and topics, don't disconnect
    // The WebSocket connection should persist across component remounts
    return () => {
      unsubMessage();
      unsubConnect();
      unsubDisconnect();
      webSocketService.unsubscribe(topics);
      // Note: We don't disconnect here to avoid issues with React Strict Mode
      // The connection will persist for the app lifetime
    };
  }, [enabled, topics, handleMessage]);

  return {
    isConnected,
    subscribe: webSocketService.subscribe.bind(webSocketService),
    unsubscribe: webSocketService.unsubscribe.bind(webSocketService),
  };
}

// Hook for displaying connection status
export function useWebSocketStatus() {
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>(
    'disconnected'
  );

  useEffect(() => {
    const checkStatus = () => {
      const state = webSocketService.getState();
      switch (state) {
        case WebSocket.CONNECTING:
          setStatus('connecting');
          break;
        case WebSocket.OPEN:
          setStatus('connected');
          break;
        default:
          setStatus('disconnected');
      }
    };

    // Check initial status
    checkStatus();

    // Set up listeners
    const unsubConnect = webSocketService.onConnect(() => {
      setStatus('connected');
    });
    const unsubDisconnect = webSocketService.onDisconnect(() => {
      setStatus('disconnected');
    });

    return () => {
      unsubConnect();
      unsubDisconnect();
    };
  }, []);

  return status;
}
