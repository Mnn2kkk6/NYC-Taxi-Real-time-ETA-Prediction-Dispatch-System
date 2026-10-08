import { useMemo } from "react";
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid, Legend } from "recharts";
import { useSystemStatus } from "../hooks/useSystemStatus";
import { useLiveEventsContext } from "../liveEventsContext";

function Stat({ label, value, unit, small }) {
  return (
    <div className="stat">
      <div className="stat-label">{label}</div>
      <div className={`stat-value${small ? " small" : ""}`}>
        {value}
        {unit && <span className="unit">{unit}</span>}
      </div>
    </div>
  );
}

function describeEvent(e) {
  if (e.type === "trip_created") {
    return { title: "Trip created", detail: e.status };
  }
  if (e.type === "trip_prediction") {
    return {
      title: "Prediction ready",
      detail: `${e.eta_minutes} min${e.cache_hit ? " (cached)" : ""}`,
    };
  }
  return { title: e.type || "event", detail: "" };
}

export default function DashboardPage() {
  const { status, history, error } = useSystemStatus();
  const { events, connected } = useLiveEventsContext();

  const counts = useMemo(() => {
    let created = 0;
    let dispatched = 0;
    let predictions = 0;
    for (const e of events) {
      if (e.type === "trip_created") {
        created += 1;
        if (e.status === "DISPATCHED") dispatched += 1;
      }
      if (e.type === "trip_prediction") predictions += 1;
    }
    return { created, dispatched, predictions };
  }, [events]);

  const hitRate = status ? `${Math.round(status.cache_hit_rate * 100)}` : "–";

  return (
    <div>
      <h1 className="page-title">Dashboard</h1>
      <p className="page-sub">
        {connected ? "Receiving live events." : "Live connection is down — retrying."}
        {error && ` Status polling failed: ${error}`}
      </p>

      <div className="stat-row">
        <Stat label="Jobs waiting" value={status ? status.queue_length : "–"} unit={status ? `/ ${status.queue_capacity}` : ""} />
        <Stat label="Busy workers" value={status ? status.busy_workers : "–"} />
        <Stat label="Cache hit rate" value={hitRate} unit="%" />
        <Stat label="Cache hits / misses" value={status ? `${status.cache_hits} / ${status.cache_misses}` : "–"} small />
      </div>

      <div className="stat-row">
        <Stat label="Trips created (this session)" value={counts.created} />
        <Stat label="Trips dispatched" value={counts.dispatched} />
        <Stat label="Predictions served" value={counts.predictions} />
      </div>

      <div className="two-col">
        <div className="panel">
          <p className="panel-title">Queue and workers over the last minute</p>
          {history.length < 2 ? (
            <p className="empty-state">Collecting data…</p>
          ) : (
            <ResponsiveContainer width="100%" height={260}>
              <LineChart data={history} margin={{ top: 4, right: 8, bottom: 0, left: -20 }}>
                <CartesianGrid stroke="#3a362f" vertical={false} />
                <XAxis dataKey="t" hide />
                <YAxis allowDecimals={false} stroke="#9c948a" fontSize={12} />
                <Tooltip
                  contentStyle={{ background: "#242220", border: "1px solid #3a362f", fontSize: 12 }}
                  labelFormatter={() => ""}
                />
                <Legend verticalAlign="top" height={28} wrapperStyle={{ fontSize: 12 }} />
                <Line type="monotone" dataKey="queue_length" name="Jobs waiting" stroke="#f0b429" dot={false} strokeWidth={2} isAnimationActive={false} />
                <Line type="monotone" dataKey="busy_workers" name="Busy workers" stroke="#3e8e7e" dot={false} strokeWidth={2} isAnimationActive={false} />
              </LineChart>
            </ResponsiveContainer>
          )}
        </div>

        <div className="panel">
          <p className="panel-title">Live events</p>
          {events.length === 0 ? (
            <p className="empty-state">
              Nothing yet. Create a trip, request a prediction, or run the simulator to see events here.
            </p>
          ) : (
            <div className="event-feed">
              {events.slice(0, 30).map((e, i) => {
                const d = describeEvent(e);
                return (
                  <div className="event-row" key={`${e._receivedAt}-${i}`}>
                    <span>{d.title}</span>
                    <span className="event-type">{d.detail}</span>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}