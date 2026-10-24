import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import './styles/base.css'
import './styles/app.css'
import './styles/shared.css'
import './styles/books.css'
import './styles/settings.css'
import { AppProvider, useApp } from './lib/app-context'
import { Loading, ToastProvider } from './components/ui'
import Layout from './components/Layout'
import { ErrorBoundary } from './components/ErrorBoundary'
import Login from './pages/Login'
import Home from './pages/Home'
import Browse from './pages/Browse'
import BookPage from './pages/BookPage'
import Authors from './pages/Authors'
import AuthorPage from './pages/AuthorPage'
import SeriesList from './pages/SeriesList'
import SeriesPage from './pages/SeriesPage'
import Tags from './pages/Tags'
import Shelves from './pages/Shelves'
import ShelfPage from './pages/ShelfPage'
import Reading from './pages/Reading'
import Highlights from './pages/Highlights'
import Stats from './pages/Stats'
import Devices from './pages/Devices'
import Admin from './pages/Admin'
import Settings from './pages/Settings'

// the reader is big (EPUB/PDF/comic engines): loaded only when a book is opened
const Reader = React.lazy(() => import('./reader/Reader'))

function Gate({ children }: { children: React.ReactNode }) {
  const { status, user } = useApp()
  const loc = useLocation()
  if (!status) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (status.needs_setup) return <Navigate to="/setup" replace />
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  return <>{children}</>
}

function Signed() {
  return <Gate><Outlet /></Gate>
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login mode="login" />} />
      <Route path="/setup" element={<Login mode="setup" />} />
      <Route element={<Signed />}>
        <Route path="/read/:id" element={<ErrorBoundary where="reader"><React.Suspense fallback={<div className="boot"><Loading label="Opening the book…" /></div>}><Reader /></React.Suspense></ErrorBoundary>} />
        <Route path="/settings/:tab?" element={<ErrorBoundary where="settings"><Settings /></ErrorBoundary>} />
        <Route element={<ErrorBoundary where="library"><Layout /></ErrorBoundary>}>
          <Route path="/" element={<Home />} />
          <Route path="/library" element={<Browse />} />
          <Route path="/book/:id" element={<BookPage />} />
          <Route path="/authors" element={<Authors />} />
          <Route path="/author/:id" element={<AuthorPage />} />
          <Route path="/series" element={<SeriesList />} />
          <Route path="/series/:id" element={<SeriesPage />} />
          <Route path="/tags" element={<Tags />} />
          <Route path="/shelves" element={<Shelves />} />
          <Route path="/shelf/:id" element={<ShelfPage />} />
          <Route path="/reading/:tab?" element={<Reading />} />
          <Route path="/highlights" element={<Highlights />} />
          <Route path="/stats" element={<Stats />} />
          <Route path="/devices" element={<Devices />} />
          <Route path="/admin/:tab?" element={<Admin />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Route>
    </Routes>
  )
}

// an older build may have registered a service worker: take it away
navigator.serviceWorker?.getRegistrations?.().then(rs => rs.forEach(r => r.unregister())).catch(() => {})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary where="app"><ToastProvider><AppProvider><App /></AppProvider></ToastProvider></ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)
