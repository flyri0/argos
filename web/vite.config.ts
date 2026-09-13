/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { VitePWA } from 'vite-plugin-pwa'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
    // §2.2: "manifest.json + a service worker ... caches the app shell so
    // the PWA can be installed and opened with no network at all."
    // vite-plugin-pwa generates that service worker via workbox-build's
    // `generateSW` strategy (still Workbox under the hood, just driven from
    // Vite config instead of a hand-written workbox-cli setup, which is the
    // standard pairing for a Vite project) and precaches every built asset
    // (JS/CSS/HTML/icons) as the app shell.
    VitePWA({
      manifestFilename: 'manifest.json',
      registerType: 'autoUpdate',
      includeAssets: ['favicon.svg', 'icons.svg'],
      manifest: {
        name: 'Argos',
        short_name: 'Argos',
        description: 'Local-first envelope budgeting',
        start_url: '/',
        display: 'standalone',
        background_color: '#ffffff',
        theme_color: '#7e14ff',
        icons: [
          { src: '/favicon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
          { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
          {
            src: '/icons/icon-512-maskable.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'maskable',
          },
        ],
      },
      workbox: {
        // The app itself is a single index.html with in-memory tab state
        // (no client-side router), so every navigation should resolve to
        // the same cached shell offline.
        navigateFallback: '/index.html',
      },
    }),
  ],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
  },
})
