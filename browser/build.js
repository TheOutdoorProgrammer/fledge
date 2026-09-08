import { buildTelemetry } from '@nerdswhofish/browser-telemetry/build';

await buildTelemetry({
  entry: 'browser/telemetry.js',
  outfile: 'internal/web/assets/telemetry.js',
  revision: process.env.APP_VERSION,
});
