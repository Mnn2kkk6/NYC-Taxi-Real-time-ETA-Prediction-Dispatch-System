import { NavLink, Outlet } from "react-router-dom";
import { useLiveEvents } from "../hooks/useLiveEvents";
import { LiveEventsContext } from "../liveEventsContext";

export default function Layout() {
  const live = useLiveEvents();

  return (
    <LiveEventsContext.Provider value={live}>
      <div className="app-shell">
        <aside className="sidebar">
          <div>
            <div className="brand">NYC ETA</div>
            <div className="brand-sub">dispatch console</div>
          </div>
          <nav className="nav-list">
            <NavLink to="/" end className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
              Predict a trip
            </NavLink>
            <NavLink to="/dashboard" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
              Dashboard
            </NavLink>
            <NavLink to="/trips" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
              Trips
            </NavLink>
            <NavLink to="/map" className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
              Map
            </NavLink>
          </nav>
          <div className="sidebar-footer">
            <span className={`dot ${live.connected ? "dot-on" : "dot-off"}`} />
            {live.connected ? "Live" : "Reconnecting…"}
          </div>
        </aside>
        <main className="main">
          <Outlet />
        </main>
      </div>
    </LiveEventsContext.Provider>
  );
}
