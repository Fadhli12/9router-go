### Dashboard UI (web/src)

- Surface HTTP status code and server response error details in Request Details modal dialog (`RequestDetailsTab.svelte`).
- Resolves issue #800 where failed requests displayed only generic `STATUS: error` without status codes (e.g. 404, 429, 502) or visible server error details.
- Adds `formatDetailStatus` and `extractDetailError` helpers with unit test coverage, and includes before/after screenshot evidence in `docs/screenshots/request-details-error-modal.png`.
