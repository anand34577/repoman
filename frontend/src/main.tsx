import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import '@mantine/spotlight/styles.css'
import '@mantine/dropzone/styles.css'
import '@fontsource-variable/inter'
import '@fontsource-variable/jetbrains-mono'
import './app.css'

import { MantineProvider, createTheme, type MantineColorsTuple } from '@mantine/core'
import { ModalsProvider } from '@mantine/modals'
import { Notifications } from '@mantine/notifications'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter, RouterProvider } from 'react-router-dom'
import App from './App'
import { ErrorBoundary, RouteError } from './util'

// Indigo-violet brand; shade 6 passes WCAG AA for white text.
const brand: MantineColorsTuple = ['#f0efff', '#dedcff', '#bab5ff', '#938bfd', '#7267fb', '#5d50fa', '#5143f0', '#4236d6', '#382fc0', '#2c27a6']
// Slightly cool dark surfaces instead of neutral grey.
const dark: MantineColorsTuple = ['#c9ccd6', '#a7abb8', '#7d8292', '#5b6070', '#3a3f4d', '#2a2e39', '#20232c', '#181a22', '#12141a', '#0d0e13']

const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: { light: 6, dark: 5 },
  colors: { brand, dark },
  defaultRadius: 'md',
  cursorType: 'pointer',
  focusRing: 'auto',
  fontFamily: 'Inter Variable, Inter, system-ui, -apple-system, Segoe UI, Roboto, sans-serif',
  fontFamilyMonospace: 'JetBrains Mono Variable, ui-monospace, SFMono-Regular, Consolas, monospace',
  headings: { fontWeight: '650', sizes: { h2: { fontSize: '1.625rem', lineHeight: '1.25' } } },
  shadows: {
    xs: '0 1px 2px rgba(16, 18, 27, 0.05)',
    sm: '0 1px 3px rgba(16, 18, 27, 0.06), 0 1px 2px rgba(16, 18, 27, 0.04)',
    md: '0 4px 12px -2px rgba(16, 18, 27, 0.08), 0 2px 4px -2px rgba(16, 18, 27, 0.05)',
    lg: '0 12px 28px -6px rgba(16, 18, 27, 0.14), 0 4px 8px -4px rgba(16, 18, 27, 0.06)',
    xl: '0 24px 48px -12px rgba(16, 18, 27, 0.22)',
  },
  components: {
    Card: { defaultProps: { withBorder: true, radius: 'lg', shadow: 'xs' } },
    Paper: { defaultProps: { radius: 'lg' } },
    Modal: { defaultProps: { radius: 'lg', centered: true, overlayProps: { backgroundOpacity: 0.45, blur: 3 } } },
    Drawer: { defaultProps: { overlayProps: { backgroundOpacity: 0.35, blur: 2 } } },
    Tooltip: { defaultProps: { withArrow: true, openDelay: 250 } },
    Menu: { defaultProps: { shadow: 'lg', radius: 'md' } },
    Badge: { defaultProps: { radius: 'sm' } },
    Notification: { defaultProps: { radius: 'md', withBorder: true } },
    Table: { defaultProps: { verticalSpacing: 'sm' } },
  },
})

// One catch-all route: pages are matched by <Routes> inside App. The data router is what lets a page
// hold back navigation while it has unsaved changes (useUnsavedGuard).
const router = createBrowserRouter([{ path: '*', element: <App />, errorElement: <RouteError /> }])

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="auto">
      <ModalsProvider>
        <Notifications position="top-right" limit={4} />
        <ErrorBoundary>
          <RouterProvider router={router} />
        </ErrorBoundary>
      </ModalsProvider>
    </MantineProvider>
  </StrictMode>,
)
