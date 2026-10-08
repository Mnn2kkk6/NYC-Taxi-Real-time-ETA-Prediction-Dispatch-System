import { useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import { useLiveEventsContext } from "../liveEventsContext";

const FILTERS = ["ALL", "PENDING", "DISPATCHED"];

function shortId(id) {
  return id ? id.slice(0, 8) : "–";
}

function coord(lat, lng) {
  return `${lat.toFixed(4)}, ${lng.toFixed(4)}`;
}

export default function TripsPage() {
  const { events } = useLiveEventsContext();
  const [trips, setTrips] = useState([]);
  const [filter, setFilter] = useState("ALL");
  const [error, setError] = useState(null);
  const [loaded, setLoaded] = useState(false);
  const seenIds = useRef(new Set());
  const [freshIds, setFreshIds] = useState(new Set());

  // refetch on mount, on filter change, and whenever a new trip_created event arrives
  const latestCreated = events.find((e) => e.type === "trip_created");
  const refreshKey = latestCreated?._receivedAt;

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const params = { limit: 50 };
        if (filter !== "ALL") params.status = filter;
        const data = await api.listTrips(params);
        if (cancelled) return;

        const fresh = new Set();
        if (seenIds.current.size > 0) {
          for (const t of data) {
            if (!seenIds.current.has(t.id)) fresh.add(t.id);
          }
        }
        for (const t of data) seenIds.current.add(t.id);

        setFreshIds(fresh);
        setTrips(data || []);
        setError(null);
        setLoaded(true);
      } catch (e) {
        if (!cancelled) setError(e.message);
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, [filter, refreshKey]);

  return (
    <div>
      <h1 className="page-title">Trips</h1>
      <p className="page-sub">The 50 most recent trips. New ones flash when they arrive.</p>

      <div style={{ display: "flex", gap: 8, marginBottom: 16 }}>
        {FILTERS.map((f) => (
          <button
            key={f}
            className={f === filter ? "btn" : "btn btn-secondary"}
            style={{ padding: "6px 14px", fontSize: 13 }}
            onClick={() => setFilter(f)}
          >
            {f === "ALL" ? "All" : f === "PENDING" ? "Pending" : "Dispatched"}
          </button>
        ))}
      </div>

      {error && <div className="error-banner">Couldn't load trips: {error}</div>}

      <div className="panel" style={{ padding: 0 }}>
        {loaded && trips.length === 0 ? (
          <p className="empty-state" style={{ padding: "20px 24px" }}>
            No trips yet. Run the simulator or POST to /api/v1/trips to create some.
          </p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Trip</th>
                <th>Pickup</th>
                <th>Dropoff</th>
                <th>Riders</th>
                <th>Status</th>
                <th>Driver</th>
                <th>Requested</th>
              </tr>
            </thead>
            <tbody>
              {trips.map((t) => (
                <tr key={t.id} className={freshIds.has(t.id) ? "trip-new" : ""}>
                  <td>{shortId(t.id)}</td>
                  <td>{coord(t.pickup_lat, t.pickup_lng)}</td>
                  <td>{coord(t.dropoff_lat, t.dropoff_lng)}</td>
                  <td>{t.passenger_count}</td>
                  <td>
                    <span className={`badge badge-${t.status.toLowerCase()}`}>{t.status.toLowerCase()}</span>
                  </td>
                  <td>{shortId(t.driver_id)}</td>
                  <td>{new Date(t.requested_at).toLocaleTimeString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
