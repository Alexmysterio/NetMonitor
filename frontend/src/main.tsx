import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/inter'
import '@fontsource-variable/jetbrains-mono'
import './styles.css'
import App from './App'
import ErrorBoundary from './ErrorBoundary'
import { RealtimeProvider } from './realtime'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <ErrorBoundary>
        <RealtimeProvider>
          <App />
        </RealtimeProvider>
      </ErrorBoundary>
    </BrowserRouter>
  </StrictMode>,
)
