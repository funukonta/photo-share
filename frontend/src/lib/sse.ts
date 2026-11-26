import { roomEventsUrl, type Photo, type PublicConfigResponse, type ReactionPayload } from './api';

export type SSEEventType =
  | 'photo_added'
  | 'photo_deleted'
  | 'photo_spotlight'
  | 'reaction'
  | 'config_updated'
  | 'room_deleted';

export interface SSEEventMap {
  photo_added: Photo;
  photo_deleted: { id: string } | Photo;
  photo_spotlight: Photo;
  reaction: ReactionPayload;
  config_updated: PublicConfigResponse;
  room_deleted: { room: string };
}

export interface SSEHandlers {
  onPhotoAdded?: (photo: Photo) => void;
  onPhotoDeleted?: (payload: { id: string } | Photo) => void;
  onPhotoSpotlight?: (photo: Photo) => void;
  onReaction?: (reaction: ReactionPayload) => void;
  onConfigUpdated?: (config: PublicConfigResponse) => void;
  /** The host deleted this event. */
  onRoomDeleted?: () => void;
  onConnectionChange?: (connected: boolean) => void;
}

export interface SSEClient {
  close: () => void;
}

/**
 * Connects to a room's Server-Sent Events stream (`/api/rooms/{room}/events`) with automatic reconnection
 * and typed event listeners matching the Go SSEHub envelope `{ event, data }`.
 */
export function connectSSE(room: string, handlers: SSEHandlers): SSEClient {
  const url = roomEventsUrl(room);
  let eventSource: EventSource | null = null;
  let closed = false;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let retryAttempt = 0;

  function parseEnvelope<T>(raw: string): T | null {
    try {
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed === 'object' && 'data' in parsed) {
        return parsed.data as T;
      }
      return parsed as T;
    } catch {
      return null;
    }
  }

  function connect() {
    if (closed) return;

    try {
      eventSource = new EventSource(url, { withCredentials: true });
    } catch {
      scheduleReconnect();
      return;
    }

    eventSource.onopen = () => {
      retryAttempt = 0;
      handlers.onConnectionChange?.(true);
    };

    eventSource.onerror = () => {
      handlers.onConnectionChange?.(false);
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }
      scheduleReconnect();
    };

    eventSource.addEventListener('photo_added', (evt: MessageEvent) => {
      const data = parseEnvelope<Photo>(evt.data);
      if (data) handlers.onPhotoAdded?.(data);
    });

    eventSource.addEventListener('photo_deleted', (evt: MessageEvent) => {
      const data = parseEnvelope<{ id: string } | Photo>(evt.data);
      if (data) handlers.onPhotoDeleted?.(data);
    });

    eventSource.addEventListener('photo_spotlight', (evt: MessageEvent) => {
      const data = parseEnvelope<Photo>(evt.data);
      if (data) handlers.onPhotoSpotlight?.(data);
    });

    eventSource.addEventListener('reaction', (evt: MessageEvent) => {
      const data = parseEnvelope<ReactionPayload>(evt.data);
      if (data) handlers.onReaction?.(data);
    });

    eventSource.addEventListener('config_updated', (evt: MessageEvent) => {
      const data = parseEnvelope<PublicConfigResponse>(evt.data);
      if (data) handlers.onConfigUpdated?.(data);
    });

    eventSource.addEventListener('room_deleted', () => {
      handlers.onRoomDeleted?.();
    });
  }

  function scheduleReconnect() {
    if (closed || reconnectTimer) return;
    const delay = Math.min(1000 * Math.pow(1.5, retryAttempt), 8000);
    retryAttempt += 1;
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connect();
    }, delay);
  }

  connect();

  return {
    close() {
      closed = true;
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }
      handlers.onConnectionChange?.(false);
    },
  };
}
