# Next.js SSR plan

## Goal

Render useful, owner-authorized console content in initial HTML for routes where
data is safe to serialize. Retain client-side mutations, polling, browser APIs,
and interactive state.

## Current state

- `app/[[...route]]/page.tsx` is already an async server component. It validates
  top-level routes and loads owner status plus API status.
- `src/App.tsx` is a client component. It dynamically loads every view.
- Each view fetches its data in `useEffect`, so direct navigation first renders a
  loading or empty state, then fetches after hydration.
- `app/lib/server-api.ts` forwards cookies to the Go API and uses `no-store`.
- `src/lib/api.ts` depends on browser-only `document.cookie` for mutating calls.
  It must remain client-only.

## Boundaries

Server-render:

- owner-visible read models from safe GET endpoints;
- route-specific first view data;
- shell auth and API-health state;
- device, candidate, and incident base records for direct detail URLs.

Remain client-only:

- every mutation and CSRF handling;
- five-second polling and manual refresh;
- modal, filter, selection, and responsive viewport state;
- clipboard, `window`, `document`, and confirmation APIs;
- live metrics, charts, and other high-volume detail data until payload limits are
  measured.

## Design

### 1. Server data module

Extend `apps/web/app/lib/server-api.ts` with:

- typed `fetchApiJSON<T>()` that forwards request cookie and returns a typed
  success or safe error result;
- route loader functions that use `Promise.allSettled` for independent sources;
- explicit query limits for every collection;
- `RouteData` discriminated by top-level route and detail route;
- a serialization allow-list. Do not pass credential secrets, CSRF tokens,
  `Set-Cookie` headers, or raw failed-response bodies to React props.

Every request remains `cache: "no-store"`. Console data is personalized and
changes frequently; static generation and ISR are out of scope.

### 2. Route-aware server composition

In `apps/web/app/[[...route]]/page.tsx`:

1. Validate full route shape, including known detail route forms.
2. Load shell data first.
3. If owner is signed in, select exactly one route loader.
4. Pass `initialData` and source-error state to `App`.
5. Return `notFound()` for invalid details. Preserve current client fallback for
   records deleted between server render and hydration.

Do not fetch data for hidden views. Do not make an internal HTTP request through
the Next proxy; server loaders call configured Go API origins directly.

### 3. Client hydration contract

Add `initialData?: RouteData` props to `App` and relevant views. Each view:

- initializes list/detail state from matching initial data;
- renders source-level error state supplied by server;
- skips only its first duplicate read when initial data is present;
- begins normal polling after hydration;
- retains retry controls and client refresh behavior.

Use shared read-model TypeScript types, not the browser `api` client. Keep
`src/lib/api.ts` unchanged for client requests and mutations.

## Delivery order

### Phase 1: foundation and highest-value routes

- Add server fetch/result helpers and `RouteData` types.
- Load Overview: devices, candidates, active incidents.
- Load Systems: devices and candidates.
- Load Network: topology and candidates. Fetch scan statuses client-side after
  hydration because count varies with returned candidates.
- Add route-loader tests and SSR HTML checks.

Exit criteria: signed-in direct loads of `/overview`, `/systems`, and `/network`
contain data rows/cards in initial HTML. Browser API calls and polling remain
functional after hydration.

### Phase 2: operational lists

- Load Incidents: initial list, alert rules, sites, devices. Load selected
  incident and transitions for `/incidents/:id`.
- Load Notifications: destinations, suppression windows, deliveries, recovery,
  sites, devices.
- Load Scopes and Enrollment: list/configuration read models.

Exit criteria: initial lists have no hydration-only loading state; partial source
failures show existing scoped error UI rather than failing whole route.

### Phase 3: configuration and telemetry-adjacent routes

- Load Services, Hardware, Storage, Updates, and Access read models.
- Load base Device and Candidate details for direct detail URLs.
- Keep metrics, collector refreshes, heavy chart series, and mutations client
  fetched unless measured payload budget allows server data.

Exit criteria: every authenticated route gets a route-specific SSR decision:
server initial data, intentionally client-only, or deferred due to documented
payload/runtime constraint.

### Phase 4: polish and guardrails

- Add SSR payload size instrumentation in development/test output.
- Set per-route response-size budgets before adding charts or metric series.
- Ensure error and loading boundaries distinguish server-load failure from client
  refresh failure.
- Document SSR data boundary and route inventory in architecture docs.

## Risks and controls

- **Auth drift:** server API requests must forward current cookies. Treat 401/403
  as signed-out server state; never hydrate protected stale data.
- **Double fetches:** view effects must consume matching initial data once, then
  poll normally.
- **Data leak:** use explicit server DTOs and review serialized props in SSR
  tests.
- **Large HTML:** list endpoints need bounded limits; defer metric series.
- **Stale UI:** use `no-store` for initial requests and preserve client polling.
- **Route transitions:** client navigation can use existing loading behavior
  initially; later add server-prefetch only after SSR correctness is stable.

## Validation

1. Unit-test server helpers for configured/fallback API origins, cookie forwarding,
   source failures, and serialization allow-list.
2. Build and type-check web workspace.
3. In Playwright, request authenticated routes with JavaScript disabled and assert
   initial HTML contains expected overview/system/network content.
4. With JavaScript enabled, verify hydration has no mismatch, polling updates
   data, and mutations still send CSRF headers.
5. Verify signed-out requests render setup screen without protected list data.
6. Capture SSR response size and server-render time for representative 100-device
   fixture. Set budgets before Phase 3.

## Definition of done

- Initial HTML contains useful safe data for Phase 1 routes.
- No protected field, token, or secret appears in HTML or React payload.
- Client interactions, mutations, detail navigation, and polling still work.
- Tests cover signed-in, signed-out, API-partial-failure, and hydration paths.
- Every remaining route has explicit SSR status and reason.
