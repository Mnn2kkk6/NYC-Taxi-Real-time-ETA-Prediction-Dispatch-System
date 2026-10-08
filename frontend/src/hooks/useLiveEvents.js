import { useEffect, useState } from "react";
import { wsURL } from "../api/client";

const MAX_HISTORY = 200;

/**
 * Connects to the backend's /ws broadcast channel. Reconnects automatically
 * with backoff if the connection drops (a backend restart or network blip
 * shouldn't require a page reload). Keeps the last MAX_HISTORY events and
 * exposes connection status so the UI can show it honestly.
 *
 * All connection state lives inside the effect so that unmounting cancels
 * both the socket and any pending reconnect timer — no stray reconnects
 * after the component is gone.
 */
export function useLiveEvents() {
  const [events, setEvents] = useState([]);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    let ws = null;
    let retryTimer = null;
    let retryDelay = 1000;
    let stopped = false;

    function connect() {
      ws = new WebSocket(wsURL());

      ws.onopen = () => {
        setConnected(true);
        retryDelay = 1000;
      };

      ws.onmessage = (msg) => {
        try {
          const event = JSON.parse(msg.data);
          event._receivedAt = Date.now();
          setEvents((prev) => [event, ...prev].slice(0, MAX_HISTORY));
        } catch {
          // ignore malformed frames rather than crashing the UI
        }
      };

      ws.onclose = () => {
        setConnected(false);
        if (stopped) return;
        retryTimer = setTimeout(connect, retryDelay);
        retryDelay = Math.min(retryDelay * 2, 15000);
      };

      ws.onerror = () => ws.close();
    }

    connect();

    return () => {
      stopped = true;
      clearTimeout(retryTimer);
      ws?.close();
    };
  }, []);

  return { events, connected };
}
