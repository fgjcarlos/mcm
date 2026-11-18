import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
// Self-hosted, the same faces as the site; Big Shoulders with its optical-size axis.
import '@fontsource-variable/big-shoulders/opsz.css'
import '@fontsource-variable/overpass'
import '@fontsource-variable/overpass-mono'
import './index.css'
import App from './App.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
