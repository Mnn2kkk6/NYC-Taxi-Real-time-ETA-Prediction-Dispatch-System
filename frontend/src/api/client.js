// In production the app is served by Nginx and API calls stay same-origin.
// In Vite development mode, default to the Go server running on localhost:8080.
const API_BASE =
  import.meta.env.VITE_API_BASE_URL ??
  (import.meta.env.DEV ? "http://localhost:8080" : "");

async function request(path, options = {}) {
  let res;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      headers: { "Content-Type": "application/json" },
      ...options,
    });
  } catch {
    const target = API_BASE || window.location.origin;
    throw new Error(`Can't reach the backend at ${target}. Is the API available?`);
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

  getTrip: (id) => request(`/api/v1/trips/${encodeURIComponent(id)}`),

  listDrivers: () => request("/api/v1/drivers"),

  systemStatus: () => request("/api/v1/system/status"),
};

export function wsURL() {
  // Relative API mode is used behind Nginx; the browser must connect to the
  // public host, never to Docker's internal service name.
  const url = new URL(API_BASE || window.location.origin);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.pathname = "/ws";
  url.search = "";
  url.hash = "";
  return url.toString();
}

export { API_BASE };
