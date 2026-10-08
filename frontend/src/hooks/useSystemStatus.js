import { useEffect, useState, useRef } from "react";
import { api } from "../api/client";

const MAX_POINTS = 40;
const POLL_MS = 2000;

export function useSystemStatus() {
  const [status, setStatus] = useState(null);
  const [history, setHistory] = useState([]);
  const [error, setError] = useState(null);
  const tick = useRef(0);

  useEffect(() => {
    let cancelled = false;

    async function poll() {
      try {
        const data = await api.systemStatus();
        if (cancelled) return;
        setStatus(data);
        setError(null);
        tick.current += 1;
        setHistory((prev) =>
          [...prev, { t: tick.current, ...data }].slice(-MAX_POINTS)
        );
      } catch (e) {
        if (!cancelled) setError(e.message);
      }
    }

    poll();
    const id = setInterval(poll, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  return { status, history, error };
}
