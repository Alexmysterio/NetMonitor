import { Navigate, Outlet, Route, Routes } from 'react-router-dom'
import { getToken } from './api'
import { ConfirmHost } from './confirm'
import Shell from './components/Shell'
import LoginPage from './pages/LoginPage'
import MapPage from './pages/MapPage'
import MapsLanding from './pages/MapsLanding'
import DevicesPage from './pages/DevicesPage'
import OutagesPage from './pages/OutagesPage'
import ProbesPage from './pages/ProbesPage'
import SettingsPage from './pages/SettingsPage'
import NetworksPage from './pages/NetworksPage'
import PanelsPage from './pages/PanelsPage'
import HistoryPage from './pages/HistoryPage'
import RouterOSPage from './pages/RouterOSPage'
import TrapsPage from './pages/TrapsPage'
import ImportsPage from './pages/ImportsPage'
import ReportsPage from './pages/ReportsPage'

function Protected() {
  if (!getToken()) return <Navigate to="/login" replace />
  return <Shell><Outlet /></Shell>
}

export default function App() {
  return (
    <>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route element={<Protected />}>
          <Route path="/" element={<Navigate to="/maps" replace />} />
          <Route path="/maps" element={<MapsLanding />} />
          <Route path="/maps/:mapId" element={<MapPage />} />
          <Route path="/devices" element={<DevicesPage />} />
          <Route path="/outages" element={<OutagesPage />} />
          <Route path="/probes" element={<ProbesPage />} />
          <Route path="/networks" element={<NetworksPage />} />
          <Route path="/panels" element={<PanelsPage />} />
          <Route path="/history" element={<HistoryPage />} />
          <Route path="/routeros" element={<RouterOSPage />} />
          <Route path="/traps" element={<TrapsPage />} />
          <Route path="/reports" element={<ReportsPage />} />
          <Route path="/imports" element={<ImportsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/maps" replace />} />
        </Route>
      </Routes>
      <ConfirmHost />
    </>
  )
}
