import type { UpdateNotification, WebSocketMessage } from '@/types';

type MessageHandler = (notification: UpdateNotification) => void;
type ConnectionHandler = () => void;

interface WebSocketConfig {
  url?: string;
  reconnectAttempts?: number;
  reconnectDelay?: number;
  heartbeatInterval?: number;
}

const DEFAULT_CONFIG: Required<WebSocketConfig> = {
  url: import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws',
  reconnectAttempts: 5,
  reconnectDelay: 1000,
  heartbeatInterval: 30000,
};

class WebSocketService {
  private ws: WebSocket | null = null;
  private config: Required<WebSocketConfig>;
  private reconnectCount = 0;
  private heartbeatTimer: number | null = null;
  private reconnectTimer: number | null = null;
  private messageHandlers: Set<MessageHandler> = new Set();
  private connectHandlers: Set<ConnectionHandler> = new Set();
  private disconnectHandlers: Set<ConnectionHandler> = new Set();
  private subscribedTopics: Set<string> = new Set();
  private isManualClose = false;

  constructor(config: WebSocketConfig = {}) {
    this.config = { ...DEFAULT_CONFIG, ...config };
  }

  connect(): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      console.warn('[WebSocket] Already connected');
      return;
    }

    this.isManualClose = false;

    try {
      this.ws = new WebSocket(this.config.url);

      this.ws.onopen = this.handleOpen.bind(this);
      this.ws.onmessage = this.handleMessage.bind(this);
      this.ws.onclose = this.handleClose.bind(this);
      this.ws.onerror = this.handleError.bind(this);
    } catch (error) {
      console.error('[WebSocket] Connection error:', error);
      this.attemptReconnect();
    }
  }

  disconnect(): void {
    this.isManualClose = true;
    this.cleanup();

    if (this.ws) {
      this.ws.close(1000, 'Client disconnect');
      this.ws = null;
    }
  }

  private handleOpen(): void {
    console.log('[WebSocket] Connected');
    this.reconnectCount = 0;
    this.startHeartbeat();

    // Re-subscribe to topics
    if (this.subscribedTopics.size > 0) {
      this.subscribe(Array.from(this.subscribedTopics));
    }

    // Notify handlers
    this.connectHandlers.forEach((handler) => handler());
  }

  private handleMessage(event: MessageEvent): void {
    try {
      const data: WebSocketMessage = JSON.parse(event.data);

      if (data.type === 'pong') {
        // Heartbeat response
        return;
      }

      const notification = this.parseNotification(data);
      if (notification) {
        this.messageHandlers.forEach((handler) => handler(notification));
      }
    } catch (error) {
      console.error('[WebSocket] Message parse error:', error);
    }
  }

  private handleClose(event: CloseEvent): void {
    console.log('[WebSocket] Disconnected:', event.code, event.reason);
    this.cleanup();

    // Notify handlers
    this.disconnectHandlers.forEach((handler) => handler());

    // Attempt reconnect if not manual close
    if (!this.isManualClose && event.code !== 1000) {
      this.attemptReconnect();
    }
  }

  private handleError(event: Event): void {
    console.error('[WebSocket] Error:', event);
  }

  private parseNotification(data: WebSocketMessage): UpdateNotification | null {
    switch (data.type) {
      case 'document_updated':
        return {
          type: 'document_updated',
          source: (data.payload as { source?: string })?.source,
          message: `New content available from ${(data.payload as { source?: string })?.source || 'unknown source'}`,
          timestamp: new Date(),
        };

      case 'cache_invalidated':
        return {
          type: 'cache_invalidated',
          message: 'Cache has been refreshed',
          timestamp: new Date(),
        };

      case 'system':
        return {
          type: 'system',
          message: (data.payload as { message?: string })?.message || 'System notification',
          timestamp: new Date(),
        };

      default:
        console.warn('[WebSocket] Unknown message type:', data.type);
        return null;
    }
  }

  private attemptReconnect(): void {
    if (this.reconnectCount >= this.config.reconnectAttempts) {
      console.error('[WebSocket] Max reconnection attempts reached');
      return;
    }

    const delay = this.config.reconnectDelay * Math.pow(2, this.reconnectCount);
    console.log(`[WebSocket] Reconnecting in ${delay}ms (attempt ${this.reconnectCount + 1})`);

    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectCount++;
      this.connect();
    }, delay);
  }

  private startHeartbeat(): void {
    this.heartbeatTimer = window.setInterval(() => {
      if (this.ws?.readyState === WebSocket.OPEN) {
        this.ws.send(JSON.stringify({ type: 'ping' }));
      }
    }, this.config.heartbeatInterval);
  }

  private cleanup(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }

    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  subscribe(topics: string[]): void {
    topics.forEach((topic) => this.subscribedTopics.add(topic));

    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(
        JSON.stringify({
          type: 'subscribe',
          topics,
        })
      );
    }
  }

  unsubscribe(topics: string[]): void {
    topics.forEach((topic) => this.subscribedTopics.delete(topic));

    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(
        JSON.stringify({
          type: 'unsubscribe',
          topics,
        })
      );
    }
  }

  onMessage(handler: MessageHandler): () => void {
    this.messageHandlers.add(handler);
    return () => this.messageHandlers.delete(handler);
  }

  onConnect(handler: ConnectionHandler): () => void {
    this.connectHandlers.add(handler);
    return () => this.connectHandlers.delete(handler);
  }

  onDisconnect(handler: ConnectionHandler): () => void {
    this.disconnectHandlers.add(handler);
    return () => this.disconnectHandlers.delete(handler);
  }

  isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  getState(): number | null {
    return this.ws?.readyState ?? null;
  }
}

// Singleton instance
export const webSocketService = new WebSocketService();

export default WebSocketService;
