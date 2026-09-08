import { initializeTelemetry } from '@nerdswhofish/browser-telemetry';

const url = document.querySelector('meta[name="faro-collector-url"]')?.content;
if (url) {
  initializeTelemetry({
    url,
    app: { name: 'fledge', version: __APP_VERSION__, environment: 'production' },
    routes: ['/', '/api/apps', '/api/builds', '/api/devices', '/api/invites'],
    assets: ['/assets/telemetry.js'],
    operations: [],
  });
}
