import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { useAuthStore } from '@shared'
import { LoginPage } from './pages/Login'
import { InboxLayout } from './pages/Inbox'
import { ContactsPage } from './pages/Contacts'
import { CalendarPage } from './pages/Calendar'
import { SettingsPage } from './pages/Settings'
import { SearchPage } from './pages/Search'
import { TasksPage } from './pages/Tasks'

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const token = useAuthStore((s) => s.token)
  if (!token) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/*" element={<ProtectedRoute><Routes>
          <Route path="/" element={<InboxLayout />} />
          <Route path="/inbox" element={<InboxLayout />} />
          <Route path="/contacts" element={<ContactsPage />} />
          <Route path="/calendar" element={<CalendarPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/search" element={<SearchPage />} />
          <Route path="/tasks" element={<TasksPage />} />
        </Routes></ProtectedRoute>} />
      </Routes>
    </BrowserRouter>
  )
}
