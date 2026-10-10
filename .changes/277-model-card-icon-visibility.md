### Fixed
- Model card action icons (copy, remove combo, delete unusable model) on the Provider detail "Available Models" grid were hidden by default on desktop (`sm:` breakpoint and up) and only appeared on hover/focus. Removed the `sm:opacity-0 sm:group-hover:opacity-100` gate so icons are always visible, matching the mobile behavior and the test-status indicators on the same card.
