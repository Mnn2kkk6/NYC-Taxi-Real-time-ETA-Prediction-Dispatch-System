import { useEffect, useState } from "react";
import { MapContainer, TileLayer, CircleMarker, Polyline, Tooltip } from "react-leaflet";
import "leaflet/dist/leaflet.css";
import { api } from "../api/client";
import { useLiveEventsContext } from "../liveEventsContext";

const NYC_CENTER = [40.73, -73.95];

const STATUS_COLOR = {
  AVAILABLE: "#3e8e7e",
  BUSY: "#c1502e",
  OFFLINE: "#9c948a",
};

export default function MapPage() {
  const { events } = useLiveEventsContext();
  const [drivers, setDrivers] = useState([]);
  const [trips, setTrips] = useState([]);
  const [error, setError] = useState(null);

  const latestCreated = events.find((e) => e.type === "trip_created");
  const refreshKey = latestCreated?._receivedAt;

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const [d, t] = await Promise.all([api.listDrivers(), api.listTrips({ limit: 30 })]);
        if (cancelled) return;
        setDrivers(d || []);
        setTrips(t || []);
        setError(null);
      } catch (e) {
        if (!cancelled) setError(e.message);
      }
    }
    load();
    const id = setInterval(load, 4000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [refreshKey]);

  const counts = drivers.reduce((acc, d) => {
    acc[d.status] = (acc[d.status] || 0) + 1;
    return acc;
  }, {});

  return (
    <div>
      <h1 className="page-title">Map</h1>
      <p className="page-sub">
        {drivers.length} drivers
        {counts.AVAILABLE ? ` · ${counts.AVAILABLE} available` : ""}
        {counts.BUSY ? ` · ${counts.BUSY} busy` : ""}. Lines connect each recent trip's pickup to its dropoff.
      </p>

      {error && <div className="error-banner">Couldn't load map data: {error}</div>}

      <div className="map-wrap">
        <MapContainer center={NYC_CENTER} zoom={11} style={{ height: "100%", width: "100%", background: "#1b1918" }}>
          <TileLayer
            attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
            url="https://tile.openstreetmap.org/{z}/{x}/{y}.png"
          />

          {trips.map((t) => (
            <Polyline
              key={`line-${t.id}`}
              positions={[
                [t.pickup_lat, t.pickup_lng],
                [t.dropoff_lat, t.dropoff_lng],
              ]}
              pathOptions={{
                color: t.status === "DISPATCHED" ? "#3e8e7e" : "#f0b429",
                weight: 1.5,
                opacity: 0.55,
              }}
            />
          ))}

          {trips.map((t) => (
            <CircleMarker
              key={`pickup-${t.id}`}
              center={[t.pickup_lat, t.pickup_lng]}
              radius={4}
              pathOptions={{
                color: "#f0b429",
                fillColor: "#f0b429",
                fillOpacity: 0.9,
                weight: 1,
              }}
            >
              <Tooltip>
                Pickup · {t.status.toLowerCase()} · {t.passenger_count} rider{t.passenger_count > 1 ? "s" : ""}
              </Tooltip>
            </CircleMarker>
          ))}

          {drivers.map((d) => (
            <CircleMarker
              key={d.id}
              center={[d.latitude, d.longitude]}
              radius={7}
              pathOptions={{
                color: "#1b1918",
                fillColor: STATUS_COLOR[d.status] || "#9c948a",
                fillOpacity: 1,
                weight: 2,
              }}
            >
              <Tooltip>
                Driver {d.id.slice(0, 8)} · {d.status.toLowerCase()}
              </Tooltip>
            </CircleMarker>
          ))}
        </MapContainer>
      </div>

      <div style={{ display: "flex", gap: 20, marginTop: 14, fontSize: 13, color: "var(--text-muted)" }}>
        <span><span className="dot" style={{ background: STATUS_COLOR.AVAILABLE, marginRight: 6 }} />Available driver</span>
        <span><span className="dot" style={{ background: STATUS_COLOR.BUSY, marginRight: 6 }} />Busy driver</span>
        <span><span className="dot" style={{ background: "#f0b429", marginRight: 6 }} />Trip pickup</span>
      </div>
    </div>
  );
}