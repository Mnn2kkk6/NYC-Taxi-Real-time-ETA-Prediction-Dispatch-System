import { BrowserRouter, Routes, Route } from "react-router-dom";
import Layout from "./components/Layout";
import PredictionPage from "./pages/PredictionPage";
import DashboardPage from "./pages/DashboardPage";
import TripsPage from "./pages/TripsPage";
import MapPage from "./pages/MapPage";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<PredictionPage />} />
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/trips" element={<TripsPage />} />
          <Route path="/map" element={<MapPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
