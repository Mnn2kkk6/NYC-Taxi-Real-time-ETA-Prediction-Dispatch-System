import { useState } from "react";
import { api } from "../api/client";

const PRESETS = [
  { label: "JFK → Midtown", pickup_latitude: 40.6413, pickup_longitude: -73.7781, dropoff_latitude: 40.7549, dropoff_longitude: -73.984 },
  { label: "Times Sq → Wall St", pickup_latitude: 40.758, pickup_longitude: -73.9855, dropoff_latitude: 40.7074, dropoff_longitude: -74.0113 },
  { label: "Brooklyn → LGA", pickup_latitude: 40.6782, pickup_longitude: -73.9442, dropoff_latitude: 40.7769, dropoff_longitude: -73.874 },
];

function nowLocalDatetime() {
  const d = new Date();
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}

export default function PredictionPage() {
  const [form, setForm] = useState({
    pickup_latitude: 40.761,
    pickup_longitude: -73.982,
    dropoff_latitude: 40.73,
    dropoff_longitude: -73.995,
    passenger_count: 1,
    pickup_datetime: nowLocalDatetime(),
  });
  const [result, setResult] = useState(null);
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);

  function update(field, value) {
    setForm((f) => ({ ...f, [field]: value }));
  }

  function applyPreset(preset) {
    setForm((f) => ({
      ...f,
      pickup_latitude: preset.pickup_latitude,
      pickup_longitude: preset.pickup_longitude,
      dropoff_latitude: preset.dropoff_latitude,
      dropoff_longitude: preset.dropoff_longitude,
    }));
  }

  async function submit(e) {
    e.preventDefault();
    setLoading(true);
    setError(null);
    setResult(null);
    try {
      const payload = {
        ...form,
        pickup_latitude: Number(form.pickup_latitude),
        pickup_longitude: Number(form.pickup_longitude),
        dropoff_latitude: Number(form.dropoff_latitude),
        dropoff_longitude: Number(form.dropoff_longitude),
        passenger_count: Number(form.passenger_count),
        pickup_datetime: new Date(form.pickup_datetime).toISOString(),
      };
      const res = await api.predict(payload);
      setResult(res);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  return (
    <div>
      <h1 className="page-title">Predict a trip</h1>
      <p className="page-sub">Enter pickup and dropoff to get an ETA from the XGBoost model.</p>

      <div className="two-col">
        <form className="panel" onSubmit={submit}>
          <p className="panel-title">Trip details</p>

          <div style={{ marginBottom: 16, display: "flex", gap: 8, flexWrap: "wrap" }}>
            {PRESETS.map((p) => (
              <button
                type="button"
                key={p.label}
                className="btn-secondary btn"
                style={{ padding: "6px 12px", fontSize: 12 }}
                onClick={() => applyPreset(p)}
              >
                {p.label}
              </button>
            ))}
          </div>

          <div className="form-grid">
            <div className="field">
              <label>Pickup latitude</label>
              <input
                type="number"
                step="0.0001"
                value={form.pickup_latitude}
                onChange={(e) => update("pickup_latitude", e.target.value)}
                required
              />
            </div>
            <div className="field">
              <label>Pickup longitude</label>
              <input
                type="number"
                step="0.0001"
                value={form.pickup_longitude}
                onChange={(e) => update("pickup_longitude", e.target.value)}
                required
              />
            </div>
            <div className="field">
              <label>Dropoff latitude</label>
              <input
                type="number"
                step="0.0001"
                value={form.dropoff_latitude}
                onChange={(e) => update("dropoff_latitude", e.target.value)}
                required
              />
            </div>
            <div className="field">
              <label>Dropoff longitude</label>
              <input
                type="number"
                step="0.0001"
                value={form.dropoff_longitude}
                onChange={(e) => update("dropoff_longitude", e.target.value)}
                required
              />
            </div>
            <div className="field">
              <label>Passenger count</label>
              <input
                type="number"
                min="1"
                max="6"
                value={form.passenger_count}
                onChange={(e) => update("passenger_count", e.target.value)}
                required
              />
            </div>
            <div className="field">
              <label>Pickup date &amp; time</label>
              <input
                type="datetime-local"
                value={form.pickup_datetime}
                onChange={(e) => update("pickup_datetime", e.target.value)}
                required
              />
            </div>
          </div>

          <div style={{ marginTop: 20 }}>
            <button className="btn" type="submit" disabled={loading}>
              {loading ? "Predicting…" : "Get ETA"}
            </button>
          </div>

          {error && <div className="error-banner">Couldn't get a prediction: {error}</div>}
        </form>

        <div>
          {result ? (
            <div className="result-card">
              <div className="eta-hero">
                {result.predicted_duration_minutes}
                <span className="unit">min</span>
              </div>
              <div className="meta-row">
                <div className="meta-item">
                  <span className="k">Distance</span>
                  <span className="v">{result.distance_km} km</span>
                </div>
                <div className="meta-item">
                  <span className="k">Model</span>
                  <span className="v">{result.model_version}</span>
                </div>
                <div className="meta-item">
                  <span className="k">Cache</span>
                  <span className="v">{result.cache_hit ? "hit" : "miss"}</span>
                </div>
              </div>
              <hr className="hairline" />
              <div className="meta-row">
                <div className="meta-item">
                  <span className="k">Request ID</span>
                  <span className="v">{result.request_id}</span>
                </div>
              </div>
              <div className="meta-row">
                <div className="meta-item">
                  <span className="k">Predicted at</span>
                  <span className="v">{new Date(result.prediction_timestamp).toLocaleTimeString()}</span>
                </div>
              </div>
            </div>
          ) : (
            <div className="panel">
              <p className="panel-title">Result</p>
              <p className="empty-state">Submit a trip to see the predicted ETA here.</p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
