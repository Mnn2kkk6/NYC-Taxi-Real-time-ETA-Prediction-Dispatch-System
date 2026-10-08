const API_BASE = import.meta.env.VITE_API_BASE_URL || "http://localhost:8080";

async function request(path, options = {}) {
  let res;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      headers: { "Content-Type": "application/json" },
      ...options,
    });
  } catch {
    // fetch only rejects on network-level failure (server down, wrong URL, blocked)
    throw new Error(`Can't reach the backend at ${API_BASE}. Is the Go server running?`);
  }
  const body = await res.json().catch(() => null);
  if (!res.ok) {
    const message = body?.detail || body?.error || `Request failed (${res.status})`;
    throw new Error(message);
  }
  return body;
}

export const api = {
  health: () => request("/health"),

  predict: (payload) =>
    request("/api/v1/predict", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  createTrip: (payload) =>
    request("/api/v1/trips", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  listTrips: (params = {}) => {
    const qs = new URLSearchParams(params).toString();
    return request(`/api/v1/trips${qs ? `?${qs}` : ""}`);
  },

  getTrip: (id) => request(`/api/v1/trips/${id}`),

  listDrivers: () => request("/api/v1/drivers"),

  systemStatus: () => request("/api/v1/system/status"),
};

export function wsURL() {
  const url = new URL(API_BASE);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.pathname = "/ws";
  return url.toString();
}

export { API_BASE };